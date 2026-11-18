package review

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

var previousDay = civil.Date{Year: 2026, Month: 1, Day: 4}

func appendEvents(t *testing.T, st *store.Store, events ...event.Event) {
	t.Helper()
	for _, e := range events {
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func manualSwitch(at time.Time, ticket string) event.Event {
	return event.Event{At: at, Source: sessionizer.ManualSource, Kind: event.KindContext, Ticket: ticket}
}

func shellContext(at time.Time, repo, branch string) event.Event {
	return event.Event{At: at, Source: "shell", Kind: event.KindContext, Repo: repo, Branch: branch}
}

func assertNoTime(t *testing.T, st *store.Store, day civil.Date) {
	t.Helper()
	d, err := LoadDay(context.Background(), st, testConfig(), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 0 || len(d.Items) != 0 || d.TotalMinutes != 0 {
		t.Fatalf("%s must stay empty, got blocks %+v items %+v", day, d.Blocks, d.Items)
	}
}

func TestLoadDayCarriesPinAcrossMidnight(t *testing.T) {
	st := openStore(t)
	evening := time.Date(2026, 1, 4, 23, 0, 0, 0, time.UTC)
	appendEvents(t, st, manualSwitch(evening, "PIN-1"))
	// The first shell prompt of the morning must not take the day over.
	seed(t, st, dayStart, 60, "")
	appendEvents(t, st, shellContext(dayStart.Add(10*time.Minute), "/work/api", "ABC-7-fix"))

	d, err := LoadDay(context.Background(), st, testConfig(), testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	b := d.Blocks[0]
	if b.Ticket != "PIN-1" || b.Tier != resolver.TierExplicit || b.Reason != "manual switch to PIN-1" {
		t.Errorf("block = %+v", b)
	}
	if !b.Start.Equal(dayStart) || !b.End.Equal(dayStart.Add(time.Hour)) {
		t.Errorf("block range = %v..%v, want the day's presence", b.Start, b.End)
	}
	if len(d.Items) != 1 || d.Items[0].Ticket != "PIN-1" || d.Items[0].Minutes != 60 {
		t.Errorf("items = %+v", d.Items)
	}
	assertNoTime(t, st, previousDay)
}

func TestLoadDayUsesBranchOfPreviousDayForMorningPresence(t *testing.T) {
	st := openStore(t)
	appendEvents(t, st, shellContext(time.Date(2026, 1, 4, 17, 0, 0, 0, time.UTC), "/work/api", "ABC-7-fix"))
	seed(t, st, dayStart, 60, "")
	appendEvents(t, st, shellContext(dayStart.Add(40*time.Minute), "/work/web", "XYZ-2-ui"))

	d, err := LoadDay(context.Background(), st, testConfig(), testDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 2 {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	morning := d.Blocks[0]
	if morning.Ticket != "ABC-7" || morning.Repo != "/work/api" || morning.Branch != "ABC-7-fix" {
		t.Errorf("morning block = %+v", morning)
	}
	if !morning.Start.Equal(dayStart) || !morning.End.Equal(dayStart.Add(40*time.Minute)) {
		t.Errorf("morning range = %v..%v", morning.Start, morning.End)
	}
	if d.Blocks[1].Ticket != "XYZ-2" {
		t.Errorf("second block = %+v", d.Blocks[1])
	}
	assertNoTime(t, st, previousDay)
}

func TestLoadDayCarriedContextCases(t *testing.T) {
	yesterday := func(hour int) time.Time { return time.Date(2026, 1, 4, hour, 0, 0, 0, time.UTC) }
	tests := []struct {
		name    string
		before  []event.Event
		ticket  string
		tier    resolver.Tier
		noRepo  bool
		reasons string
	}{
		{name: "no earlier context stays unassigned", tier: resolver.TierUnknown, noRepo: true, reasons: "no context signal"},
		{
			name:   "cleared pin restores the branch",
			before: []event.Event{shellContext(yesterday(15), "/work/api", "ABC-7-fix"), manualSwitch(yesterday(16), "PIN-1"), manualSwitch(yesterday(17), "")},
			ticket: "ABC-7", tier: resolver.TierExplicit,
		},
		{
			name:    "cleared pin without a branch stays unassigned",
			before:  []event.Event{manualSwitch(yesterday(16), "PIN-1"), manualSwitch(yesterday(17), "")},
			tier:    resolver.TierUnknown,
			noRepo:  true,
			reasons: "no context signal",
		},
		{
			name:   "pin older than the branch still pins",
			before: []event.Event{manualSwitch(yesterday(10), "PIN-1"), shellContext(yesterday(15), "/work/api", "ABC-7-fix")},
			ticket: "PIN-1", tier: resolver.TierExplicit,
		},
		{
			name:   "context from several days ago is carried",
			before: []event.Event{shellContext(time.Date(2025, 12, 20, 12, 0, 0, 0, time.UTC), "/work/api", "ABC-7-fix")},
			ticket: "ABC-7", tier: resolver.TierExplicit,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			appendEvents(t, st, tt.before...)
			seed(t, st, dayStart, 30, "")
			d, err := LoadDay(context.Background(), st, testConfig(), testDay)
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Blocks) != 1 {
				t.Fatalf("blocks = %+v", d.Blocks)
			}
			b := d.Blocks[0]
			if b.Ticket != tt.ticket || b.Tier != tt.tier {
				t.Errorf("block = %+v", b)
			}
			if tt.noRepo && b.Repo != "" {
				t.Errorf("repo = %q, want none", b.Repo)
			}
			if tt.reasons != "" && b.Reason != tt.reasons {
				t.Errorf("reason = %q, want %q", b.Reason, tt.reasons)
			}
			if !b.Start.Equal(dayStart) {
				t.Errorf("block starts %v, before or after the first presence %v", b.Start, dayStart)
			}
		})
	}
}

func TestLoadDayInputKeepsPresenceInsideTheDay(t *testing.T) {
	st := openStore(t)
	// Presence late on the previous day must not leak into the day.
	seed(t, st, time.Date(2026, 1, 4, 22, 0, 0, 0, time.UTC), 30, "OLD-1")
	seed(t, st, dayStart, 30, "")
	from := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	in, err := LoadDayInput(context.Background(), st, testConfig(), from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	carried := 0
	for _, e := range in.Events {
		if !e.At.Before(from) {
			continue
		}
		carried++
		if e.Kind != event.KindContext {
			t.Errorf("event before the day is not context: %+v", e)
		}
	}
	if carried != 1 {
		t.Errorf("carried %d events, want the one context event", carried)
	}
	if len(in.Blocks) != 1 || in.Blocks[0].Ticket != "OLD-1" || !in.Blocks[0].Start.Equal(dayStart) {
		t.Errorf("blocks = %+v", in.Blocks)
	}
}
