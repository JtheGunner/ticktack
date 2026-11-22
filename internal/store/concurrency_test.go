package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

func TestOpenSetsBusyTimeoutAndWAL(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	var mode string
	if err := s.root.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var timeout int
	if err := s.root.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestOpenRejectsPathWithQuestionMark(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("Open accepted a path containing '?'")
	}
}

// TestBundledSQLiteHasWALResetFix checks the SQLite version bundled with the
// driver against the releases that fix the WAL-reset bug: 3.51.3 and the
// backports 3.44.6 and 3.50.7.
func TestBundledSQLiteHasWALResetFix(t *testing.T) {
	s, _ := openTemp(t)
	var version string
	if err := s.root.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		t.Fatalf("unexpected SQLite version %q", version)
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			t.Fatalf("unexpected SQLite version %q", version)
		}
		v[i] = n
	}
	fixed := [3]int{3, 51, 3}
	switch {
	case v[0] == 3 && v[1] == 44:
		fixed = [3]int{3, 44, 6}
	case v[0] == 3 && v[1] == 50:
		fixed = [3]int{3, 50, 7}
	}
	for i := range v {
		if v[i] > fixed[i] {
			return
		}
		if v[i] < fixed[i] {
			t.Fatalf("bundled SQLite %s lacks the WAL-reset fix (needs >= %d.%d.%d)", version, fixed[0], fixed[1], fixed[2])
		}
	}
}

// TestTransactionsTakeTheWriteLockImmediately shows that a transaction of the
// store holds the write lock from its start, before its first write, so the
// _txlock=immediate DSN option takes effect with this driver.
func TestTransactionsTakeTheWriteLockImmediately(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	probe, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Close() }()

	err = s.InTx(ctx, func(Tx) error {
		// No write yet; a deferred transaction would not hold the lock.
		if _, probeErr := probe.ExecContext(ctx, "BEGIN IMMEDIATE"); probeErr == nil {
			t.Error("another connection got the write lock while a store transaction was open")
			_, _ = probe.ExecContext(ctx, "ROLLBACK")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppendBatch(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		ctx     context.Context
		events  []event.Event
		wantErr bool
		stored  int
	}{
		{"empty batch", context.Background(), nil, false, 0},
		{"stores all", context.Background(), []event.Event{ev(0), ev(time.Minute)}, false, 2},
		{"known key is ignored", context.Background(), []event.Event{keyed(ev(0), "k"), keyed(ev(time.Minute), "k")}, false, 1},
		{"cancelled context stores nothing", cancelled, []event.Event{ev(0)}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			err := s.AppendBatch(tt.ctx, tt.events)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			got, err := s.Events(context.Background(), base.Add(-time.Hour), base.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.stored {
				t.Errorf("stored %d events, want %d", len(got), tt.stored)
			}
		})
	}
}

func keyed(e event.Event, key string) event.Event {
	e.Key = key
	return e
}

func TestAppendBatchRollsBackWhenAnEventFails(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.root.ExecContext(ctx, `CREATE TRIGGER reject_marker BEFORE INSERT ON events
		WHEN NEW.branch = 'reject' BEGIN SELECT RAISE(ABORT, 'rejected'); END`); err != nil {
		t.Fatal(err)
	}
	rejected := ev(time.Minute)
	rejected.Branch = "reject"
	if err := s.AppendBatch(ctx, []event.Event{ev(0), rejected}); err == nil {
		t.Fatal("expected an error")
	}
	got, err := s.Events(ctx, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("stored %d events of a failed batch, want 0", len(got))
	}
}

func TestWALSideFilesAreOwnerOnly(t *testing.T) {
	s, path := openTemp(t)
	if err := s.Append(context.Background(), ev(0)); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("stat %s: %v", suffix, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %v, want 0600", suffix, got)
		}
	}
}

// TestConcurrentWritersLoseNoEvents runs many goroutines, each with its own
// connection to the same file, like the agent, the shell hook and the CLI.
func TestConcurrentWritersLoseNoEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticktack.db")
	ctx := context.Background()
	const writers, perWriter = 20, 10

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path)
			if err != nil {
				errs <- fmt.Errorf("writer %d open: %w", w, err)
				return
			}
			defer func() { _ = s.Close() }()
			for i := 0; i < perWriter; i++ {
				at := base.Add(time.Duration(w*perWriter+i) * time.Second)
				batch := []event.Event{
					{At: at, Source: "shell", Kind: event.KindContext, Repo: "r"},
					{At: at, Source: "shell", Kind: event.KindPresence},
				}
				if err := s.AppendBatch(ctx, batch); err != nil {
					errs <- fmt.Errorf("writer %d batch %d: %w", w, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Events(ctx, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := writers * perWriter * 2; len(got) != want {
		t.Errorf("stored %d events, want %d", len(got), want)
	}
}

// TestConcurrentFirstOpensNeverFail opens many fresh database files, each from
// many goroutines at once. The first open of a file switches it to WAL and
// creates the schema; none of the racing opens may fail with SQLITE_BUSY.
func TestConcurrentFirstOpensNeverFail(t *testing.T) {
	const files, openers = 25, 20
	dir := t.TempDir()
	for f := 0; f < files; f++ {
		path := filepath.Join(dir, fmt.Sprintf("fresh-%d.db", f))
		var wg sync.WaitGroup
		errs := make(chan error, openers)
		start := make(chan struct{})
		for o := 0; o < openers; o++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				s, err := Open(path)
				if err != nil {
					errs <- fmt.Errorf("file %d opener %d: %w", f, o, err)
					return
				}
				if err := s.Close(); err != nil {
					errs <- fmt.Errorf("file %d opener %d close: %w", f, o, err)
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	}
}
