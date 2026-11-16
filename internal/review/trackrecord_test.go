package review

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/store"
)

type fakeLog struct{ records []store.ReviewRecord }

func (f *fakeLog) ReviewRecords(context.Context) ([]store.ReviewRecord, error) {
	return f.records, nil
}

// rec seeds one review record the given number of days before the reference day.
func (f *fakeLog) rec(daysBefore, corrections, confirmed int) {
	d := civil.DateOf(fixedNow).AddDays(-daysBefore)
	at := time.Date(d.Year, d.Month, d.Day, 18, 0, 0, 0, time.UTC)
	f.records = append(f.records, store.ReviewRecord{
		Day: d.String(), SessionStart: at, FinishedAt: at, Corrections: corrections, Confirmed: confirmed,
	})
}

func streakOf(t *testing.T, f *fakeLog) int {
	t.Helper()
	n, err := TrackRecord(context.Background(), f, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrackRecordCountsCleanDaysAndSkipsUnreviewedOnes(t *testing.T) {
	f := &fakeLog{}
	// Days 0, 1, 4 reviewed (a weekend gap in between).
	f.rec(0, 0, 2)
	f.rec(1, 0, 1)
	f.rec(4, 0, 3)
	if got := streakOf(t, f); got != 3 {
		t.Fatalf("streak = %d, want 3", got)
	}
}

func TestTrackRecordCorrectionBreaksStreak(t *testing.T) {
	f := &fakeLog{}
	f.rec(5, 0, 1)
	f.rec(4, 0, 1)
	f.rec(3, 1, 1) // correction
	f.rec(2, 0, 1)
	f.rec(1, 0, 1)
	if got := streakOf(t, f); got != 2 {
		t.Fatalf("streak = %d, want 2", got)
	}
}

func TestTrackRecordUsesLatestRecordOfADay(t *testing.T) {
	f := &fakeLog{}
	f.rec(1, 2, 1)
	f.records = append(f.records, store.ReviewRecord{
		Day: civil.DateOf(fixedNow).AddDays(-1).String(), SessionStart: fixedNow, FinishedAt: fixedNow, Confirmed: 1,
	})
	if got := streakOf(t, f); got != 1 {
		t.Fatalf("streak = %d, want 1 (latest record is clean)", got)
	}
}

func TestTrackRecordIgnoresDaysWithoutConfirmations(t *testing.T) {
	f := &fakeLog{}
	f.rec(2, 0, 1)
	f.rec(1, 0, 0)
	if got := streakOf(t, f); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}

func TestTrackRecordIgnoresFutureDays(t *testing.T) {
	f := &fakeLog{}
	f.rec(-3, 0, 1)
	f.rec(0, 0, 1)
	if got := streakOf(t, f); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}

func TestTrackRecordEmpty(t *testing.T) {
	if got := streakOf(t, &fakeLog{}); got != 0 {
		t.Fatalf("streak = %d, want 0", got)
	}
}

func recordsOf(t *testing.T, st *store.Store) []store.ReviewRecord {
	t.Helper()
	got, err := st.ReviewRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSessionCountsCorrectionsButNotConfirms(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		act         func(s *Session) error
		corrections int
	}{
		{"confirm", func(s *Session) error { return s.Confirm(ctx, 1) }, 0},
		{"confirm all", func(s *Session) error { _, err := s.ConfirmAll(ctx); return err }, 0},
		{"reassign", func(s *Session) error { return s.Reassign(ctx, 1, "XYZ-9") }, 1},
		{"discard", func(s *Session) error { return s.Discard(ctx, 1) }, 1},
		{"break", func(s *Session) error { return s.MarkBreak(ctx, 1) }, 1},
		{"split", func(s *Session) error { return s.Split(ctx, 1, "09:30", "XYZ-9") }, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			seed(t, st, dayStart, 60, "ABC-1")
			s := newSession(t, st, testConfig())
			if err := tt.act(s); err != nil {
				t.Fatal(err)
			}
			got := recordsOf(t, st)
			if len(got) != 1 || got[0].Corrections != tt.corrections || got[0].Day != testDay.String() {
				t.Fatalf("records = %+v, want %d corrections", got, tt.corrections)
			}
		})
	}
}

func TestSessionRecordsRememberedRuleAsCorrection(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seedContext(t, st, dayStart, 60, learnRepo, learnBranch)
	s := newSession(t, st, testConfig())
	if err := s.Reassign(ctx, 1, "ABC-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember(ctx, true); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Corrections != 2 {
		t.Fatalf("records = %+v, want 2 corrections (reassign + remembered rule)", got)
	}
}

func TestSessionRecordSurvivesWithoutFinish(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s := newSession(t, st, testConfig())
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// No Finish: the session was aborted. What it did is still logged.
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Corrections != 1 {
		t.Fatalf("records = %+v", got)
	}
}

func TestSessionFinishRecordsConfirmedCount(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s := newSession(t, st, testConfig())
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Confirmed != 1 || got[0].Corrections != 0 {
		t.Fatalf("records = %+v", got)
	}
	if n, err := TrackRecord(ctx, st, fixedNow); err != nil || n != 1 {
		t.Fatalf("streak = %d err %v", n, err)
	}
}

func TestSessionWithoutActionsAndFinishLeavesNoRecord(t *testing.T) {
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	newSession(t, st, testConfig())
	if got := recordsOf(t, st); len(got) != 0 {
		t.Fatalf("records = %+v", got)
	}
}
