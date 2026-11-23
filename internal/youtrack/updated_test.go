package youtrack

import (
	"context"
	"net/http"
	"testing"
)

func TestMyOpenIssuesDecodesUpdated(t *testing.T) {
	var fields string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields = r.URL.Query().Get("fields")
		writeFirstPageOnly(w, r, `[{"idReadable":"ABC-1","summary":"s","updated":1700000000000}]`)
	}))
	issues, err := c.MyOpenIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Updated != 1700000000000 {
		t.Errorf("issues = %+v", issues)
	}
	if fields != "idReadable,summary,updated" {
		t.Errorf("fields = %q", fields)
	}
}
