package youtrack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/JtheGunner/ticktack/internal/sink"
)

func TestWorkItemExists(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantExists bool
		wantErr    bool
		wantIs     error
		wantCalls  int32
	}{
		{name: "found", status: http.StatusOK, body: `{"id":"196-7"}`, wantExists: true, wantCalls: 1},
		{name: "not found", status: http.StatusNotFound, body: `<html>404</html>`, wantCalls: 1},
		{name: "server error is retried and returned", status: http.StatusBadGateway, wantErr: true, wantCalls: 3},
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: true, wantIs: sink.ErrUnauthorized, wantCalls: 1},
		{name: "answer for another item", status: http.StatusOK, body: `{"id":"196-8"}`, wantErr: true, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/issues/ABC-1/timeTracking/workItems/196-7" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if got := r.URL.Query().Get("fields"); got != "id" {
					t.Errorf("fields = %q", got)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}), WithMaxRetries(2))

			exists, err := c.WorkItemExists(context.Background(), "ABC-1", "ABC-1/196-7")

			if exists != tt.wantExists || (err != nil) != tt.wantErr {
				t.Fatalf("exists=%v err=%v", exists, err)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("err = %v, want %v", err, tt.wantIs)
			}
			if calls.Load() != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
		})
	}
}

func TestWorkItemExistsRecoversFromServerError(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"id":"196-7"}`)
	}))

	exists, err := c.WorkItemExists(context.Background(), "ABC-1", "ABC-1/196-7")

	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
}

func TestWorkItemExistsRejectsIDsItCannotCheck(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no request expected")
	}))
	for _, id := range []string{"", "196-7", "ABC-2/196-7"} {
		if exists, err := c.WorkItemExists(context.Background(), "ABC-1", id); !errors.Is(err, ErrInvalidID) || exists {
			t.Errorf("id %q: exists=%v err=%v, want ErrInvalidID", id, exists, err)
		}
	}
}
