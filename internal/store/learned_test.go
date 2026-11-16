package store

import (
	"context"
	"errors"
	"testing"
)

func TestLearnedRulesRoundTripUpdateDelete(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	added, err := s.AddLearnedRule(ctx, LearnedRule{Repo: "/work/app/", BranchPattern: "main", Ticket: "abc-1", CreatedAt: base})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == 0 || added.Repo != "/work/app" || added.Ticket != "ABC-1" {
		t.Fatalf("added = %+v", added)
	}
	got, err := s.LearnedRules(ctx)
	if err != nil || len(got) != 1 || got[0] != added {
		t.Fatalf("rules = %+v err=%v", got, err)
	}
	added.Ticket = "XYZ-2"
	added.BranchPattern = ""
	if err := s.UpdateLearnedRule(ctx, added); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LearnedRules(ctx)
	if len(got) != 1 || got[0].Ticket != "XYZ-2" || got[0].BranchPattern != "" {
		t.Fatalf("after update = %+v", got)
	}
	if err := s.DeleteLearnedRule(ctx, added.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.LearnedRules(ctx); len(got) != 0 {
		t.Fatalf("after delete = %+v", got)
	}
	if err := s.DeleteLearnedRule(ctx, added.ID); !errors.Is(err, ErrNoSuchLearnedRule) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.UpdateLearnedRule(ctx, added); !errors.Is(err, ErrNoSuchLearnedRule) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestLearnedRuleRejectsMatchEverythingAndEmptyTicket(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, r := range []LearnedRule{
		{Ticket: "ABC-1"},
		{Repo: "  ", BranchPattern: " ", Ticket: "ABC-1"},
		{Repo: "/work/app"},
	} {
		if _, err := s.AddLearnedRule(ctx, r); !errors.Is(err, ErrInvalidLearnedRule) {
			t.Errorf("add %+v: err = %v", r, err)
		}
	}
	ok, err := s.AddLearnedRule(ctx, LearnedRule{Repo: "/work/app", Ticket: "ABC-1"})
	if err != nil {
		t.Fatal(err)
	}
	ok.Repo = ""
	if err := s.UpdateLearnedRule(ctx, ok); !errors.Is(err, ErrInvalidLearnedRule) {
		t.Errorf("update to match-everything: err = %v", err)
	}
}
