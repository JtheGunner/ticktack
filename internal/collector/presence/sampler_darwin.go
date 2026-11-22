//go:build darwin

package presence

import (
	"context"
	"os/exec"
	"time"
)

// commandWaitDelay is how long a killed command may keep its output pipe open
// before the call returns anyway.
const commandWaitDelay = time.Second

// NewSampler returns the platform sampler.
func NewSampler() Sampler { return commandSampler{run: runCommand} }

// runCommand runs a command without a shell. When ctx ends the process is
// killed; WaitDelay keeps a child that inherited the pipe from blocking the
// call beyond that.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = commandWaitDelay
	return cmd.Output()
}
