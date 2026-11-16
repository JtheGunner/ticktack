package cli

import (
	"context"
	"time"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// provisionForAuto makes sure the standard tasks of the selected items exist,
// without ever asking. An existing task is simply used; a missing one is
// created only for a project whose creation the user confirmed in `ticktack
// sync` before. Everything else is moved to the skipped items with a reason,
// so a placeholder is never booked.
func provisionForAuto(ctx context.Context, st *store.Store, cfg config.Config, target sink.TimeSink, sel auto.Selection, dryRun bool, now func() time.Time) (auto.Selection, error) {
	if cfg.DefaultIssue == nil {
		return sel, nil
	}
	prov, ok := target.(sink.TaskProvisioner)
	if !ok {
		var placeholders []string
		for _, e := range sel.Book {
			if _, isPlaceholder := resolver.ParseDefaultRef(e.Item.Ticket); isPlaceholder {
				placeholders = append(placeholders, e.Item.Ticket)
			}
		}
		return sel.Resolve(nil, placeholders), nil
	}
	consented, err := st.ConsentedProjects(ctx)
	if err != nil {
		return sel, err
	}
	known, err := st.DefaultTasks(ctx)
	if err != nil {
		return sel, err
	}
	// A task the tool already knew that cannot be found now may have been
	// moved or edited, not deleted: only a person decides to replace it.
	approveConsented := func(projects []string) ([]string, error) {
		var approved []string
		for _, p := range projects {
			if _, hadTask := known[p]; consented[p] && !hadTask {
				approved = append(approved, p)
			}
		}
		return approved, nil
	}
	_, report, err := ledger.Provision(ctx, sel.Items(), prov, st, ledger.ProvisionOptions{
		Summary: cfg.DefaultIssue.Summary,
		Confirm: approveConsented,
		DryRun:  dryRun,
		Now:     now,
	})
	if err != nil {
		return sel, err
	}
	return sel.Resolve(report.Rewritten, report.DroppedTickets), nil
}
