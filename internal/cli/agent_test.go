package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
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

func TestNewTitleCollectorIsOffUnlessEnabled(t *testing.T) {
	cfg, err := config.LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if c := newTitleCollector(cfg, &title.Fake{}, &titleRecorder{}, "", time.Second, nil); c != nil {
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
	c := newTitleCollector(cfg, src, sink, pausePath, time.Second, nil)
	if c == nil {
		t.Fatal("collector expected")
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background())
	if len(sink.events) != 0 {
		t.Fatalf("stored while paused: %+v", sink.events)
	}
	if err := os.Remove(pausePath); err != nil {
		t.Fatal(err)
	}
	c.Tick(context.Background())
	if len(sink.events) != 1 || sink.events[0].Repo != "/work/proj" || sink.events[0].Source != "title" {
		t.Fatalf("events = %+v", sink.events)
	}
}

type titleRecorder struct{ events []event.Event }

func (r *titleRecorder) Append(_ context.Context, e event.Event) error {
	r.events = append(r.events, e)
	return nil
}
