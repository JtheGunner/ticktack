package cli

import (
	"context"
	"reflect"
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
	// The pin is from the day before: it still decides the ticket, but only
	// as a suggestion.
	for _, want := range []string{"08:00-08:40", "PIN-1", "suggested", "context unconfirmed for 9h"} {
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
	// The branch was last seen 15 hours ago, beyond context_max_age.
	for _, want := range []string{"08:00-08:40", "ABC-7", "suggested", "context unconfirmed for 15h"} {
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
		if !reflect.DeepEqual(d.Blocks[i], derived.Blocks[i]) {
			t.Errorf("block %d differs: today %+v, review %+v", i+1, derived.Blocks[i], d.Blocks[i])
		}
	}
}

// An app-bound window-title hint only applies while its app is frontmost and
// is never carried into a day, so status must not let one from an earlier day
// decide the current context either.
func TestStatusTodayAndReviewIgnoreTitleHintOfThePreviousDay(t *testing.T) {
	const ide = "com.example.ide"
	deps, st, out := newDailyEnv(t, "")
	ctx := context.Background()
	evening := time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC)
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st,
		shellContext(evening, "/work/api", "XYZ-7-fix"),
		event.Event{At: evening.Add(time.Minute), Source: "presence", Kind: event.KindPresence, App: ide},
		event.Event{At: evening.Add(2 * time.Minute), Source: sessionizer.TitleSource, Kind: event.KindContext, App: ide, Ticket: "ABC-1"},
	)
	// Today the IDE is frontmost, but its title yields no hint.
	for _, e := range morningPresence(morning, 40) {
		e.App = ide
		mustAppend(t, st, e)
	}

	if err := runStatus(ctx, deps); err != nil {
		t.Fatal(err)
	}
	status := out.String()
	for _, want := range []string{"XYZ-7", "/work/api", "last seen"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "ABC-1") {
		t.Errorf("status follows a title hint from the previous day:\n%s", status)
	}

	out.Reset()
	if err := runToday(ctx, deps, ""); err != nil {
		t.Fatal(err)
	}
	today := out.String()
	if !strings.Contains(today, "XYZ-7") || strings.Contains(today, "ABC-1") {
		t.Errorf("today disagrees with status:\n%s", today)
	}

	d, err := review.LoadDay(ctx, st, loadTestConfig(t, deps), civil.Date{Year: 2026, Month: 3, Day: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].Ticket != "XYZ-7" {
		t.Errorf("review blocks = %+v", d.Blocks)
	}
}

// A title hint of the current day still decides while its app is frontmost.
func TestStatusFollowsTitleHintOfTheCurrentDay(t *testing.T) {
	const ide = "com.example.ide"
	deps, st, out := newDailyEnv(t, "")
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st,
		shellContext(morning, "/work/api", "XYZ-7-fix"),
		event.Event{At: morning.Add(time.Minute), Source: "presence", Kind: event.KindPresence, App: ide},
		event.Event{At: morning.Add(2 * time.Minute), Source: sessionizer.TitleSource, Kind: event.KindContext, App: ide, Ticket: "ABC-1"},
	)
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ABC-1") {
		t.Errorf("status ignores the title hint of the current day:\n%s", out)
	}
}

// Presence is reported from the lookback even when the current day has none.
func TestStatusReportsPresenceOfAnEarlierDay(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	evening := time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC)
	mustAppend(t, st,
		shellContext(evening, "/work/api", "XYZ-7-fix"),
		event.Event{At: evening, Source: "presence", Kind: event.KindPresence},
	)
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XYZ-7", "last seen", "17h0m0s ago"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
}

func TestExplainDatesDecidingEventFromBeforeTheDay(t *testing.T) {
	tests := []struct {
		name    string
		carried event.Event
		want    string
	}{
		{
			name:    "branch of an earlier day",
			carried: shellContext(time.Date(2026, 2, 27, 17, 5, 9, 0, time.UTC), "/work/api", "ABC-7-fix"),
			want:    "context event from shell at 2026-02-27 17:05:09 (",
		},
		{
			name: "pin of the previous evening",
			carried: event.Event{
				At: time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "PIN-1",
			},
			want: "context event from manual at 2026-03-01 23:00:00 (",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, st, out := newDailyEnv(t, "")
			mustAppend(t, st, tt.carried)
			seedPresence(t, st, time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC), 30)

			got := runExplainOut(t, deps, out, "1")
			if !strings.Contains(got, tt.want) {
				t.Errorf("explain lacks %q:\n%s", tt.want, got)
			}
			// Times inside the explained day stay clock-only.
			if !strings.Contains(got, "between 08:00:00 and 08:30:00") {
				t.Errorf("explain dates a time of the explained day:\n%s", got)
			}
		})
	}
}

func TestExplainNamesTheAgeOfUnconfirmedContext(t *testing.T) {
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		context event.Event
		minutes int
		block   string
		want    []string
	}{
		{
			name:    "branch older than the limit within the day",
			context: shellContext(morning, "/work/api", "ABC-7-fix"),
			minutes: 150,
			block:   "2",
			want: []string{
				"suggested",
				"context event from shell at 08:00:00 (",
				"ticket ABC-7 found in the branch name",
				"context unconfirmed for 2h: last confirmed at 08:00:00, limit 2h (context_max_age)",
			},
		},
		{
			name:    "branch carried from the day before",
			context: shellContext(time.Date(2026, 3, 1, 17, 0, 0, 0, time.UTC), "/work/api", "ABC-7-fix"),
			minutes: 30,
			block:   "1",
			want:    []string{"suggested", "context unconfirmed for 15h: last confirmed at 2026-03-01 17:00:00, limit 2h (context_max_age)"},
		},
		{
			name: "pin of the previous evening",
			context: event.Event{
				At: time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC), Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: "PIN-1",
			},
			minutes: 30,
			block:   "1",
			want: []string{
				"suggested",
				"manual switch to PIN-1",
				"context unconfirmed for 9h: the manual switch was set at 2026-03-01 23:00:00 and is exempt from context_max_age only on that day",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, st, out := newDailyEnv(t, "")
			mustAppend(t, st, tt.context)
			seedPresence(t, st, morning, tt.minutes)

			got := runExplainOut(t, deps, out, tt.block)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("explain lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestExplainDoesNotMentionAgeForConfirmedContext(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(morning, "/work/api", "ABC-7-fix"))
	seedPresence(t, st, morning, 150)

	if got := runExplainOut(t, deps, out, "1"); strings.Contains(got, "unconfirmed") {
		t.Errorf("explain names an age for confirmed context:\n%s", got)
	}
}

func TestExplainKeepsClockTimeForEventOfTheDay(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	morning := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(morning, "/work/api", "ABC-7-fix"))
	seedPresence(t, st, morning, 30)

	got := runExplainOut(t, deps, out, "1")
	if !strings.Contains(got, "context event from shell at 08:00:00 (") {
		t.Errorf("explain output:\n%s", got)
	}
}
