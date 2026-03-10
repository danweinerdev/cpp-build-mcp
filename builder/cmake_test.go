package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
)

// ---------------------------------------------------------------------------
// Unit tests — no cmake binary required
// ---------------------------------------------------------------------------

func TestNewBuilderFactory(t *testing.T) {
	t.Run("ninja returns CMakeBuilder", func(t *testing.T) {
		cfg := &config.Config{Generator: "ninja"}
		b, err := NewBuilder(cfg)
		if err != nil {
			t.Fatalf("NewBuilder() returned error: %v", err)
		}
		if _, ok := b.(*CMakeBuilder); !ok {
			t.Fatalf("expected *CMakeBuilder, got %T", b)
		}
	})

	t.Run("empty generator returns CMakeBuilder", func(t *testing.T) {
		cfg := &config.Config{Generator: ""}
		b, err := NewBuilder(cfg)
		if err != nil {
			t.Fatalf("NewBuilder() returned error: %v", err)
		}
		if _, ok := b.(*CMakeBuilder); !ok {
			t.Fatalf("expected *CMakeBuilder, got %T", b)
		}
	})

	t.Run("make returns MakeBuilder", func(t *testing.T) {
		cfg := &config.Config{Generator: "make"}
		b, err := NewBuilder(cfg)
		if err != nil {
			t.Fatalf("NewBuilder() returned error: %v", err)
		}
		if _, ok := b.(*MakeBuilder); !ok {
			t.Fatalf("expected *MakeBuilder, got %T", b)
		}
	})

	t.Run("unknown generator returns error", func(t *testing.T) {
		cfg := &config.Config{Generator: "bazel"}
		_, err := NewBuilder(cfg)
		if err == nil {
			t.Fatal("expected error for unknown generator")
		}
		if !strings.Contains(err.Error(), "unsupported generator") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})
}

func TestBuildConfigureArgs(t *testing.T) {
	t.Run("basic configure args", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             "src",
			BuildDir:              "build",
			Generator:             "ninja",
			Toolchain:             "auto",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		b.moduleWritten = true
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "-S", "src")
		assertContainsSequence(t, args, "-B", "build")
		assertContainsSequence(t, args, "-G", "Ninja")
		assertContains(t, args, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")

		// InjectDiagnosticFlags=true → CMAKE_PROJECT_INCLUDE pointing to the module (absolute path)
		assertContainsDiagModuleArg(t, args, "build")
	})

	t.Run("make generator produces Unix Makefiles", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir: "src",
			BuildDir:  "build",
			Generator: "make",
			Toolchain: "auto",
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "-G", "Unix Makefiles")
	})

	t.Run("empty generator defaults to Ninja", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir: "src",
			BuildDir:  "build",
			Generator: "",
			Toolchain: "auto",
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "-G", "Ninja")
	})

	t.Run("inject enabled includes diagnostic module", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "clang",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		b.moduleWritten = true
		args := b.buildConfigureArgs(nil)

		assertContainsDiagModuleArg(t, args, "out")
		// Hardcoded flags are no longer injected — CMake module probes the compiler.
		assertNotContainsPrefix(t, args, "-DCMAKE_C_FLAGS=")
		assertNotContainsPrefix(t, args, "-DCMAKE_CXX_FLAGS=")
	})

	t.Run("inject disabled does not include diagnostic module", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "clang",
			InjectDiagnosticFlags: false,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertNotContainsPrefix(t, args, "-DCMAKE_PROJECT_INCLUDE=")
	})

	t.Run("gcc toolchain with inject enabled also includes module", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "gcc",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		b.moduleWritten = true
		args := b.buildConfigureArgs(nil)

		// CMake module is now toolchain-agnostic — injected for all toolchains.
		assertContainsDiagModuleArg(t, args, "out")
	})

	t.Run("preset mode args", func(t *testing.T) {
		cfg := &config.Config{
			Preset:                "debug",
			SourceDir:             "src",
			BuildDir:              "out/debug",
			Generator:             "ninja",
			Toolchain:             "auto",
			InjectDiagnosticFlags: false,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "--preset", "debug")
		assertContains(t, args, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")

		// Preset mode must NOT contain -S, -B, or -G
		assertNotContains(t, args, "-S")
		assertNotContains(t, args, "-B")
		assertNotContains(t, args, "-G")
	})

	t.Run("non-preset mode regression", func(t *testing.T) {
		cfg := &config.Config{
			Preset:                "",
			SourceDir:             "src",
			BuildDir:              "build",
			Generator:             "ninja",
			Toolchain:             "auto",
			InjectDiagnosticFlags: false,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "-S", "src")
		assertContainsSequence(t, args, "-B", "build")
		assertContainsSequence(t, args, "-G", "Ninja")
		assertContains(t, args, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")

		// Non-preset mode must NOT contain --preset
		assertNotContains(t, args, "--preset")
	})

	t.Run("preset mode with diagnostic flags", func(t *testing.T) {
		cfg := &config.Config{
			Preset:                "debug",
			BuildDir:              "out/debug",
			Toolchain:             "clang",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		b.moduleWritten = true
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "--preset", "debug")
		assertContainsDiagModuleArg(t, args, "out/debug")

		// Must NOT contain -S, -B, or -G
		assertNotContains(t, args, "-S")
		assertNotContains(t, args, "-B")
		assertNotContains(t, args, "-G")
	})

	t.Run("preset mode with CMakeArgs", func(t *testing.T) {
		cfg := &config.Config{
			Preset:                "debug",
			BuildDir:              "out/debug",
			Toolchain:             "auto",
			InjectDiagnosticFlags: false,
			CMakeArgs:             []string{"-DFOO=bar"},
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "--preset", "debug")
		assertContains(t, args, "-DFOO=bar")

		// --preset should come before -DFOO=bar
		presetIdx := indexOf(args, "--preset")
		fooIdx := indexOf(args, "-DFOO=bar")
		if presetIdx >= fooIdx {
			t.Fatalf("--preset (index %d) should appear before -DFOO=bar (index %d)", presetIdx, fooIdx)
		}
	})

	t.Run("preset mode with extraArgs", func(t *testing.T) {
		cfg := &config.Config{
			Preset:                "debug",
			BuildDir:              "out/debug",
			Toolchain:             "auto",
			InjectDiagnosticFlags: false,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs([]string{"--extra"})

		assertContainsSequence(t, args, "--preset", "debug")
		assertContains(t, args, "--extra")

		// --extra should come after --preset
		presetIdx := indexOf(args, "--preset")
		extraIdx := indexOf(args, "--extra")
		if presetIdx >= extraIdx {
			t.Fatalf("--preset (index %d) should appear before --extra (index %d)", presetIdx, extraIdx)
		}
	})

	t.Run("cmake args and extra args are appended", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "build",
			Toolchain:             "auto",
			InjectDiagnosticFlags: false,
			CMakeArgs:             []string{"-DBUILD_TESTS=ON"},
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs([]string{"-DEXTRA=1"})

		assertContains(t, args, "-DBUILD_TESTS=ON")
		assertContains(t, args, "-DEXTRA=1")

		// CMakeArgs should come before extraArgs
		cmakeIdx := indexOf(args, "-DBUILD_TESTS=ON")
		extraIdx := indexOf(args, "-DEXTRA=1")
		if cmakeIdx >= extraIdx {
			t.Fatalf("CMakeArgs (index %d) should appear before extraArgs (index %d)", cmakeIdx, extraIdx)
		}
	})
}

func TestBuildBuildArgs(t *testing.T) {
	t.Run("basic build args", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 0)

		assertContainsSequence(t, args, "--build", "build")
		assertContains(t, args, "--")
		assertNotContains(t, args, "--clean-first")
	})

	t.Run("with targets", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildBuildArgs([]string{"app", "lib"}, 0)

		assertContainsSequence(t, args, "--target", "app")
		assertContainsSequence(t, args, "--target", "lib")
	})

	t.Run("with jobs", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 4)

		assertContains(t, args, "-j4")
	})

	t.Run("diagnostic serial build forces j1", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:              "build",
			BuildTimeout:          5 * time.Minute,
			DiagnosticSerialBuild: true,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 8)

		assertContains(t, args, "-j1")
		assertNotContains(t, args, "-j8")
		assertContains(t, args, "-k")
		assertContains(t, args, "0")
	})

	t.Run("dirty flag adds clean-first and clears", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewCMakeBuilder(cfg)
		b.SetDirty(true)

		args := b.buildBuildArgs(nil, 0)
		assertContains(t, args, "--clean-first")

		if b.dirty {
			t.Fatal("expected dirty flag to be cleared after buildBuildArgs")
		}

		// Second call should not have --clean-first
		args2 := b.buildBuildArgs(nil, 0)
		assertNotContains(t, args2, "--clean-first")
	})

	t.Run("separator comes before jobs", func(t *testing.T) {
		cfg := &config.Config{
			BuildDir:     "build",
			BuildTimeout: 5 * time.Minute,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildBuildArgs(nil, 4)

		sepIdx := indexOf(args, "--")
		jobIdx := indexOf(args, "-j4")
		if sepIdx < 0 {
			t.Fatal("expected -- separator in args")
		}
		if jobIdx < 0 {
			t.Fatal("expected -j4 in args")
		}
		if sepIdx >= jobIdx {
			t.Fatalf("-- (index %d) should appear before -j4 (index %d)", sepIdx, jobIdx)
		}
	})
}

func TestBuildCleanArgs(t *testing.T) {
	cfg := &config.Config{BuildDir: "build"}
	b := NewCMakeBuilder(cfg)
	args := b.buildCleanArgs()

	assertContainsSequence(t, args, "--build", "build")
	assertContainsSequence(t, args, "--target", "clean")
}

func TestParseTargetList(t *testing.T) {
	t.Run("ninja format", func(t *testing.T) {
		input := "all\nclean\nhelp\nmyapp\nmylib\nedit_cache\nrebuild_cache\nCMakeFiles/myapp.dir/all\nCMakeFiles/mylib.dir/all\n"
		result := parseTargetList(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 targets, got %d: %v", len(result), result)
		}
		if result[0].Name != "myapp" {
			t.Errorf("expected first target 'myapp', got %q", result[0].Name)
		}
		if result[1].Name != "mylib" {
			t.Errorf("expected second target 'mylib', got %q", result[1].Name)
		}
	})

	t.Run("ninja format with phony suffix", func(t *testing.T) {
		input := "all: phony\nclean: phony\nmyapp: phony\nmylib: PHONY\n"
		result := parseTargetList(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 targets, got %d: %v", len(result), result)
		}
		if result[0].Name != "myapp" {
			t.Errorf("expected first target 'myapp', got %q", result[0].Name)
		}
		if result[1].Name != "mylib" {
			t.Errorf("expected second target 'mylib', got %q", result[1].Name)
		}
	})

	t.Run("makefile format", func(t *testing.T) {
		input := "The following are some of the valid targets for this Makefile:\n... all (the default if no target is provided)\n... clean\n... depend\n... rebuild_cache\n... edit_cache\n... myapp\n... mylib\n... myapp.o\n... mylib.o\n"
		result := parseTargetList(input)
		if len(result) != 2 {
			t.Fatalf("expected 2 targets, got %d: %v", len(result), result)
		}
		if result[0].Name != "myapp" {
			t.Errorf("expected first target 'myapp', got %q", result[0].Name)
		}
		if result[1].Name != "mylib" {
			t.Errorf("expected second target 'mylib', got %q", result[1].Name)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := parseTargetList("")
		if result == nil {
			t.Fatal("expected empty slice, got nil")
		}
		if len(result) != 0 {
			t.Fatalf("expected 0 targets, got %d: %v", len(result), result)
		}
	})

	t.Run("all internal targets filtered", func(t *testing.T) {
		input := "all\nclean\nhelp\ndepend\nedit_cache\nrebuild_cache\ninstall\ntest\n"
		result := parseTargetList(input)
		if result == nil {
			t.Fatal("expected empty slice, got nil")
		}
		if len(result) != 0 {
			t.Fatalf("expected 0 targets, got %d: %v", len(result), result)
		}
	})

	t.Run("targets with slash filtered", func(t *testing.T) {
		input := "myapp\nCMakeFiles/myapp.dir/all\ninstall/local\n"
		result := parseTargetList(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 target, got %d: %v", len(result), result)
		}
		if result[0].Name != "myapp" {
			t.Errorf("expected target 'myapp', got %q", result[0].Name)
		}
	})

	t.Run("object file targets filtered", func(t *testing.T) {
		input := "myapp\nmain.o\nutils.obj\n"
		result := parseTargetList(input)
		if len(result) != 1 {
			t.Fatalf("expected 1 target, got %d: %v", len(result), result)
		}
		if result[0].Name != "myapp" {
			t.Errorf("expected target 'myapp', got %q", result[0].Name)
		}
	})
}

// ---------------------------------------------------------------------------
// Progress notification tests — uses shell scripts, no cmake required
// ---------------------------------------------------------------------------

func TestRunWithProgressCallback(t *testing.T) {
	cfg := &config.Config{
		SourceDir:    ".",
		BuildDir:     "build",
		BuildTimeout: 5 * time.Minute,
	}

	t.Run("callback receives correct current and total", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0 // no throttle

		type progressEvent struct {
			current, total int
			message        string
		}
		var events []progressEvent

		b.SetProgressFunc(func(current, total int, message string) {
			events = append(events, progressEvent{current, total, message})
		})

		// Shell script writes Ninja-style progress to stdout (like real Ninja)
		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/3] Building CXX object a.cpp.o"
echo "[2/3] Building CXX object b.cpp.o"
echo "[3/3] Linking CXX executable main"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}

		if len(events) != 3 {
			t.Fatalf("expected 3 progress events, got %d", len(events))
		}

		// Verify each event
		for i, want := range []progressEvent{
			{1, 3, "[1/3] Building CXX object a.cpp.o"},
			{2, 3, "[2/3] Building CXX object b.cpp.o"},
			{3, 3, "[3/3] Linking CXX executable main"},
		} {
			if events[i].current != want.current || events[i].total != want.total {
				t.Errorf("event[%d]: got (%d, %d), want (%d, %d)", i, events[i].current, events[i].total, want.current, want.total)
			}
			if events[i].message != want.message {
				t.Errorf("event[%d]: got message %q, want %q", i, events[i].message, want.message)
			}
		}
	})

	t.Run("no callback when progressFunc is nil", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		// progressFunc is nil by default

		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/3] Building CXX object a.cpp.o"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
		// If we get here without panic, the nil path works.
		if !strings.Contains(result.Stdout, "[1/3]") {
			t.Errorf("expected stdout to contain [1/3], got %q", result.Stdout)
		}
	})

	t.Run("rate limiting reduces callback count", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 1 * time.Millisecond

		callCount := 0
		b.SetProgressFunc(func(current, total int, message string) {
			callCount++
		})

		// Generate 20 rapid progress lines with no delay between them
		script := ""
		for i := 1; i <= 20; i++ {
			script += fmt.Sprintf(`echo "[%d/20] Building file%d.cpp.o"`+"\n", i, i)
		}

		_, err := b.run(context.Background(), "sh", []string{"-c", script})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		// With 50ms throttle and rapid output, callback count should be less than 20
		// but at least 1 (the final [20/20] is always sent)
		if callCount >= 20 {
			t.Errorf("expected throttle to reduce callbacks below 20, got %d", callCount)
		}
		if callCount < 1 {
			t.Error("expected at least 1 callback (final line)")
		}
	})

	t.Run("final line always delivered", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 1 * time.Hour // extreme throttle

		var lastCurrent, lastTotal int
		b.SetProgressFunc(func(current, total int, message string) {
			lastCurrent = current
			lastTotal = total
		})

		_, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/5] Building a.cpp.o"
echo "[5/5] Linking main"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		// The first line [1/5] is always sent (first event, lastNotify is zero).
		// The final line [5/5] should always be sent despite the extreme throttle.
		if lastCurrent != 5 || lastTotal != 5 {
			t.Errorf("expected final event (5, 5), got (%d, %d)", lastCurrent, lastTotal)
		}
	})

	t.Run("malformed lines produce no callback", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0

		callCount := 0
		b.SetProgressFunc(func(current, total int, message string) {
			callCount++
		})

		_, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "some random output"
echo "[abc/def] not a number"
echo "-- Configuring done"
echo "[2/5] Valid line"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		// Only the "[2/5] Valid line" should trigger the callback
		if callCount != 1 {
			t.Errorf("expected 1 callback, got %d", callCount)
		}
	})

	t.Run("stdout integrity with progress enabled", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0

		b.SetProgressFunc(func(current, total int, message string) {
			// consume but don't care
		})

		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/3] Building a.cpp.o"
echo "some other output"
echo "[2/3] Building b.cpp.o"
echo "[3/3] Linking main"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		// BuildResult.Stdout must contain ALL lines, including [N/M] lines
		if !strings.Contains(result.Stdout, "[1/3] Building a.cpp.o") {
			t.Error("stdout missing [1/3] line")
		}
		if !strings.Contains(result.Stdout, "some other output") {
			t.Error("stdout missing other output line")
		}
		if !strings.Contains(result.Stdout, "[3/3] Linking main") {
			t.Error("stdout missing [3/3] line")
		}
	})

	t.Run("stderr is unaffected by progress", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0

		b.SetProgressFunc(func(current, total int, message string) {})

		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/1] Building"
echo "stderr content" >&2`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		if !strings.Contains(result.Stderr, "stderr content") {
			t.Errorf("expected stderr to contain 'stderr content', got %q", result.Stderr)
		}
	})

	t.Run("panic in callback does not deadlock", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0

		b.SetProgressFunc(func(current, total int, message string) {
			panic("test panic in progress callback")
		})

		// Script emits several lines — the callback panics on the first one,
		// but run() must still return normally with complete stdout.
		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/3] Building a.cpp.o"
echo "[2/3] Building b.cpp.o"
echo "[3/3] Linking main"`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
		// Stdout must still contain all output despite the panic
		if !strings.Contains(result.Stdout, "[1/3]") {
			t.Error("stdout missing [1/3] line after panic")
		}
		if !strings.Contains(result.Stdout, "[3/3]") {
			t.Error("stdout missing [3/3] line after panic")
		}
	})

	t.Run("non-zero exit code with progress", func(t *testing.T) {
		b := NewCMakeBuilder(cfg)
		b.progressMinInterval = 0

		var events []int
		b.SetProgressFunc(func(current, total int, message string) {
			events = append(events, current)
		})

		result, err := b.run(context.Background(), "sh", []string{"-c",
			`echo "[1/3] Building a.cpp.o"
echo "[2/3] Building b.cpp.o"
exit 1`})
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}

		if result.ExitCode != 1 {
			t.Errorf("expected exit code 1, got %d", result.ExitCode)
		}
		if len(events) != 2 {
			t.Errorf("expected 2 progress events before failure, got %d", len(events))
		}
	})
}

func TestSetProgressFunc(t *testing.T) {
	cfg := &config.Config{BuildTimeout: time.Minute}
	b := NewCMakeBuilder(cfg)

	// Default is nil
	if b.progressFunc != nil {
		t.Fatal("expected progressFunc to be nil by default")
	}

	// Set a callback
	b.SetProgressFunc(func(current, total int, message string) {})
	if b.progressFunc == nil {
		t.Fatal("expected progressFunc to be set")
	}

	// Clear it
	b.SetProgressFunc(nil)
	if b.progressFunc != nil {
		t.Fatal("expected progressFunc to be nil after clear")
	}
}

func TestProgressMinIntervalDefault(t *testing.T) {
	cfg := &config.Config{BuildTimeout: time.Minute}
	b := NewCMakeBuilder(cfg)
	if b.progressMinInterval != 250*time.Millisecond {
		t.Errorf("expected default progressMinInterval of 250ms, got %v", b.progressMinInterval)
	}
}

// ---------------------------------------------------------------------------
// ListTargets tests
// ---------------------------------------------------------------------------

func TestCMakeBuilderListTargetsArgs(t *testing.T) {
	// This test verifies that ListTargets constructs the correct
	// cmake command. We can't easily mock exec, so we use a non-existent
	// build directory to verify it doesn't panic and returns an error.
	cfg := &config.Config{
		BuildDir:     "/tmp/test-build-dir",
		BuildTimeout: time.Minute,
	}
	b := NewCMakeBuilder(cfg)

	// ListTargets will fail because the build dir doesn't exist
	// (cmake won't find it), but we can at least verify it doesn't panic
	// and returns an error.
	_, err := b.ListTargets(context.Background())
	if err == nil {
		t.Fatal("expected error for non-existent build dir")
	}
}

func TestCMakeBuilderListTargetsIntegration(t *testing.T) {
	// Skip if cmake/make not found.
	// We use "Unix Makefiles" rather than Ninja because CMake >= 3.31
	// changed Ninja's --target help output to omit user-defined targets.
	// The Makefile generator still lists all targets in its help output.
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not found")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not found")
	}

	// Create a temp project with a known target
	dir := t.TempDir()
	cmakeLists := `cmake_minimum_required(VERSION 3.10)
project(test_project C)
add_executable(test_app main.c)
`
	os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte(cmakeLists), 0o644)
	os.WriteFile(filepath.Join(dir, "main.c"), []byte("int main(void) { return 0; }\n"), 0o644)

	buildDir := filepath.Join(dir, "build")
	cfg := &config.Config{
		SourceDir:             dir,
		BuildDir:              buildDir,
		Generator:             "make",
		BuildTimeout:          2 * time.Minute,
		InjectDiagnosticFlags: false,
	}

	b := NewCMakeBuilder(cfg)
	ctx := context.Background()

	// Configure first
	result, err := b.Configure(ctx, nil)
	if err != nil {
		t.Fatalf("Configure failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Configure exit code %d: %s", result.ExitCode, result.Stderr)
	}

	// Now list targets
	targets, err := b.ListTargets(ctx)
	if err != nil {
		t.Fatalf("ListTargets failed: %v", err)
	}

	// Should contain test_app
	found := false
	for _, tgt := range targets {
		if tgt.Name == "test_app" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find 'test_app' in targets, got %v", targets)
	}

	// Should NOT contain internal targets
	for _, tgt := range targets {
		if tgt.Name == "clean" || tgt.Name == "all" || tgt.Name == "help" || tgt.Name == "edit_cache" || tgt.Name == "rebuild_cache" {
			t.Errorf("internal target %q should have been filtered", tgt.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration test — requires cmake and ninja installed
// ---------------------------------------------------------------------------

func TestCMakeBuilderIntegration(t *testing.T) {
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not found, skipping integration test")
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("ninja not found, skipping integration test")
	}

	dir := t.TempDir()
	srcDir := dir
	buildDir := filepath.Join(dir, "build")

	// Write a minimal CMakeLists.txt
	cmakeLists := `cmake_minimum_required(VERSION 3.10)
project(test_project C)
add_executable(test_app main.c)
`
	if err := os.WriteFile(filepath.Join(srcDir, "CMakeLists.txt"), []byte(cmakeLists), 0o644); err != nil {
		t.Fatalf("writing CMakeLists.txt: %v", err)
	}

	// Write a minimal main.c
	mainC := `int main(void) { return 0; }
`
	if err := os.WriteFile(filepath.Join(srcDir, "main.c"), []byte(mainC), 0o644); err != nil {
		t.Fatalf("writing main.c: %v", err)
	}

	cfg := &config.Config{
		SourceDir:             srcDir,
		BuildDir:              buildDir,
		Toolchain:             "auto",
		Generator:             "ninja",
		BuildTimeout:          2 * time.Minute,
		InjectDiagnosticFlags: false,
	}

	b := NewCMakeBuilder(cfg)
	ctx := context.Background()

	t.Run("configure", func(t *testing.T) {
		result, err := b.Configure(ctx, nil)
		if err != nil {
			t.Fatalf("Configure() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("Configure() exit code %d, stderr: %s", result.ExitCode, result.Stderr)
		}
		if result.Duration <= 0 {
			t.Fatal("expected positive duration")
		}
	})

	t.Run("build", func(t *testing.T) {
		result, err := b.Build(ctx, nil, 0)
		if err != nil {
			t.Fatalf("Build() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("Build() exit code %d, stderr: %s", result.ExitCode, result.Stderr)
		}
		if result.Duration <= 0 {
			t.Fatal("expected positive duration")
		}
	})

	t.Run("clean", func(t *testing.T) {
		result, err := b.Clean(ctx, nil)
		if err != nil {
			t.Fatalf("Clean() returned error: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("Clean() exit code %d, stderr: %s", result.ExitCode, result.Stderr)
		}
	})
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// assertContains checks that the slice contains the given value.
func assertContains(t *testing.T, slice []string, value string) {
	t.Helper()
	for _, s := range slice {
		if s == value {
			return
		}
	}
	t.Errorf("expected %v to contain %q", slice, value)
}

// assertNotContains checks that the slice does not contain the given value.
func assertNotContains(t *testing.T, slice []string, value string) {
	t.Helper()
	for _, s := range slice {
		if s == value {
			t.Errorf("expected %v to NOT contain %q", slice, value)
			return
		}
	}
}

// assertContainsSequence checks that value1 is immediately followed by value2
// somewhere in the slice.
func assertContainsSequence(t *testing.T, slice []string, value1, value2 string) {
	t.Helper()
	for i := 0; i < len(slice)-1; i++ {
		if slice[i] == value1 && slice[i+1] == value2 {
			return
		}
	}
	t.Errorf("expected %v to contain sequence [%q, %q]", slice, value1, value2)
}

// indexOf returns the index of value in slice, or -1 if not found.
func indexOf(slice []string, value string) int {
	for i, s := range slice {
		if s == value {
			return i
		}
	}
	return -1
}

// assertContainsDiagModuleArg checks that the args slice contains a
// -DCMAKE_PROJECT_INCLUDE= entry pointing to <buildDir>/.cpp-build-mcp/DiagnosticFormat.cmake.
// Since diagnosticModulePath now returns an absolute path, the expected path
// is resolved via filepath.Abs.
func assertContainsDiagModuleArg(t *testing.T, args []string, buildDir string) {
	t.Helper()
	rel := filepath.Join(buildDir, ".cpp-build-mcp", "DiagnosticFormat.cmake")
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}
	expected := "-DCMAKE_PROJECT_INCLUDE=" + abs
	for _, a := range args {
		if a == expected {
			return
		}
	}
	t.Errorf("expected args to contain %q, got %v", expected, args)
}

// assertNotContainsPrefix checks that no element in the slice starts with prefix.
func assertNotContainsPrefix(t *testing.T, slice []string, prefix string) {
	t.Helper()
	for _, s := range slice {
		if strings.HasPrefix(s, prefix) {
			t.Errorf("expected no element with prefix %q, found %q", prefix, s)
			return
		}
	}
}
