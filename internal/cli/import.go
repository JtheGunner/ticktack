package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector/activitywatch"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
)

const (
	// importDefaultLookback is how far back the first import reaches.
	importDefaultLookback = 24 * time.Hour
	// importOverlap re-reads this much before the newest imported event, so
	// intervals ActivityWatch extended by heartbeat are completed. Re-reading
	// is harmless: every imported event has a deterministic key.
	importOverlap = time.Hour
	// importCursorWindow bounds the store lookup for the newest imported event.
	importCursorWindow = 30 * 24 * time.Hour
)

// newSourceFunc builds the ActivityWatch event source for a configuration.
type newSourceFunc func(cfg config.ActivityWatch) (activitywatch.EventSource, error)

func newActivityWatchSource(cfg config.ActivityWatch) (activitywatch.EventSource, error) {
	return activitywatch.New(cfg.URL)
}

func newImportCommand() *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import events from another tool",
	}
	var since string
	aw := &cobra.Command{
		Use:   "activitywatch",
		Short: "Import window and AFK events from a local ActivityWatch server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runImportActivityWatch(cmd.Context(), defaultDailyDeps(cmd), newActivityWatchSource, since)
		},
	}
	aw.Flags().StringVar(&since, "since", "", "import from this day as YYYY-MM-DD (default: shortly before the last import, else the last 24 hours)")
	importCmd.AddCommand(aw)
	return importCmd
}

// runImportActivityWatch runs the explicit, one-shot import. It needs
// enabled = true in the configuration, honours the pause flag and is
// idempotent. since is empty for the automatic cursor.
func runImportActivityWatch(ctx context.Context, deps dailyDeps, newSource newSourceFunc, since string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	if !cfg.ActivityWatch.Enabled {
		path, pathErr := config.DefaultPath()
		if pathErr != nil {
			path = msgImportConfigPath
		}
		return fmt.Errorf(msgImportDisabled, path)
	}
	paused, err := isPaused(deps)
	if err != nil {
		return err
	}
	if paused {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusWarn, msgImportPaused))
		return err
	}

	now := deps.Now()
	var explicit time.Time
	if since != "" {
		explicit, err = time.ParseInLocation(dayLayout, since, cfg.Location)
		if err != nil {
			return fmt.Errorf(msgImportBadSince, since)
		}
	}
	source, err := newSource(cfg.ActivityWatch)
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	from := explicit
	if from.IsZero() {
		from, err = importCursor(ctx, st, now)
		if err != nil {
			return err
		}
	}

	imp := &activitywatch.Importer{Source: source, Now: func() time.Time { return now }, Hostname: cfg.ActivityWatch.Hostname}
	events, collectErr := imp.Collect(ctx, from)
	var presence, gaps, hints int
	for _, e := range events {
		if err := st.Append(ctx, e); err != nil {
			return err
		}
		switch e.Kind {
		case event.KindPresence:
			presence++
		case event.KindGap:
			gaps++
		case event.KindContext:
			hints++
		}
	}
	if len(events) > 0 || collectErr == nil {
		since := from.In(cfg.Location).Format(time.RFC3339)
		if _, err := fmt.Fprintln(deps.Out, formatImportResult(deps.Render, len(events), since, presence, gaps, hints)); err != nil {
			return err
		}
	}
	if collectErr != nil {
		// The error carries server-supplied text such as bucket ids.
		return fmt.Errorf(msgImportPartial, SanitizeForTerminal(collectErr.Error()))
	}
	return nil
}

// formatImportResult lays out the import result as a gutter line followed by
// the per-kind counts.
func formatImportResult(r render.Renderer, total int, since string, presence, gaps, hints int) string {
	lines := []string{r.Gutter(render.StatusOK, fmt.Sprintf(msgImportDone, total, since))}
	lines = append(lines, r.Labels([]render.LabelRow{
		{Label: msgImportLabelPresence, Value: strconv.Itoa(presence)},
		{Label: msgImportLabelGaps, Value: strconv.Itoa(gaps)},
		{Label: msgImportLabelHints, Value: strconv.Itoa(hints)},
	}, 5)...)
	return strings.Join(lines, "\n")
}

// importCursor returns where the next import starts: shortly before the
// newest event already imported, else a day back.
func importCursor(ctx context.Context, st dailyStore, now time.Time) (time.Time, error) {
	events, err := st.Events(ctx, now.Add(-importCursorWindow), now.Add(time.Minute))
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, e := range events {
		if e.Source == activitywatch.Source && e.At.After(newest) {
			newest = e.At
		}
	}
	if newest.IsZero() {
		return now.Add(-importDefaultLookback), nil
	}
	return newest.Add(-importOverlap), nil
}
