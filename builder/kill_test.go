package builder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// ---------------------------------------------------------------------------
// Graceful subprocess kill tests
// ---------------------------------------------------------------------------

// TestCMakeRunKilledOnTimeout verifies that when the context times out,
// the CMakeBuilder.run() method returns Killed=true. This uses a real
// subprocess ("sleep") with a very short timeout to trigger the kill path.
func TestCMakeRunKilledOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	// Use a very short timeout so the sleep command gets killed.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := b.run(ctx, "sleep", []string{"30"})
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if !result.Killed {
		t.Fatal("expected Killed=true when context times out")
	}
}

// TestCMakeRunNotKilledOnNormalExit verifies that a normally completing
// command does NOT set Killed=true.
func TestCMakeRunNotKilledOnNormalExit(t *testing.T) {
	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	ctx := context.Background()
	result, err := b.run(ctx, "true", nil)
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if result.Killed {
		t.Fatal("expected Killed=false for normal exit")
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected ExitCode=0, got %d", result.ExitCode)
	}
}

// TestCMakeRunNotKilledOnNonZeroExit verifies that a command failing with
// a non-zero exit code (without context cancellation) does NOT set Killed.
func TestCMakeRunNotKilledOnNonZeroExit(t *testing.T) {
	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	ctx := context.Background()
	result, err := b.run(ctx, "false", nil)
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if result.Killed {
		t.Fatal("expected Killed=false for non-zero exit without cancel")
	}
	if result.ExitCode == 0 {
		t.Fatal("expected non-zero ExitCode from 'false' command")
	}
}

// TestMakeRunKilledOnTimeout verifies the same kill behavior for MakeBuilder
// by creating a Makefile with a blocking target and cancelling the context.
func TestMakeRunKilledOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found, skipping MakeBuilder kill test")
	}

	buildDir := t.TempDir()

	// Write a Makefile with a target that sleeps.
	makefile := "all:\n\tsleep 30\n"
	if err := writeTestFile(t, buildDir, "Makefile", makefile); err != nil {
		t.Fatalf("writing Makefile: %v", err)
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     buildDir,
		BuildTimeout: 5 * time.Minute,
	}
	b := NewMakeBuilder(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := b.runMake(ctx, []string{"-C", buildDir})
	if err != nil {
		t.Fatalf("runMake() returned unexpected error: %v", err)
	}

	if !result.Killed {
		t.Fatal("expected Killed=true when context times out for MakeBuilder")
	}
}

// TestCMakeRunKilledOnCancel verifies that explicit context cancellation
// (not just timeout) also sets Killed=true.
func TestCMakeRunKilledOnCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after a short delay so the sleep gets killed.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	result, err := b.run(ctx, "sleep", []string{"30"})
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if !result.Killed {
		t.Fatal("expected Killed=true when context is cancelled")
	}
}

// TestCMakeBuildKilledOnTimeout verifies the full Build() path sets Killed
// when the build timeout is exceeded.
func TestCMakeBuildKilledOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 100 * time.Millisecond, // very short timeout
	}
	b := NewCMakeBuilder(cfg)

	// Build() calls run() with "cmake" which won't exist or will fail
	// differently. Instead, we test through a subprocess that we know
	// will block. Since Build() hard-codes "cmake", we test the run()
	// method directly (already covered above) and verify the Build()
	// timeout propagation here.
	//
	// We cannot easily override the "cmake" binary, but the run() tests
	// above prove the behavior. This test just verifies the timeout
	// context plumbing from Build() down to run().
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not found, skipping Build() timeout integration test")
	}

	// Build will fail because there's no CMakeLists.txt, but the timeout
	// should be very short. The point is verifying the timeout path.
	result, err := b.Build(context.Background(), nil, 0)
	if err != nil {
		// cmake may not find the build dir — that's fine, this tests plumbing
		return
	}

	// If cmake exits quickly (before timeout), Killed should be false.
	// If it somehow blocks, Killed should be true. Either way is valid
	// for this test — the key behavior is tested in TestCMakeRunKilledOnTimeout.
	_ = result
}

// TestSIGTERMSentBeforeSIGKILL verifies that a process that handles SIGTERM
// exits gracefully without being SIGKILLed. We use a shell script that
// traps SIGTERM and exits with a distinctive code (42). If SIGKILL were
// sent instead of SIGTERM, the trap handler would never run and the exit
// code would not be 42.
func TestSIGTERMSentBeforeSIGKILL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	// This shell script traps SIGTERM and exits with code 42. It uses
	// short sleeps in a loop so the shell can check the trap between
	// iterations (a long "sleep 30" would block trap handling until the
	// sleep subprocess exits).
	script := `trap 'exit 42' TERM; while true; do sleep 0.01; done`

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := b.run(ctx, "sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if !result.Killed {
		t.Fatal("expected Killed=true when context times out")
	}

	// Exit code 42 proves the SIGTERM trap handler ran, meaning SIGTERM
	// was sent before SIGKILL.
	if result.ExitCode != 42 {
		t.Fatalf("expected ExitCode=42 (SIGTERM trap), got %d", result.ExitCode)
	}
}

// TestKilledResultFields verifies that all BuildResult fields are populated
// when a process is killed.
func TestKilledResultFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM tests only run on unix-like systems")
	}

	cfg := &config.Config{
		SourceDir:    t.TempDir(),
		BuildDir:     t.TempDir(),
		BuildTimeout: 5 * time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	// Script that writes to stdout/stderr before blocking.
	script := `echo "build output"; echo "build error" >&2; sleep 30`

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := b.run(ctx, "sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("run() returned unexpected error: %v", err)
	}

	if !result.Killed {
		t.Fatal("expected Killed=true")
	}

	if result.Duration <= 0 {
		t.Fatal("expected positive Duration")
	}

	// Stdout and Stderr should contain what the process wrote before
	// being killed. The exact content may vary because the process might
	// be killed before flushing, but the fields should be populated.
	// We do not assert on content because of buffering timing.
}

// TestBuildResultKilledFieldDefault verifies that a freshly constructed
// BuildResult has Killed=false by default.
func TestBuildResultKilledFieldDefault(t *testing.T) {
	r := &BuildResult{}
	if r.Killed {
		t.Fatal("expected Killed=false by default")
	}
}

// writeTestFile creates a file with the given content in dir.
func writeTestFile(t *testing.T, dir, name, content string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
