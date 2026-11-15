//go:build darwin

package presence

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DarwinSampler reads idle time via ioreg and the frontmost app via lsappinfo.
// Neither requires any macOS permission.
type DarwinSampler struct{}

// NewSampler returns the platform sampler.
func NewSampler() Sampler { return DarwinSampler{} }

// Sample implements Sampler.
func (DarwinSampler) Sample(ctx context.Context) (time.Duration, string, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return 0, "", fmt.Errorf("run ioreg: %w", err)
	}
	idle, err := ParseIdle(string(out))
	if err != nil {
		return 0, "", err
	}
	front, err := exec.CommandContext(ctx, "lsappinfo", "front").Output()
	if err != nil {
		return 0, "", fmt.Errorf("run lsappinfo front: %w", err)
	}
	asn := strings.TrimSpace(string(front))
	if asn == "" {
		return 0, "", errors.New("lsappinfo front returned no application")
	}
	info, err := exec.CommandContext(ctx, "lsappinfo", "info", "-only", "bundleid", asn).Output()
	if err != nil {
		return 0, "", fmt.Errorf("run lsappinfo info: %w", err)
	}
	app, err := ParseBundleID(string(info))
	if err != nil {
		return 0, "", err
	}
	return idle, app, nil
}
