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
		name  string
		sum   sessionizer.DaySummary
		items []sessionizer.DayItem
		want  []string
		not   []string
	}{
		{"exact day stays quiet", sessionizer.DaySummary{Date: date, Raw: time.Hour, Booked: time.Hour},
			[]sessionizer.DayItem{{Ticket: "ABC-1", Minutes: 60, RawMinutes: 60}}, nil, nil},
		{
			"ticket rounded up is listed",
			sessionizer.DaySummary{Date: date, Raw: 38 * time.Minute, Booked: 45 * time.Minute},
			[]sessionizer.DayItem{{Ticket: "ABC-1", Minutes: 45, RawMinutes: 38}},
			[]string{"ABC-1: tracked 38m, booked 45m (+7m)."}, nil,
		},
		{
			"ticket that lost its remainder unit is listed, exact ticket is not",
			sessionizer.DaySummary{Date: date, Raw: 70 * time.Minute, Booked: 75 * time.Minute},
			[]sessionizer.DayItem{
				{Ticket: "ABC-1", Minutes: 45, RawMinutes: 40},
				{Ticket: "DEF-2", Minutes: 30, RawMinutes: 30},
			},
			[]string{"ABC-1: tracked 40m, booked 45m (+5m)."}, []string{"DEF-2"},
		},
		{
			"downward shift and dropped ticket appear once each",
			sessionizer.DaySummary{
				Date: date, Raw: 61 * time.Minute, Booked: 60 * time.Minute,
				Dropped: []sessionizer.Dropped{{Date: date, Ticket: "GHI-3", Raw: 6 * time.Minute}},
			},
			[]sessionizer.DayItem{{Ticket: "ABC-1", Minutes: 60, RawMinutes: 55}, {Ticket: "XYZ-9", Minutes: 15, RawMinutes: 20}},
			[]string{"ABC-1: tracked 55m, booked 1h (+5m).", "XYZ-9: tracked 20m, booked 15m (-5m).", "GHI-3: 6m rounds to zero"},
			[]string{"GHI-3: tracked"},
		},
		{
			"shifts that cancel out on the day are still listed",
			sessionizer.DaySummary{Date: date, Raw: time.Hour, Booked: time.Hour},
			[]sessionizer.DayItem{{Ticket: "ABC-1", Minutes: 30, RawMinutes: 25}, {Ticket: "DEF-2", Minutes: 30, RawMinutes: 35}},
			[]string{"ABC-1: tracked 25m, booked 30m (+5m).", "DEF-2: tracked 35m, booked 30m (-5m)."}, nil,
		},
		{
			"dropped ticket is named",
			sessionizer.DaySummary{
				Date: date, Raw: 46 * time.Minute, Booked: 45 * time.Minute,
				Dropped: []sessionizer.Dropped{{Date: date, Ticket: "ABC-1", Raw: 6 * time.Minute}},
			},
			nil,
			[]string{"tracked 46m, rounded total 45m (rounding removed 1m)", "ABC-1: 6m rounds to zero and is not booked."}, nil,
		},
		{
			"rounding up is stated",
			sessionizer.DaySummary{Date: date, Raw: 10 * time.Minute, Booked: 15 * time.Minute},
			nil,
			[]string{"tracked 10m, rounded total 15m (rounding added 5m)"}, nil,
		},
		{
			"unassigned time and sub-minute time",
			sessionizer.DaySummary{
				Date: date, Raw: 20 * time.Second, Booked: 0,
				Dropped: []sessionizer.Dropped{{Date: date, Ticket: "", Raw: 20 * time.Second}},
			},
			nil,
			[]string{"rounding removed <1m", "(unassigned): <1m rounds to zero"}, nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(roundingLines(render.Renderer{}, tt.sum, tt.items), "\n")
			if len(tt.want) == 0 && got != "" {
				t.Fatalf("expected no lines, got %q", got)
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("unexpected %q in:\n%s", n, got)
				}
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
