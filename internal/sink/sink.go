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

// TimeSink stores, changes and looks up work items.
//
// The id returned by Create and FindByMarker is opaque to callers and must be
// passed back unchanged to Update and Delete.
type TimeSink interface {
	Create(ctx context.Context, w WorkItem) (id string, err error)
	Update(ctx context.Context, id string, w WorkItem) error
	Delete(ctx context.Context, id string) error
	// FindByMarker reports the work item on issue whose text contains marker
	// verbatim. Sinks do not deduplicate, so this is how callers stay idempotent.
	FindByMarker(ctx context.Context, issue, marker string) (id string, found bool, err error)
}
