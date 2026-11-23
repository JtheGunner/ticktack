package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/suggest"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// Errors returned by learning and suggestion actions.
var (
	ErrNothingToRemember = errors.New("nothing to remember: reassign an entry that has a repository first")
	ErrNoSuchSuggestion  = errors.New("no such suggestion")
)

// IssueSource lists the user's open issues in progress. *youtrack.Client
// implements it; tests use a fake.
type IssueSource interface {
	MyOpenIssues(ctx context.Context) ([]youtrack.Issue, error)
}

// Option configures a Session.
type Option func(*Session)

// WithIssueSource enables ticket suggestions for unassigned entries. The
// source is queried at most once per session, and only when suggestions are
// first requested. A failing source is not an error: there are simply no
// suggestions.
func WithIssueSource(src IssueSource) Option {
	return func(s *Session) { s.issues = src }
}

// RememberOffer describes the learned rule that Remember would write after
// the last reassignment.
type RememberOffer struct {
	Repo string
	// Branch is the branch of the corrected entry; empty when it had none.
	Branch string
	Ticket string
}

// Remembering returns the offer created by the last reassignment, if any. It
// is dropped as soon as another action changes the day.
func (s *Session) Remembering() (RememberOffer, bool) {
	if s.pending == nil {
		return RememberOffer{}, false
	}
	return *s.pending, true
}

// Remember writes a visible learned rule for the last reassignment: for the
// exact branch, or for the whole repository when wholeRepo is set or the entry
// had no branch. A rule with the same repo and branch is updated instead of
// duplicated. The corrected time itself is already covered by the override.
// Remembering is not a correction of its own: the reassignment before it
// already counted as one when it changed time that had a ticket.
func (s *Session) Remember(ctx context.Context, wholeRepo bool) (store.LearnedRule, error) {
	if s.pending == nil {
		return store.LearnedRule{}, ErrNothingToRemember
	}
	offer := *s.pending
	rule := store.LearnedRule{Repo: offer.Repo, BranchPattern: offer.Branch, Ticket: offer.Ticket, CreatedAt: s.now()}
	if wholeRepo {
		rule.BranchPattern = ""
	}
	existing, err := s.st.LearnedRules(ctx)
	if err != nil {
		return store.LearnedRule{}, err
	}
	update := false
	for _, r := range existing {
		if r.Repo == rule.Repo && r.BranchPattern == rule.BranchPattern {
			rule.ID, rule.CreatedAt = r.ID, r.CreatedAt
			update = true
			break
		}
	}
	err = s.apply(ctx, false, nil, nil, func(tx store.Tx) error {
		if update {
			return tx.UpdateLearnedRule(ctx, rule)
		}
		var err error
		rule, err = tx.AddLearnedRule(ctx, rule)
		return err
	})
	if err != nil {
		return store.LearnedRule{}, err
	}
	return rule, nil
}

// Suggestions returns ordered ticket candidates for an unassigned entry n,
// taken from the user's open issues in progress. Entries that already have a
// ticket get none. Suggestions are proposals only: taking one goes through
// Reassign and still needs confirmation.
func (s *Session) Suggestions(ctx context.Context, n int) ([]suggest.Candidate, error) {
	e, err := s.entry(n)
	if err != nil {
		return nil, err
	}
	if e.Kind != KindUnassigned || s.issues == nil {
		return nil, nil
	}
	if !s.fetched {
		s.fetched = true
		// A failure (no token, offline) is non-fatal: no suggestions.
		if issues, err := s.issues.MyOpenIssues(ctx); err == nil {
			s.candidates = issues
		}
	}
	return suggest.Rank(s.candidates, e.Repo+" "+e.Branch), nil
}

// ReassignSuggestion assigns entry n to its k-th (1-based) suggestion.
func (s *Session) ReassignSuggestion(ctx context.Context, n, k int) (string, error) {
	list, err := s.Suggestions(ctx, n)
	if err != nil {
		return "", err
	}
	if k < 1 || k > len(list) {
		return "", fmt.Errorf("%w: %d", ErrNoSuchSuggestion, k)
	}
	return list[k-1].Key, s.Reassign(ctx, n, list[k-1].Key)
}
