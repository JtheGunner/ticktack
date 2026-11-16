package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

func reviewTestDeps(t *testing.T, input string) (reviewDeps, *store.Store, *bytes.Buffer) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	out := &bytes.Buffer{}
	return reviewDeps{
		OpenStore: func() (reviewStore, error) { return nopCloseStore{st}, nil },
		LoadConfig: func() (config.Config, error) {
			cfg := config.Config{Location: time.UTC, GapThreshold: 5 * time.Minute, MinBlockDuration: 10 * time.Minute, Rounding: 15 * time.Minute}
			cfg.TicketPattern = regexp.MustCompile(config.DefaultTicketPattern)
			return cfg, nil
		},
		Now: func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) },
		In:  strings.NewReader(input),
		Out: out,
	}, st, out
}

func TestReviewCommandScriptedSession(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "t 1 def-2\nx\nc 9\ns 1 09:30\nABC-1\ny\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Review for 2026-01-05", "DEF-2", "explicit", "Unknown command", "no such entry", "split at 09:30", "2 confirmed", "ticktack sync"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	confs, err := st.Confirmations(context.Background(), "2026-01-05")
	if err != nil || len(confs) != 2 {
		t.Fatalf("confirmations = %+v err=%v", confs, err)
	}
}

func TestReviewCommandEmptyDayAndEOF(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "")
	if err := runReview(context.Background(), deps, "2026-02-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No time recorded for 2026-02-01") || !strings.Contains(out.String(), "Nothing confirmed yet") {
		t.Fatalf("output:\n%s", out.String())
	}
	if err := runReview(context.Background(), deps, "bad"); err == nil {
		t.Fatal("expected invalid day error")
	}
}

type fakeIssueSource struct{ issues []youtrack.Issue }

func (f fakeIssueSource) MyOpenIssues(context.Context) ([]youtrack.Issue, error) {
	return f.issues, nil
}

// repoOnlyDeps replaces the seeded day with a context that has a repo and
// branch but no ticket.
func repoOnlyDeps(t *testing.T, input string) (reviewDeps, *store.Store, *bytes.Buffer) {
	t.Helper()
	deps, st, out := reviewTestDeps(t, input)
	if err := st.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Repo: "/work/app", Branch: "spike"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
	return deps, st, out
}

func TestReviewCommandRemembersReassignmentAsVisibleRule(t *testing.T) {
	deps, st, out := repoOnlyDeps(t, "t 1 abc-7\nr\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Remember ABC-7", "Learned rule #1", "/work/app", "spike", "ABC-7"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	rules, err := st.LearnedRules(context.Background())
	if err != nil || len(rules) != 1 || rules[0].BranchPattern != "spike" {
		t.Fatalf("rules = %+v err=%v", rules, err)
	}
}

func TestReviewCommandRememberRepoWideAndWithoutOffer(t *testing.T) {
	deps, st, out := repoOnlyDeps(t, "r\nt 1 abc-7\nr repo\nr x y\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to remember") || !strings.Contains(out.String(), "Usage: r [repo]") {
		t.Errorf("output:\n%s", out.String())
	}
	rules, _ := st.LearnedRules(context.Background())
	if len(rules) != 1 || rules[0].BranchPattern != "" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestReviewCommandShowsAndTakesSuggestions(t *testing.T) {
	deps, st, out := repoOnlyDeps(t, "u 1 2\nu 1 9\nq\n")
	deps.NewIssueSource = func(config.Config) (review.IssueSource, error) {
		return fakeIssueSource{issues: []youtrack.Issue{{ID: "ABC-1", Summary: "first", Updated: 9}, {ID: "DEF-2", Summary: "second", Updated: 5}}}, nil
	}
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"1) ABC-1  first", "2) DEF-2  second", "assigned to DEF-2", "only a proposal"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if confs, _ := st.Confirmations(context.Background(), "2026-01-05"); len(confs) != 0 {
		t.Fatalf("suggestion confirmed on its own: %+v", confs)
	}
}

func TestReviewCommandSuggestionSetupFailureIsIgnored(t *testing.T) {
	deps, _, out := repoOnlyDeps(t, "q\n")
	deps.NewIssueSource = func(config.Config) (review.IssueSource, error) { return nil, errors.New("no token") }
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "suggestions for") {
		t.Errorf("unexpected suggestions:\n%s", out.String())
	}
}

func TestReviewCommandSanitizesSuggestionText(t *testing.T) {
	deps, _, out := repoOnlyDeps(t, "q\n")
	deps.NewIssueSource = func(config.Config) (review.IssueSource, error) {
		return fakeIssueSource{issues: []youtrack.Issue{
			{ID: "ABC-1\x1b]0;pwned\x07", Summary: "\x1b[2Jclear\rfake\u202eevil\x9b", Updated: 1},
		}}, nil
	}
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	assertNoControl(t, out.String())
	if !strings.Contains(out.String(), "ABC-1") {
		t.Errorf("suggestion missing:\n%q", out.String())
	}
}
