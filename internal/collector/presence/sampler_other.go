//go:build !darwin

package presence

import (
	"context"
	"errors"
	"time"
)

type unsupportedSampler struct{}

// NewSampler returns a sampler that always fails: presence sampling is only
// implemented on macOS.
func NewSampler() Sampler { return unsupportedSampler{} }

func (unsupportedSampler) Sample(context.Context) (time.Duration, string, error) {
	return 0, "", errors.New("presence sampling is only supported on macOS")
}
