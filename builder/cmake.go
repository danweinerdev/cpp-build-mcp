package builder

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

//go:embed diagnostic_format.cmake
var diagnosticFormatCMake string

// ProgressFunc is called with build progress updates. current and total
// correspond to Ninja's [current/total] progress line. message is the
// full progress line text.
type ProgressFunc func(current, total int, message string)

// ninjaProgressRe matches Ninja progress lines like [1/803] and captures
// both the current (N) and total (M) values.
var ninjaProgressRe = regexp.MustCompile(`^\[(\d+)/(\d+)\]`)

// CMakeBuilder implements the Builder interface using CMake as the meta-build
// system. It supports Ninja as the build tool (make support is planned).
type CMakeBuilder struct {
	cfg                 *config.Config
	dirty               bool
	moduleWritten       bool
	progressFunc        ProgressFunc
	progressMinInterval time.Duration
}

// NewCMakeBuilder creates a CMakeBuilder for the given configuration.
func NewCMakeBuilder(cfg *config.Config) *CMakeBuilder {
	return &CMakeBuilder{
		cfg:                 cfg,
		progressMinInterval: 250 * time.Millisecond,
	}
}

// SetProgressFunc sets an optional progress callback for the next Build call.
// Pass nil to disable. The callback is not cleared automatically — the caller
// is responsible for clearing it after Build returns.
func (b *CMakeBuilder) SetProgressFunc(fn ProgressFunc) {
	b.progressFunc = fn
}

// SetDirty sets the internal dirty flag. When dirty is true, the next Build
// call will pass --clean-first to cmake and then clear the flag.
func (b *CMakeBuilder) SetDirty(dirty bool) {
	b.dirty = dirty
}

// Configure runs cmake to generate the build system. Any extraArgs are
// appended after the configured CMakeArgs.
//
// When InjectDiagnosticFlags is true, Configure writes the embedded
// DiagnosticFormat.cmake module into the build directory and passes it
// via -DCMAKE_PROJECT_INCLUDE so CMake probes the active compiler for
// structured diagnostic support at configure time.
func (b *CMakeBuilder) Configure(ctx context.Context, extraArgs []string) (*BuildResult, error) {
	b.moduleWritten = false
	if b.cfg.InjectDiagnosticFlags {
		if err := b.writeDiagnosticModule(); err != nil {
			slog.Warn("failed to write diagnostic format module", "error", err)
			// Non-fatal: configure will proceed without diagnostic flags.
			// moduleWritten stays false, so buildConfigureArgs won't reference
			// the missing file.
		} else {
			b.moduleWritten = true
		}
	}
	args := b.buildConfigureArgs(extraArgs)
	return b.run(ctx, "cmake", args)
}

// writeDiagnosticModule writes the embedded DiagnosticFormat.cmake to
// <buildDir>/.cpp-build-mcp/DiagnosticFormat.cmake so it can be included
// via CMAKE_PROJECT_INCLUDE during configure. The build directory tree is
// created if it does not yet exist (preset-derived build dirs often don't
// exist before the first configure).
func (b *CMakeBuilder) writeDiagnosticModule() error {
	dir := filepath.Join(b.cfg.BuildDir, ".cpp-build-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating diagnostic module directory %s: %w", dir, err)
	}
	dest := filepath.Join(dir, "DiagnosticFormat.cmake")
	if err := os.WriteFile(dest, []byte(diagnosticFormatCMake), 0o644); err != nil {
		return fmt.Errorf("writing diagnostic module %s: %w", dest, err)
	}
	slog.Debug("wrote diagnostic format module", "path", dest)
	return nil
}

// diagnosticModulePath returns the absolute path to the written
// DiagnosticFormat.cmake, or "" if the module was not successfully written
// (injection disabled or write failed). An absolute path is required because
// cmake resolves CMAKE_PROJECT_INCLUDE relative to the source directory, which
// may differ from the process working directory.
func (b *CMakeBuilder) diagnosticModulePath() string {
	if !b.moduleWritten {
		return ""
	}
	p := filepath.Join(b.cfg.BuildDir, ".cpp-build-mcp", "DiagnosticFormat.cmake")
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
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

// ListTargets returns the list of build targets. This is a stub that will be
// implemented with real Ninja target parsing in a later task.
func (b *CMakeBuilder) ListTargets(ctx context.Context) ([]TargetInfo, error) {
	return nil, errors.New("not yet implemented")
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
		// Note: NewBuilder routes Generator=="make" to MakeBuilder, so this
		// branch is not reachable via CMakeBuilder in production. It exists
		// for completeness and forward-compatibility.
		return "Unix Makefiles"
	default:
		return gen
	}
}

// buildConfigureArgs constructs the argument list for a cmake configure
// invocation. This method is exported-via-test (lowercase) so unit tests can
// verify argument construction without invoking cmake.
//
// When cfg.Preset is non-empty, the preset path is used: --preset <name> is
// emitted and -S, -B, -G flags are omitted because cmake resolves source dir,
// build dir, and generator from the preset.
func (b *CMakeBuilder) buildConfigureArgs(extraArgs []string) []string {
	var args []string

	if b.cfg.Preset != "" {
		// Preset mode: cmake resolves source dir, build dir, and generator
		// from the preset — do NOT emit -S, -B, or -G.
		if b.cfg.BuildDir == "build" {
			slog.Warn("preset is set but build_dir is the default; build_dir should match the preset's binaryDir",
				"preset", b.cfg.Preset)
		}
		args = []string{"--preset", b.cfg.Preset, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"}
	} else {
		// Non-preset mode: explicit source dir, build dir, and generator.
		args = []string{
			"-S", b.cfg.SourceDir,
			"-B", b.cfg.BuildDir,
			"-G", generatorCMakeName(b.cfg.Generator),
			"-DCMAKE_EXPORT_COMPILE_COMMANDS=ON",
		}
	}

	if modPath := b.diagnosticModulePath(); modPath != "" {
		args = append(args, "-DCMAKE_PROJECT_INCLUDE="+modPath)
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
		// Keep going despite failures so diagnostic output is collected from
		// all translation units, not just the first one that fails.
		args = append(args, "-k", "0")
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
// When progressFunc is set, stdout is teed via io.MultiWriter to both a buffer
// (for BuildResult.Stdout) and an io.Pipe feeding a scanner goroutine. Stdout
// is used because Ninja writes [N/M] progress lines there. The goroutine
// matches these lines and calls progressFunc with throttling. A sync.WaitGroup
// ensures the goroutine exits before run returns.
//
// When the context is cancelled or times out, the command receives SIGTERM
// first (via cmd.Cancel). If the process does not exit within 3 seconds,
// Go sends SIGKILL automatically (via cmd.WaitDelay). The returned
// BuildResult has Killed=true when the process was terminated this way.
func (b *CMakeBuilder) run(ctx context.Context, name string, args []string) (*BuildResult, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout

	var wg sync.WaitGroup
	var pipeW *io.PipeWriter

	cmd.Stderr = &stderr

	if b.progressFunc != nil {
		var pipeR *io.PipeReader
		pipeR, pipeW = io.Pipe()
		cmd.Stdout = io.MultiWriter(&stdout, pipeW)

		wg.Add(1)
		go b.scanProgress(pipeR, &wg)
	}

	// Graceful shutdown: send SIGTERM on context cancellation, then SIGKILL
	// after WaitDelay if the process has not exited.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// Close the pipe writer after cmd.Run returns (process exited, all internal
	// I/O goroutines done). This causes the scanner goroutine to see EOF.
	if pipeW != nil {
		pipeW.Close()
	}
	// Wait for the scanner goroutine to finish before returning, ensuring no
	// data race between the goroutine calling progressFunc and the caller's
	// deferred SetProgressFunc(nil).
	wg.Wait()

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

// scanProgress reads lines from r (teed from stdout, where Ninja writes its
// [N/M] progress lines), matches them, and calls b.progressFunc with
// throttling. It is run in a goroutine by run().
//
// On panic: recovers, logs, and continues draining the pipe until EOF. This
// ensures io.MultiWriter never sees ErrClosedPipe from a prematurely closed
// pipe reader.
func (b *CMakeBuilder) scanProgress(r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()

	defer func() {
		if p := recover(); p != nil {
			slog.Error("progress scanner panic", "panic", p)
			// Drain remaining pipe data to avoid blocking io.MultiWriter.
			// The outer loop has unwound, so we drain here directly.
			io.Copy(io.Discard, r)
		}
	}()

	scanner := bufio.NewScanner(r)
	// Zero value means the first matching line is always delivered regardless
	// of throttle, since any real time minus time.Time{} exceeds any interval.
	lastNotify := time.Time{}

	for scanner.Scan() {
		line := scanner.Text()
		m := ninjaProgressRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		current, err1 := strconv.Atoi(m[1])
		total, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}

		now := time.Now()
		// Final line (N == M) is always sent, regardless of throttle.
		if current == total || now.Sub(lastNotify) >= b.progressMinInterval {
			b.progressFunc(current, total, line)
			lastNotify = now
		}
	}
}
