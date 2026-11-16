package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

const (
	learnRepo   = "/work/app"
	learnBranch = "spike"
)

// seedContext records a repo/branch context without a ticket, plus presence
// every minute for minutes+1 samples from start.
func seedContext(t *testing.T, st *store.Store, start time.Time, minutes int, repo, branch string) {
	t.Helper()
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Repo: repo, Branch: branch}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= minutes; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

type fakeIssues struct {
	issues []youtrack.Issue
	err    error
	calls  int
}

func (f *fakeIssues) MyOpenIssues(context.Context) ([]youtrack.Issue, error) {
	f.calls++
	return f.issues, f.err
}

func nextDay() (civil.Date, time.Time) {
	return civil.Date{Year: 2026, Month: 1, Day: 6}, dayStart.Add(24 * time.Hour)
}

func TestLearnedRuleAppliesToFutureBlocks(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, testConfig())
	if e := s.Entries()[0]; e.Kind != KindGap || e.Repo != learnRepo || e.Branch != learnBranch {
		t.Fatalf("entry = %+v", e)
	}
	if _, ok := s.Remembering(); ok {
		t.Fatal("no offer before a reassignment")
	}
	if err := s.Reassign(ctx, 1, "abc-7"); err != nil {
		t.Fatal(err)
	}
	offer, ok := s.Remembering()
	if !ok || offer.Repo != learnRepo || offer.Branch != learnBranch || offer.Ticket != "ABC-7" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
	rule, err := s.Remember(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID == 0 || rule.BranchPattern != learnBranch || rule.Ticket != "ABC-7" {
		t.Fatalf("rule = %+v", rule)
	}
	if _, ok := s.Remembering(); ok {
		t.Error("offer must be consumed")
	}

	// Another day, same repo and branch: attributed with tier learned.
	day2, start2 := nextDay()
	seedContext(t, st, start2, 60, learnRepo, learnBranch)
	d, err := LoadDay(ctx, st, testConfig(), day2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 1 || d.Blocks[0].Ticket != "ABC-7" || d.Blocks[0].Tier != resolver.TierLearned {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	// A different branch is not covered by the exact-branch rule.
	seedContext(t, st, start2.Add(3*time.Hour), 60, learnRepo, "other")
	d, _ = LoadDay(ctx, st, testConfig(), day2)
	if last := d.Blocks[len(d.Blocks)-1]; last.Tier != resolver.TierUnknown {
		t.Fatalf("other branch = %+v", last)
	}
}

func TestRememberRepoWideAndUpdatesExistingRule(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 1, "ABC-7"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember(ctx, true); err != nil {
		t.Fatal(err)
	}
	rules, _ := st.LearnedRules(ctx)
	if len(rules) != 1 || rules[0].BranchPattern != "" || rules[0].Repo != learnRepo {
		t.Fatalf("rules = %+v", rules)
	}
	if _, err := s.Remember(ctx, true); !errors.Is(err, ErrNothingToRemember) {
		t.Fatalf("second remember: %v", err)
	}

	// Learned tier entries can be corrected again; the same-scope rule is updated.
	day2, start2 := nextDay()
	seedContext(t, st, start2, 60, learnRepo, learnBranch)
	s2, err := NewSession(ctx, st, testConfig(), day2, testClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Reassign(ctx, 1, "XYZ-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Remember(ctx, true); err != nil {
		t.Fatal(err)
	}
	rules, _ = st.LearnedRules(ctx)
	if len(rules) != 1 || rules[0].Ticket != "XYZ-1" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestNoRememberOfferWithoutRepoOrForExplicitTier(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seed(t, st, dayStart, 60, "ABC-1") // explicit ticket, no repo
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 1, "XYZ-9"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Remembering(); ok {
		t.Fatal("offer without repo")
	}
}

func TestLearnedRuleIsEditableViaMap(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	rule, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: learnRepo, BranchPattern: learnBranch, Ticket: "ABC-7", CreatedAt: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	tier := func() (string, resolver.Tier) {
		d, err := LoadDay(ctx, st, testConfig(), testDay)
		if err != nil || len(d.Blocks) != 1 {
			t.Fatalf("day = %+v err=%v", d, err)
		}
		return d.Blocks[0].Ticket, d.Blocks[0].Tier
	}
	if tk, tr := tier(); tk != "ABC-7" || tr != resolver.TierLearned {
		t.Fatalf("before edit: %s %s", tk, tr)
	}

	rule.Ticket = "NEW-3"
	if err := st.UpdateLearnedRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if tk, tr := tier(); tk != "NEW-3" || tr != resolver.TierLearned {
		t.Fatalf("after update: %s %s", tk, tr)
	}

	if err := st.DeleteLearnedRule(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	if tk, tr := tier(); tk != "" || tr != resolver.TierUnknown {
		t.Fatalf("after delete: %q %s", tk, tr)
	}
}

func TestLearnedRuleNeverOverridesExplicitOrConfigRule(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: learnRepo, Ticket: "LRN-1", CreatedAt: fixedNow}); err != nil {
		t.Fatal(err)
	}
	seedContext(t, st, dayStart, 60, learnRepo, "feature/ABC-5-x")
	d, err := LoadDay(ctx, st, testConfig(), testDay)
	if err != nil || len(d.Blocks) != 1 || d.Blocks[0].Ticket != "ABC-5" || d.Blocks[0].Tier != resolver.TierExplicit {
		t.Fatalf("explicit: %+v err=%v", d.Blocks, err)
	}
	cfg := testConfig()
	cfg.Rules = append(cfg.Rules, ruleFor(learnRepo, "CFG-1"))
	seedContext(t, st, dayStart.Add(3*time.Hour), 60, learnRepo, "spike")
	d, _ = LoadDay(ctx, st, cfg, testDay)
	if last := d.Blocks[len(d.Blocks)-1]; last.Ticket != "CFG-1" || last.Tier != resolver.TierRule {
		t.Fatalf("config rule: %+v", last)
	}
}

func TestSuggestionsComeFromAssignedInProgress(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedContext(t, st, dayStart, 60, learnRepo, "feature/def-2-spike")
	src := &fakeIssues{issues: []youtrack.Issue{
		{ID: "ABC-1", Summary: "old", Updated: 100},
		{ID: "DEF-2", Summary: "named in branch", Updated: 50},
		{ID: "GHI-3", Summary: "recent", Updated: 900},
	}}
	s, err := NewSession(ctx, st, testConfig(), testDay, testClock, WithIssueSource(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Suggestions(ctx, 1)
	if err != nil || len(got) != 3 {
		t.Fatalf("suggestions = %+v err=%v", got, err)
	}
	if got[0].Key != "DEF-2" || got[1].Key != "GHI-3" || got[2].Key != "ABC-1" {
		t.Fatalf("order = %+v", got)
	}
	if _, err := s.Suggestions(ctx, 1); err != nil || src.calls != 1 {
		t.Fatalf("source called %d times, want once per session (err=%v)", src.calls, err)
	}
	// A suggestion is only a proposal: nothing is booked or confirmed.
	items, _ := ConfirmedItems(ctx, st, testConfig(), testDay)
	if len(items) != 0 {
		t.Fatalf("items = %+v", items)
	}
	key, err := s.ReassignSuggestion(ctx, 1, 2)
	if err != nil || key != "GHI-3" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if e := s.Entries()[0]; e.Ticket != "GHI-3" || e.Confirmed {
		t.Fatalf("entry = %+v", e)
	}
	if items, _ = ConfirmedItems(ctx, st, testConfig(), testDay); len(items) != 0 {
		t.Fatalf("suggestion must still need confirmation: %+v", items)
	}
	if got, _ := s.Suggestions(ctx, 1); got != nil {
		t.Errorf("assigned entry got suggestions: %+v", got)
	}
}

func TestSuggestionsFailureIsNonFatal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	seedContext(t, st, dayStart, 60, learnRepo, "spike")
	src := &fakeIssues{err: errors.New("offline")}
	s, err := NewSession(ctx, st, testConfig(), testDay, testClock, WithIssueSource(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Suggestions(ctx, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if _, err := s.ReassignSuggestion(ctx, 1, 1); !errors.Is(err, ErrNoSuchSuggestion) {
		t.Fatalf("err = %v", err)
	}
	// Without a source nothing is fetched at all.
	if got, err := newSession(t, st, testConfig()).Suggestions(ctx, 1); err != nil || got != nil {
		t.Fatalf("no source: %+v %v", got, err)
	}
}
