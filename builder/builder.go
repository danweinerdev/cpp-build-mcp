// Package builder provides build system abstractions.
package builder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// TargetInfo describes a single build target.
type TargetInfo struct {
	Name string `json:"name"`
}

// ErrTargetsNotSupported is returned by ListTargets when the build system
// backend does not support enumerating targets.
var ErrTargetsNotSupported = errors.New("target listing not supported for this build system")

// BuildResult holds the output and metadata from a build system invocation.
type BuildResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	Killed   bool // true if the subprocess was killed due to timeout/cancellation
}

// Builder is the interface that all build system backends must implement.
type Builder interface {
	Configure(ctx context.Context, args []string) (*BuildResult, error)
	Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error)
	Clean(ctx context.Context, targets []string) (*BuildResult, error)
	ListTargets(ctx context.Context) ([]TargetInfo, error)
	SetDirty(dirty bool)
}

// NewBuilder creates a Builder for the given configuration. The generator
// field determines which backend is used.
func NewBuilder(cfg *config.Config) (Builder, error) {
	switch cfg.Generator {
	case "ninja", "":
		return NewCMakeBuilder(cfg), nil
	case "make":
		return NewMakeBuilder(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported generator: %s (supported: ninja, make)", cfg.Generator)
	}
}
