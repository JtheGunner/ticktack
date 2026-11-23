package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/JtheGunner/ticktack/internal/commitsubject"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// scriptedDescriber answers from a script, one step per call.
type scriptedDescriber struct {
	steps []scriptStep
	calls int
}

type scriptStep struct {
	text     string
	reliable bool
}

func (d *scriptedDescriber) next() scriptStep {
	step := d.steps[d.calls]
	d.calls++
	return step
}

func (d *scriptedDescriber) Describe(ctx context.Context, it sessionizer.DayItem) string {
	text, _ := d.DescribeReliably(ctx, it)
	return text
}

func (d *scriptedDescriber) DescribeReliably(context.Context, sessionizer.DayItem) (string, bool) {
	step := d.next()
	return step.text, step.reliable
}

func TestBookedCommitTextSurvivesAnUnreadableRepository(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	// The summary is what an unreadable repository yields.
	d := &scriptedDescriber{steps: []scriptStep{
		{"fix parser", true},
		{"ABC-1 work", false},
		{"fix parser", true},
		{"ABC-1 work", false},
		{"ABC-1 work", false},
		{"fix parser; add tests", true},
	}}
	items := []sessionizer.DayItem{dayItem("ABC-1", 60)}
	want := []string{
		"fix parser", "fix parser", "fix parser", "fix parser", "fix parser", "fix parser; add tests",
	}
	for i, w := range want {
		mustSync(t, items, s, withDescriber(repo, d))
		if _, text := onlyItem(t, s); text != w+" "+Marker(key) {
			t.Fatalf("sync %d: text = %q, want %q", i+1, text, w)
		}
	}
	// Only the two real changes were booked: create, then update.
	if got := len(repo.runs); got != 2 {
		t.Errorf("sync runs = %d, want 2 (create and one update)", got)
	}
}

func TestUnreadableRepositoryKeepsTheSummaryOfAnItemWithoutCommitText(t *testing.T) {
	tests := []struct {
		name string
		prev *scriptStep
	}{
		{"never booked", nil},
		{"booked with the summary", &scriptStep{"ABC-1 — 3 blocks", true}},
		{"booked with the summary of one block", &scriptStep{"ABC-1 — 1 block", true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, repo := newFakeSink(), newFakeRepo()
			key := Key("ABC-1", "2026-01-05", "Development")
			items := []sessionizer.DayItem{dayItem("ABC-1", 60)}
			steps := []scriptStep{{"ABC-1 — 2 blocks", false}}
			if tc.prev != nil {
				steps = []scriptStep{*tc.prev, {"ABC-1 — 2 blocks", false}}
				mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: steps[:1]}))
			}
			mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: steps[len(steps)-1:]}))
			if _, text := onlyItem(t, s); text != "ABC-1 — 2 blocks "+Marker(key) {
				t.Errorf("text = %q, want the new summary", text)
			}
		})
	}
}

func TestReliableEmptyResultFallsBackToTheSummary(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	items := []sessionizer.DayItem{dayItem("ABC-1", 60)}
	d := &scriptedDescriber{steps: []scriptStep{{"fix parser", true}, {"ABC-1 work", true}}}
	mustSync(t, items, s, withDescriber(repo, d))
	mustSync(t, items, s, withDescriber(repo, d))
	if _, text := onlyItem(t, s); text != "ABC-1 work "+Marker(key) {
		t.Errorf("text = %q, want the summary after a reliable read without commits", text)
	}
}

// textErrRepo fails LastBookedText.
type textErrRepo struct {
	*fakeRepo
	err error
}

func (r textErrRepo) LastBookedText(context.Context, string, string, string) (string, bool, error) {
	return "", false, r.err
}

func TestDeleteRowReportsAFailedTextRead(t *testing.T) {
	s, fake := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(fake))

	boom := errors.New("history unreadable")
	repo := textErrRepo{fakeRepo: fake, err: boom}
	rep := mustSync(t, nil, s, reconciling(repo))

	if len(rep.Items) != 1 || rep.Items[0].Action != ActionFailed || rep.Items[0].Error != boom.Error() {
		t.Fatalf("report = %+v, want one failed item carrying the read error", rep.Items)
	}
	if _, ok := fake.rows[rowKey("ABC-1", "2026-01-05", "Development")]; !ok {
		t.Errorf("ledger row of %s was dropped without its text", key)
	}
	// The next sync, with a readable history, finishes the deletion.
	rep = mustSync(t, nil, s, reconciling(fake))
	if rep.Deleted != 1 {
		t.Errorf("retry report = %+v, want one deletion", rep)
	}
}

// alternatingReader fails on every second read.
type alternatingReader struct{ calls int }

func (r *alternatingReader) Read(context.Context, string, string) (commitsubject.Commit, error) {
	r.calls++
	if r.calls%2 == 0 {
		return commitsubject.Commit{}, errors.New("repository busy")
	}
	return commitsubject.Commit{Subject: "fix parser"}, nil
}

func TestBookedTextDoesNotFlipWithAnAlternatingCommitReader(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	it := dayItem("ABC-1", 60)
	it.Commits = []sessionizer.CommitRef{{Repo: "/repo", Hash: "0123456789012345678901234567890123456789"}}
	d := commitsubject.Describer{Reader: &alternatingReader{}}
	for i := 1; i <= 6; i++ {
		mustSync(t, []sessionizer.DayItem{it}, s, withDescriber(repo, d))
		if _, text := onlyItem(t, s); text != "fix parser "+Marker(key) {
			t.Fatalf("sync %d: text = %q, want the commit text", i, text)
		}
	}
	if got := len(repo.runs); got != 1 {
		t.Errorf("sync runs = %d, want 1 (only the creation)", got)
	}
}

func TestDeletedBookingTextIsNotReusedWhenTheItemReturns(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	items := []sessionizer.DayItem{dayItem("ABC-1", 60)}
	mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: []scriptStep{{"discarded work", true}}}))
	// The time is gone: the work item is deleted and the ledger row dropped.
	mustSync(t, nil, s, reconciling(repo))
	if len(s.items) != 0 {
		t.Fatalf("sink holds %d items after the deletion, want 0", len(s.items))
	}

	mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: []scriptStep{{"ABC-1 work", false}}}))

	if _, text := onlyItem(t, s); text != "ABC-1 work "+Marker(key) {
		t.Errorf("text = %q, want the fresh summary, not the deleted booking's text", text)
	}
}

func TestRecoveredItemGetsTheFreshTextWhenTheReadIsUnreliable(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	items := []sessionizer.DayItem{dayItem("ABC-1", 60)}
	mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: []scriptStep{{"fix parser", true}}}))
	// The ledger is lost; the work item still exists and is found by marker.
	delete(repo.rows, rowKey("ABC-1", "2026-01-05", "Development"))

	rep := mustSync(t, items, s, withDescriber(repo, &scriptedDescriber{steps: []scriptStep{{"ABC-1 work", false}}}))

	if rep.Recovered != 1 {
		t.Fatalf("report = %+v, want one recovery", rep)
	}
	if _, text := onlyItem(t, s); text != "ABC-1 work "+Marker(key) {
		t.Errorf("text = %q, want the fresh text for a recovered item", text)
	}
}
