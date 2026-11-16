package store

import (
	"context"
	"testing"
	"time"
)

func TestReviewRecordIsReplacedPerSession(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	rec := ReviewRecord{Day: "2026-01-05", SessionStart: base, FinishedAt: base.Add(time.Minute), Corrections: 1, Confirmed: 2}
	if err := s.SaveReviewRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.FinishedAt, rec.Corrections = base.Add(2*time.Minute), 3
	if err := s.SaveReviewRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	other := ReviewRecord{Day: "2026-01-05", SessionStart: base.Add(time.Hour), FinishedAt: base.Add(time.Hour), Confirmed: 1}
	if err := s.SaveReviewRecord(ctx, other); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReviewRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Corrections != 3 || got[0].Confirmed != 2 || got[1].Corrections != 0 {
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
