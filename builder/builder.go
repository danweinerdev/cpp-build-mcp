// Package builder provides build system abstractions.
package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// BuildResult holds the output and metadata from a build system invocation.
type BuildResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Builder is the interface that all build system backends must implement.
type Builder interface {
	Configure(ctx context.Context, args []string) (*BuildResult, error)
	Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error)
	Clean(ctx context.Context, targets []string) (*BuildResult, error)
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
