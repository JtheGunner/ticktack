//go:build darwin

package presence

import (
	"context"
	"testing"
)

// The bundle ID may be empty (no GUI session, unbundled frontmost binary), so
// only the idle time is asserted against the real system.
func TestDarwinSamplerReturnsIdleTime(t *testing.T) {
	idle, _, err := NewSampler().Sample(context.Background())
	if err != nil {
		t.Skipf("idle time not readable here: %v", err)
	}
	if idle < 0 {
		t.Fatalf("unexpected sample idle=%v", idle)
	}
}
