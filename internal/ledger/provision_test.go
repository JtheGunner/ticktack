package ledger

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

var provisionDay = civil.Date{Year: 2026, Month: time.March, Day: 2}

// fakeProvisioner is an in-memory YouTrack: issues[project] is the key of the
// standard task that exists there.
type fakeProvisioner struct {
	issues  map[string]string
	created []string
	// findErrs and createErrs fail the call for one project.
	findErrs   map[string]error
	createErrs map[string]error
	nextKeys   map[string]string
	lastDesc   string
	lastSum    string
}

func (f *fakeProvisioner) Projects(context.Context) ([]sink.ProjectRef, error) { return nil, nil }

func (f *fakeProvisioner) FindIssueByMarker(_ context.Context, project, marker string) (string, bool, error) {
	if err := f.findErrs[project]; err != nil {
		return "", false, err
	}
	if marker != DefaultIssueMarker {
		return "", false, errors.New("unexpected marker " + marker)
	}
	key, ok := f.issues[project]
	return key, ok, nil
}

func (f *fakeProvisioner) CreateIssue(_ context.Context, project, summary, description string) (string, error) {
	if err := f.createErrs[project]; err != nil {
		return "", err
	}
	f.created = append(f.created, project)
	f.lastSum, f.lastDesc = summary, description
	key := f.nextKeys[project]
	if f.issues == nil {
		f.issues = map[string]string{}
	}
	f.issues[project] = key
	return key, nil
}

func provisionStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func item(ticket string, minutes int) sessionizer.DayItem {
	return sessionizer.DayItem{Date: provisionDay, Ticket: ticket, Type: "Development", Minutes: minutes}
}

func confirm(t *testing.T, st *store.Store, ticket string, minutes int) {
	t.Helper()
	err := st.Confirm(context.Background(), store.Confirmation{
		Day: provisionDay.String(), Issue: ticket, Type: "Development", Minutes: minutes,
		ConfirmedAt: time.Date(2026, 3, 2, 18, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func tickets(items []sessionizer.DayItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Ticket)
	}
	return out
}

func answer(yes bool, asked *[][]string) func([]string) ([]string, error) {
	return func(projects []string) ([]string, error) {
		*asked = append(*asked, projects)
		if yes {
			return projects, nil
		}
		return nil, nil
	}
}

func provisionOpts(confirmFn func([]string) ([]string, error)) ProvisionOptions {
	return ProvisionOptions{
		Summary: "Meetings / Admin",
		Confirm: confirmFn,
		Now:     func() time.Time { return time.Date(2026, 3, 2, 19, 0, 0, 0, time.UTC) },
	}
}

func TestProvisionLeavesOrdinaryTicketsAlone(t *testing.T) {
	st := provisionStore(t)
	prov := &fakeProvisioner{}
	var asked [][]string
	items := []sessionizer.DayItem{item("ABC-1", 30), item("", 15)}

	got, report, err := Provision(context.Background(), items, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, items) || len(asked) != 0 || len(prov.created) != 0 || len(report.Created) != 0 {
		t.Errorf("ordinary items changed: got %v asked %v created %v", got, asked, prov.created)
	}
}

func TestProvisionFindsExistingByMarker(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	confirm(t, st, "TICKT:default", 60)
	prov := &fakeProvisioner{issues: map[string]string{"TICKT": "TICKT-7"}}
	var asked [][]string

	got, report, err := Provision(ctx, []sessionizer.DayItem{item("TICKT:default", 60)}, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TICKT-7"}; !reflect.DeepEqual(tickets(got), want) {
		t.Fatalf("tickets = %v, want %v", tickets(got), want)
	}
	if len(asked) != 0 || len(prov.created) != 0 {
		t.Errorf("an existing task must neither ask nor create: asked %v created %v", asked, prov.created)
	}
	if len(report.Found) != 1 || report.Found[0] != (ProvisionedTask{Project: "TICKT", Issue: "TICKT-7"}) {
		t.Errorf("report.Found = %+v", report.Found)
	}
	if tasks, _ := st.DefaultTasks(ctx); tasks["TICKT"] != "TICKT-7" {
		t.Errorf("task key not remembered: %v", tasks)
	}
	confs, _ := st.Confirmations(ctx, provisionDay.String())
	if len(confs) != 1 || confs[0].Issue != "TICKT-7" {
		t.Errorf("confirmation not moved to the real key: %+v", confs)
	}
}

func TestProvisionCreatesAfterOneConfirmation(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-1", "B": "B-1", "C": "C-1"}}
	var asked [][]string
	items := []sessionizer.DayItem{item("C:default", 30), item("A:default", 30), item("B:default", 30), item("A:default", 15)}

	got, report, err := Provision(ctx, items, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"A", "B", "C"}}; !reflect.DeepEqual(asked, want) {
		t.Fatalf("confirm calls = %v, want exactly one listing %v", asked, want)
	}
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(prov.created, want) {
		t.Errorf("created = %v, want %v", prov.created, want)
	}
	if want := []string{"C-1", "A-1", "B-1"}; !reflect.DeepEqual(tickets(got), want) {
		t.Errorf("tickets = %v, want %v", tickets(got), want)
	}
	if prov.lastSum != "Meetings / Admin" || !strings.Contains(prov.lastDesc, DefaultIssueMarker) {
		t.Errorf("created with summary %q description %q", prov.lastSum, prov.lastDesc)
	}
	if len(report.Created) != 3 {
		t.Errorf("report.Created = %+v", report.Created)
	}
	tasks, _ := st.DefaultTasks(ctx)
	consent, _ := st.ConsentedProjects(ctx)
	for _, p := range []string{"A", "B", "C"} {
		if tasks[p] != p+"-1" || !consent[p] {
			t.Errorf("project %s: task %q consent %v", p, tasks[p], consent[p])
		}
	}
}

func TestProvisionDeclinedLeavesItemsUnbooked(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	confirm(t, st, "A:default", 30)
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-1"}}
	var asked [][]string
	items := []sessionizer.DayItem{item("A:default", 30), item("ABC-1", 45)}

	got, report, err := Provision(ctx, items, prov, st, provisionOpts(answer(false, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ABC-1"}; !reflect.DeepEqual(tickets(got), want) {
		t.Errorf("tickets = %v, want only the ordinary item", tickets(got))
	}
	if len(prov.created) != 0 || !reflect.DeepEqual(report.Declined, []string{"A"}) {
		t.Errorf("created %v declined %v", prov.created, report.Declined)
	}
	if tasks, _ := st.DefaultTasks(ctx); len(tasks) != 0 {
		t.Errorf("declined run stored tasks: %v", tasks)
	}
	if confs, _ := st.Confirmations(ctx, provisionDay.String()); len(confs) != 1 || confs[0].Issue != "A:default" {
		t.Errorf("confirmation changed on decline: %+v", confs)
	}
}

func TestProvisionCreatesOnlyTheApprovedProjects(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-1", "B": "B-1"}}
	onlyB := func(projects []string) ([]string, error) { return []string{"B"}, nil }

	got, report, err := Provision(ctx, []sessionizer.DayItem{item("A:default", 30), item("B:default", 30)}, prov, st, provisionOpts(onlyB))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B"}; !reflect.DeepEqual(prov.created, want) {
		t.Errorf("created = %v, want %v", prov.created, want)
	}
	if want := []string{"B-1"}; !reflect.DeepEqual(tickets(got), want) {
		t.Errorf("tickets = %v, want only the approved project's item", tickets(got))
	}
	if !reflect.DeepEqual(report.Declined, []string{"A"}) {
		t.Errorf("Declined = %v, want [A]", report.Declined)
	}
}

func TestProvisionReportsRewrittenAndDroppedTickets(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	prov := &fakeProvisioner{issues: map[string]string{"A": "A-7"}}
	var asked [][]string

	_, report, err := Provision(ctx, []sessionizer.DayItem{item("A:default", 30), item("B:default", 30)}, prov, st, provisionOpts(answer(false, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if report.Rewritten["A:default"] != "A-7" {
		t.Errorf("Rewritten = %v, want A:default → A-7", report.Rewritten)
	}
	if !reflect.DeepEqual(report.DroppedTickets, []string{"B:default"}) {
		t.Errorf("DroppedTickets = %v, want [B:default]", report.DroppedTickets)
	}
}

func TestProvisionMergesItemsThatShareARewrittenTicket(t *testing.T) {
	st := provisionStore(t)
	prov := &fakeProvisioner{issues: map[string]string{"A": "A-7"}}
	items := []sessionizer.DayItem{item("A-7", 30), item("A:default", 45)}

	got, _, err := Provision(context.Background(), items, prov, st, provisionOpts(answer(true, new([][]string))))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Ticket != "A-7" || got[0].Minutes != 75 {
		t.Fatalf("items = %+v, want one A-7 item of 75 minutes", got)
	}
}

func TestProvisionDryRunCreatesNothing(t *testing.T) {
	st := provisionStore(t)
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-1"}}
	var asked [][]string
	o := provisionOpts(answer(true, &asked))
	o.DryRun = true

	got, report, err := Provision(context.Background(), []sessionizer.DayItem{item("A:default", 30)}, prov, st, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 || len(prov.created) != 0 || len(got) != 0 {
		t.Errorf("dry run acted: asked %v created %v items %v", asked, prov.created, got)
	}
	if !reflect.DeepEqual(report.WouldCreate, []string{"A"}) {
		t.Errorf("WouldCreate = %v", report.WouldCreate)
	}
}

func TestProvisionRerunDoesNotDuplicate(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-1"}}
	var asked [][]string
	if _, _, err := Provision(ctx, []sessionizer.DayItem{item("A:default", 30)}, prov, st, provisionOpts(answer(true, &asked))); err != nil {
		t.Fatal(err)
	}

	asked = nil
	got, _, err := Provision(ctx, []sessionizer.DayItem{item("A-1", 30)}, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 || len(prov.created) != 1 {
		t.Errorf("second run asked %v and created %v, want no new creation", asked, prov.created)
	}
	if want := []string{"A-1"}; !reflect.DeepEqual(tickets(got), want) {
		t.Errorf("tickets = %v, want %v", tickets(got), want)
	}
}

func TestProvisionDeletedTaskIsRecreatedAfterConfirmation(t *testing.T) {
	st := provisionStore(t)
	ctx := context.Background()
	if err := st.SetDefaultTask(ctx, "A", "A-1"); err != nil {
		t.Fatal(err)
	}
	confirm(t, st, "A-1", 30)
	prov := &fakeProvisioner{nextKeys: map[string]string{"A": "A-9"}} // nothing exists in YouTrack any more
	var asked [][]string

	got, _, err := Provision(ctx, []sessionizer.DayItem{item("A-1", 30)}, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"A"}}; !reflect.DeepEqual(asked, want) {
		t.Errorf("confirm calls = %v, want %v", asked, want)
	}
	if want := []string{"A-9"}; !reflect.DeepEqual(tickets(got), want) {
		t.Errorf("tickets = %v, want %v", tickets(got), want)
	}
	if tasks, _ := st.DefaultTasks(ctx); tasks["A"] != "A-9" {
		t.Errorf("task key = %q, want A-9", tasks["A"])
	}
	if confs, _ := st.Confirmations(ctx, provisionDay.String()); len(confs) != 1 || confs[0].Issue != "A-9" {
		t.Errorf("confirmation = %+v, want it moved to A-9", confs)
	}
}

func TestProvisionStopErrorsEndTheRun(t *testing.T) {
	tests := []struct {
		name string
		prov *fakeProvisioner
		want error
	}{
		{"lookup unauthorized", &fakeProvisioner{findErrs: map[string]error{"A": youtrack.ErrUnauthorized}}, youtrack.ErrUnauthorized},
		{"lookup rate limited", &fakeProvisioner{findErrs: map[string]error{"A": youtrack.ErrRateLimited}}, youtrack.ErrRateLimited},
		{"create unauthorized", &fakeProvisioner{createErrs: map[string]error{"A": youtrack.ErrUnauthorized}}, youtrack.ErrUnauthorized},
		{"create rate limited", &fakeProvisioner{createErrs: map[string]error{"A": youtrack.ErrRateLimited}}, youtrack.ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := provisionStore(t)
			tt.prov.nextKeys = map[string]string{"A": "A-1", "B": "B-1"}
			var asked [][]string
			items := []sessionizer.DayItem{item("A:default", 30), item("B:default", 30)}

			_, _, err := Provision(context.Background(), items, tt.prov, st, provisionOpts(answer(true, &asked)))
			if !errors.Is(err, tt.want) || !IsStopError(err) {
				t.Fatalf("err = %v, want the stop error %v", err, tt.want)
			}
			if len(tt.prov.created) != 0 {
				t.Errorf("created %v after a stop error", tt.prov.created)
			}
		})
	}
}

func TestProvisionReportsAFailedProjectAndProvidesTheOthers(t *testing.T) {
	boom := errors.New("youtrack down")
	tests := []struct {
		name string
		prov *fakeProvisioner
		// wantAsked are the projects offered for creation: a project whose
		// lookup failed is never among them.
		wantAsked []string
	}{
		{"lookup fails", &fakeProvisioner{findErrs: map[string]error{"B": boom}}, []string{"A", "C"}},
		{"create fails", &fakeProvisioner{createErrs: map[string]error{"B": boom}}, []string{"A", "B", "C"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := provisionStore(t)
			tt.prov.nextKeys = map[string]string{"A": "A-1", "B": "B-1", "C": "C-1"}
			var asked [][]string
			items := []sessionizer.DayItem{item("A:default", 30), item("B:default", 45), item("C:default", 15), item("TICKT-9", 60)}

			got, report, err := Provision(context.Background(), items, tt.prov, st, provisionOpts(answer(true, &asked)))
			if err != nil {
				t.Fatalf("a failing project must not abort the others: %v", err)
			}
			if want := []string{"A-1", "C-1", "TICKT-9"}; !reflect.DeepEqual(tickets(got), want) {
				t.Errorf("tickets = %v, want %v", tickets(got), want)
			}
			if want := []string{"A", "C"}; !reflect.DeepEqual(tt.prov.created, want) {
				t.Errorf("created = %v, want %v", tt.prov.created, want)
			}
			if len(asked) != 1 || !reflect.DeepEqual(asked[0], tt.wantAsked) {
				t.Errorf("asked = %v, want one question for %v", asked, tt.wantAsked)
			}
			if len(report.Failed) != 1 {
				t.Fatalf("Failed = %+v, want one item", report.Failed)
			}
			f := report.Failed[0]
			if f.Key != Key("B:default", provisionDay.String(), "Development") || f.Action != ActionFailed || f.Minutes != 45 {
				t.Errorf("Failed[0] = %+v", f)
			}
			if !strings.Contains(f.Error, "B") || !strings.Contains(f.Error, "youtrack down") {
				t.Errorf("Failed[0].Error = %q, want the project and the cause", f.Error)
			}
			if !reflect.DeepEqual(report.FailedTickets, []string{"B:default"}) {
				t.Errorf("FailedTickets = %v, want [B:default]", report.FailedTickets)
			}
			if !reflect.DeepEqual(report.FailedProjects, []string{"B"}) {
				t.Errorf("FailedProjects = %v, want [B]", report.FailedProjects)
			}
			if len(report.DroppedTickets) != 0 || len(report.Declined) != 0 {
				t.Errorf("a failed project was reported as missing its task: dropped %v declined %v", report.DroppedTickets, report.Declined)
			}
			tasks, err := st.DefaultTasks(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, known := tasks["B"]; known {
				t.Errorf("a task was stored for the failed project: %v", tasks)
			}
		})
	}
}

func TestProvisionDryRunReportsAFailedLookupWithoutPromisingTime(t *testing.T) {
	st := provisionStore(t)
	prov := &fakeProvisioner{findErrs: map[string]error{"B": errors.New("youtrack down")}}
	o := provisionOpts(nil)
	o.DryRun = true

	got, report, err := Provision(context.Background(), []sessionizer.DayItem{item("A:default", 30), item("B:default", 45)}, prov, st, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(report.Failed) != 1 {
		t.Errorf("items %v, Failed %+v", tickets(got), report.Failed)
	}
	if !reflect.DeepEqual(report.WouldCreate, []string{"A"}) || report.WouldBook["B"] != 0 {
		t.Errorf("WouldCreate = %v, WouldBook = %v", report.WouldCreate, report.WouldBook)
	}
}

// One project fails, the rest of the day is still booked and the failure is
// part of the sync report.
func TestSyncBooksTheOtherItemsWhenOneProjectCannotBeProvided(t *testing.T) {
	ctx := context.Background()
	st := provisionStore(t)
	prov := &fakeProvisioner{
		findErrs: map[string]error{"B": errors.New("youtrack down")},
		nextKeys: map[string]string{"A": "A-1"},
	}
	var asked [][]string
	items := []sessionizer.DayItem{item("A:default", 30), item("B:default", 45), item("TICKT-9", 60)}

	provided, provisioned, err := Provision(ctx, items, prov, st, provisionOpts(answer(true, &asked)))
	if err != nil {
		t.Fatal(err)
	}
	fs := newFakeSink()
	report, err := Sync(ctx, provided, fs, SyncOptions{Repo: st, Failed: provisioned.Failed})
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 2 || report.Failed != 1 || report.NotSent != 0 {
		t.Errorf("report = %+v, want 2 created and 1 failed", report)
	}
	if len(fs.items) != 2 {
		t.Errorf("booked %v, want A-1 and TICKT-9", fs.items)
	}
	for _, fi := range fs.items {
		if fi.item.Issue != "A-1" && fi.item.Issue != "TICKT-9" {
			t.Errorf("booked on %s", fi.item.Issue)
		}
	}
	listed := false
	for _, res := range report.Items {
		if res.Action == ActionFailed && strings.HasPrefix(res.Key, "B:default") {
			listed = true
		}
	}
	if !listed {
		t.Errorf("the failed project is not in the report: %+v", report.Items)
	}
}

func TestNotSentReportMarksEveryItem(t *testing.T) {
	report := NotSentReport([]sessionizer.DayItem{item("TICKT-9", 60), item("A:default", 30), item("", 15)})
	if report.NotSent != 2 || report.Skipped != 1 || len(report.Items) != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.Items[1].Key != Key("A:default", provisionDay.String(), "Development") || report.Items[1].Action != ActionNotSent {
		t.Errorf("Items[1] = %+v", report.Items[1])
	}
}

func TestProvisionCancelledContextEndsTheRun(t *testing.T) {
	st := provisionStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prov := &fakeProvisioner{findErrs: map[string]error{"A": context.Canceled}}

	_, report, err := Provision(ctx, []sessionizer.DayItem{item("A:default", 30)}, prov, st, provisionOpts(nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if len(report.Failed) != 0 {
		t.Errorf("a cancelled run reported a failed project: %+v", report.Failed)
	}
}
