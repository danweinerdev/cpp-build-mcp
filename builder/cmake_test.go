package builder

import (
	"context"
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
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "-S", "src")
		assertContainsSequence(t, args, "-B", "build")
		assertContainsSequence(t, args, "-G", "Ninja")
		assertContains(t, args, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")

		// auto toolchain should NOT inject diagnostic flags
		assertNotContains(t, args, "-DCMAKE_C_FLAGS=-fdiagnostics-format=json")
		assertNotContains(t, args, "-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json")
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

	t.Run("clang toolchain injects diagnostic flags", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "clang",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertContains(t, args, "-DCMAKE_C_FLAGS=-fdiagnostics-format=json")
		assertContains(t, args, "-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json")
	})

	t.Run("clang toolchain with inject disabled does not inject", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "clang",
			InjectDiagnosticFlags: false,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertNotContains(t, args, "-DCMAKE_C_FLAGS=-fdiagnostics-format=json")
		assertNotContains(t, args, "-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json")
	})

	t.Run("gcc toolchain does not inject diagnostic flags", func(t *testing.T) {
		cfg := &config.Config{
			SourceDir:             ".",
			BuildDir:              "out",
			Toolchain:             "gcc",
			InjectDiagnosticFlags: true,
		}
		b := NewCMakeBuilder(cfg)
		args := b.buildConfigureArgs(nil)

		assertNotContains(t, args, "-DCMAKE_C_FLAGS=-fdiagnostics-format=json")
		assertNotContains(t, args, "-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json")
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
		args := b.buildConfigureArgs(nil)

		assertContainsSequence(t, args, "--preset", "debug")
		assertContains(t, args, "-DCMAKE_C_FLAGS=-fdiagnostics-format=json")
		assertContains(t, args, "-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json")

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
