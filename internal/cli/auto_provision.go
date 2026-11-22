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
//
// Items of a project whose lookup or creation failed leave the selection and
// are returned in the report's Failed for the sync report: whether their task
// exists is unknown, so they are not reported as waiting for its creation.
// The report's FailedProjects names their projects, whose bookings must not
// be reconciled in this run.
func provisionForAuto(ctx context.Context, st *store.Store, cfg config.Config, target sink.TimeSink, sel auto.Selection, dryRun bool, now func() time.Time) (auto.Selection, ledger.ProvisionReport, error) {
	if cfg.DefaultIssue == nil {
		return sel, ledger.ProvisionReport{}, nil
	}
	prov, ok := target.(sink.TaskProvisioner)
	if !ok {
		var placeholders []string
		for _, e := range sel.Book {
			if _, isPlaceholder := resolver.ParseDefaultRef(e.Item.Ticket); isPlaceholder {
				placeholders = append(placeholders, e.Item.Ticket)
			}
		}
		return sel.Resolve(nil, placeholders), ledger.ProvisionReport{}, nil
	}
	consented, err := st.ConsentedProjects(ctx)
	if err != nil {
		return sel, ledger.ProvisionReport{}, err
	}
	known, err := st.DefaultTasks(ctx)
	if err != nil {
		return sel, ledger.ProvisionReport{}, err
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
		return sel, ledger.ProvisionReport{}, err
	}
	return sel.Without(report.FailedTickets).Resolve(report.Rewritten, report.DroppedTickets), report, nil
}
