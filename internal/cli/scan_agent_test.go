package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
)

type syncSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *syncSink) Append(_ context.Context, e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *syncSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestRunWithBackgroundWaitsForEveryJobBeforeReturning(t *testing.T) {
	var finished atomic.Int32
	slowJob := func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a final append that must not be cut off
		finished.Add(1)
	}
	mainErr := errors.New("presence agent stopped")
	err := runWithBackground(context.Background(), []backgroundJob{slowJob, nil, slowJob},
		func(context.Context) error { return mainErr })
	if !errors.Is(err, mainErr) {
		t.Fatalf("err = %v, want the main error", err)
	}
	if got := finished.Load(); got != 2 {
		t.Fatalf("%d of 2 jobs finished before runWithBackground returned", got)
	}
}

func TestRunWithBackgroundStopsJobsWhenParentIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var finished atomic.Bool
	job := func(ctx context.Context) {
		<-ctx.Done()
		finished.Store(true)
	}
	err := runWithBackground(ctx, []backgroundJob{job}, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return nil
	})
	if err != nil || !finished.Load() {
		t.Fatalf("err = %v, finished = %v", err, finished.Load())
	}
}

func fakeRepoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNewScanJobIsOffWithoutIntervalOrRepos(t *testing.T) {
	now := func() time.Time { return dailyNow }
	cfg := config.Config{RepoPaths: []string{"/work/a"}, Location: time.UTC}
	if newScanJob(cfg, "/home/u", &syncSink{}, nil, func() bool { return false }, 0, now, t.Logf) != nil {
		t.Error("interval 0 must not start a scan")
	}
	cfg.RepoPaths = nil
	if newScanJob(cfg, "/home/u", &syncSink{}, nil, func() bool { return false }, time.Minute, now, t.Logf) != nil {
		t.Error("no repo_paths must not start a scan")
	}
}

func TestScanJobScansAtStartAndOnEveryTick(t *testing.T) {
	repo := fakeRepoDir(t)
	var calls atomic.Int32
	scan := func(_ context.Context, path string, _, _ time.Time) ([]event.Event, error) {
		n := calls.Add(1)
		return []event.Event{{At: dailyNow, Source: "git", Kind: event.KindContext, Repo: path, Key: fmt.Sprintf("k%d", n)}}, nil
	}
	sink := &syncSink{}
	cfg := config.Config{RepoPaths: []string{repo}, Location: time.UTC}
	job := newScanJob(cfg, "/home/u", sink, scan, func() bool { return false }, 10*time.Millisecond,
		func() time.Time { return dailyNow }, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { job(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for sink.count() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d scans ran", calls.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestScanJobDoesNothingWhilePaused(t *testing.T) {
	repo := fakeRepoDir(t)
	var calls atomic.Int32
	scan := func(context.Context, string, time.Time, time.Time) ([]event.Event, error) {
		calls.Add(1)
		return []event.Event{{At: dailyNow, Source: "git", Kind: event.KindContext, Key: "k"}}, nil
	}
	var paused atomic.Bool
	paused.Store(true)
	sink := &syncSink{}
	cfg := config.Config{RepoPaths: []string{repo}, Location: time.UTC}
	job := newScanJob(cfg, "/home/u", sink, scan, paused.Load, 10*time.Millisecond,
		func() time.Time { return dailyNow }, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { job(ctx); close(done) }()

	time.Sleep(80 * time.Millisecond)
	if calls.Load() != 0 || sink.count() != 0 {
		t.Fatalf("paused job scanned %d times and stored %d events", calls.Load(), sink.count())
	}
	paused.Store(false)
	deadline := time.After(5 * time.Second)
	for sink.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("job did not resume after the pause flag was removed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestScanJobKeepsRunningWhenOneRepositoryFails(t *testing.T) {
	good := fakeRepoDir(t)
	scan := func(_ context.Context, path string, _, _ time.Time) ([]event.Event, error) {
		return []event.Event{{At: dailyNow, Source: "git", Kind: event.KindContext, Repo: path, Key: "k"}}, nil
	}
	sink := &syncSink{}
	cfg := config.Config{RepoPaths: []string{"/does/not/exist", good}, Location: time.UTC}
	job := newScanJob(cfg, "/home/u", sink, scan, func() bool { return false }, time.Hour,
		func() time.Time { return dailyNow }, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { job(ctx); close(done) }()
	deadline := time.After(5 * time.Second)
	for sink.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("good repository was not scanned")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
