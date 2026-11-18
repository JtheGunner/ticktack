package review

import (
	"context"
	"errors"
	"testing"
)

func TestSplitUnassignedGap(t *testing.T) {
	tests := []struct {
		name    string
		ticket  string
		wantErr error
	}{
		{"without a ticket is rejected", "", ErrSplitNeedsTicket},
		{"blank ticket is rejected", "  ", ErrSplitNeedsTicket},
		{"with a ticket assigns the second part", "XYZ-2", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := openStore(t)
			seed(t, st, dayStart, 60, "")
			s := newSession(t, st, testConfig())
			if got := s.Entries(); len(got) != 1 || got[0].Kind != KindGap {
				t.Fatalf("entries = %+v, want one gap", got)
			}
			err := s.Split(ctx, 1, "09:30", tt.ticket)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			entries, records := s.Entries(), recordsOf(t, st)
			if tt.wantErr != nil {
				// Nothing changed and nothing was logged as a correction.
				if len(entries) != 1 || len(records) != 0 {
					t.Errorf("entries = %d, records = %+v, want the day untouched", len(entries), records)
				}
				return
			}
			if len(entries) != 2 || entries[0].Ticket != "" || entries[1].Ticket != "XYZ-2" {
				t.Errorf("entries = %+v, want unassigned then XYZ-2", entries)
			}
			// Giving unassigned time a ticket is logged, but not as a correction.
			if len(records) != 1 || records[0].Corrections != 0 {
				t.Errorf("records = %+v, want one record without a correction", records)
			}
		})
	}
}
