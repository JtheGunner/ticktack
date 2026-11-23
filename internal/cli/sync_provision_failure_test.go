package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sink"
)

var errYouTrackDown = errors.New("youtrack down")

// seedTicketHour adds an hour on ABC-1 to the day standardTaskDeps seeds, so
// the day has time on a standard task and time on an independent ticket.
func seedTicketHour(t *testing.T, deps syncDeps) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	start := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSyncBooksTheOtherItemsWhenAStandardTaskProjectFails(t *testing.T) {
	tests := []struct {
		name   string
		fail   func(p *provisioningSink)
		answer string
		// wantPrompt says whether sync may ask to create the task: after a
		// failed lookup it must not, the task may well exist.
		wantPrompt bool
		wantCause  string
	}{
		{"lookup fails", func(p *provisioningSink) { p.findErrs = map[string]error{"GEN": errYouTrackDown} }, "y\n", false, "look up the standard task of GEN: youtrack down"},
		{"create fails", func(p *provisioningSink) { p.createErrs = map[string]error{"GEN": errYouTrackDown} }, "y\n", true, "create the standard task of GEN: youtrack down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, prov := standardTaskDeps(t)
			seedTicketHour(t, deps)
			confirmDay(t, deps)
			tt.fail(prov)

			out, _, err := execCommandWithInput(t, deps, tt.answer, "sync", "--day", "2026-01-05")
			if err != nil {
				t.Fatalf("a failing project must not abort the sync: %v\n%s", err, out)
			}
			if _, ok := prov.items["ABC-1/1"]; !ok || len(prov.items) != 1 {
				t.Errorf("booked %v, want ABC-1 only", prov.items)
			}
			if len(prov.created) != 0 {
				t.Errorf("created = %v, want nothing", prov.created)
			}
			if asked := strings.Contains(out, "Create standard task in"); asked != tt.wantPrompt {
				t.Errorf("asked to create = %v, want %v:\n%s", asked, tt.wantPrompt, out)
			}
			for _, want := range []string{"GEN:default:2026-01-05", "failed: " + tt.wantCause, "failed 1, not sent 0", "Created 1"} {
				if !strings.Contains(out, want) {
					t.Errorf("report lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "not created") {
				t.Errorf("a failed project is reported as declined:\n%s", out)
			}
		})
	}
}

func TestSyncDryRunListsAFailedStandardTaskProject(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	confirmDay(t, deps)
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "failed: look up the standard task of GEN") || !strings.Contains(out, "1 failed") {
		t.Errorf("plan does not list the failed project:\n%s", out)
	}
	if strings.Contains(out, "Would create") || strings.Contains(out, "No time to book") {
		t.Errorf("plan treats the failed project as missing or empty:\n%s", out)
	}
}

func TestSyncStopErrorWhileProvidingStandardTasksMarksEverythingNotSent(t *testing.T) {
	for name, stop := range map[string]error{"unauthorized": sink.ErrUnauthorized, "rate limited": sink.ErrRateLimited} {
		t.Run(name, func(t *testing.T) {
			deps, prov := standardTaskDeps(t)
			seedTicketHour(t, deps)
			confirmDay(t, deps)
			prov.findErrs = map[string]error{"GEN": stop}

			out, _, err := execCommandWithInput(t, deps, "y\n", "sync", "--day", "2026-01-05")
			if !errors.Is(err, stop) {
				t.Fatalf("err = %v, want the stop error %v\n%s", err, stop, out)
			}
			if len(prov.items) != 0 || len(prov.created) != 0 || len(prov.calls) != 0 {
				t.Errorf("a stopped run acted: booked %v created %v calls %v", prov.items, prov.created, prov.calls)
			}
			if got := strings.Count(out, "not-sent"); got != 2 {
				t.Errorf("%d item(s) listed as not sent, want 2:\n%s", got, out)
			}
			if !strings.Contains(out, "failed 0, not sent 2") {
				t.Errorf("summary does not count the unsent items:\n%s", out)
			}
		})
	}
}

// A lookup that fails for a project whose time is already booked must leave
// that booking alone: the failure says nothing about the day.
func TestSyncKeepsAnEarlierBookingWhenItsStandardTaskLookupFails(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	prov.existing["GEN"] = "GEN-5"
	confirmDay(t, deps)
	if out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05"); err != nil {
		t.Fatalf("first sync: %v\n%s", err, out)
	}
	if _, ok := prov.items["GEN-5/1"]; !ok {
		t.Fatalf("time was not booked on GEN-5: %v", prov.items)
	}
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}
	prov.calls = nil

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, out)
	}
	if _, ok := prov.items["GEN-5/1"]; !ok || len(prov.calls) != 0 {
		t.Errorf("the earlier booking was touched: items %v calls %v", prov.items, prov.calls)
	}
	if !strings.Contains(out, "failed: look up the standard task of GEN") || strings.Contains(out, "deleted  ") {
		t.Errorf("report:\n%s", out)
	}
}

func TestAutoReportsAFailedStandardTaskProjectAsFailed(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	consentTo(t, deps, "GEN")
	seedTicketHour(t, deps)
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}

	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if _, ok := prov.items["ABC-1/1"]; !ok || len(prov.items) != 1 {
		t.Errorf("booked %v, want ABC-1 only\n%s", prov.items, out)
	}
	if len(prov.created) != 0 {
		t.Errorf("auto created a task after a failed lookup: %v", prov.created)
	}
	if !strings.Contains(out, "failed") || !strings.Contains(out, "youtrack down") || !strings.Contains(out, "failed 1") {
		t.Errorf("digest does not list the failure:\n%s", out)
	}
	if strings.Contains(out, auto.ReasonStandardTask) {
		t.Errorf("digest claims the standard task does not exist yet:\n%s", out)
	}
}

func TestAutoStopErrorWhileProvidingStandardTasksMarksEverythingNotSent(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	seedTicketHour(t, deps)
	prov.findErrs = map[string]error{"GEN": sink.ErrUnauthorized}

	out, err := runAutoFor(t, deps)
	if !errors.Is(err, sink.ErrUnauthorized) {
		t.Fatalf("err = %v, want the stop error\n%s", err, out)
	}
	if len(prov.items) != 0 {
		t.Errorf("a stopped run booked: %v", prov.items)
	}
	if !strings.Contains(out, "not sent 2") {
		t.Errorf("digest does not list the items as not sent:\n%s", out)
	}
}
