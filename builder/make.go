package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// MakeBuilder implements the Builder interface using GNU Make as the build
// tool. Unlike CMakeBuilder, Make has no configure step so Configure is a
// no-op.
type MakeBuilder struct {
	cfg   *config.Config
	dirty bool
}

// NewMakeBuilder creates a MakeBuilder for the given configuration.
func NewMakeBuilder(cfg *config.Config) *MakeBuilder {
	return &MakeBuilder{cfg: cfg}
}

// SetDirty sets the internal dirty flag. When dirty is true, the next Build
// call will run "make clean" before the build and then clear the flag.
func (b *MakeBuilder) SetDirty(dirty bool) {
	b.dirty = dirty
}

// Configure is a no-op for Make since Make has no separate configure step.
// It always returns a successful BuildResult with ExitCode 0.
func (b *MakeBuilder) Configure(_ context.Context, _ []string) (*BuildResult, error) {
	return &BuildResult{ExitCode: 0}, nil
}

// Build runs make to compile the project. If the builder is marked dirty,
// "make clean" is run first and the flag is cleared. A timeout from
// cfg.BuildTimeout is applied to the context.
func (b *MakeBuilder) Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, b.cfg.BuildTimeout)
	defer cancel()

	if b.dirty {
		cleanResult, err := b.runMake(timeoutCtx, b.buildCleanArgs())
		if err != nil {
			return nil, err
		}
		if cleanResult.ExitCode != 0 {
			return cleanResult, nil
		}
		b.dirty = false
	}

	args := b.buildBuildArgs(targets, jobs)
	return b.runMake(timeoutCtx, args)
}

// Clean runs "make clean" regardless of any target arguments, since Make
// does not support targeted clean well.
func (b *MakeBuilder) Clean(ctx context.Context, _ []string) (*BuildResult, error) {
	args := b.buildCleanArgs()
	return b.runMake(ctx, args)
}

// buildBuildArgs constructs the argument list for a make invocation.
func (b *MakeBuilder) buildBuildArgs(targets []string, jobs int) []string {
	args := []string{"-C", b.cfg.BuildDir}

	args = append(args, targets...)

	if b.cfg.DiagnosticSerialBuild {
		jobs = 1
	}
	if jobs > 0 {
		args = append(args, fmt.Sprintf("-j%d", jobs))
	}

	return args
}

// buildCleanArgs constructs the argument list for a "make clean" invocation.
func (b *MakeBuilder) buildCleanArgs() []string {
	return []string{"-C", b.cfg.BuildDir, "clean"}
}

// diagnosticFlag returns the compiler flag string for structured diagnostics,
// based on the configured toolchain. Clang uses SARIF; everything else uses JSON.
func (b *MakeBuilder) diagnosticFlag() string {
	switch strings.ToLower(b.cfg.Toolchain) {
	case "clang":
		return "-fdiagnostics-format=sarif -Wno-sarif-format-unstable"
	default:
		return "-fdiagnostics-format=json"
	}
}

// buildEnv constructs the environment variable slice for a make invocation.
// When InjectDiagnosticFlags is true, it appends the appropriate diagnostic
// format flag to CFLAGS and CXXFLAGS.
func (b *MakeBuilder) buildEnv() []string {
	env := os.Environ()
	if !b.cfg.InjectDiagnosticFlags {
		return env
	}

	diagFlag := b.diagnosticFlag()

	cflags := os.Getenv("CFLAGS")
	cxxflags := os.Getenv("CXXFLAGS")

	if cflags != "" {
		cflags = cflags + " " + diagFlag
	} else {
		cflags = diagFlag
	}
	if cxxflags != "" {
		cxxflags = cxxflags + " " + diagFlag
	} else {
		cxxflags = diagFlag
	}

	// Replace or append CFLAGS and CXXFLAGS in the env slice.
	env = setEnvVar(env, "CFLAGS", cflags)
	env = setEnvVar(env, "CXXFLAGS", cxxflags)

	return env
}

// setEnvVar replaces the value of key in the env slice, or appends it if
// the key is not already present.
func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// runMake executes the make command with the given arguments, captures stdout
// and stderr, measures duration, and returns a BuildResult.
//
// When the context is cancelled or times out, the command receives SIGTERM
// first (via cmd.Cancel). If the process does not exit within 3 seconds,
// Go sends SIGKILL automatically (via cmd.WaitDelay). The returned
// BuildResult has Killed=true when the process was terminated this way.
func (b *MakeBuilder) runMake(ctx context.Context, args []string) (*BuildResult, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, "make", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = b.buildEnv()

	// Graceful shutdown: send SIGTERM on context cancellation, then SIGKILL
	// after WaitDelay if the process has not exited.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	killed := false
	if err != nil && ctx.Err() != nil {
		// The context was cancelled or timed out — the process was killed.
		killed = true
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &BuildResult{
				ExitCode: exitErr.ExitCode(),
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				Duration: duration,
				Killed:   killed,
			}, nil
		}
		if killed {
			return &BuildResult{
				ExitCode: -1,
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				Duration: duration,
				Killed:   true,
			}, nil
		}
		return nil, err
	}

	return &BuildResult{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}, nil
}
