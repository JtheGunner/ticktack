package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/logfile"
)

func TestAgentCommandFlags(t *testing.T) {
	cmd := newAgentCommand()
	f := cmd.Flags().Lookup("interval")
	if f == nil || f.DefValue != (30*time.Second).String() {
		t.Fatalf("interval flag missing or wrong default: %+v", f)
	}
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "--interval", "0s"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for non-positive interval")
	}
}

// A log rotation that cannot write its backup must not keep the agent from
// starting: the logger is returned regardless and still records the failure.
func TestNewAgentLoggerSurvivesFailedRotation(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ticktack.log")
	oversized := strings.Repeat("x", int(logfile.DefaultMaxBytes)+1) + "\n"
	if err := os.WriteFile(logPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	// The backup path is a directory, so the rotation cannot replace it.
	if err := os.Mkdir(logPath+".1", 0o700); err != nil {
		t.Fatal(err)
	}

	logger := newAgentLogger(logPath)
	if logger == nil {
		t.Fatal("no logger after a failed rotation")
	}
	logger.Printf("presence sample skipped: %v", "boom")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	appended := strings.TrimPrefix(string(data), oversized)
	if appended == string(data) {
		t.Fatal("failed rotation changed the existing log")
	}
	for _, want := range []string{"agent: ", "log rotation failed", "presence sample skipped: boom"} {
		if !strings.Contains(appended, want) {
			t.Fatalf("log after failed rotation lacks %q: %q", want, appended)
		}
	}
}

func TestNewAgentLoggerRotatesOversizedLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ticktack.log")
	oversized := strings.Repeat("x", int(logfile.DefaultMaxBytes)+1)
	if err := os.WriteFile(logPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	newAgentLogger(logPath)
	for path, wantSize := range map[string]int64{logPath: 0, logPath + ".1": logfile.DefaultMaxBytes} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != wantSize {
			t.Fatalf("%s is %d bytes, want %d", filepath.Base(path), info.Size(), wantSize)
		}
	}
}

func TestNewTitleCollectorIsOffUnlessEnabled(t *testing.T) {
	cfg, err := config.LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c := newTitleCollector(cfg, &title.Fake{}, "", "", time.Second, nil); c != nil {
		t.Fatal("collector must not exist while the feature is disabled")
	}
}

func TestNewTitleCollectorHonoursPauseFlag(t *testing.T) {
	cfg := config.Config{TitleHint: config.TitleHint{Enabled: true, Projects: map[string]string{"proj": "/work/proj"}}}
	pausePath := filepath.Join(t.TempDir(), "paused")
	src := &title.Fake{}
	src.Push("com.jetbrains.goland", "proj – main.go")
	src.Push("com.jetbrains.goland", "proj – main.go")
	sink := &titleRecorder{}
	c := newTitleCollector(cfg, src, pausePath, "", time.Second, nil)
	if c == nil {
		t.Fatal("collector expected")
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background(), sink)
	if len(sink.events) != 0 {
		t.Fatalf("stored while paused: %+v", sink.events)
	}
	if err := os.Remove(pausePath); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background(), sink)
	if len(sink.events) != 1 || sink.events[0].Repo != "/work/proj" || sink.events[0].Source != "title" {
		t.Fatalf("events = %+v", sink.events)
	}
}

func TestNewTitleCollectorRecordsTheProbeStatus(t *testing.T) {
	cfg := config.Config{TitleHint: config.TitleHint{Enabled: true}}
	statusPath := filepath.Join(t.TempDir(), "title-status.json")
	src := &title.Fake{}
	src.PushError(title.ErrPermission)
	c := newTitleCollector(cfg, src, "", statusPath, time.Second, nil)
	if c == nil {
		t.Fatal("collector expected")
	}
	c.Tick(context.Background(), &titleRecorder{})
	got, err := title.ReadStatus(statusPath)
	if err != nil || got.State != title.ProbeDenied {
		t.Fatalf("status = %+v, %v; want denied", got, err)
	}
}

type titleRecorder struct{ events []event.Event }

func (r *titleRecorder) Append(_ context.Context, e event.Event) error {
	r.events = append(r.events, e)
	return nil
}

func (r *titleRecorder) AppendBatch(ctx context.Context, events []event.Event) error {
	for _, e := range events {
		if err := r.Append(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

func TestPauseFlagCheckIsFailClosed(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "paused")
	if pauseFlagCheck(flag)() {
		t.Error("missing flag must not pause")
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !pauseFlagCheck(flag)() {
		t.Error("existing flag must pause")
	}
	if !pauseFlagCheck(filepath.Join(flag, "unreadable"))() {
		t.Error("a flag that cannot be read must count as paused")
	}
}
