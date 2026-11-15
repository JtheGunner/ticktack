package youtrack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sink"
)

const testToken = "perm:SUPER-SECRET-TOKEN"

var _ sink.TimeSink = (*Client)(nil)

const settingsJSON = `{"enabled":true,"workItemTypes":[{"id":"type-1","name":"Development"},{"id":"type-2","name":"Meeting"}]}`

func newTestClient(t *testing.T, h http.Handler, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	opts = append([]Option{
		WithBackoff(func(int) time.Duration { return 0 }),
		WithLocation(time.UTC),
	}, opts...)
	c, err := New(srv.URL, testToken, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sampleItem() sink.WorkItem {
	return sink.WorkItem{
		Issue:   "ABC-1",
		Date:    civil.Date{Year: 2026, Month: time.March, Day: 5},
		Minutes: 90,
		Type:    "Development",
		Text:    "did things [ticktack:k1]",
	}
}

func TestCreateSendsDurationDateTextAndType(t *testing.T) {
	var got map[string]any
	var auth string
	zone := time.FixedZone("x", 2*3600)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/admin/projects/ABC/timeTrackingSettings":
			_, _ = io.WriteString(w, settingsJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/ABC-1/timeTracking/workItems":
			auth = r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = io.WriteString(w, `{"id":"196-7"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}), WithLocation(zone))

	id, err := c.Create(context.Background(), sampleItem())
	if err != nil {
		t.Fatal(err)
	}
	if id != "ABC-1/196-7" {
		t.Errorf("id = %q", id)
	}
	if auth != "Bearer "+testToken {
		t.Errorf("auth header = %q", auth)
	}
	if got["text"] != "did things [ticktack:k1]" {
		t.Errorf("text = %v", got["text"])
	}
	if d := got["duration"].(map[string]any)["minutes"]; d != float64(90) {
		t.Errorf("minutes = %v", d)
	}
	if ty := got["type"].(map[string]any)["id"]; ty != "type-1" {
		t.Errorf("type id = %v", ty)
	}
	want := time.Date(2026, time.March, 5, 12, 0, 0, 0, zone).UnixMilli()
	if got["date"] != float64(want) {
		t.Errorf("date = %v, want local noon %d", got["date"], want)
	}
}

func TestCreateFailsWhenTimeTrackingDisabled(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"enabled":false,"workItemTypes":[]}`)
	}))
	_, err := c.Create(context.Background(), sampleItem())
	if !errors.Is(err, ErrTimeTrackingDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateFailsOnUnknownType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, settingsJSON)
	}))
	w := sampleItem()
	w.Type = "Nope"
	if _, err := c.Create(context.Background(), w); !errors.Is(err, ErrWorkItemTypeNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestTypeIDIsCachedPerProject(t *testing.T) {
	var settingsCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/timeTrackingSettings") {
			settingsCalls.Add(1)
			_, _ = io.WriteString(w, settingsJSON)
			return
		}
		_, _ = io.WriteString(w, `{"id":"1-1"}`)
	}))
	for range 2 {
		if _, err := c.Create(context.Background(), sampleItem()); err != nil {
			t.Fatal(err)
		}
	}
	if n := settingsCalls.Load(); n != 1 {
		t.Errorf("settings fetched %d times", n)
	}
}

func TestUpdateReplacesDuration(t *testing.T) {
	var got map[string]any
	var method, path string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"id":"196-7"}`)
	}))
	w := sampleItem()
	w.Minutes = 45
	if err := c.Update(context.Background(), "ABC-1/196-7", w); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/issues/ABC-1/timeTracking/workItems/196-7" {
		t.Errorf("%s %s", method, path)
	}
	if d := got["duration"].(map[string]any)["minutes"]; d != float64(45) {
		t.Errorf("minutes = %v", d)
	}
	if _, ok := got["type"]; ok {
		t.Error("update must not send type")
	}
}

func TestDeleteUsesIssueAndItemID(t *testing.T) {
	var method, path string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
	}))
	if err := c.Delete(context.Background(), "ABC-1/196-7"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodDelete || path != "/api/issues/ABC-1/timeTracking/workItems/196-7" {
		t.Errorf("%s %s", method, path)
	}
	if err := c.Delete(context.Background(), "malformed"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("err = %v", err)
	}
}

func TestFindByMarkerMatchesVerbatim(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[
			{"id":"1-1","text":"x [ticktack:ABC-10]"},
			{"id":"1-2","text":"no marker"},
			{"id":"1-3","text":"work [ticktack:ABC-1] more"}]`)
	}))
	id, found, err := c.FindByMarker(context.Background(), "ABC-1", "[ticktack:ABC-1]")
	if err != nil || !found || id != "ABC-1/1-3" {
		t.Fatalf("id=%q found=%v err=%v", id, found, err)
	}
	_, found, err = c.FindByMarker(context.Background(), "ABC-1", "[ticktack:ABC-2]")
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, _, err = c.FindByMarker(context.Background(), "ABC-1", ""); err == nil {
		t.Fatal("empty marker must be rejected")
	}
}

func TestUnauthorizedIsTypedAndNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"Unauthorized"}`)
	}))
	_, err := c.MyOpenIssues(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestRateLimitIsRetriedThenReturned(t *testing.T) {
	var calls atomic.Int32
	var waits []time.Duration
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}), WithMaxRetries(2))
	c.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	_, err := c.MyOpenIssues(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
	if len(waits) != 2 || waits[0] != 2*time.Second {
		t.Errorf("waits = %v, want Retry-After honored", waits)
	}
}

func TestServerErrorIsRetriedThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `[{"idReadable":"ABC-1","summary":"s"}]`)
	}))
	issues, err := c.MyOpenIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].ID != "ABC-1" || issues[0].Summary != "s" {
		t.Errorf("issues = %+v", issues)
	}
}

func TestMyOpenIssuesQuery(t *testing.T) {
	var q string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query().Get("query")
		_, _ = io.WriteString(w, `[]`)
	}))
	if _, err := c.MyOpenIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if q != "for: me State: {In Progress}" {
		t.Errorf("query = %q", q)
	}
}

func TestWorkItemTypes(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, settingsJSON)
	}))
	types, err := c.WorkItemTypes(context.Background(), "ABC")
	if err != nil || len(types) != 2 || types[1].Name != "Meeting" || types[1].ID != "type-2" {
		t.Fatalf("types=%+v err=%v", types, err)
	}
}

func TestIssueNotFoundIsTyped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<html><body>404 Not Found</body></html>`)
	}))
	_, _, err := c.FindByMarker(context.Background(), "ABC-1", "[m]")
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Errorf("html body leaked into error: %v", err)
	}
}

func TestForbiddenWithHTMLBodyIsTyped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<html><body>Forbidden</body></html>`)
	}))
	err := c.Delete(context.Background(), "ABC-1/1-1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Errorf("html body leaked into error: %v", err)
	}
}

func TestTokenNeverAppearsInErrorsOrLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, status := range []int{200, 400, 401, 403, 404, 429, 500} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Echo the token back as a hostile server might.
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"`+r.Header.Get("Authorization")+`"}`)
		}), WithLogger(logger), WithMaxRetries(1))
		_, err := c.MyOpenIssues(context.Background())
		if err != nil && strings.Contains(err.Error(), "SUPER-SECRET") {
			t.Errorf("status %d: token in error: %v", status, err)
		}
		for _, s := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
			if strings.Contains(s, "SUPER-SECRET") {
				t.Errorf("token in formatted client: %s", s)
			}
		}
	}
	if strings.Contains(logs.String(), "SUPER-SECRET") {
		t.Errorf("token in logs: %s", logs.String())
	}
	if logs.Len() == 0 {
		t.Error("expected some log output")
	}
}

func TestMeReturnsLoginAndRequestsOnlyLogin(t *testing.T) {
	var gotPath, gotFields string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotFields = r.URL.Path, r.URL.Query().Get("fields")
		_, _ = io.WriteString(w, `{"login":"jane"}`)
	}))
	login, err := c.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if login != "jane" || gotPath != "/api/users/me" || gotFields != "login" {
		t.Fatalf("login=%q path=%q fields=%q", login, gotPath, gotFields)
	}
}

func TestMeMapsStatusCodesToTypedErrors(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrForbidden},
	}
	for _, tc := range tests {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		if _, err := c.Me(context.Background()); !errors.Is(err, tc.want) {
			t.Errorf("status %d: got %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestNewValidatesInput(t *testing.T) {
	if _, err := New("", testToken); err == nil {
		t.Error("empty base URL accepted")
	}
	if _, err := New("http://x", ""); err == nil {
		t.Error("empty token accepted")
	}
}
