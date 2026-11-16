package cli

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
)

func TestScanJobPassesConfiguredTicketPattern(t *testing.T) {
	repo := fakeRepoDir(t)
	got := make(chan *regexp.Regexp, 1)
	scan := func(_ context.Context, _ string, _, _ time.Time, pattern *regexp.Regexp) ([]event.Event, error) {
		select {
		case got <- pattern:
		default:
		}
		return nil, nil
	}
	custom := regexp.MustCompile(`[A-Z]+_\d+`)
	cfg := config.Config{RepoPaths: []string{repo}, Location: time.UTC, TicketPattern: custom}
	job := newScanJob(cfg, "/home/u", &syncSink{}, scan, func() bool { return false }, time.Hour,
		func() time.Time { return dailyNow }, t.Logf)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { job(ctx); close(done) }()
	select {
	case pattern := <-got:
		if pattern != custom {
			t.Errorf("scanner got pattern %v, want the configured one", pattern)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not run")
	}
	cancel()
	<-done
}

func TestScanUsesConfiguredTicketPattern(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	load := env.deps.LoadConfig
	env.deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.TicketPattern = regexp.MustCompile(`ABC-\d`)
		return cfg, err
	}
	if err := env.scan(""); err != nil {
		t.Fatal(err)
	}
	events := env.events(t)
	if len(events) == 0 {
		t.Fatal("no events stored")
	}
	for _, e := range events {
		if e.Ticket != "ABC-1" {
			t.Errorf("ticket = %q, want ABC-1 from the configured pattern", e.Ticket)
		}
	}
}
