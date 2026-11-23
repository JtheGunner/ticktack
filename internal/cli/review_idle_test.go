package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/store"
)

// withIdleGap adds DEF-2 from 11:00 to 12:00 to the seeded ABC-1 hour, leaving
// the hour from 10:00 to 11:00 as an idle gap. The scripted entries are then
// 1 = ABC-1, 2 = idle gap, 3 = DEF-2.
func withIdleGap(t *testing.T, st *store.Store) {
	t.Helper()
	start := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "DEF-2"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
}

func overridesOf(t *testing.T, st *store.Store) []event.Override {
	t.Helper()
	day := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	got, err := st.Overrides(context.Background(), day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReviewScriptedIdleGapActions(t *testing.T) {
	tests := []struct {
		name      string
		script    string
		wantOut   []string
		wantKinds []event.OverrideKind
	}{
		{
			name:      "fill the gap, confirm everything",
			script:    "t 2 meet-1\ny\nq\n",
			wantOut:   []string{"idle gap", "MEET-1", "manuell gefüllt", "3 confirmed"},
			wantKinds: []event.OverrideKind{event.OverrideFill},
		},
		{
			name:      "mark the gap as a break",
			script:    "b 2\nq\n",
			wantOut:   []string{"marked as break", "Breaks: 1h (never booked", "1h of breaks (not booked)"},
			wantKinds: []event.OverrideKind{event.OverrideBreak},
		},
		{
			name:      "discard the gap",
			script:    "d 2\nq\n",
			wantOut:   []string{"Entry 2 discarded", "1 discarded"},
			wantKinds: []event.OverrideKind{event.OverrideDiscard},
		},
		{
			name:      "confirming an idle gap is refused",
			script:    "c 2\nq\n",
			wantOut:   []string{"never booked"},
			wantKinds: nil,
		},
		{
			name:      "undo after discard",
			script:    "d 1\nz\nq\n",
			wantOut:   []string{"Undid the last action: discard of 09:00-10:00", "0 correction(s)"},
			wantKinds: nil,
		},
		{
			name:      "undo after break",
			script:    "b 2\nz\nq\n",
			wantOut:   []string{"Undid the last action: break 10:00-11:00"},
			wantKinds: nil,
		},
		{
			name:      "undo after fill",
			script:    "t 2 meet-1\nz\nq\n",
			wantOut:   []string{"Undid the last action: fill of 10:00-11:00 with MEET-1"},
			wantKinds: nil,
		},
		{
			name:      "undo after reassign",
			script:    "t 1 xyz-1\nz\nq\n",
			wantOut:   []string{"Undid the last action: assignment of 09:00-10:00 to XYZ-1", "0 correction(s)"},
			wantKinds: nil,
		},
		{
			name:      "undo twice takes back the last two only",
			script:    "d 1\nt 1 meet-1\nt 2 xyz-1\nz\nz\nq\n",
			wantOut:   []string{"assignment of 11:00-12:00 to XYZ-1", "Undid the last action: fill of 10:00-11:00 with MEET-1", "1 correction(s)"},
			wantKinds: []event.OverrideKind{event.OverrideDiscard},
		},
		{
			name:      "undo with nothing to undo says so",
			script:    "z\nd 1\nz\nz\nq\n",
			wantOut:   []string{"Nothing to undo"},
			wantKinds: nil,
		},
		{
			name:      "undo takes no arguments",
			script:    "z 1\nq\n",
			wantOut:   []string{"Usage: z"},
			wantKinds: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, st, out := reviewTestDeps(t, tt.script)
			withIdleGap(t, st)
			if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
			got := overridesOf(t, st)
			if len(got) != len(tt.wantKinds) {
				t.Fatalf("overrides = %+v, want kinds %v", got, tt.wantKinds)
			}
			for i, k := range tt.wantKinds {
				if got[i].Kind != k {
					t.Errorf("override %d kind = %s, want %s", i, got[i].Kind, k)
				}
			}
		})
	}
}

func TestReviewIdleGapGolden(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "t 2 meet-1\nb 3\nz\nb 3\nq\n")
	withIdleGap(t, st)
	deps.Render = render.Plain()
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "review_idle_gap_plain", out.String())
}

func TestReviewUndoLeavesNoCorrectionInTheLog(t *testing.T) {
	deps, st, _ := reviewTestDeps(t, "d 1\nt 2 xyz-1\nz\nz\nq\n")
	withIdleGap(t, st)
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	records, err := st.ReviewRecords(context.Background())
	if err != nil || len(records) != 1 || records[0].Corrections != 0 {
		t.Fatalf("records = %+v err=%v, want one record without corrections", records, err)
	}
}

func TestReviewSplitQuestionWithoutAnswerAbortsTheSplit(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "s 1 09:30\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Split cancelled") || strings.Contains(out.String(), "split at") {
		t.Errorf("output:\n%s", out.String())
	}
	if got := overridesOf(t, st); len(got) != 0 {
		t.Errorf("overrides = %+v, want none: end of input must not confirm the split", got)
	}
}

func TestReviewSplitQuestionAnswers(t *testing.T) {
	tests := []struct {
		name      string
		script    string
		wantOut   string
		wantCount int
	}{
		{"empty answer keeps the ticket", "s 1 09:30\n\nq\n", "split at 09:30", 1},
		{"ticket answer assigns the second part", "s 1 09:30\nxyz-1\nq\n", "split at 09:30", 1},
		{"override block cannot be split keeping its ticket", "t 1 zzz-1\ns 1 09:30\n\nq\n", "different ticket", 1},
		{"override block split with a new ticket works", "t 1 zzz-1\ns 1 09:30\nxyz-1\nq\n", "split at 09:30", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, st, out := reviewTestDeps(t, tt.script)
			if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output missing %q:\n%s", tt.wantOut, out.String())
			}
			if got := overridesOf(t, st); len(got) != tt.wantCount {
				t.Errorf("%d overrides, want %d: %+v", len(got), tt.wantCount, got)
			}
		})
	}
}

func TestReviewCancelledReadAtSplitQuestionDoesNotSplit(t *testing.T) {
	deps, st, _ := reviewTestDeps(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	deps.In = NewContextReader(ctx, pr)
	go func() {
		_, _ = pw.Write([]byte("s 1 09:30\n"))
		cancel()
	}()
	err := runReview(ctx, deps, "2026-01-05")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := overridesOf(t, st); len(got) != 0 {
		t.Errorf("overrides = %+v, a cancelled read is never consent", got)
	}
}

func TestReviewSplitOfABreakIsRefused(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "b 1\ns 1 09:30 abc-9\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "a break cannot be split") {
		t.Errorf("output:\n%s", out.String())
	}
	if got := overridesOf(t, st); len(got) != 1 || got[0].Kind != event.OverrideBreak {
		t.Errorf("overrides = %+v, want only the break", got)
	}
}
