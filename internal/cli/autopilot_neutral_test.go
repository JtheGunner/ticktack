package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// neutralDeps is the standard-task test setup (GEN) switched to the autopilot
// with a mixed day on 2026-01-05: 09:00-10:00 repository time without a
// ticket, 10:00-11:00 explicit ABC-1, 11:00-12:00 learned ABC-2.
func neutralDeps(t *testing.T) (syncDeps, *provisioningSink) {
	t.Helper()
	deps, prov := standardTaskDeps(t)
	deps = asAutopilot(deps, 7)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto.Enabled = true
		cfg.DailyCap = 10 * time.Hour
		return cfg, path, err
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		ten := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
		eleven := ten.Add(time.Hour)
		if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/work/two", Ticket: "ABC-2", CreatedAt: ten}); err != nil {
			t.Fatal(err)
		}
		events := []event.Event{
			{At: ten, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"},
			{At: eleven, Source: "test", Kind: event.KindContext, Repo: "/work/two", Branch: "main"},
		}
		for m := 1; m <= 120; m++ {
			events = append(events, event.Event{At: ten.Add(time.Duration(m) * time.Minute), Source: "test", Kind: event.KindPresence})
		}
		for _, e := range events {
			if err := st.Append(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
	})
	return deps, prov
}

func TestAutopilotBooksTicketAndNeutralInOneRun(t *testing.T) {
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	abc1, ok1 := prov.items["ABC-1/1"]
	neutral, ok2 := prov.items["GEN-5/1"]
	if !ok1 || !ok2 || len(prov.items) != 2 {
		t.Fatalf("items = %v\n%s", prov.items, out)
	}
	if abc1.Minutes != 60 || neutral.Minutes != 120 {
		t.Errorf("minutes ABC-1 %d, GEN-5 %d", abc1.Minutes, neutral.Minutes)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		d, found, err := st.LastDigest(ctx)
		if err != nil || !found || !strings.Contains(d.Text, "uncertain time on standard task") {
			t.Errorf("digest lacks the neutral line: %+v %v %v", d, found, err)
		}
	})
}

func TestAutopilotNeutralRerunIsANoOp(t *testing.T) {
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	creates, updates, deletes := countCalls(prov.recordingSink, "create"), countCalls(prov.recordingSink, "update"), countCalls(prov.recordingSink, "delete")
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if countCalls(prov.recordingSink, "create") != creates || countCalls(prov.recordingSink, "update") != updates || countCalls(prov.recordingSink, "delete") != deletes {
		t.Fatalf("second run changed bookings: %v", prov.calls)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if last, _, _ := st.LastDigest(ctx); last.ID != 1 {
			t.Errorf("an idle run stored a digest (id %d)", last.ID)
		}
	})
}

func TestAutopilotNeutralRerunKeepsTheNeutralBooking(t *testing.T) {
	// The first run books on the real key; a run in which the task is no
	// longer found by lookup must still keep it (known default task).
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(prov.recordingSink, "delete"); n != 0 {
		t.Fatalf("deleted %d item(s)", n)
	}
	if _, ok := prov.items["GEN-5/1"]; !ok {
		t.Fatalf("neutral booking lost: %v", prov.items)
	}
}

func TestAutopilotWithoutDefaultIssueSkipsAsBefore(t *testing.T) {
	deps, prov := neutralDeps(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.DefaultIssue = nil
		return cfg, path, err
	}
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// ABC-1 is fully explicit; ABC-2 is learned and the rest has no ticket.
	if _, ok := prov.items["ABC-1/1"]; !ok || len(prov.items) != 1 {
		t.Fatalf("items = %v\n%s", prov.items, out)
	}
	if strings.Contains(out, "uncertain time on standard task") {
		t.Errorf("neutral line without default issue:\n%s", out)
	}
}

func TestGatedModeLeavesUncertainTimeSkipped(t *testing.T) {
	deps, prov := neutralDeps(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto = config.Auto{Enabled: true, TrustedDays: 1}
		return cfg, path, err
	}
	seedCleanDays(t, deps, 1)
	prov.existing["GEN"] = "GEN-5"
	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := prov.items["ABC-2/1"]; ok {
		t.Errorf("gated mode booked the learned item: %v", prov.items)
	}
	if strings.Contains(out, "uncertain time on standard task") {
		t.Errorf("gated mode moved time to the neutral item:\n%s", out)
	}
}

func TestAutopilotOverCapBooksNothing(t *testing.T) {
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.DailyCap = 2 * time.Hour
		return cfg, path, err
	}
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(prov.items) != 0 || len(prov.calls) != 0 {
		t.Fatalf("a capped day was touched: %v %v", prov.items, prov.calls)
	}
	if !strings.Contains(out, auto.ReasonOverCap) {
		t.Errorf("digest does not name the cap:\n%s", out)
	}
}

func TestAutopilotNeutralWithoutConsentOrTaskBooksTheTicketsOnly(t *testing.T) {
	deps, prov := neutralDeps(t)
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := prov.items["ABC-1/1"]; !ok || len(prov.items) != 1 || len(prov.created) != 0 {
		t.Fatalf("items %v created %v\n%s", prov.items, prov.created, out)
	}
	if !strings.Contains(out, "standard task does not exist yet") {
		t.Errorf("digest does not say why the neutral time was skipped:\n%s", out)
	}
}

func TestAutopilotNeutralCreatesTheTaskWithConsent(t *testing.T) {
	deps, prov := neutralDeps(t)
	consentTo(t, deps, "GEN")
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(prov.created) != 1 {
		t.Fatalf("created = %v", prov.created)
	}
	if _, ok := prov.items["GEN-1/1"]; !ok {
		t.Errorf("items = %v", prov.items)
	}
}

func TestAutopilotNeutralMergesWithAnAlreadyNeutralItem(t *testing.T) {
	// The day already derives neutral time of its own (the project default
	// rule); uncertain time joins it into one booking on the task.
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if err := st.SetDefaultTask(ctx, "GEN", "GEN-5"); err != nil {
			t.Fatal(err)
		}
	})
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(prov.items) != 2 || prov.items["GEN-5/1"].Minutes != 120 {
		t.Fatalf("items = %v", prov.items)
	}
}

func TestAutopilotRemovesTheBookingOfATicketWhoseTimeMovedToNeutral(t *testing.T) {
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	// An earlier run booked the learned ticket ABC-2 as a whole.
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		items := []sessionizer.DayItem{{Ticket: "ABC-2", Date: day, Minutes: 60, Type: sessionizer.DefaultWorkItemType, Certainty: sessionizer.Certainty{Rule: 60}}}
		if _, err := ledger.Sync(ctx, items, prov, ledger.SyncOptions{Repo: st, Now: deps.Now}); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok := prov.items["ABC-2/1"]; !ok {
		t.Fatalf("setup: %v", prov.items)
	}
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, ok := prov.items["ABC-2/1"]; ok {
		t.Errorf("the booking of the moved ticket stayed: %v", prov.items)
	}
	if prov.items["GEN-5/1"].Minutes != 120 || prov.items["ABC-1/1"].Minutes != 60 {
		t.Errorf("items = %v", prov.items)
	}
}

func TestAutopilotLeavesAConfirmedDayAloneWithNeutralConfigured(t *testing.T) {
	deps, prov := neutralDeps(t)
	prov.existing["GEN"] = "GEN-5"
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if err := st.Confirm(ctx, store.Confirmation{Day: "2026-01-05", Issue: "ABC-1", Type: sessionizer.DefaultWorkItemType, Minutes: 60, ConfirmedAt: deps.Now()}); err != nil {
			t.Fatal(err)
		}
	})
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(prov.items) != 0 {
		t.Fatalf("a day of the user was booked: %v", prov.items)
	}
}

// bookItemsEarlier books items through the ledger as an earlier run would have.
func bookItemsEarlier(t *testing.T, deps syncDeps, prov *provisioningSink, items ...sessionizer.DayItem) {
	t.Helper()
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if _, err := ledger.Sync(ctx, items, prov, ledger.SyncOptions{Repo: st, Now: deps.Now}); err != nil {
			t.Fatal(err)
		}
	})
}

// addMixedTicket adds 12:00-13:00 explicit and 13:00-14:00 learned time for ABC-3.
func addMixedTicket(t *testing.T, deps syncDeps) {
	t.Helper()
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		noon := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
		if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/work/three", Ticket: "ABC-3", CreatedAt: noon}); err != nil {
			t.Fatal(err)
		}
		events := []event.Event{
			{At: noon, Source: "test", Kind: event.KindContext, Ticket: "ABC-3"},
			{At: noon.Add(time.Hour), Source: "test", Kind: event.KindContext, Repo: "/work/three", Branch: "main"},
		}
		for m := 1; m <= 120; m++ {
			events = append(events, event.Event{At: noon.Add(time.Duration(m) * time.Minute), Source: "test", Kind: event.KindPresence})
		}
		for _, e := range events {
			if err := st.Append(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestAutopilotKeepsBookedTicketsWhenTheNeutralTimeCannotBeBooked(t *testing.T) {
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	earlier := func(ticket string, minutes int) sessionizer.DayItem {
		return sessionizer.DayItem{Ticket: ticket, Date: day, Minutes: minutes, Type: sessionizer.DefaultWorkItemType, Certainty: sessionizer.Certainty{Rule: minutes}}
	}
	tests := []struct {
		name  string
		setup func(prov *provisioningSink)
	}{
		{"no consent and no task", func(*provisioningSink) {}},
		{"lookup of the standard task fails", func(prov *provisioningSink) {
			prov.findErrs = map[string]error{"GEN": errors.New("youtrack unreachable")}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, prov := neutralDeps(t)
			addMixedTicket(t, deps)
			tt.setup(prov)
			// ABC-2 moves entirely, ABC-3 partly; both were booked before.
			bookItemsEarlier(t, deps, prov, earlier("ABC-2", 60), earlier("ABC-3", 120))
			out, err := runAutopilotByHand(t, deps, "", false)
			if err != nil && !strings.Contains(tt.name, "fails") {
				t.Fatalf("%v\n%s", err, out)
			}
			if prov.items["ABC-2/1"].Minutes != 60 {
				t.Errorf("the booking of ABC-2 was lost: %v\n%s", prov.items, out)
			}
			if prov.items["ABC-3/1"].Minutes != 120 {
				t.Errorf("the booking of ABC-3 was reduced: %v\n%s", prov.items, out)
			}
			if prov.items["ABC-1/1"].Minutes != 60 {
				t.Errorf("explicit ABC-1 not booked: %v", prov.items)
			}
			for key := range prov.items {
				if strings.HasPrefix(key, "GEN") {
					t.Errorf("neutral time booked: %v", prov.items)
				}
			}
		})
	}
}
