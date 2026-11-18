package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func morningPresence(start time.Time, minutes int) []event.Event {
	var out []event.Event
	for m := 0; m <= minutes; m++ {
		out = append(out, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "presence", Kind: event.KindPresence})
	}
	return out
}

func loadTestConfig(t *testing.T, deps dailyDeps) config.Config {
	t.Helper()
	cfg, err := deps.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTodayStatusAndReviewAgreeOnPinFromThePreviousEvening(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	ctx := context.Background()
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, event.Event{
		At: time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "PIN-1",
	})
	mustAppend(t, st, morningPresence(morning, 40)...)
	mustAppend(t, st, shellContext(morning.Add(5*time.Minute), "/work/api", "ABC-7-fix"))

	if err := runToday(ctx, deps, ""); err != nil {
		t.Fatal(err)
	}
	today := out.String()
	for _, want := range []string{"08:00-08:40", "PIN-1", "manual switch to PIN-1"} {
		if !strings.Contains(today, want) {
			t.Errorf("today output lacks %q:\n%s", want, today)
		}
	}
	if strings.Contains(today, "ABC-7") {
		t.Errorf("today attributes pinned time to the branch:\n%s", today)
	}

	out.Reset()
	if err := runStatus(ctx, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "manual switch to PIN-1") {
		t.Errorf("status disagrees with today:\n%s", out)
	}

	cfg := loadTestConfig(t, deps)
	d, err := review.LoadDay(ctx, st, cfg, civil.Date{Year: 2026, Month: 3, Day: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].Ticket != "PIN-1" || !d.Blocks[0].Start.Equal(morning) {
		t.Errorf("review blocks = %+v", d.Blocks)
	}

	out.Reset()
	if err := runToday(ctx, deps, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No time recorded for 2026-03-01") {
		t.Errorf("the pin created time on the previous day:\n%s", out)
	}
}

func TestTodayUsesBranchOfPreviousDayBeforeTheFirstPrompt(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC), "/work/api", "ABC-7-fix"))
	mustAppend(t, st, morningPresence(morning, 40)...)

	if err := runToday(context.Background(), deps, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"08:00-08:40", "ABC-7", "explicit"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("today output lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusSeesPinOlderThanItsLookback(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	mustAppend(t, st,
		event.Event{At: dailyNow.Add(-30 * 24 * time.Hour), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "PIN-1"},
		shellContext(dailyNow.Add(-5*time.Minute), "/work/api", "ABC-7-fix"),
	)
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PIN-1", "manual switch to PIN-1", "/work/api"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestDeriveDayAndLoadDayShareBlocks(t *testing.T) {
	deps, st, _ := newDailyEnv(t, "")
	ctx := context.Background()
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC), "/work/api", "ABC-7-fix"))
	mustAppend(t, st, morningPresence(morning, 40)...)
	mustAppend(t, st, shellContext(morning.Add(20*time.Minute), "/work/web", "XYZ-2-ui"))

	cfg := loadTestConfig(t, deps)
	derived, err := deriveDay(ctx, nopCloseStore{st}, cfg, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	d, err := review.LoadDay(ctx, st, cfg, civil.Date{Year: 2026, Month: 3, Day: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(derived.Blocks) != 2 || len(d.Blocks) != len(derived.Blocks) {
		t.Fatalf("today blocks %+v, review blocks %+v", derived.Blocks, d.Blocks)
	}
	for i := range d.Blocks {
		if d.Blocks[i] != derived.Blocks[i] {
			t.Errorf("block %d differs: today %+v, review %+v", i+1, derived.Blocks[i], d.Blocks[i])
		}
	}
}
