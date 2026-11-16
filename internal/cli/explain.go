package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/explain"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func newExplainCommand() *cobra.Command {
	var day string
	cmd := &cobra.Command{
		Use:   "explain <N|HH:MM>",
		Short: "Show the signal and rule behind a block",
		Long: "Show which signal and which rule decided a block of `ticktack today`.\n" +
			"The argument is the block number printed by `ticktack today`, or a local time HH:MM inside a block.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplain(cmd.Context(), defaultDailyDeps(cmd), day, args[0])
		},
	}
	cmd.Flags().StringVar(&day, "day", "", "day as YYYY-MM-DD (default: today)")
	return cmd
}

// runExplain prints the explanation of the block selected by target.
func runExplain(ctx context.Context, deps dailyDeps, day, target string) error {
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
	label := start.Format(dayLayout)
	if len(derived.Blocks) == 0 {
		return fmt.Errorf(msgExplainNoBlocks, label)
	}
	n, err := selectBlock(derived.Blocks, target, start, loc, label)
	if err != nil {
		return err
	}
	b := derived.Blocks[n-1]
	ex := explain.Block(explain.Input{
		Block: b, Events: derived.Events, Overrides: derived.Overrides,
		Rules: derived.Rules, Config: sessionizer.ConfigFrom(cfg),
	})
	printExplanation(deps, ex, n, label, loc)
	return nil
}

// selectBlock returns the 1-based number of the block named by target.
func selectBlock(blocks []sessionizer.Block, target string, dayStart time.Time, loc *time.Location, label string) (int, error) {
	if !strings.Contains(target, ":") {
		n, err := strconv.Atoi(target)
		if err != nil {
			return 0, fmt.Errorf(msgExplainBadTarget, SanitizeForTerminal(target))
		}
		if n < 1 || n > len(blocks) {
			return 0, fmt.Errorf(msgExplainNoSuchNum, n, label, len(blocks))
		}
		return n, nil
	}
	clock, err := time.ParseInLocation("15:04", target, loc)
	if err != nil {
		return 0, fmt.Errorf(msgExplainBadTarget, SanitizeForTerminal(target))
	}
	at := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	for i, b := range blocks {
		if !at.Before(b.Start) && at.Before(b.End) {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf(msgExplainNoBlockAt, target, label)
}

func printExplanation(deps dailyDeps, ex explain.Explanation, n int, label string, loc *time.Location) {
	out := deps.Out
	clock := func(t time.Time) string { return t.In(loc).Format("15:04:05") }
	b := ex.Block
	minutes := int(b.Duration() / time.Minute)
	line := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format+"\n", args...) }

	line(msgExplainHeader, n, label, b.Start.In(loc).Format("15:04"), b.End.In(loc).Format("15:04"), formatMinutes(minutes))
	line(msgExplainTicket, SanitizeForTerminal(ticketLabel(b.Ticket)))
	line(msgExplainTier, b.Tier)

	switch ex.Source {
	case explain.SourceOverride:
		line(msgExplainSignal, msgExplainOverrideSignal(clock(ex.Override.Start), clock(ex.Override.End)))
		line(msgExplainRule, SanitizeForTerminal(msgExplainOverrideRule(ex.Override.Ticket)))
	case explain.SourceNoContext:
		line(msgExplainSignal, msgExplainNoContext)
	case explain.SourceTooShort:
		line(msgExplainSignal, msgExplainTooShort)
	default:
		e := ex.Event
		line(msgExplainSignal, SanitizeForTerminal(msgExplainEventSignal(
			e.Source, string(e.Kind), clock(e.At), noneIfEmpty(e.Repo), noneIfEmpty(e.Branch), noneIfEmpty(e.Ticket))))
		line(msgExplainRule, SanitizeForTerminal(msgExplainRuleText(ex.Decision, b.Ticket)))
		var skipped []string
		for _, kind := range ex.Skipped {
			skipped = append(skipped, msgExplainSkipped(kind, noneIfEmpty(ex.Signal.Repo), noneIfEmpty(ex.Signal.Branch)))
		}
		if len(skipped) > 0 {
			line(msgExplainNotApplied, SanitizeForTerminal(strings.Join(skipped, "; ")))
		}
	}
	printPresence(line, ex.Presence, b.Start, b.End, clock)
}

func printPresence(line func(string, ...any), p explain.Presence, start, end time.Time, clock func(time.Time) string) {
	if p.Samples == 0 {
		line(msgExplainNoSamples)
	} else {
		line(msgExplainPresence, p.Samples, clock(p.First), clock(p.Last))
	}
	if !p.Previous.IsZero() {
		line(msgExplainBefore, clock(p.Previous), start.Sub(p.Previous).Round(time.Second))
	}
	if !p.Next.IsZero() {
		line(msgExplainAfter, clock(p.Next), p.Next.Sub(end).Round(time.Second))
	}
	for _, g := range p.Gaps {
		line(msgExplainGap, clock(g.Start), clock(g.End))
	}
}

func noneIfEmpty(s string) string {
	if s == "" {
		return msgNoRepo
	}
	return s
}
