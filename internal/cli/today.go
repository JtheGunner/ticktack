package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

const dayLayout = "2006-01-02"

func newTodayCommand() *cobra.Command {
	var day string
	cmd := &cobra.Command{
		Use:   "today",
		Short: "List today's blocks and totals",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runToday(cmd.Context(), defaultDailyDeps(cmd), day)
		},
	}
	cmd.Flags().StringVar(&day, "day", "", "day to show as YYYY-MM-DD (default: today)")
	return cmd
}

// runToday derives and prints the blocks and per-ticket totals of one day.
// day is empty for today in the configured time zone.
func runToday(ctx context.Context, deps dailyDeps, day string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	loc := cfg.Location
	start, err := dayStart(day, deps.Now(), loc)
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	derived, err := deriveDay(ctx, st, cfg, start)
	if err != nil {
		return err
	}
	blocks := derived.Blocks
	label := start.Format(dayLayout)
	if len(blocks) == 0 {
		_, err = fmt.Fprintf(deps.Out, msgNoBlocks+"\n", label)
		return err
	}

	out := deps.Out
	_, _ = fmt.Fprintf(out, "Blocks for %s:\n", label)
	for i, b := range blocks {
		_, _ = fmt.Fprintf(out, "  %2d. %s-%s  %-12s %-9s %s\n", i+1,
			b.Start.In(loc).Format("15:04"), b.End.In(loc).Format("15:04"),
			SanitizeForTerminal(ticketLabel(b.Ticket)), b.Tier, SanitizeForTerminal(b.Reason))
	}

	_, _ = fmt.Fprintln(out, "Totals:")
	total := 0
	overCap := false
	for _, item := range sessionizer.Aggregate(blocks, sessionizer.RoundingConfigFrom(cfg), loc) {
		if item.Date.String() != label {
			continue
		}
		total += item.Minutes
		overCap = overCap || item.OverCap
		_, _ = fmt.Fprintf(out, "  %-12s %6s  (raw %s)\n", SanitizeForTerminal(ticketLabel(item.Ticket)), formatMinutes(item.Minutes), formatMinutes(item.RawMinutes))
	}
	_, err = fmt.Fprintf(out, "  %-12s %6s%s\n", "Total", formatMinutes(total), overCapNote(overCap, cfg.DailyCap))
	return err
}

// dayStart returns local midnight of the requested day (default: today).
func dayStart(day string, now time.Time, loc *time.Location) (time.Time, error) {
	if day == "" {
		local := now.In(loc)
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc), nil
	}
	parsed, err := time.ParseInLocation(dayLayout, day, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("--day %q: expected format YYYY-MM-DD", day)
	}
	return parsed, nil
}

func ticketLabel(ticket string) string {
	if ticket == "" {
		return msgUnassigned
	}
	return ticket
}

func overCapNote(overCap bool, dailyCap time.Duration) string {
	if !overCap {
		return ""
	}
	return fmt.Sprintf("  OVER DAILY CAP (%s)", dailyCap)
}

// derivedDay is the input and result of deriving one local day.
type derivedDay struct {
	Events    []event.Event
	Overrides []event.Override
	Rules     resolver.Rules
	Blocks    []sessionizer.Block
}

// deriveDay loads the events, overrides and rules of the local day starting at
// start and derives its blocks. Block numbers (1-based) are indexes into
// Blocks, so today and explain agree.
func deriveDay(ctx context.Context, st dailyStore, cfg config.Config, start time.Time) (derivedDay, error) {
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, cfg.Location)
	events, err := st.Events(ctx, start, end)
	if err != nil {
		return derivedDay{}, err
	}
	overrides, err := st.Overrides(ctx, start, end)
	if err != nil {
		return derivedDay{}, err
	}
	rules, err := resolverRules(ctx, st, cfg)
	if err != nil {
		return derivedDay{}, err
	}
	blocks := sessionizer.Derive(events, overrides, sessionizer.ConfigFrom(cfg), rules)
	return derivedDay{Events: events, Overrides: overrides, Rules: rules, Blocks: blocks}, nil
}
