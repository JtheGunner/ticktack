//go:build !darwin

package title

import (
	"context"
	"errors"
)

type unsupportedSource struct{}

// NewSource returns a source that always fails: reading window titles is
// only implemented on macOS.
func NewSource() Source { return unsupportedSource{} }

func (unsupportedSource) Frontmost(context.Context) (Window, error) {
	return Window{}, errors.New("window title hints are only supported on macOS")
}
