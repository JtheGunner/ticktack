package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuleChangesRoundTripInOrder(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	actions := []RuleChangeAction{RuleAdded, RuleEdited, RuleRemoved}
	for i, a := range actions {
		if err := s.RecordRuleChange(ctx, RuleChange{ChangedAt: at.Add(time.Duration(i) * time.Hour), Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RuleChanges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(actions) {
		t.Fatalf("got %d rule changes, want %d", len(got), len(actions))
	}
	for i, a := range actions {
		if got[i].Action != a || !got[i].ChangedAt.Equal(at.Add(time.Duration(i)*time.Hour)) || got[i].ID == 0 {
			t.Errorf("change %d = %+v, want action %s", i, got[i], a)
		}
	}
}

func TestRecordRuleChangeRejectsUnknownAction(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.RecordRuleChange(context.Background(), RuleChange{ChangedAt: time.Now(), Action: "rename"}); err == nil {
		t.Fatal("an unknown action must be rejected")
	}
}

func TestPurgeClearsRuleChanges(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if err := s.RecordRuleChange(ctx, RuleChange{ChangedAt: time.Now(), Action: RuleAdded}); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.RuleChanges(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("rule changes after purge = %+v, err %v", got, err)
	}
}

// A rule change and the rule write it belongs to are stored together or not
// at all.
func TestInTxRollsBackRuleChangeWithItsRule(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.InTx(ctx, func(tx Tx) error {
		if _, err := tx.AddLearnedRule(ctx, LearnedRule{Repo: "/work/app", Ticket: "ABC-1", CreatedAt: time.Now()}); err != nil {
			return err
		}
		if err := tx.RecordRuleChange(ctx, RuleChange{ChangedAt: time.Now(), Action: RuleAdded}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	rules, err := s.LearnedRules(ctx)
	if err != nil || len(rules) != 0 {
		t.Fatalf("rules = %+v, err %v", rules, err)
	}
	changes, err := s.RuleChanges(ctx)
	if err != nil || len(changes) != 0 {
		t.Fatalf("rule changes = %+v, err %v", changes, err)
	}
}
