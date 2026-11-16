package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

func TestInTxCommitsAllWritesOrNone(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		fnErr   error
		wantErr error
		stored  int
	}{
		{"success writes both", nil, nil, 1},
		{"failure rolls the first write back", boom, boom, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := openTemp(t)
			at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
			err := s.InTx(ctx, func(tx Tx) error {
				if err := tx.AddOverride(ctx, event.Override{Start: at, End: at.Add(time.Hour), Ticket: "ABC-1"}); err != nil {
					return err
				}
				if tt.fnErr != nil {
					return tt.fnErr
				}
				_, err := tx.SaveReviewRecord(ctx, ReviewRecord{Day: "2026-03-02", SessionStart: at, FinishedAt: at, Corrections: 1})
				return err
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			overrides, err := s.Overrides(ctx, at.Add(-time.Hour), at.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			records, err := s.ReviewRecords(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(overrides) != tt.stored || len(records) != tt.stored {
				t.Errorf("stored %d overrides and %d records, want %d each", len(overrides), len(records), tt.stored)
			}
		})
	}
}
