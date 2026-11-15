//go:build darwin

package presence

import (
	"context"
	"testing"
)

func TestDarwinSamplerReturnsBundleID(t *testing.T) {
	idle, app, err := NewSampler().Sample(context.Background())
	if err != nil {
		t.Skipf("no GUI session available: %v", err)
	}
	if idle < 0 || app == "" {
		t.Fatalf("unexpected sample idle=%v app=%q", idle, app)
	}
}
