package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/store"
)

func reviewTestDeps(t *testing.T, input string) (reviewDeps, *store.Store, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	out := &bytes.Buffer{}
	return reviewDeps{
		OpenStore: func() (reviewStore, error) { return nopCloseStore{st}, nil },
		LoadConfig: func() (config.Config, error) {
			cfg := config.Config{Location: time.UTC, GapThreshold: 5 * time.Minute, MinBlockDuration: 10 * time.Minute, Rounding: 15 * time.Minute}
			cfg.TicketPattern = regexp.MustCompile(config.DefaultTicketPattern)
			return cfg, nil
		},
		Now: func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) },
		In:  strings.NewReader(input),
		Out: out,
	}, st, out
}

func TestReviewCommandScriptedSession(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "t 1 def-2\nx\nc 9\ns 1 09:30\nABC-1\ny\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Review for 2026-01-05", "DEF-2", "explicit", "Unknown command", "no such entry", "split at 09:30", "2 confirmed", "ticktack sync"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	confs, err := st.Confirmations(context.Background(), "2026-01-05")
	if err != nil || len(confs) != 2 {
		t.Fatalf("confirmations = %+v err=%v", confs, err)
	}
}

func TestReviewCommandEmptyDayAndEOF(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "")
	if err := runReview(context.Background(), deps, "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No time recorded for 2026-02-01") || !strings.Contains(out.String(), "Nothing confirmed yet") {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := runReview(context.Background(), deps, "bad"); err == nil {
		t.Fatal("expected invalid day error")
	}
}
