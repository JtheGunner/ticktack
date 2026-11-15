package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/store"
)

type fakeSink struct {
	events []event.Event
	closed bool
}

func (f *fakeSink) Append(_ context.Context, e event.Event) error {
	f.events = append(f.events, e)
	return nil
}

func (f *fakeSink) Close() error { f.closed = true; return nil }

var hookTestNow = time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)

func newTestDeps(sink *fakeSink, gitOut string, gitErr error) (hookDeps, *bytes.Buffer) {
	var logs bytes.Buffer
	return hookDeps{
		OpenStore: func() (eventSink, error) { return sink, nil },
		Git: func(context.Context, string) (string, error) {
			return gitOut, gitErr
		},
		Now:    func() time.Time { return hookTestNow },
		Paused: func() bool { return false },
		Logf: func(format string, args ...any) {
			logs.WriteString(format)
		},
	}, &logs
}

func TestHookOutsideGitRepoEmitsNothing(t *testing.T) {
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "", errors.New("fatal: not a git repository"))
	if err := runHook(context.Background(), deps, "/tmp/nowhere"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no events, got %+v", sink.events)
	}
}

func TestHookMalformedGitOutputEmitsNothing(t *testing.T) {
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "HEAD\n", nil)
	// Only one line of output means no work tree (for example a bare repo).
	if err := runHook(context.Background(), deps, "/tmp/bare"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no events, got %+v", sink.events)
	}
}

func TestHookExtractsTicketFromBranch(t *testing.T) {
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "/work/repo\nfeature/PROJ-123-foo\n", nil)
	if err := runHook(context.Background(), deps, "/work/repo/sub"); err != nil {
		t.Fatal(err)
	}
	want := []event.Event{
		{At: hookTestNow, Source: "shell", Kind: event.KindContext, Repo: "/work/repo", Branch: "feature/PROJ-123-foo", Ticket: "PROJ-123"},
		{At: hookTestNow, Source: "shell", Kind: event.KindPresence},
	}
	if len(sink.events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(sink.events), len(want), sink.events)
	}
	for i := range want {
		if sink.events[i] != want[i] {
			t.Errorf("event %d: got %+v want %+v", i, sink.events[i], want[i])
		}
	}
	if !sink.closed {
		t.Error("store not closed")
	}
}

func TestHookDetachedHeadHasNoBranch(t *testing.T) {
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "/work/repo\nHEAD\n", nil)
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[0].Branch != "" || sink.events[0].Ticket != "" {
		t.Fatalf("unexpected events: %+v", sink.events)
	}
}

func TestHookRespectsPause(t *testing.T) {
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "/work/repo\nmain\n", nil)
	deps.Paused = func() bool { return true }
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("expected no events while paused, got %+v", sink.events)
	}
}

func TestExtractTicket(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"feature/PROJ-123-foo", "PROJ-123"},
		{"fix/proj-12", "PROJ-12"},
		{"fix/PROJ-1-and-PROJ-2", "PROJ-1"},
		{"main", ""},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			deps, _ := newTestDeps(&fakeSink{}, "/r\n"+tt.branch+"\n", nil)
			sink := &fakeSink{}
			deps.OpenStore = func() (eventSink, error) { return sink, nil }
			if err := runHook(context.Background(), deps, "/r"); err != nil {
				t.Fatal(err)
			}
			if len(sink.events) == 0 || sink.events[0].Ticket != tt.want {
				t.Fatalf("got %+v, want ticket %q", sink.events, tt.want)
			}
		})
	}
}

func TestHookSilentOnStoreError(t *testing.T) {
	deps, logs := newTestDeps(nil, "/work/repo\nmain\n", nil)
	deps.OpenStore = func() (eventSink, error) { return nil, errors.New("disk full") }
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatalf("store error must not surface: %v", err)
	}
	if logs.Len() == 0 {
		t.Error("store error was not logged")
	}

	// End to end through the command: unwritable data dir, no output, nil error.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(blocker, "sub"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"hook", "--cwd", t.TempDir()})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}

func TestHookCommandIsHidden(t *testing.T) {
	if !newHookCommand().Hidden {
		t.Fatal("hook command must be hidden")
	}
}

func TestHookCommandWritesEventsInRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"checkout", "-q", "-b", "feature/ABC-7-x"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	root := NewRootCommand()
	root.SetArgs([]string{"hook", "--cwd", repo})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(data, "ticktack", "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := st.Events(context.Background(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events: %+v", len(evs), evs)
	}
	if evs[0].Ticket != "ABC-7" || evs[0].Branch != "feature/ABC-7-x" || !strings.HasSuffix(evs[0].Repo, filepath.Base(repo)) {
		t.Errorf("unexpected context event: %+v", evs[0])
	}
}

func BenchmarkHook(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git not installed")
	}
	repo := b.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		b.Fatalf("git init: %v\n%s", err, out)
	}
	b.Setenv("XDG_DATA_HOME", b.TempDir())
	b.Setenv("XDG_STATE_HOME", b.TempDir())
	deps := defaultHookDeps()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runHook(context.Background(), deps, repo); err != nil {
			b.Fatal(err)
		}
	}
}
