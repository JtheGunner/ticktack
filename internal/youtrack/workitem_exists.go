package youtrack

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// WorkItemExists reports whether the work item id still exists on issue, with
// a single GET of that one item. Unlike a listing it does not depend on how
// many work items the issue has. Not found means the item (or its issue) is
// gone and is reported as false without an error; every other failure is
// returned, because it says nothing about the item. The request is idempotent
// and goes through the normal retry.
func (c *Client) WorkItemExists(ctx context.Context, issue, id string) (bool, error) {
	itemIssue, itemID, err := splitID(id)
	if err != nil {
		return false, err
	}
	if itemIssue != issue {
		return false, fmt.Errorf("%q does not belong to issue %s: %w", id, issue, ErrInvalidID)
	}
	var out struct {
		ID string `json:"id"`
	}
	err = c.do(ctx, http.MethodGet, itemPath(issue, itemID), url.Values{"fields": {"id"}}, nil, &out)
	if errors.Is(err, ErrIssueNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if out.ID != itemID {
		return false, fmt.Errorf("youtrack: work item lookup of %q answered with id %q", id, out.ID)
	}
	return true, nil
}
