package youtrack

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const (
	// collectionPageSize is the page size requested for collections read
	// with getAllPages. YouTrack documents at most 42 entries per page for
	// most collections; a server that returns fewer than requested is taken
	// to have no further page, so the value must not exceed the real cap.
	collectionPageSize = 42
	// maxCollectionPages bounds the loop against a server that keeps
	// answering with new entries forever.
	maxCollectionPages = 1000
)

// getAllPages reads a collection page by page with $top and $skip and calls
// visit for every entry until visit reports stop or the collection ends. The
// collection ends at an empty page, a page shorter than requested, or a page
// that holds only entries already seen (a server that ignores $skip). key
// identifies an entry for that last check. The query is not modified.
func getAllPages[T any](ctx context.Context, c *Client, path string, query url.Values, key func(T) string, visit func(T) (stop bool)) error {
	seen := map[string]struct{}{}
	for page := 0; page < maxCollectionPages; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("$top", strconv.Itoa(collectionPageSize))
		q.Set("$skip", strconv.Itoa(page*collectionPageSize))
		var items []T
		if err := c.do(ctx, http.MethodGet, path, q, nil, &items); err != nil {
			return err
		}
		fresh := false
		for _, it := range items {
			id := key(it)
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			fresh = true
			if visit(it) {
				return nil
			}
		}
		if len(items) < collectionPageSize || !fresh {
			return nil
		}
	}
	return fmt.Errorf("youtrack: %s: more than %d pages, giving up", path, maxCollectionPages)
}
