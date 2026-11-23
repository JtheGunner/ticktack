// Package sink defines the destination for tracked time. Implementations
// (for example the YouTrack client) store work items somewhere durable.
package sink

import (
	"context"

	"cloud.google.com/go/civil"
)

// WorkItem is one block of tracked time booked against an issue.
type WorkItem struct {
	// Issue is the readable issue key, for example "ABC-123".
	Issue string
	// Date is the calendar day the work is booked on.
	Date civil.Date
	// Minutes is the booked duration in minutes.
	Minutes int
	// Type is the work item type name, for example "Development".
	Type string
	// Text is the free-form description; it may carry a dedup marker.
	Text string
}

// ProjectRef is a project of the booking target, identified by its key and
// matched to repository folders by its name.
type ProjectRef struct {
	Key  string
	Name string
}

// TaskProvisioner lists projects and finds or creates the standard task of a
// project in the booking target. It is separate from TimeSink so a sink that
// only books time need not implement it.
type TaskProvisioner interface {
	// Projects returns every non-archived project the user can see.
	Projects(ctx context.Context) ([]ProjectRef, error)
	// FindIssueByMarker returns the issue of project whose description
	// contains marker verbatim.
	FindIssueByMarker(ctx context.Context, project, marker string) (key string, found bool, err error)
	// CreateIssue creates an issue in project, assigned to the user the sink
	// acts for, and returns its readable key.
	CreateIssue(ctx context.Context, project, summary, description string) (key string, err error)
}

// TimeSink stores, changes and looks up work items.
//
// The id returned by Create and FindByMarker is opaque to callers and must be
// passed back unchanged to Update, Delete and WorkItemExists.
type TimeSink interface {
	Create(ctx context.Context, w WorkItem) (id string, err error)
	Update(ctx context.Context, id string, w WorkItem) error
	Delete(ctx context.Context, id string) error
	// WorkItemExists reports whether the work item id, as returned by Create
	// or FindByMarker, still exists on issue. It looks the item up by its id
	// and is therefore definitive, unlike a FindByMarker that finds nothing.
	// A missing item (or a missing issue) is exists == false with a nil error;
	// any other failure is an error and says nothing about the item.
	WorkItemExists(ctx context.Context, issue, id string) (exists bool, err error)
	// FindByMarker reports the work item on issue, authored by the user the
	// sink acts for, whose text contains marker verbatim. Items of other users
	// are never returned. Sinks do not deduplicate, so this is how callers stay
	// idempotent.
	FindByMarker(ctx context.Context, issue, marker string) (id string, found bool, err error)
}
