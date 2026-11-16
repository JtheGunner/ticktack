package activitywatch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/loopback"
)

func TestNewRejectsNonLoopbackURLs(t *testing.T) {
	for _, raw := range []string{
		"http://example.com:5600",
		"http://localhost.evil.com:5600",
		"http://localhost@evil.com",
		"http://localhost:5600@evil.com",
		"http://user:pw@localhost:5600",
		"http://10.0.0.5:5600",
		"gopher://localhost",
		"",
	} {
		_, err := New(raw)
		if !errors.Is(err, loopback.ErrNotLoopback) {
			t.Errorf("New(%q) = %v, want ErrNotLoopback", raw, err)
		}
	}
}

func TestNewAcceptsLoopbackURLs(t *testing.T) {
	for _, raw := range []string{"http://localhost:5600", "http://127.0.0.1:5600", "http://[::1]:5600"} {
		if _, err := New(raw); err != nil {
			t.Errorf("New(%q) = %v", raw, err)
		}
	}
}

func TestClientRefusesRedirectToNonLoopbackHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid:5600/api/0/buckets/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Buckets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("err = %v, want a refused redirect", err)
	}
}

func TestClientRefusesRedirectChainThroughInjectedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	// An injected client without its own policy still gets the loopback one.
	c, err := New(srv.URL, WithHTTPClient(&http.Client{Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Buckets(context.Background()); err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientFollowsLoopbackRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Buckets(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestClientHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Buckets(ctx); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}
