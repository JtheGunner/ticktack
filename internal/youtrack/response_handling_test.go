package youtrack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/sink"
)

func TestOversizedResponseIsRejected(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[")
		_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes))
		_, _ = io.WriteString(w, "]")
	}))
	_, err := c.MyOpenIssues(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
}

func TestResponseAtTheLimitIsAccepted(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[")
		_, _ = io.WriteString(w, strings.Repeat(" ", maxResponseBytes-2))
		_, _ = io.WriteString(w, "]")
	}))
	if _, err := c.MyOpenIssues(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestBadRequestCarriesTheServerReason(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		notWant string
	}{
		{name: "error_description", body: `{"error":"Bad Request","error_description":"Unknown field: foo"}`, want: "status 400: Unknown field: foo"},
		{name: "error only", body: `{"error":"Bad Request"}`, want: "status 400: Bad Request"},
		{name: "html is not quoted", body: `<html><body>secret page</body></html>`, want: "status 400", notWant: "secret page"},
		{name: "empty body", body: ``, want: "status 400"},
		{name: "token echoed back is masked", body: `{"error_description":"bad header Bearer ` + testToken + `"}`, want: "[redacted]", notWant: "SUPER-SECRET"},
		{name: "control characters are flattened", body: `{"error_description":"a\nb\u0000c"}`, want: "status 400: a b c"},
		{name: "long reason is truncated", body: `{"error_description":"` + strings.Repeat("x", 1000) + `"}`, want: strings.Repeat("x", maxReasonRunes) + "..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, tc.body)
			}))
			_, err := c.MyOpenIssues(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("err = %v, must not contain %q", err, tc.notWant)
			}
			if len(err.Error()) > 400 {
				t.Errorf("error message is %d bytes long", len(err.Error()))
			}
		})
	}
}

func TestRetryAfterForms(t *testing.T) {
	now := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		status     int
		retryAfter string
		wantWait   time.Duration
		wantErr    error
	}{
		{name: "429 seconds", status: 429, retryAfter: "3", wantWait: 3 * time.Second, wantErr: sink.ErrRateLimited},
		{name: "429 http date", status: 429, retryAfter: now.Add(7 * time.Second).Format(http.TimeFormat), wantWait: 7 * time.Second, wantErr: sink.ErrRateLimited},
		{name: "503 seconds", status: 503, retryAfter: "4", wantWait: 4 * time.Second},
		{name: "503 http date", status: 503, retryAfter: now.Add(9 * time.Second).Format(http.TimeFormat), wantWait: 9 * time.Second},
		{name: "date in the past falls back to backoff", status: 503, retryAfter: now.Add(-time.Hour).Format(http.TimeFormat), wantWait: 0},
		{name: "garbage falls back to backoff", status: 503, retryAfter: "soon", wantWait: 0},
		{name: "huge delay is capped", status: 503, retryAfter: "100000", wantWait: maxRetryAfter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var waits []time.Duration
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", tc.retryAfter)
				w.WriteHeader(tc.status)
			}), WithMaxRetries(1))
			c.now = func() time.Time { return now }
			c.sleep = func(_ context.Context, d time.Duration) error {
				waits = append(waits, d)
				return nil
			}
			_, err := c.MyOpenIssues(context.Background())
			if err == nil {
				t.Fatal("want an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if calls.Load() != 2 || len(waits) != 1 {
				t.Fatalf("calls = %d, waits = %v, want one retry", calls.Load(), waits)
			}
			if waits[0] != tc.wantWait {
				t.Errorf("wait = %v, want %v", waits[0], tc.wantWait)
			}
		})
	}
}
