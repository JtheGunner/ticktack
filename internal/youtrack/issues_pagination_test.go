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
	"unicode/utf8"
	"unsafe"
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

func TestMyOpenIssuesEntryBudget(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		wantErr bool
	}{
		{"exactly at the limit", maxCollectionEntries, false},
		{"one over the limit", maxCollectionEntries + 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, pagedServer(t, "/api/issues", tc.total, 42, issueEntry, &calls))
			issues, err := c.MyOpenIssues(context.Background())
			if tc.wantErr {
				if err == nil || issues != nil {
					t.Fatalf("issues=%d err=%v, want an error and no partial result", len(issues), err)
				}
				return
			}
			if err != nil || len(issues) != tc.total {
				t.Fatalf("issues=%d err=%v", len(issues), err)
			}
		})
	}
}

func TestMyOpenIssuesTruncatesHugeSummariesAtRuneBoundary(t *testing.T) {
	huge := strings.Repeat("ä", 1<<20) // 2 MiB of two-byte runes
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skip := r.URL.Query().Get("$skip")
		n, _ := strconv.Atoi(skip)
		if n >= 20 {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, fmt.Sprintf(`[{"idReadable":"ABC-%d","summary":%q}]`, n+1, huge))
	}))
	issues, err := c.MyOpenIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 20 {
		t.Fatalf("issues = %d, want 20", len(issues))
	}
	total := 0
	for _, is := range issues {
		if len(is.Summary) > maxIssueSummaryBytes || !utf8.ValidString(is.Summary) || is.Summary == "" {
			t.Fatalf("summary of %s: len=%d valid=%v", is.ID, len(is.Summary), utf8.ValidString(is.Summary))
		}
		total += len(is.Summary)
	}
	if total > 20*maxIssueSummaryBytes {
		t.Errorf("retained %d bytes", total)
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		in    string
		limit int
		want  string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"aää", 2, "a"},
		{"aää", 3, "aä"},
		{"€", 2, ""},
		{"", 5, ""},
	}
	for _, tc := range tests {
		if got := truncateUTF8(tc.in, tc.limit); got != tc.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

// A plain slice of a huge string would keep the whole original allocation alive,
// so the retained summaries would not be bounded by the cut. The cut part must be
// its own copy.
func TestTruncateUTF8DoesNotKeepTheOriginalAllocation(t *testing.T) {
	huge := strings.Repeat("a", 1<<20)
	got := truncateUTF8(huge, maxIssueSummaryBytes)
	if len(got) != maxIssueSummaryBytes {
		t.Fatalf("len = %d, want %d", len(got), maxIssueSummaryBytes)
	}
	if unsafe.StringData(got) == unsafe.StringData(huge) {
		t.Fatal("truncated string shares memory with the original")
	}
}

func TestEntryBudgetStopsRequestingPages(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/users/me" {
			_, _ = io.WriteString(w, `{"login":"me"}`)
			return
		}
		n := int(calls.Add(1))
		var parts []string
		for i := 0; i < 42; i++ { // endless supply of new entries
			parts = append(parts, fmt.Sprintf(`{"id":"1-%d","text":"t","author":{"login":"other"}}`, n*100+i))
		}
		_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
	}))
	_, found, err := c.FindByMarker(context.Background(), "ABC-1", "[m]")
	if err == nil || found {
		t.Fatalf("found=%v err=%v, want an error", found, err)
	}
	if limit := int32(maxCollectionEntries/42 + 1); calls.Load() > limit {
		t.Errorf("calls = %d, want at most %d", calls.Load(), limit)
	}
	if strings.Contains(err.Error(), "other") {
		t.Errorf("error leaks response text: %v", err)
	}
}
