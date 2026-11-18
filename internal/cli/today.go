package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/review"
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
	r := deps.Render
	if len(blocks) == 0 {
		_, err = fmt.Fprintln(deps.Out, r.Gutter(render.StatusInfo, fmt.Sprintf(msgNoBlocks, label)))
		return err
	}

	lines := []string{r.Heading(msgTodayHeading(label)), ""}
	blockRows := make([][]string, 0, len(blocks))
	for i, b := range blocks {
		blockRows = append(blockRows, []string{
			strconv.Itoa(i + 1),
			b.Start.In(loc).Format("15:04") + "-" + b.End.In(loc).Format("15:04"),
			SanitizeForTerminal(ticketLabel(b.Ticket)),
			colorTier(r, b.Tier),
			SanitizeForTerminal(b.Reason),
		})
	}
	lines = append(lines, r.Table([]render.Column{
		{Header: msgColBlock, Right: true},
		{Header: msgColTime},
		{Header: msgColTicket},
		{Header: msgColTier},
		{Header: msgColReason, Flex: true},
	}, blockRows)...)

	total := 0
	overCap := false
	var totalRows [][]string
	for _, item := range sessionizer.Aggregate(blocks, sessionizer.RoundingConfigFrom(cfg), loc) {
		if item.Date.String() != label {
			continue
		}
		total += item.Minutes
		overCap = overCap || item.OverCap
		totalRows = append(totalRows, []string{
			SanitizeForTerminal(ticketLabel(item.Ticket)),
			formatMinutes(item.Minutes),
			formatMinutes(item.RawMinutes),
		})
	}
	lines = append(lines, "", r.Heading(msgTotalsHeading))
	lines = append(lines, r.Table([]render.Column{
		{Header: msgColTicket},
		{Header: msgColTime, Right: true},
		{Header: msgColRaw, Right: true},
	}, totalRows)...)
	lines = append(lines, "")
	lines = append(lines, r.Labels([]render.LabelRow{{Label: msgLabelTotal, Value: formatMinutes(total)}}, 2)...)
	if overCap {
		lines = append(lines, r.Gutter(render.StatusWarn, msgOverDailyCap(cfg.DailyCap)))
	}
	_, err = fmt.Fprintln(deps.Out, strings.Join(lines, "\n"))
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
	if project, ok := resolver.ParseDefaultRef(ticket); ok {
		return fmt.Sprintf(msgStandardTaskNew, project)
	}
	return ticket
}

// derivedDay is the input and result of deriving one local day.
type derivedDay struct {
	Events    []event.Event
	Overrides []event.Override
	Rules     resolver.Rules
	Blocks    []sessionizer.Block
}

// deriveDay loads the events, overrides and rules of the local day starting at
// start and derives its blocks through the loader review uses, so the day
// starts with the context carried over from before it. Block numbers (1-based) are indexes into
// Blocks, so today and explain agree.
func deriveDay(ctx context.Context, st dailyStore, cfg config.Config, start time.Time) (derivedDay, error) {
	end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, cfg.Location)
	in, err := review.LoadDayInput(ctx, st, cfg, start, end)
	if err != nil {
		return derivedDay{}, err
	}
	return derivedDay{Events: in.Events, Overrides: in.Overrides, Rules: in.Rules, Blocks: in.Blocks}, nil
}
