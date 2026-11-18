package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// statusLookback bounds how far back status looks for the last presence
// sample. The context is not read from it: see currentDayEvents.
const statusLookback = 14 * 24 * time.Hour

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what ticktack currently detects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd.Context(), defaultDailyDeps(cmd))
		},
	}
}

// runStatus prints the current repo, branch, ticket, tier and reason, whether
// capture is paused and when presence was last seen.
func runStatus(ctx context.Context, deps dailyDeps) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	now := deps.Now()
	events, err := currentDayEvents(ctx, st, now, cfg.Location)
	if err != nil {
		return err
	}
	recent, err := st.Events(ctx, now.Add(-statusLookback), now.Add(time.Minute))
	if err != nil {
		return err
	}
	paused, err := isPaused(deps)
	if err != nil {
		return err
	}

	rules, err := resolverRules(ctx, st, cfg)
	if err != nil {
		return err
	}
	current, ok := sessionizer.LatestContext(events, rules)
	if !ok {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgNothingRecorded))
		return err
	}

	ticket := current.Attribution.Ticket
	if ticket == "" {
		ticket = msgUnassigned
	}
	repo := current.Signal.Repo
	if repo == "" {
		repo = msgNoRepo
	}
	branch := current.Signal.Branch
	if branch == "" {
		branch = msgNoRepo
	}
	r := deps.Render
	captureStatus, captureText := render.StatusOK, msgCaptureActive
	if paused {
		captureStatus, captureText = render.StatusWarn, msgCapturePaused
	}
	lines := []string{r.Heading(msgStatusHeading), ""}
	lines = append(lines, r.Labels([]render.LabelRow{
		{Label: msgLabelRepo, Value: SanitizeForTerminal(repo)},
		{Label: msgLabelBranch, Value: SanitizeForTerminal(branch)},
		{Label: msgLabelTicket, Value: SanitizeForTerminal(ticket)},
		{Label: msgLabelTier, Value: colorTier(r, current.Attribution.Tier)},
		{Label: msgLabelReason, Value: SanitizeForTerminal(current.Attribution.Reason)},
	}, 2)...)
	lines = append(lines, "",
		r.Gutter(captureStatus, captureText),
		r.Gutter(render.StatusInfo, presenceLine(recent, now, cfg.Location)))
	_, err = fmt.Fprintln(deps.Out, strings.Join(lines, "\n"))
	return err
}

// currentDayEvents returns the events status takes the current context from:
// those of the current local day up to now, led by the context carried into
// the day. That is the input today and review derive the day from, so the
// three agree. A longer window would differ: an app-bound window-title hint
// is not carried into a day, but inside one window it applies whenever its
// app is frontmost again.
func currentDayEvents(ctx context.Context, st dailyStore, now time.Time, loc *time.Location) ([]event.Event, error) {
	start, err := dayStart("", now, loc)
	if err != nil {
		return nil, err
	}
	return review.EventsWithCarriedContext(ctx, st, start, now.Add(time.Minute))
}

func presenceLine(events []event.Event, now time.Time, loc *time.Location) string {
	var last time.Time
	for _, e := range events {
		if e.Kind == event.KindPresence && e.At.After(last) {
			last = e.At
		}
	}
	if last.IsZero() {
		return msgNoPresence
	}
	return msgPresenceSeen(last, now, loc)
}
