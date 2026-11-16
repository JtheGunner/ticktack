package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/store"
)

var errLogWrite = errors.New("review log write failed")

// failingLogStore makes every review log write fail inside InTx, as if the
// process died between the action and its log entry.
type failingLogStore struct{ *store.Store }

func (f failingLogStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return f.Store.InTx(ctx, func(tx store.Tx) error { return fn(failingLogTx{tx}) })
}

type failingLogTx struct{ store.Tx }

func (failingLogTx) SaveReviewRecord(context.Context, store.ReviewRecord) (int64, error) {
	return 0, errLogWrite
}

func TestActionAndReviewLogAreWrittenAtomically(t *testing.T) {
	actions := []struct {
		name string
		run  func(ctx context.Context, s *Session) error
	}{
		{"confirm", func(ctx context.Context, s *Session) error { return s.Confirm(ctx, 1) }},
		{"confirm all", func(ctx context.Context, s *Session) error { _, err := s.ConfirmAll(ctx); return err }},
		{"reassign", func(ctx context.Context, s *Session) error { return s.Reassign(ctx, 1, "XYZ-9") }},
		{"discard", func(ctx context.Context, s *Session) error { return s.Discard(ctx, 1) }},
		{"break", func(ctx context.Context, s *Session) error { return s.MarkBreak(ctx, 1) }},
		{"split", func(ctx context.Context, s *Session) error { return s.Split(ctx, 1, "09:30", "XYZ-2") }},
	}
	end := dayStart.Add(24 * time.Hour)
	for _, a := range actions {
		t.Run(a.name+" rolls back when the log write fails", func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			seed(t, st, dayStart, 60, "ABC-1")
			s, err := NewSession(ctx, failingLogStore{st}, testConfig(), testDay, testClock)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.run(ctx, s); !errors.Is(err, errLogWrite) {
				t.Fatalf("err = %v, want the log write failure", err)
			}
			overrides, _ := st.Overrides(ctx, dayStart, end)
			confirmations, _ := st.Confirmations(ctx, testDay.String())
			if len(overrides) != 0 || len(confirmations) != 0 || len(recordsOf(t, st)) != 0 {
				t.Errorf("half written: %d overrides, %d confirmations, %d records",
					len(overrides), len(confirmations), len(recordsOf(t, st)))
			}
		})
		t.Run(a.name+" writes action and log together", func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			seed(t, st, dayStart, 60, "ABC-1")
			s := newSession(t, st, testConfig())
			if err := a.run(ctx, s); err != nil {
				t.Fatal(err)
			}
			overrides, _ := st.Overrides(ctx, dayStart, end)
			confirmations, _ := st.Confirmations(ctx, testDay.String())
			if len(overrides)+len(confirmations) == 0 || len(recordsOf(t, st)) != 1 {
				t.Errorf("stored %d overrides, %d confirmations, %d records",
					len(overrides), len(confirmations), len(recordsOf(t, st)))
			}
		})
	}
}

func TestFailedActionLeavesSessionCountersUntouched(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	seed(t, st, dayStart, 60, "ABC-1")
	s, err := NewSession(ctx, failingLogStore{st}, testConfig(), testDay, testClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reassign(ctx, 1, "XYZ-9"); err == nil {
		t.Fatal("expected the log failure")
	}
	// Continue on a healthy store: the failed action must not have counted.
	s.st = st
	if err := s.Confirm(ctx, 1); err != nil {
		t.Fatal(err)
	}
	got := recordsOf(t, st)
	if len(got) != 1 || got[0].Corrections != 0 || got[0].Confirmed != 1 {
		t.Fatalf("records = %+v, want 0 corrections and 1 confirmed", got)
	}
}
