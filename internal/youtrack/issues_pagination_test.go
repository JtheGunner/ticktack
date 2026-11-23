package youtrack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func issueEntry(i int) string {
	return fmt.Sprintf(`{"idReadable":"ABC-%d","summary":"s%d","updated":1}`, i+1, i)
}

func TestMyOpenIssuesPaginates(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		wantCalls int32
	}{
		{"empty result", 0, 1},
		{"single short page", 5, 2},
		{"full first page and a second one", 60, 3},
		{"exactly full last page", 84, 3},
		{"exactly one full page", 42, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, pagedServer(t, "/api/issues", tc.total, 42, issueEntry, &calls))
			issues, err := c.MyOpenIssues(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) != tc.total {
				t.Fatalf("issues = %d, want %d", len(issues), tc.total)
			}
			for i, is := range issues {
				if is.ID != fmt.Sprintf("ABC-%d", i+1) {
					t.Fatalf("issues[%d] = %q", i, is.ID)
				}
			}
			if calls.Load() != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestMyOpenIssuesKeepsQueryOnEveryPage(t *testing.T) {
	var queries []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query")+"|"+r.URL.Query().Get("$skip"))
		if r.URL.Query().Get("$skip") == "0" {
			_, _ = io.WriteString(w, `[{"idReadable":"ABC-1"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	if _, err := c.MyOpenIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "for: me State: {In Progress}"
	if len(queries) != 2 || queries[0] != want+"|0" || queries[1] != want+"|1" {
		t.Errorf("queries = %q", queries)
	}
}

func TestMyOpenIssuesFailsAfterPageCap(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = io.WriteString(w, fmt.Sprintf(`[{"idReadable":"ABC-%d"}]`, n))
	}))
	issues, err := c.MyOpenIssues(context.Background())
	if err == nil || issues != nil {
		t.Fatalf("issues=%v err=%v, want an error", issues, err)
	}
	if calls.Load() != maxCollectionPages {
		t.Errorf("calls = %d, want %d", calls.Load(), maxCollectionPages)
	}
}

func TestMyOpenIssuesFailsWhenServerIgnoresPaging(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var parts []string
		for i := 0; i < 42; i++ {
			parts = append(parts, issueEntry(i))
		}
		_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
	}))
	if _, err := c.MyOpenIssues(context.Background()); err == nil {
		t.Fatal("want an error for a repeated page")
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}
