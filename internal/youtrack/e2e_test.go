//go:build e2e

package youtrack

import (
	"context"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sink"
)

// TestE2ELifecycle runs create, update, find and delete against a real
// YouTrack. It is skipped unless TICKTACK_E2E_URL, TICKTACK_E2E_TOKEN,
// TICKTACK_E2E_ISSUE and TICKTACK_E2E_TYPE are set.
func TestE2ELifecycle(t *testing.T) {
	baseURL, token := os.Getenv("TICKTACK_E2E_URL"), os.Getenv("TICKTACK_E2E_TOKEN")
	issue, typ := os.Getenv("TICKTACK_E2E_ISSUE"), os.Getenv("TICKTACK_E2E_TYPE")
	if baseURL == "" || token == "" || issue == "" || typ == "" {
		t.Skip("TICKTACK_E2E_{URL,TOKEN,ISSUE,TYPE} not set")
	}
	c, err := New(baseURL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	marker := "[ticktack:e2e-" + time.Now().Format("20060102150405") + "]"
	w := sink.WorkItem{Issue: issue, Date: civil.DateOf(time.Now()), Minutes: 5, Type: typ, Text: "e2e " + marker}

	id, err := c.Create(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, id) })

	w.Minutes = 10
	if err := c.Update(ctx, id, w); err != nil {
		t.Fatal(err)
	}
	found, ok, err := c.FindByMarker(ctx, issue, marker)
	if err != nil || !ok || found != id {
		t.Fatalf("found=%q ok=%v err=%v", found, ok, err)
	}
	if err := c.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = c.FindByMarker(ctx, issue, marker); ok {
		t.Fatal("item still present after delete")
	}
}
