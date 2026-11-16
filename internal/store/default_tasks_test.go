package store

import (
	"context"
	"testing"
)

func TestProjectsRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if got, at, err := s.Projects(ctx); err != nil || len(got) != 0 || !at.IsZero() {
		t.Fatalf("empty cache = (%v, %v, %v)", got, at, err)
	}
	first := []Project{{Key: "A", Name: "Alpha"}, {Key: "B", Name: "Beta"}}
	if err := s.ReplaceProjects(ctx, first, base); err != nil {
		t.Fatal(err)
	}
	got, at, err := s.Projects(ctx)
	if err != nil || len(got) != 2 || got[0] != first[0] || got[1] != first[1] || !at.Equal(base) {
		t.Fatalf("Projects = (%v, %v, %v), want %v at %v", got, at, err, first, base)
	}

	later := base.Add(3600e9)
	if err := s.ReplaceProjects(ctx, []Project{{Key: "C", Name: "Gamma"}}, later); err != nil {
		t.Fatal(err)
	}
	got, at, err = s.Projects(ctx)
	if err != nil || len(got) != 1 || got[0].Key != "C" || !at.Equal(later) {
		t.Fatalf("after replace = (%v, %v, %v), want only C at %v", got, at, err, later)
	}
}

func TestDefaultTaskRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if got, err := s.DefaultTasks(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty = (%v, %v)", got, err)
	}
	if err := s.SetDefaultTask(ctx, "TICKT", "TICKT-7"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultTask(ctx, "TICKT", "TICKT-9"); err != nil {
		t.Fatal(err)
	}
	got, err := s.DefaultTasks(ctx)
	if err != nil || len(got) != 1 || got["TICKT"] != "TICKT-9" {
		t.Fatalf("DefaultTasks = (%v, %v), want TICKT-9 after overwrite", got, err)
	}
}

func TestConsent(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if got, err := s.ConsentedProjects(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty = (%v, %v)", got, err)
	}
	if err := s.SetConsent(ctx, "TICKT", base); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConsent(ctx, "TICKT", base); err != nil {
		t.Fatalf("setting consent twice must be idempotent: %v", err)
	}
	got, err := s.ConsentedProjects(ctx)
	if err != nil || len(got) != 1 || !got["TICKT"] {
		t.Fatalf("ConsentedProjects = (%v, %v)", got, err)
	}
}

func TestRenameConfirmationIssue(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, c := range []Confirmation{
		{Day: "2026-03-02", Issue: "TICKT:default", Type: "Development", Minutes: 60, ConfirmedAt: base},
		{Day: "2026-03-03", Issue: "TICKT:default", Type: "Development", Minutes: 30, ConfirmedAt: base},
		{Day: "2026-03-03", Issue: "TICKT-7", Type: "Development", Minutes: 45, ConfirmedAt: base},
		{Day: "2026-03-03", Issue: "OTHER-1", Type: "Development", Minutes: 15, ConfirmedAt: base},
	} {
		if err := s.Confirm(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenameConfirmationIssue(ctx, "TICKT:default", "TICKT-7"); err != nil {
		t.Fatal(err)
	}

	moved, _ := s.Confirmations(ctx, "2026-03-02")
	if len(moved) != 1 || moved[0].Issue != "TICKT-7" || moved[0].Minutes != 60 {
		t.Errorf("day 2 = %+v, want the row moved to TICKT-7", moved)
	}
	conflict, _ := s.Confirmations(ctx, "2026-03-03")
	if len(conflict) != 2 {
		t.Fatalf("day 3 = %+v, want the target row and the unrelated row only", conflict)
	}
	for _, c := range conflict {
		if c.Issue == "TICKT:default" {
			t.Errorf("placeholder row survived: %+v", c)
		}
		if c.Issue == "TICKT-7" && c.Minutes != 45 {
			t.Errorf("target row was overwritten: %+v", c)
		}
	}
}

func TestMigrationKeepsExistingTables(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if err := s.Confirm(ctx, Confirmation{Day: "2026-03-02", Issue: "ABC-1", Type: "Development", Minutes: 15, ConfirmedAt: base}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LearnedRules(ctx); err != nil {
		t.Fatalf("learned_rules unreadable after the migration: %v", err)
	}
}
