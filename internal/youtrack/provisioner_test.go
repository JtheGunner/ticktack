package youtrack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/sink"
)

var _ sink.TaskProvisioner = (*Client)(nil)

func TestProjectsPaginatesAndSkipsArchived(t *testing.T) {
	var skips []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/admin/projects" {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		if r.URL.Query().Get("fields") == "" {
			t.Errorf("fields must be requested explicitly")
		}
		skip := r.URL.Query().Get("$skip")
		skips = append(skips, skip)
		var page []map[string]any
		switch skip {
		case "", "0":
			for i := 0; i < 100; i++ {
				page = append(page, map[string]any{"shortName": "P" + strconv.Itoa(i), "name": "Project " + strconv.Itoa(i)})
			}
		case "100":
			page = []map[string]any{
				{"shortName": "X1", "name": "Gamma"},
				{"shortName": "X2", "name": "Delta"},
				{"shortName": "X3", "name": "Epsilon"},
				{"shortName": "OLD", "name": "Old", "archived": true},
			}
		default:
			t.Errorf("unexpected $skip %q", skip)
		}
		_ = json.NewEncoder(w).Encode(page)
	}))

	got, err := c.Projects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 103 {
		t.Fatalf("got %d projects, want 103 (the archived one is skipped)", len(got))
	}
	last := got[len(got)-1]
	if last.Key != "X3" || last.Name != "Epsilon" {
		t.Errorf("last project = %+v", last)
	}
	if len(skips) != 2 {
		t.Errorf("requested pages %v, want exactly two", skips)
	}
}

func TestFindIssueByMarker(t *testing.T) {
	const marker = "[ticktack:default-issue]"
	tests := []struct {
		name      string
		issues    []map[string]any
		wantKey   string
		wantFound bool
	}{
		{"hit", []map[string]any{
			{"idReadable": "G-1", "description": "unrelated", "reporter": map[string]any{"login": "jane"}},
			{"idReadable": "G-2", "description": "Standard task " + marker, "reporter": map[string]any{"login": "jane"}},
		}, "G-2", true},
		{"miss", []map[string]any{{"idReadable": "G-1", "description": "unrelated", "reporter": map[string]any{"login": "jane"}}}, "", false},
		{"marker not in description", []map[string]any{{"idReadable": "G-3", "description": "mentions ticktack default-issue in prose", "reporter": map[string]any{"login": "jane"}}}, "", false},
		{"no description", []map[string]any{{"idReadable": "G-4", "reporter": map[string]any{"login": "jane"}}}, "", false},
		{"marker created by a colleague is never adopted", []map[string]any{
			{"idReadable": "G-5", "description": marker, "reporter": map[string]any{"login": "colleague"}},
		}, "", false},
		{"own issue wins after a colleague's", []map[string]any{
			{"idReadable": "G-5", "description": marker, "reporter": map[string]any{"login": "colleague"}},
			{"idReadable": "G-6", "description": marker, "reporter": map[string]any{"login": "jane"}},
		}, "G-6", true},
		{"no reporter is not adopted", []map[string]any{{"idReadable": "G-7", "description": marker}}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/users/me":
					_, _ = io.WriteString(w, `{"login":"jane"}`)
				case "/api/issues":
					q := r.URL.Query().Get("query")
					if !strings.Contains(q, "project: G") || !strings.Contains(q, "reporter: me") {
						t.Errorf("query %q must scope to the project and to the token owner as reporter", q)
					}
					if !strings.Contains(r.URL.Query().Get("fields"), "reporter(login)") {
						t.Errorf("fields %q must include the reporter", r.URL.Query().Get("fields"))
					}
					_ = json.NewEncoder(w).Encode(tt.issues)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			}))
			key, found, err := c.FindIssueByMarker(context.Background(), "G", marker)
			if err != nil {
				t.Fatal(err)
			}
			if key != tt.wantKey || found != tt.wantFound {
				t.Errorf("= (%q, %v), want (%q, %v)", key, found, tt.wantKey, tt.wantFound)
			}
		})
	}
}

func TestFindIssueByMarkerRejectsEmptyMarker(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no request expected")
	}))
	if _, _, err := c.FindIssueByMarker(context.Background(), "G", ""); err == nil {
		t.Fatal("empty marker accepted")
	}
}

func TestCreateIssueSendsProjectSummaryDescriptionAndAssignee(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/admin/projects/G":
			_, _ = io.WriteString(w, `{"id":"0-5"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/me":
			_, _ = io.WriteString(w, `{"login":"jane"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues":
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = io.WriteString(w, `{"idReadable":"G-9"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))

	key, err := c.CreateIssue(context.Background(), "G", "Meetings / Admin", "body [ticktack:default-issue]")
	if err != nil {
		t.Fatal(err)
	}
	if key != "G-9" {
		t.Errorf("key = %q, want G-9", key)
	}
	if got["summary"] != "Meetings / Admin" || got["description"] != "body [ticktack:default-issue]" {
		t.Errorf("body = %v", got)
	}
	if project, _ := got["project"].(map[string]any); project["id"] != "0-5" {
		t.Errorf("project = %v, want id 0-5", got["project"])
	}
	fields, _ := got["customFields"].([]any)
	if len(fields) != 1 {
		t.Fatalf("customFields = %v, want one Assignee entry", got["customFields"])
	}
	field, _ := fields[0].(map[string]any)
	value, _ := field["value"].(map[string]any)
	if field["name"] != "Assignee" || value["login"] != "jane" {
		t.Errorf("assignee field = %v", field)
	}
}

func TestCreateIssueFailsWithoutAnIssueKey(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/projects/G":
			_, _ = io.WriteString(w, `{"id":"0-5"}`)
		case "/api/users/me":
			_, _ = io.WriteString(w, `{"login":"jane"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	if _, err := c.CreateIssue(context.Background(), "G", "s", "d"); err == nil {
		t.Fatal("empty response accepted")
	}
}

func TestProvisionerErrorsDoNotLeakTheToken(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	_, err := c.Projects(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(fmt.Sprint(err), testToken) {
		t.Errorf("error leaks the token: %v", err)
	}
}
