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
)

var provisionDay = civil.Date{Year: 2026, Month: time.March, Day: 2}

// fakeProvisioner is an in-memory YouTrack: issues[project] is the key of the
// standard task that exists there.
type fakeProvisioner struct {
	issues   map[string]string
	created  []string
	findErr  error
	nextKeys map[string]string
	lastDesc string
	lastSum  string
}

func (f *fakeProvisioner) Projects(context.Context) ([]sink.ProjectRef, error) { return nil, nil }

func (f *fakeProvisioner) FindIssueByMarker(_ context.Context, project, marker string) (string, bool, error) {
	if f.findErr != nil {
		return "", false, f.findErr
	}
	if marker != DefaultIssueMarker {
		return "", false, errors.New("unexpected marker " + marker)
	}
	key, ok := f.issues[project]
	return key, ok, nil
}

func (f *fakeProvisioner) CreateIssue(_ context.Context, project, summary, description string) (string, error) {
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

func answer(yes bool, asked *[][]string) func([]string) (bool, error) {
	return func(projects []string) (bool, error) {
		*asked = append(*asked, projects)
		return yes, nil
	}
}

func provisionOpts(confirmFn func([]string) (bool, error)) ProvisionOptions {
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
	if want := []string{"C-1", "A-1", "B-1", "A-1"}; !reflect.DeepEqual(tickets(got), want) {
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

func TestProvisionPropagatesLookupErrors(t *testing.T) {
	st := provisionStore(t)
	boom := errors.New("youtrack down")
	prov := &fakeProvisioner{findErr: boom}
	var asked [][]string

	_, _, err := Provision(context.Background(), []sessionizer.DayItem{item("A:default", 30)}, prov, st, provisionOpts(answer(true, &asked)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the lookup error", err)
	}
	if len(asked) != 0 || len(prov.created) != 0 {
		t.Errorf("a failed lookup must not lead to creation: asked %v created %v", asked, prov.created)
	}
}
