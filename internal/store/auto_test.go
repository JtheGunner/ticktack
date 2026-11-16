package store

import (
	"context"
	"testing"
	"time"
)

func TestReviewRecordsOfSameDayAreSeparateRows(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	// Two sessions with identical start times must not overwrite each other.
	rec := ReviewRecord{Day: "2026-01-05", SessionStart: base, FinishedAt: base.Add(time.Minute), Corrections: 1, Confirmed: 2}
	first, err := s.SaveReviewRecord(ctx, rec)
	if err != nil || first == 0 {
		t.Fatalf("first: %d %v", first, err)
	}
	second, err := s.SaveReviewRecord(ctx, ReviewRecord{Day: "2026-01-05", SessionStart: base, FinishedAt: base, Confirmed: 1})
	if err != nil || second == first {
		t.Fatalf("second session must get its own row: %d %v", second, err)
	}
	// Saving with the id updates that session's row.
	rec.ID, rec.FinishedAt, rec.Corrections = first, base.Add(2*time.Minute), 3
	if id, err := s.SaveReviewRecord(ctx, rec); err != nil || id != first {
		t.Fatalf("update: %d %v", id, err)
	}
	got, err := s.ReviewRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Corrections != 3 || got[0].Confirmed != 2 || got[1].Corrections != 0 || got[1].Confirmed != 1 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].SessionStart.Equal(base) || !got[0].FinishedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("times not round-tripped: %+v", got[0])
	}
}

func TestDigestRoundTripAndLast(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, found, err := s.LastDigest(ctx); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	first, err := s.AddDigest(ctx, Digest{Day: "2026-01-05", CreatedAt: base, Text: "first"})
	if err != nil || first.ID == 0 {
		t.Fatalf("add: %+v %v", first, err)
	}
	if _, err := s.AddDigest(ctx, Digest{Day: "2026-01-06", CreatedAt: base.Add(time.Hour), Text: "second"}); err != nil {
		t.Fatal(err)
	}
	last, found, err := s.LastDigest(ctx)
	if err != nil || !found || last.Text != "second" || last.Day != "2026-01-06" || !last.CreatedAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("last = %+v found=%v err=%v", last, found, err)
	}
}
