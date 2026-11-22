package cli

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// bookEarlier books an hour on each issue for the seeded day, as an earlier
// run would have, without recording a standard task for any project: the
// local default_tasks table stays empty.
func bookEarlier(t *testing.T, deps syncDeps, prov *provisioningSink, issues ...string) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	items := make([]sessionizer.DayItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, sessionizer.DayItem{Date: civil.Date{Year: 2026, Month: 1, Day: 5}, Ticket: issue, Minutes: 60, Description: issue})
	}
	report, err := ledger.Sync(context.Background(), items, prov, ledger.SyncOptions{Repo: st, Now: deps.Now})
	if err != nil || report.Created != len(issues) {
		t.Fatalf("earlier booking: report %+v, err %v", report, err)
	}
	tasks, err := st.DefaultTasks(context.Background())
	if err != nil || len(tasks) != 0 {
		t.Fatalf("default tasks = %v, err %v, want none", tasks, err)
	}
	prov.calls = nil
}

func ledgerIssues(t *testing.T, deps syncDeps) []string {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.LedgerRowsForDate(context.Background(), "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	return rowIssues(rows)
}

func rowIssues(rows []store.LedgerRow) []string {
	issues := make([]string, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, row.Issue)
	}
	return issues
}

// assertHeldBookingSurvived checks the state after a run whose lookup of the
// GEN standard task failed while GEN-5 and OLD-9 were booked and unknown to
// the day: the booking in the failed project stays, the other one is stale.
func assertHeldBookingSurvived(t *testing.T, deps syncDeps, prov *provisioningSink, out string) {
	t.Helper()
	if _, ok := prov.items["GEN-5/1"]; !ok {
		t.Errorf("the booking on GEN-5 was deleted in the sink: %v\n%s", prov.items, out)
	}
	if _, ok := prov.items["OLD-9/1"]; ok {
		t.Errorf("the stale booking of an unaffected project was kept: %v\n%s", prov.items, out)
	}
	if got := strings.Join(prov.calls, ","); got != "delete" {
		t.Errorf("sink calls = %q, want one delete (OLD-9)\n%s", got, out)
	}
	if got := strings.Join(ledgerIssues(t, deps), ","); got != "GEN-5" {
		t.Errorf("ledger rows = %q, want GEN-5 only\n%s", got, out)
	}
	if !strings.Contains(out, "look up the standard task of GEN: youtrack down") {
		t.Errorf("the failed project is not reported:\n%s", out)
	}
	if strings.Contains(out, "GEN-5") {
		t.Errorf("the report touches the held booking:\n%s", out)
	}
}

// Without an entry in default_tasks the day's placeholder cannot be mapped to
// the task the time was booked on. A failed lookup must then not let
// reconciliation treat that booking as stale.
func TestSyncKeepsABookingOfAFailedProjectThatIsUnknownLocally(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	bookEarlier(t, deps, prov, "GEN-5", "OLD-9")
	confirmDay(t, deps)
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}

	out, _, err := execCommandWithInput(t, deps, "y\n", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	assertHeldBookingSurvived(t, deps, prov, out)
}

func TestSyncDryRunPlansNoDeletionForAFailedProject(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	bookEarlier(t, deps, prov, "GEN-5", "OLD-9")
	confirmDay(t, deps)
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatalf("sync --dry-run: %v\n%s", err, out)
	}
	if strings.Contains(out, "GEN-5") || !strings.Contains(out, "OLD-9") {
		t.Errorf("plan must delete OLD-9 only:\n%s", out)
	}
	if len(prov.calls) != 0 || len(prov.items) != 2 {
		t.Errorf("dry run acted: calls %v items %v", prov.calls, prov.items)
	}
}

func TestAutoKeepsABookingOfAFailedProjectThatIsUnknownLocally(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	consentTo(t, deps, "GEN")
	bookEarlier(t, deps, prov, "GEN-5", "OLD-9")
	prov.findErrs = map[string]error{"GEN": errYouTrackDown}

	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	assertHeldBookingSurvived(t, deps, prov, out)
}
