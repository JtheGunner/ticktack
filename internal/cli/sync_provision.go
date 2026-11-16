package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// provisionStandardTasks makes sure the standard tasks the confirmed items
// point at exist and swaps their placeholders for the real issue keys. It asks
// once, on in, before creating anything; an empty or missing answer declines.
// Items whose task could not be provided are dropped from the result and the
// reason is printed, so they are never booked on a guess.
func provisionStandardTasks(ctx context.Context, out io.Writer, in io.Reader, r render.Renderer, cfg config.Config, st *store.Store, target sink.TimeSink, items []sessionizer.DayItem, dryRun bool) ([]sessionizer.DayItem, error) {
	if cfg.DefaultIssue == nil {
		return items, nil
	}
	prov, ok := target.(sink.TaskProvisioner)
	if !ok {
		return dropPlaceholders(out, r, items), nil
	}
	confirm := func(projects []string) ([]string, error) {
		_, _ = fmt.Fprintf(out, msgSyncCreatePrompt, sanitizedList(projects))
		if in == nil {
			return nil, nil
		}
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && line == "" {
			return nil, nil // no answer (end of input) declines
		}
		if answer := strings.ToLower(strings.TrimSpace(line)); answer == "y" || answer == "yes" {
			return projects, nil
		}
		return nil, nil
	}
	provisioned, report, err := ledger.Provision(ctx, items, prov, st, ledger.ProvisionOptions{
		Summary: cfg.DefaultIssue.Summary,
		Confirm: confirm,
		DryRun:  dryRun,
	})
	if err != nil {
		return nil, err
	}
	printProvisionReport(out, r, report)
	return provisioned, nil
}

// dropPlaceholders removes items that still point at a standard task that does
// not exist yet and says why, for sinks that cannot create issues.
func dropPlaceholders(out io.Writer, r render.Renderer, items []sessionizer.DayItem) []sessionizer.DayItem {
	kept := make([]sessionizer.DayItem, 0, len(items))
	skipped := 0
	for _, it := range items {
		if _, isPlaceholder := resolver.ParseDefaultRef(it.Ticket); isPlaceholder {
			skipped++
			continue
		}
		kept = append(kept, it)
	}
	if skipped > 0 {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusWarn, fmt.Sprintf(msgSyncSinkCannotCreate, skipped)))
	}
	return kept
}

func printProvisionReport(out io.Writer, r render.Renderer, report ledger.ProvisionReport) {
	for _, t := range report.Created {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusOK, fmt.Sprintf(msgSyncTaskCreated, SanitizeForTerminal(t.Issue), SanitizeForTerminal(t.Project))))
	}
	for _, t := range report.Found {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusInfo, fmt.Sprintf(msgSyncTaskFound, SanitizeForTerminal(t.Issue), SanitizeForTerminal(t.Project))))
	}
	if len(report.WouldCreate) > 0 {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusInfo, fmt.Sprintf(msgSyncTaskWouldCreate, sanitizedList(report.WouldCreate))))
	}
	if len(report.Declined) > 0 {
		_, _ = fmt.Fprintln(out, r.Gutter(render.StatusWarn, fmt.Sprintf(msgSyncTaskDeclined, sanitizedList(report.Declined))))
	}
}

// sanitizedList joins project keys for display. The keys come from YouTrack
// and the configuration, so every one is sanitised before it reaches a terminal.
func sanitizedList(items []string) string {
	clean := make([]string, len(items))
	for i, s := range items {
		clean[i] = SanitizeForTerminal(s)
	}
	return strings.Join(clean, ", ")
}
