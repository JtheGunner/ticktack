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
	// most collections; the server may return fewer, which the loop
	// tolerates because it advances by the entries actually received.
	collectionPageSize = 42
	// maxCollectionPages bounds the loop against a server that keeps
	// answering with new entries forever.
	maxCollectionPages = 1000
	// maxEntryKeyBytes bounds an entry id, so that the seen set stays small
	// (at most maxCollectionPages * collectionPageSize short keys).
	maxEntryKeyBytes = 256
)

// getAllPages reads a collection page by page with $top and $skip and calls
// visit for every entry until visit reports stop or the collection ends. The
// collection ends only at an empty page. A non-empty page that holds only
// entries already seen (a server that ignores $skip) is an error, because the
// end was not reached. A short page does not end it: the server may cap pages
// below $top, so $skip advances by the entries received. key identifies an
// entry for the seen check; an empty key counts as always new.
// The query is not modified.
func getAllPages[T any](ctx context.Context, c *Client, path string, query url.Values, key func(T) string, visit func(T) (stop bool)) error {
	seen := map[string]struct{}{}
	skip := 0
	for page := 0; page < maxCollectionPages; page++ {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("$top", strconv.Itoa(collectionPageSize))
		q.Set("$skip", strconv.Itoa(skip))
		var items []T
		if err := c.do(ctx, http.MethodGet, path, q, nil, &items); err != nil {
			return err
		}
		if len(items) > collectionPageSize {
			return fmt.Errorf("youtrack: %s: page of %d entries exceeds the requested %d", path, len(items), collectionPageSize)
		}
		fresh := false
		for _, it := range items {
			id := key(it)
			if len(id) > maxEntryKeyBytes {
				return fmt.Errorf("youtrack: %s: entry id longer than %d bytes", path, maxEntryKeyBytes)
			}
			if id != "" {
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
			}
			fresh = true
			if visit(it) {
				return nil
			}
		}
		if len(items) == 0 {
			return nil
		}
		if !fresh {
			// Inconsistent paging (e.g. $skip ignored): the end of the
			// collection was not seen, so fail rather than report absence.
			return fmt.Errorf("youtrack: %s: page at skip %d repeats entries already seen", path, skip)
		}
		skip += len(items)
	}
	return fmt.Errorf("youtrack: %s: more than %d pages, giving up", path, maxCollectionPages)
}
