package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/explain"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func newExplainCommand() *cobra.Command {
	var day string
	cmd := &cobra.Command{
		Use:   "explain <N|HH:MM>",
		Short: msgExplainExplainShort,
		Long:  msgExplainExplainLong,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExplain(cmd.Context(), defaultDailyDeps(cmd), day, args[0])
		},
	}
	cmd.Flags().StringVar(&day, "day", "", msgExplainDayFlag)
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
	st, err := openStoreForDay(ctx, deps.OpenStore, civilDayOf(start, loc))
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
	printExplanation(deps, ex, n, label, start, loc)
	return nil
}

// selectBlock returns the 1-based number of the block named by target. A
// clock time is read in the configured zone on the selected day: a time that
// does not exist because clocks spring forward is normalised to the following
// hour, and a time that occurs twice when clocks fall back means its first
// occurrence.
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
	// time.Date resolves a repeated wall-clock time to either occurrence;
	// pick the first one explicitly.
	if earlier := at.Add(-time.Hour).In(loc); earlier.Hour() == clock.Hour() && earlier.Minute() == clock.Minute() {
		at = earlier
	}
	for i, b := range blocks {
		if !at.Before(b.Start) && at.Before(b.End) {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf(msgExplainNoBlockAt, target, label)
}

// printExplanation prints the explanation of block n of the day starting at
// dayStart. Times are clock times of that day, except for a deciding event
// from before it (carried context), which is printed with its date.
func printExplanation(deps dailyDeps, ex explain.Explanation, n int, label string, dayStart time.Time, loc *time.Location) {
	r := deps.Render
	clock := func(t time.Time) string { return t.In(loc).Format("15:04:05") }
	eventTime := func(t time.Time) string {
		if t.Before(dayStart) {
			return t.In(loc).Format(dayLayout + " 15:04:05")
		}
		return clock(t)
	}
	b := ex.Block
	minutes := int(b.Duration() / time.Minute)

	lines := []string{
		r.Heading(fmt.Sprintf(msgExplainHeader, n, label, b.Start.In(loc).Format("15:04"), b.End.In(loc).Format("15:04"), formatMinutes(minutes))),
		"",
	}
	rows := []render.LabelRow{
		{Label: msgLabelTicket, Value: SanitizeForTerminal(ticketLabel(b.Ticket))},
		{Label: msgLabelTier, Value: colorTier(r, b.Tier)},
	}
	var conclusions, merged []string
	switch ex.Source {
	case explain.SourceOverride:
		rows = append(rows,
			render.LabelRow{Label: msgLabelSignal, Value: SanitizeForTerminal(msgExplainOverrideSignal(clock(ex.Override.Start), clock(ex.Override.End)))},
			render.LabelRow{Label: msgLabelRule, Value: SanitizeForTerminal(msgExplainOverrideRule(ex.Override.Ticket))})
	case explain.SourceFill:
		rows = append(rows,
			render.LabelRow{Label: msgLabelSignal, Value: SanitizeForTerminal(msgExplainFillSignal(clock(ex.Override.Start), clock(ex.Override.End)))},
			render.LabelRow{Label: msgLabelRule, Value: SanitizeForTerminal(msgExplainFillRule(ex.Override.Ticket))})
	case explain.SourceMerged:
		rows = append(rows, render.LabelRow{Label: msgLabelSignal, Value: msgExplainMergedSignal})
		for _, m := range ex.Merged {
			me := m.Event
			merged = append(merged, fmt.Sprintf(msgExplainMergedItem, SanitizeForTerminal(msgExplainEventSignal(
				me.Source, string(me.Kind), eventTime(me.At), noneIfEmpty(me.Repo), noneIfEmpty(me.Branch), noneIfEmpty(me.Ticket))),
				SanitizeForTerminal(msgExplainRuleText(m.Decision, m.Decision.Ticket))))
		}
	case explain.SourceNoContext:
		conclusions = append(conclusions, r.Gutter(render.StatusWarn, msgExplainNoContext))
	case explain.SourceTooShort:
		conclusions = append(conclusions, r.Gutter(render.StatusWarn, msgExplainTooShort))
	default:
		e := ex.Event
		rows = append(rows,
			render.LabelRow{Label: msgLabelSignal, Value: SanitizeForTerminal(msgExplainEventSignal(
				e.Source, string(e.Kind), eventTime(e.At), noneIfEmpty(e.Repo), noneIfEmpty(e.Branch), noneIfEmpty(e.Ticket)))},
			render.LabelRow{Label: msgLabelRule, Value: SanitizeForTerminal(msgExplainRuleText(ex.Decision, b.Ticket))})
		var skipped []string
		for _, kind := range ex.Skipped {
			skipped = append(skipped, msgExplainSkipped(kind, noneIfEmpty(ex.Signal.Repo), noneIfEmpty(ex.Signal.Branch)))
		}
		if len(skipped) > 0 {
			rows = append(rows, render.LabelRow{Label: msgLabelNotApplied, Value: SanitizeForTerminal(strings.Join(skipped, "; "))})
		}
		if ex.AppClass != "" {
			conclusions = append(conclusions, r.Gutter(render.StatusWarn, SanitizeForTerminal(msgExplainAppClass(ex.AppClass, ex.App))))
		}
		if ex.Stale {
			conclusions = append(conclusions, r.Gutter(render.StatusWarn, SanitizeForTerminal(msgExplainStale(
				sessionizer.StaleReason(ex.Age), eventTime(ex.ConfirmedAt), sessionizer.FormatAge(ex.MaxAge), ex.Pinned))))
		}
	}
	for _, f := range ex.Folded {
		rows = append(rows, render.LabelRow{Label: msgLabelFolded, Value: SanitizeForTerminal(msgExplainFolded(clock(f.Start), clock(f.End), ticketLabel(f.Ticket)))})
	}
	p := ex.Presence
	if p.Samples == 0 {
		rows = append(rows, render.LabelRow{Label: msgLabelPresence, Value: msgExplainNoSamples})
	} else {
		rows = append(rows, render.LabelRow{Label: msgLabelPresence, Value: fmt.Sprintf(msgExplainPresence, p.Samples, clock(p.First), clock(p.Last))})
	}
	lines = append(lines, r.Labels(rows, 2)...)
	for _, m := range merged {
		lines = append(lines, "    "+m)
	}
	lines = append(lines, conclusions...)
	if !p.Previous.IsZero() {
		lines = append(lines, r.Gutter(render.StatusInfo, fmt.Sprintf(msgExplainBefore, clock(p.Previous), b.Start.Sub(p.Previous).Round(time.Second))))
	}
	if !p.Next.IsZero() {
		lines = append(lines, r.Gutter(render.StatusInfo, fmt.Sprintf(msgExplainAfter, clock(p.Next), p.Next.Sub(b.End).Round(time.Second))))
	}
	for _, g := range p.Gaps {
		lines = append(lines, r.Gutter(render.StatusInfo, fmt.Sprintf(msgExplainGap, clock(g.Start), clock(g.End))))
	}
	_, _ = fmt.Fprintln(deps.Out, strings.Join(lines, "\n"))
}

func noneIfEmpty(s string) string {
	if s == "" {
		return msgNoRepo
	}
	return s
}
