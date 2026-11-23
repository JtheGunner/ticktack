package youtrack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/sink"
)

// storingServer is a fake YouTrack that stores every work item it receives and
// then answers the create POST with createStatus, like a proxy that times out
// after YouTrack has already saved the item. A status of 0 drops the connection
// instead of answering.
type storingServer struct {
	createStatus int

	mu    sync.Mutex
	texts []string // stored work item texts, in order
	posts int      // create POSTs received
}

func (s *storingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/admin/projects/ABC/timeTrackingSettings":
		_, _ = io.WriteString(w, settingsJSON)
	case r.Method == http.MethodGet && r.URL.Path == "/api/users/me":
		_, _ = io.WriteString(w, `{"login":"jane"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/issues/ABC-1/timeTracking/workItems":
		items := make([]map[string]any, 0, len(s.texts))
		for i, text := range s.texts {
			items = append(items, map[string]any{
				"id": fmt.Sprintf("196-%d", i+1), "text": text, "author": map[string]any{"login": "jane"},
			})
		}
		_ = json.NewEncoder(w).Encode(items)
	case r.Method == http.MethodPost && r.URL.Path == "/api/issues/ABC-1/timeTracking/workItems":
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.posts++
		s.texts = append(s.texts, body.Text)
		switch s.createStatus {
		case 0:
			panic(http.ErrAbortHandler)
		case http.StatusOK:
			_, _ = fmt.Fprintf(w, `{"id":"196-%d"}`, len(s.texts))
		default:
			w.WriteHeader(s.createStatus)
		}
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

func (s *storingServer) stored() (items, posts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.texts), s.posts
}

func TestCreateIsNotRepeatedWhenTheOutcomeIsUnclear(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"gateway timeout after the item was stored", http.StatusGatewayTimeout},
		{"bad gateway after the item was stored", http.StatusBadGateway},
		{"internal server error after the item was stored", http.StatusInternalServerError},
		{"connection dropped after the item was stored", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &storingServer{createStatus: tt.status}
			c := newTestClient(t, srv)

			id, err := c.Create(context.Background(), sampleItem())
			if !errors.Is(err, sink.ErrOutcomeUnclear) {
				t.Fatalf("Create = %q, %v; want sink.ErrOutcomeUnclear", id, err)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("error leaks the token: %v", err)
			}
			if items, posts := srv.stored(); items != 1 || posts != 1 {
				t.Fatalf("items = %d, posts = %d; want exactly one of each", items, posts)
			}

			// The next sync finds the stored item through its marker.
			found, ok, err := c.FindByMarker(context.Background(), "ABC-1", "[ticktack:k1]")
			if err != nil || !ok || found != "ABC-1/196-1" {
				t.Fatalf("FindByMarker = %q, %v, %v; want ABC-1/196-1", found, ok, err)
			}
		})
	}
}

func TestCreateFailuresWithKnownOutcomeAreNotUnclear(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusBadRequest, nil},
		{http.StatusUnauthorized, sink.ErrUnauthorized},
		{http.StatusForbidden, sink.ErrForbidden},
		{http.StatusNotFound, sink.ErrNotFound},
		{http.StatusTooManyRequests, sink.ErrRateLimited},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, settingsJSON)
					return
				}
				w.WriteHeader(tt.status)
			}), WithMaxRetries(0))
			_, err := c.Create(context.Background(), sampleItem())
			if err == nil {
				t.Fatal("Create succeeded")
			}
			if errors.Is(err, sink.ErrOutcomeUnclear) {
				t.Errorf("err = %v, must not be sink.ErrOutcomeUnclear: the server rejected the request", err)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCreateIsRepeatedAfterRateLimit(t *testing.T) {
	rejected := false
	srv := &storingServer{createStatus: http.StatusOK}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !rejected {
			rejected = true
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		srv.ServeHTTP(w, r)
	}))
	id, err := c.Create(context.Background(), sampleItem())
	if err != nil || id != "ABC-1/196-1" {
		t.Fatalf("Create = %q, %v", id, err)
	}
	if items, _ := srv.stored(); items != 1 {
		t.Fatalf("items = %d, want 1", items)
	}
}

func TestIdempotentCallsKeepRetryWithBackoff(t *testing.T) {
	tests := []struct {
		name   string
		method string
		call   func(c *Client) error
	}{
		{"get", http.MethodGet, func(c *Client) error {
			_, err := c.MyOpenIssues(context.Background())
			return err
		}},
		{"update", http.MethodPost, func(c *Client) error {
			return c.Update(context.Background(), "ABC-1/196-7", sampleItem())
		}},
		{"delete", http.MethodDelete, func(c *Client) error {
			return c.Delete(context.Background(), "ABC-1/196-7")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method {
					t.Errorf("method = %s, want %s", r.Method, tt.method)
				}
				calls++
				if calls < 3 {
					w.WriteHeader(http.StatusGatewayTimeout)
					return
				}
				_, _ = io.WriteString(w, `[]`)
			}), WithBackoff(func(attempt int) time.Duration { return time.Duration(attempt) * time.Second }))
			var waits []time.Duration
			c.sleep = func(_ context.Context, d time.Duration) error {
				waits = append(waits, d)
				return nil
			}
			if err := tt.call(c); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Errorf("calls = %d, want 3", calls)
			}
			if len(waits) != 2 || waits[0] != time.Second || waits[1] != 2*time.Second {
				t.Errorf("waits = %v, want [1s 2s]", waits)
			}
		})
	}
}

func TestCreateIssueDoesNotRepeatCreatePosts(t *testing.T) {
	tests := []struct {
		name         string
		tagsOnServer string
		failingPath  string
	}{
		{"issue stored, then gateway timeout", `[{"id":"5-1","name":"manual"}]`, "/api/issues"},
		{"tag stored, then gateway timeout", `[]`, "/api/tags"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posts := map[string]int{}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/admin/projects/G":
					_, _ = io.WriteString(w, `{"id":"0-5"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/users/me":
					_, _ = io.WriteString(w, `{"login":"jane"}`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/tags":
					_, _ = io.WriteString(w, tt.tagsOnServer)
				case r.Method == http.MethodPost:
					posts[r.URL.Path]++
					if r.URL.Path == tt.failingPath {
						w.WriteHeader(http.StatusGatewayTimeout)
						return
					}
					_, _ = io.WriteString(w, `{"id":"5-2","idReadable":"G-9"}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			}))

			key, err := c.CreateIssue(context.Background(), "G", "s", "d")
			if !errors.Is(err, sink.ErrOutcomeUnclear) {
				t.Fatalf("CreateIssue = %q, %v; want sink.ErrOutcomeUnclear", key, err)
			}
			if posts[tt.failingPath] != 1 {
				t.Errorf("POST %s sent %d times, want 1", tt.failingPath, posts[tt.failingPath])
			}
		})
	}
}
