package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// CMakeBuilder implements the Builder interface using CMake as the meta-build
// system. It supports Ninja as the build tool (make support is planned).
type CMakeBuilder struct {
	cfg   *config.Config
	dirty bool
}

// NewCMakeBuilder creates a CMakeBuilder for the given configuration.
func NewCMakeBuilder(cfg *config.Config) *CMakeBuilder {
	return &CMakeBuilder{cfg: cfg}
}

// SetDirty sets the internal dirty flag. When dirty is true, the next Build
// call will pass --clean-first to cmake and then clear the flag.
func (b *CMakeBuilder) SetDirty(dirty bool) {
	b.dirty = dirty
}

// Configure runs cmake to generate the build system. Any extraArgs are
// appended after the configured CMakeArgs.
func (b *CMakeBuilder) Configure(ctx context.Context, extraArgs []string) (*BuildResult, error) {
	args := b.buildConfigureArgs(extraArgs)
	return b.run(ctx, "cmake", args)
}

// Build runs cmake --build to compile the project. If the builder is marked
// dirty, --clean-first is prepended and the flag is cleared. A timeout from
// cfg.BuildTimeout is applied to the context.
func (b *CMakeBuilder) Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, b.cfg.BuildTimeout)
	defer cancel()

	args := b.buildBuildArgs(targets, jobs)
	return b.run(timeoutCtx, "cmake", args)
}

// Clean runs cmake --build with the clean target.
func (b *CMakeBuilder) Clean(ctx context.Context, targets []string) (*BuildResult, error) {
	args := b.buildCleanArgs()
	return b.run(ctx, "cmake", args)
}

// generatorCMakeName maps a normalized generator name (as stored in
// Config.Generator) to the full name that cmake's -G flag expects.
//   - "ninja" -> "Ninja"
//   - "make"  -> "Unix Makefiles"
//   - ""      -> "Ninja" (default)
//   - unknown -> passed through as-is (let cmake decide)
func generatorCMakeName(gen string) string {
	switch gen {
	case "ninja", "":
		return "Ninja"
	case "make":
		return "Unix Makefiles"
	default:
		return gen
	}
}

// buildConfigureArgs constructs the argument list for a cmake configure
// invocation. This method is exported-via-test (lowercase) so unit tests can
// verify argument construction without invoking cmake.
func (b *CMakeBuilder) buildConfigureArgs(extraArgs []string) []string {
	args := []string{
		"-S", b.cfg.SourceDir,
		"-B", b.cfg.BuildDir,
		"-G", generatorCMakeName(b.cfg.Generator),
		"-DCMAKE_EXPORT_COMPILE_COMMANDS=ON",
	}

	if b.cfg.InjectDiagnosticFlags && b.cfg.Toolchain == "clang" {
		args = append(args,
			"-DCMAKE_C_FLAGS=-fdiagnostics-format=json",
			"-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json",
		)
	}

	args = append(args, b.cfg.CMakeArgs...)
	args = append(args, extraArgs...)

	return args
}

// buildBuildArgs constructs the argument list for a cmake --build invocation.
func (b *CMakeBuilder) buildBuildArgs(targets []string, jobs int) []string {
	args := []string{"--build", b.cfg.BuildDir}

	if b.dirty {
		args = append(args, "--clean-first")
		b.dirty = false
	}

	for _, t := range targets {
		args = append(args, "--target", t)
	}

	args = append(args, "--")

	if b.cfg.DiagnosticSerialBuild {
		jobs = 1
	}
	if jobs > 0 {
		args = append(args, fmt.Sprintf("-j%d", jobs))
	}

	return args
}

// buildCleanArgs constructs the argument list for a cmake --build clean
// invocation.
func (b *CMakeBuilder) buildCleanArgs() []string {
	return []string{"--build", b.cfg.BuildDir, "--target", "clean"}
}

// run executes a command, captures stdout and stderr, measures duration, and
// returns a BuildResult. It extracts the exit code from exec.ExitError when
// the command fails with a non-zero exit.
//
// When the context is cancelled or times out, the command receives SIGTERM
// first (via cmd.Cancel). If the process does not exit within 3 seconds,
// Go sends SIGKILL automatically (via cmd.WaitDelay). The returned
// BuildResult has Killed=true when the process was terminated this way.
func (b *CMakeBuilder) run(ctx context.Context, name string, args []string) (*BuildResult, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
