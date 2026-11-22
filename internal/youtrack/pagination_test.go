package youtrack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// pagedServer serves total entries of the collection path with $top/$skip,
// capping the page at limit. entry renders entry i as JSON.
func pagedServer(t *testing.T, path string, total, limit int, entry func(i int) string, calls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/me":
			_, _ = io.WriteString(w, `{"login":"me"}`)
		case path:
			calls.Add(1)
			q := r.URL.Query()
			top, _ := strconv.Atoi(q.Get("$top"))
			skip, _ := strconv.Atoi(q.Get("$skip"))
			if top <= 0 || top > limit {
				top = limit
			}
			var parts []string
			for i := skip; i < total && i < skip+top; i++ {
				parts = append(parts, entry(i))
			}
			_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	})
}

func TestFindByMarkerPaginates(t *testing.T) {
	const path = "/api/issues/ABC-1/timeTracking/workItems"
	entry := func(i int) string {
		text := "plain"
		if i == 50 {
			text = "x [ticktack:k]"
		}
		return fmt.Sprintf(`{"id":"1-%d","text":%q,"author":{"login":"me"}}`, i, text)
	}
	tests := []struct {
		name      string
		total     int
		wantFound bool
		wantCalls int32
	}{
		{"marker on page 2", 100, true, 2},
		{"marker absent, last page short", 50, false, 2},
		{"exact multiple of page size ends on empty page", 42, false, 2},
		{"empty collection", 0, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, pagedServer(t, path, tc.total, 42, entry, &calls))
			id, found, err := c.FindByMarker(context.Background(), "ABC-1", "[ticktack:k]")
			if err != nil || found != tc.wantFound {
				t.Fatalf("id=%q found=%v err=%v", id, found, err)
			}
			if found && id != "ABC-1/1-50" {
				t.Errorf("id = %q", id)
			}
			if calls.Load() != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestFindByMarkerRequestsFieldsAndPageParams(t *testing.T) {
	var query string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/users/me" {
			_, _ = io.WriteString(w, `{"login":"me"}`)
			return
		}
		query = r.URL.Query().Encode()
		_, _ = io.WriteString(w, `[]`)
	}))
	if _, _, err := c.FindByMarker(context.Background(), "ABC-1", "[m]"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fields=id%2Ctext%2Cauthor%28login%29", "%24top=42", "%24skip=0"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
}

func TestFindByMarkerStopsWhenServerIgnoresPaging(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/users/me" {
			_, _ = io.WriteString(w, `{"login":"me"}`)
			return
		}
		calls.Add(1)
		var parts []string
		for i := 0; i < 42; i++ { // always the same full page
			parts = append(parts, fmt.Sprintf(`{"id":"1-%d","text":"t","author":{"login":"me"}}`, i))
		}
		_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
	}))
	_, found, err := c.FindByMarker(context.Background(), "ABC-1", "[m]")
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestEnsureTagPaginates(t *testing.T) {
	var calls atomic.Int32
	entry := func(i int) string {
		name := "other" + strconv.Itoa(i)
		if i == 50 {
			name = "Manual"
		}
		return fmt.Sprintf(`{"id":"5-%d","name":%q}`, i, name)
	}
	c := newTestClient(t, pagedServer(t, "/api/tags", 60, 42, entry, &calls))
	id, err := c.ensureTag(context.Background(), "manual")
	if err != nil || id != "5-50" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestResponseBodyIsBounded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"login":"`)
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 10; i++ {
			_, _ = io.WriteString(w, chunk)
		}
		_, _ = io.WriteString(w, `"}`)
	}))
	if _, err := c.Me(context.Background()); err == nil {
		t.Fatal("oversized response accepted")
	}
}
