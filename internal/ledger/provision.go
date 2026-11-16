package ledger

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
)

// DefaultIssueMarker is written into the description of every standard task
// ticktack creates; it is how the task is found again.
const DefaultIssueMarker = "[ticktack:default-issue]"

// ProvisionStore is the persistence Provision needs. *store.Store implements it.
type ProvisionStore interface {
	DefaultTasks(ctx context.Context) (map[string]string, error)
	SetDefaultTask(ctx context.Context, project, issue string) error
	SetConsent(ctx context.Context, project string, at time.Time) error
	RenameConfirmationIssue(ctx context.Context, from, to string) error
}

// ProvisionOptions configures Provision.
type ProvisionOptions struct {
	// Summary is the title of every standard task that gets created.
	Summary string
	// Confirm is asked once, with the projects whose standard task would be
	// created, and returns the ones that may be created. Nil declines all.
	Confirm func(projects []string) (approved []string, err error)
	// DryRun looks tasks up but creates nothing and writes nothing.
	DryRun bool
	// Now supplies the consent time.
	Now func() time.Time
}

// ProvisionedTask names the standard task of a project.
type ProvisionedTask struct {
	Project string
	Issue   string
}

// ProvisionReport says what Provision did.
type ProvisionReport struct {
	// Created tasks were created in YouTrack; Found tasks existed already but
	// were not yet known (or known under another key).
	Created, Found []ProvisionedTask
	// Declined projects were not confirmed; their items were left out.
	Declined []string
	// WouldCreate lists the projects a dry run found without a task.
	WouldCreate []string
	// Rewritten maps every ticket that was replaced to its real issue key.
	Rewritten map[string]string
	// DroppedTickets lists the tickets whose items were left out.
	DroppedTickets []string
}

// Provision makes sure every standard task the items point at exists, and
// returns the items with placeholders (and the keys of vanished tasks)
// replaced by the real issue keys. Items of projects whose task was neither
// found nor created are left out, never booked on a guess. Confirmations
// given on the old key move to the new one so nothing has to be reviewed
// twice.
func Provision(ctx context.Context, items []sessionizer.DayItem, prov sink.TaskProvisioner, st ProvisionStore, o ProvisionOptions) ([]sessionizer.DayItem, ProvisionReport, error) {
	var report ProvisionReport
	tasks, err := st.DefaultTasks(ctx)
	if err != nil {
		return nil, report, err
	}
	keyOwner := make(map[string]string, len(tasks))
	for project, key := range tasks {
		keyOwner[key] = project
	}

	// projectOf maps every ticket that stands for a standard task to its project.
	projectOf := map[string]string{}
	for _, it := range items {
		if project, ok := resolver.ParseDefaultRef(it.Ticket); ok {
			projectOf[it.Ticket] = project
		} else if project, ok := keyOwner[it.Ticket]; ok {
			projectOf[it.Ticket] = project
		}
	}
	if len(projectOf) == 0 {
		return append([]sessionizer.DayItem(nil), items...), report, nil
	}
	ticketsOf := map[string][]string{}
	for ticket, project := range projectOf {
		ticketsOf[project] = append(ticketsOf[project], ticket)
	}
	projects := make([]string, 0, len(ticketsOf))
	for project := range ticketsOf {
		projects = append(projects, project)
	}
	sort.Strings(projects)

	rewrite := map[string]string{}
	adopt := func(project, key string, created bool) error {
		changed := tasks[project] != key
		for _, old := range ticketsOf[project] {
			if old == key {
				continue
			}
			rewrite[old] = key
			changed = true
			if !o.DryRun {
				if err := st.RenameConfirmationIssue(ctx, old, key); err != nil {
					return err
				}
			}
		}
		if !o.DryRun && tasks[project] != key {
			if err := st.SetDefaultTask(ctx, project, key); err != nil {
				return err
			}
		}
		switch {
		case created:
			report.Created = append(report.Created, ProvisionedTask{Project: project, Issue: key})
		case changed:
			report.Found = append(report.Found, ProvisionedTask{Project: project, Issue: key})
		}
		return nil
	}

	var missing []string
	for _, project := range projects {
		key, found, err := prov.FindIssueByMarker(ctx, project, DefaultIssueMarker)
		if err != nil {
			return nil, report, fmt.Errorf("look up the standard task of %s: %w", project, err)
		}
		if found {
			if err := adopt(project, key, false); err != nil {
				return nil, report, err
			}
			continue
		}
		missing = append(missing, project)
	}

	dropped := map[string]bool{}
	if len(missing) > 0 {
		if o.DryRun {
			report.WouldCreate = missing
			markDropped(dropped, missing)
		} else {
			approved, err := approve(o.Confirm, missing)
			if err != nil {
				return nil, report, err
			}
			now := time.Now
			if o.Now != nil {
				now = o.Now
			}
			for _, project := range missing {
				if !approved[project] {
					report.Declined = append(report.Declined, project)
					dropped[project] = true
					continue
				}
				key, err := prov.CreateIssue(ctx, project, o.Summary, defaultIssueDescription())
				if err != nil {
					return nil, report, fmt.Errorf("create the standard task of %s: %w", project, err)
				}
				if err := adopt(project, key, true); err != nil {
					return nil, report, err
				}
				if err := st.SetConsent(ctx, project, now()); err != nil {
					return nil, report, err
				}
			}
		}
	}

	report.Rewritten = rewrite
	out := make([]sessionizer.DayItem, 0, len(items))
	droppedSeen := map[string]bool{}
	for _, it := range items {
		if project, isTask := projectOf[it.Ticket]; isTask && dropped[project] {
			if !droppedSeen[it.Ticket] {
				droppedSeen[it.Ticket] = true
				report.DroppedTickets = append(report.DroppedTickets, it.Ticket)
			}
			continue
		}
		if key, ok := rewrite[it.Ticket]; ok {
			it = sessionizer.RetargetItem(it, key)
		}
		out = append(out, it)
	}
	return sessionizer.MergeItems(out), report, nil
}

func markDropped(dropped map[string]bool, projects []string) {
	for _, p := range projects {
		dropped[p] = true
	}
}

// approve asks confirm once and returns the projects it approved. A missing
// confirm approves nothing; an error from it stops the run.
func approve(confirm func([]string) ([]string, error), projects []string) (map[string]bool, error) {
	approved := map[string]bool{}
	if confirm == nil {
		return approved, nil
	}
	list, err := confirm(projects)
	if err != nil {
		return nil, fmt.Errorf("confirm standard task creation: %w", err)
	}
	for _, p := range list {
		approved[p] = true
	}
	return approved, nil
}

func defaultIssueDescription() string {
	return "Standard task for time without a concrete ticket, created by ticktack.\n\n" + DefaultIssueMarker
}
