package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/store"
)

// Auto mode stays locked when one of the required days was not really
// confirmed unchanged, even though every day has a clean review session.
func TestAutoModeStaysLockedWithoutFullyConfirmedDays(t *testing.T) {
	ctx := context.Background()
	noon := time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		// spoil changes 2026-01-04 after its clean review.
		spoil func(t *testing.T, st *store.Store)
		// want is the streak shown in the lock message.
		want string
	}{
		{
			name: "minutes changed after the confirmation",
			spoil: func(t *testing.T, st *store.Store) {
				// Late events add half an hour to the confirmed item.
				at := time.Date(2026, 1, 4, 13, 0, 0, 0, time.UTC)
				events := []event.Event{{At: at, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}}
				for m := 0; m <= 30; m++ {
					events = append(events, event.Event{At: at.Add(time.Duration(m) * time.Minute), Source: "test", Kind: event.KindPresence})
				}
				for _, e := range events {
					if err := st.Append(ctx, e); err != nil {
						t.Fatal(err)
					}
				}
			},
			// The day is skipped, the two other days still count.
			want: "2 of 3",
		},
		{
			name: "a rule was changed with map",
			spoil: func(t *testing.T, st *store.Store) {
				if err := st.RecordRuleChange(ctx, store.RuleChange{ChangedAt: noon, Action: store.RuleEdited}); err != nil {
					t.Fatal(err)
				}
			},
			// The change ends the streak at 2026-01-04.
			want: "0 of 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, rec := autoTestDeps(t, true, 3)
			seedCleanDays(t, deps, 3)
			st, err := deps.OpenStore()
			if err != nil {
				t.Fatal(err)
			}
			tt.spoil(t, st)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			failOnSink(t, &deps)
			out, err := runAutoFor(t, deps)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "locked") || !strings.Contains(out, tt.want) || len(rec.calls) != 0 {
				t.Fatalf("want auto mode locked at %s:\n%s", tt.want, out)
			}
		})
	}
}
