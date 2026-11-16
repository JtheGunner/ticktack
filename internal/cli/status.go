package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// statusLookback bounds how far back status looks for the latest signals.
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
	events, err := st.Events(ctx, now.Add(-statusLookback), now.Add(time.Minute))
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
		_, err = fmt.Fprintln(deps.Out, msgNothingRecorded)
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
	out := deps.Out
	_, _ = fmt.Fprintf(out, "Repo:      %s\n", SanitizeForTerminal(repo))
	_, _ = fmt.Fprintf(out, "Branch:    %s\n", SanitizeForTerminal(branch))
	_, _ = fmt.Fprintf(out, "Ticket:    %s\n", SanitizeForTerminal(ticket))
	_, _ = fmt.Fprintf(out, "Tier:      %s\n", current.Attribution.Tier)
	_, _ = fmt.Fprintf(out, "Reason:    %s\n", SanitizeForTerminal(current.Attribution.Reason))
	if paused {
		_, _ = fmt.Fprintln(out, msgCapturePaused)
	} else {
		_, _ = fmt.Fprintln(out, msgCaptureActive)
	}
	_, err = fmt.Fprintln(out, presenceLine(events, now, cfg.Location))
	return err
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
