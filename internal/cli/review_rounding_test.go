package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestRoundingLines(t *testing.T) {
	date := civil.Date{Year: 2026, Month: 1, Day: 5}
	tests := []struct {
		name string
		sum  sessionizer.DaySummary
		want []string
	}{
		{"exact day stays quiet", sessionizer.DaySummary{Date: date, Raw: time.Hour, Booked: time.Hour}, nil},
		{
			"dropped ticket is named",
			sessionizer.DaySummary{
				Date: date, Raw: 46 * time.Minute, Booked: 45 * time.Minute,
				Dropped: []sessionizer.Dropped{{Date: date, Ticket: "ABC-1", Raw: 6 * time.Minute}},
			},
			[]string{"tracked 46m, rounded total 45m (rounding removed 1m)", "ABC-1: 6m rounds to zero and is not booked."},
		},
		{
			"rounding up is stated",
			sessionizer.DaySummary{Date: date, Raw: 10 * time.Minute, Booked: 15 * time.Minute},
			[]string{"tracked 10m, rounded total 15m (rounding added 5m)"},
		},
		{
			"unassigned time and sub-minute time",
			sessionizer.DaySummary{
				Date: date, Raw: 20 * time.Second, Booked: 0,
				Dropped: []sessionizer.Dropped{{Date: date, Ticket: "", Raw: 20 * time.Second}},
			},
			[]string{"rounding removed <1m", "(unassigned): <1m rounds to zero"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(roundingLines(render.Renderer{}, tt.sum), "\n")
			if len(tt.want) == 0 && got != "" {
				t.Fatalf("expected no lines, got %q", got)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}

func TestReviewShowsTimeLostToRounding(t *testing.T) {
	deps, st, out := reviewTestDeps(t, "q\n")
	start := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "DEF-2"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 6; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.MinBlockDuration = time.Minute
		return cfg, err
	}
	if err := runReview(ctx, deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Rounding: tracked", "DEF-2: 6m rounds to zero and is not booked."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
