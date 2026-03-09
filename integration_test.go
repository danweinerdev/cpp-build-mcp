package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
)

type progressEvent struct {
	current int
	total   int
	message string
}

type toolchainCase struct {
	name      string // subtest name, e.g. "clang" or "gcc"
	toolchain string // config.Config Toolchain value: "clang" or "gcc"
	compiler  string // full path from exec.LookPath
}

// requireCMake skips the test if running in short mode or if cmake is not
// available on PATH.
func requireCMake(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		t.Skip("cmake not found")
	}
}

// requireNinja skips the test if running in short mode or if ninja is not
// available on PATH.
func requireNinja(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("ninja"); err != nil {
		t.Skip("ninja not found")
	}
}

// requireCompiler skips the test if the named compiler is not on PATH and
// returns its full path. Does not check testing.Short() — the caller's
// requireCMake already handles that.
func requireCompiler(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found", name)
	}
	return path
}

// requireCMakeMinVersion skips the test if the installed cmake version is
// below major.minor.
func requireCMakeMinVersion(t *testing.T, major, minor int) {
	t.Helper()
	out, err := exec.Command("cmake", "--version").Output()
	if err != nil {
		t.Skipf("cmake --version failed: %v", err)
	}
	firstLine := strings.SplitN(string(out), "\n", 2)[0]
	// Expected format: "cmake version 3.28.1"
	versionStr := strings.TrimPrefix(firstLine, "cmake version ")
	if versionStr == firstLine {
		t.Skipf("cmake %d.%d+ required, found %s", major, minor, firstLine)
	}
	parts := strings.SplitN(versionStr, ".", 3)
	if len(parts) < 2 {
		t.Skipf("cmake %d.%d+ required, found %s", major, minor, versionStr)
	}
	gotMajor, err1 := strconv.Atoi(parts[0])
	gotMinor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		t.Skipf("cmake %d.%d+ required, found %s", major, minor, versionStr)
	}
	if gotMajor < major || (gotMajor == major && gotMinor < minor) {
		t.Skipf("cmake %d.%d+ required, found %s", major, minor, versionStr)
	}
}

// toolchainCases returns a slice of toolchainCase for each C++ compiler found
// on PATH (clang++ and/or g++). Skips the test if neither is found.
func toolchainCases(t *testing.T) []toolchainCase {
	t.Helper()
	var cases []toolchainCase
	if path, err := exec.LookPath("clang++"); err == nil {
		cases = append(cases, toolchainCase{
			name:      "clang",
			toolchain: "clang",
			compiler:  path,
		})
	}
	if path, err := exec.LookPath("g++"); err == nil {
		cases = append(cases, toolchainCase{
			name:      "gcc",
			toolchain: "gcc",
			compiler:  path,
		})
	}
	if len(cases) == 0 {
		t.Skip("no C++ compiler found (need clang++ or g++)")
	}
	return cases
}

// detectToolchain returns the first available toolchainCase, preferring clang
// over gcc. Skips the test if neither is found.
func detectToolchain(t *testing.T) toolchainCase {
	t.Helper()
	if path, err := exec.LookPath("clang++"); err == nil {
		return toolchainCase{name: "clang", toolchain: "clang", compiler: path}
	}
	if path, err := exec.LookPath("g++"); err == nil {
		return toolchainCase{name: "gcc", toolchain: "gcc", compiler: path}
	}
	t.Skip("no C++ compiler found (need clang++ or g++)")
	return toolchainCase{} // unreachable; satisfies compiler
}

// copyFixture copies the fixture directory testdata/<fixtureName> into a
// temporary directory and returns the temp dir path. The fixture contents
// are copied directly into TempDir (not into a subdirectory).
func copyFixture(t *testing.T, fixtureName string) string {
	t.Helper()
	src := filepath.Join("testdata", fixtureName)
	destDir := t.TempDir()

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(destDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyFixture(%q): %v", fixtureName, err)
	}
	return destDir
}

// assertDiagnosticFound asserts that at least one diagnostic matches the given
// file suffix and severity with a positive line number.
func assertDiagnosticFound(t *testing.T, diags []diagnostics.Diagnostic, fileSuffix string, severity diagnostics.Severity) {
	t.Helper()
	for _, d := range diags {
		if strings.HasSuffix(d.File, fileSuffix) && d.Severity == severity && d.Line > 0 {
			return
		}
	}
	t.Errorf("no diagnostic found matching file=%q severity=%q", fileSuffix, severity)
	for _, d := range diags {
		t.Logf("  diagnostic: file=%s line=%d severity=%s message=%s", d.File, d.Line, d.Severity, d.Message)
	}
}

// deriveCCompiler derives the C compiler path from a C++ compiler path.
// It operates on the basename to avoid replacing path components, then verifies
// the result exists via exec.LookPath. Skips the test if not found.
func deriveCCompiler(t *testing.T, cxxPath, toolchain string) string {
	t.Helper()
	base := filepath.Base(cxxPath)
	var cName string
	switch toolchain {
	case "gcc":
		cName = strings.Replace(base, "g++", "gcc", 1)
	default:
		cName = strings.Replace(base, "++", "", 1)
	}
	cPath, err := exec.LookPath(cName)
	if err != nil {
		t.Skipf("derived C compiler %s not found (from %s)", cName, cxxPath)
	}
	return cPath
}

// collectProgress returns a ProgressFunc that records each progress event and a
// pointer to the collected slice. Each call creates independent state — callers
// get their own slice with no shared globals between subtests.
func collectProgress(t *testing.T) (builder.ProgressFunc, *[]progressEvent) {
	t.Helper()
	events := &[]progressEvent{}
	fn := func(current, total int, message string) {
		*events = append(*events, progressEvent{
			current: current,
			total:   total,
			message: message,
		})
	}
	return fn, events
}

func TestIntegrationSmoke(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: false,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			configureOK := false

			t.Run("configure", func(t *testing.T) {
				result, err := b.Configure(ctx, nil)
				if err != nil {
					t.Fatalf("Configure returned error: %v", err)
				}
				if result.ExitCode != 0 {
					t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
				}
				if result.Duration <= 0 {
					t.Errorf("Configure duration should be > 0, got %v", result.Duration)
				}
				configureOK = true
			})

			t.Run("build", func(t *testing.T) {
				if !configureOK {
					t.Skip("skipping: configure failed")
				}
				result, err := b.Build(ctx, nil, 0)
				if err != nil {
					t.Fatalf("Build returned error: %v", err)
				}
				if result.ExitCode != 0 {
					t.Fatalf("Build exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
				}
				if _, err := os.Stat(filepath.Join(buildDir, "compile_commands.json")); err != nil {
					t.Errorf("compile_commands.json not found: %v", err)
				}
			})

			t.Run("clean", func(t *testing.T) {
				if !configureOK {
					t.Skip("skipping: configure failed")
				}
				result, err := b.Clean(ctx, nil)
				if err != nil {
					t.Fatalf("Clean returned error: %v", err)
				}
				if result.ExitCode != 0 {
					t.Fatalf("Clean exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
				}
			})
		})
	}
}

func TestIntegrationDiagnosticInjection(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation (e.g. clang
			// produces sarif, gcc produces json).
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure with diagnostic injection enabled.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Assert DiagnosticFormat.cmake exists.
			moduleFile := filepath.Join(buildDir, ".cpp-build-mcp", "DiagnosticFormat.cmake")
			if _, err := os.Stat(moduleFile); err != nil {
				t.Fatalf("DiagnosticFormat.cmake not found at %s", moduleFile)
			}

			// Assert configure output contains diagnostic format message.
			// CMake message(STATUS ...) goes to stdout; errors go to stderr.
			// Check both to be resilient to CMake version differences.
			configureOutput := result.Stdout + result.Stderr
			if !strings.Contains(configureOutput, "[cpp-build-mcp] Diagnostic format:") {
				t.Fatalf("configure output missing diagnostic format message")
			}
			t.Logf("configure stdout:\n%s", result.Stdout)
			t.Logf("configure stderr:\n%s", result.Stderr)

			// Assert format type per toolchain.
			switch tc.toolchain {
			case "gcc":
				if !strings.Contains(configureOutput, "json") {
					t.Errorf("GCC diagnostic format should be json, output:\n%s", configureOutput)
				}
			case "clang":
				if !strings.Contains(configureOutput, "sarif") {
					t.Errorf("Clang diagnostic format should be sarif, output:\n%s", configureOutput)
				}
			}

			// Build after injection — proves injected flags don't break compilation.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Build exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}
		})
	}
}

func TestIntegrationErrorDiagnostics(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-error")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should fail due to undeclared variable.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("Build should have failed but exit code was 0")
			}

			// Parse diagnostics.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) == 0 {
				t.Fatalf("expected at least one diagnostic, got none")
			}

			for _, d := range diags {
				t.Logf("diagnostic: file=%s line=%d col=%d severity=%s message=%s", d.File, d.Line, d.Column, d.Severity, d.Message)
			}

			assertDiagnosticFound(t, diags, "main.cpp", diagnostics.SeverityError)
		})
	}
}

func TestIntegrationWarningDiagnostics(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-warning")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should succeed (warnings don't fail the build).
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Build exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Parse diagnostics.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) == 0 {
				t.Fatalf("expected at least one diagnostic, got none")
			}

			for _, d := range diags {
				t.Logf("diagnostic: file=%s line=%d col=%d severity=%s message=%s", d.File, d.Line, d.Column, d.Severity, d.Message)
			}

			assertDiagnosticFound(t, diags, "main.cpp", diagnostics.SeverityWarning)
		})
	}
}

func TestIntegrationMultiError(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-multi-error")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				DiagnosticSerialBuild: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should fail due to undeclared variables in both files.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("Build should have failed but exit code was 0")
			}

			// Parse diagnostics.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) < 2 {
				t.Fatalf("expected at least 2 diagnostics, got %d", len(diags))
			}

			for _, d := range diags {
				t.Logf("diagnostic: file=%s line=%d col=%d severity=%s message=%s", d.File, d.Line, d.Column, d.Severity, d.Message)
			}

			assertDiagnosticFound(t, diags, "a.cpp", diagnostics.SeverityError)
			assertDiagnosticFound(t, diags, "b.cpp", diagnostics.SeverityError)
		})
	}
}

func TestIntegrationMixedDiagnostics(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-mixed-diagnostics")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				DiagnosticSerialBuild: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should fail due to undeclared variable in bad.cpp.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("Build should have failed but exit code was 0")
			}

			// Parse diagnostics.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) < 2 {
				t.Fatalf("expected at least 2 diagnostics, got %d", len(diags))
			}

			for _, d := range diags {
				t.Logf("diagnostic: file=%s line=%d col=%d severity=%s message=%s", d.File, d.Line, d.Column, d.Severity, d.Message)
			}

			assertDiagnosticFound(t, diags, "bad.cpp", diagnostics.SeverityError)
			assertDiagnosticFound(t, diags, "good.cpp", diagnostics.SeverityWarning)
		})
	}
}

func TestIntegrationNoteDiagnostics(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-note")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should fail due to overload resolution failure.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("Build should have failed but exit code was 0")
			}

			// Parse diagnostics.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) == 0 {
				t.Fatalf("expected at least one diagnostic, got none")
			}

			for _, d := range diags {
				t.Logf("diagnostic: file=%s line=%d col=%d severity=%s message=%s", d.File, d.Line, d.Column, d.Severity, d.Message)
			}

			// Check if any diagnostic has note severity — do NOT hard-fail
			// if no note found since not all compilers emit notes for this pattern.
			hasNote := false
			for _, d := range diags {
				if d.Severity == diagnostics.SeverityNote {
					hasNote = true
					break
				}
			}
			if hasNote {
				t.Logf("note-level diagnostic found (compiler emits notes)")
			} else {
				t.Logf("no note-level diagnostic found (compiler may not emit notes for this pattern)")
			}
		})
	}
}

func TestIntegrationLinkerError(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-linker-error")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: true,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Force CMake to use the specific compiler so the diagnostic
			// format detection matches the toolchain expectation.
			cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
			extraArgs := []string{
				"-DCMAKE_CXX_COMPILER=" + tc.compiler,
				"-DCMAKE_C_COMPILER=" + cCompiler,
			}

			// Configure — should succeed.
			result, err := b.Configure(ctx, extraArgs)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Build — should fail due to linker error (undefined_function).
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode == 0 {
				t.Fatalf("Build should have failed but exit code was 0")
			}

			t.Logf("stdout:\n%s", result.Stdout)
			t.Logf("stderr:\n%s", result.Stderr)

			// Parse diagnostics — linker errors are NOT structured compiler
			// diagnostics, so we expect none.
			parser := diagnostics.NewParser(tc.toolchain)
			diags, err := parser.Parse(result.Stdout, result.Stderr)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if len(diags) > 0 {
				t.Errorf("expected no structured diagnostics from linker error, got %d", len(diags))
				for _, d := range diags {
					t.Logf("  unexpected diagnostic: file=%s severity=%s message=%s", d.File, d.Severity, d.Message)
				}
			}

			// The linker error text mentioning "undefined_function" should
			// appear in the raw output.
			if !strings.Contains(result.Stderr, "undefined_function") {
				// Also check stdout — some linker configurations write to stdout.
				if !strings.Contains(result.Stdout, "undefined_function") {
					t.Errorf("expected linker error mentioning 'undefined_function' in output")
					t.Logf("stdout:\n%s", result.Stdout)
					t.Logf("stderr:\n%s", result.Stderr)
				}
			}
		})
	}
}

func TestIntegrationConfigureError(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	srcDir := copyFixture(t, "cmake-configure-error")
	buildDir := filepath.Join(srcDir, "build")

	cfg := &config.Config{
		SourceDir:             srcDir,
		BuildDir:              buildDir,
		Toolchain:             "auto",
		Generator:             "ninja",
		InjectDiagnosticFlags: false,
		BuildTimeout:          2 * time.Minute,
	}
	b := builder.NewCMakeBuilder(cfg)
	ctx := context.Background()

	// Configure — should fail due to FATAL_ERROR in CMakeLists.txt.
	result, err := b.Configure(ctx, nil)
	if err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("Configure should have failed but exit code was 0")
	}

	t.Logf("stdout:\n%s", result.Stdout)
	t.Logf("stderr:\n%s", result.Stderr)

	// Assert stderr contains the intentional failure message.
	if !strings.Contains(result.Stderr, "intentional configure failure") {
		t.Errorf("configure stderr should contain 'intentional configure failure'")
		t.Logf("stderr:\n%s", result.Stderr)
	}
}

func TestIntegrationProgress(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	for _, tc := range toolchainCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := copyFixture(t, "cmake-library")
			buildDir := filepath.Join(srcDir, "build")

			cfg := &config.Config{
				SourceDir:             srcDir,
				BuildDir:              buildDir,
				Toolchain:             tc.toolchain,
				Generator:             "ninja",
				InjectDiagnosticFlags: false,
				BuildTimeout:          2 * time.Minute,
			}
			b := builder.NewCMakeBuilder(cfg)
			ctx := context.Background()

			// Configure — should succeed.
			result, err := b.Configure(ctx, nil)
			if err != nil {
				t.Fatalf("Configure returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Set up progress collection.
			fn, events := collectProgress(t)
			b.SetProgressFunc(fn)

			// Build — should succeed and produce progress events.
			result, err = b.Build(ctx, nil, 0)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("Build exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
			}

			// Log all progress events for debugging.
			for i, ev := range *events {
				t.Logf("progress[%d]: current=%d total=%d message=%s", i, ev.current, ev.total, ev.message)
			}

			// Assert progress events were received.
			if len(*events) == 0 {
				t.Fatal("expected at least one progress callback, got none")
			}

			// Assert final callback has current == total.
			last := (*events)[len(*events)-1]
			if last.current != last.total {
				t.Errorf("final progress: current=%d total=%d, expected current == total", last.current, last.total)
			}

			// Assert total > 1 (multiple TUs = multiple progress lines).
			if last.total <= 1 {
				t.Errorf("expected total > 1 (multiple TUs), got total=%d", last.total)
			}
		})
	}
}

func TestIntegrationPresets(t *testing.T) {
	requireCMake(t)
	requireNinja(t)
	requireCMakeMinVersion(t, 3, 21)

	tc := detectToolchain(t)
	srcDir := copyFixture(t, "cmake-presets")
	buildDir := filepath.Join(srcDir, "build", "integration-test")

	cfg := &config.Config{
		SourceDir:             srcDir,
		BuildDir:              buildDir,
		Toolchain:             tc.toolchain,
		Generator:             "ninja",
		Preset:                "integration-test",
		InjectDiagnosticFlags: true,
		BuildTimeout:          2 * time.Minute,
	}
	b := builder.NewCMakeBuilder(cfg)
	ctx := context.Background()

	// Force CMake to use the specific compiler so the diagnostic
	// format detection matches the toolchain expectation.
	cCompiler := deriveCCompiler(t, tc.compiler, tc.toolchain)
	extraArgs := []string{
		"-DCMAKE_CXX_COMPILER=" + tc.compiler,
		"-DCMAKE_C_COMPILER=" + cCompiler,
		"-S", srcDir,
	}

	// Configure — should succeed.
	result, err := b.Configure(ctx, extraArgs)
	if err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Configure exit code %d, stdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	// Assert DiagnosticFormat.cmake exists at the nested build dir.
	moduleFile := filepath.Join(buildDir, ".cpp-build-mcp", "DiagnosticFormat.cmake")
	if _, err := os.Stat(moduleFile); err != nil {
		t.Fatalf("DiagnosticFormat.cmake not found at %s — MkdirAll or absolute path injection failed", moduleFile)
	}

	// Build — should succeed.
	result, err = b.Build(ctx, nil, 0)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Build exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
	}
}

func TestIntegrationTargetBuild(t *testing.T) {
	requireCMake(t)
	requireNinja(t)

	tc := detectToolchain(t)
	srcDir := copyFixture(t, "cmake-library")
	buildDir := filepath.Join(srcDir, "build")

	cfg := &config.Config{
		SourceDir:             srcDir,
		BuildDir:              buildDir,
		Toolchain:             tc.toolchain,
		Generator:             "ninja",
		InjectDiagnosticFlags: false,
		BuildTimeout:          2 * time.Minute,
	}
	b := builder.NewCMakeBuilder(cfg)
	ctx := context.Background()

	// Configure — should succeed.
	result, err := b.Configure(ctx, nil)
	if err != nil {
		t.Fatalf("Configure returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Configure exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
	}

	// Build target "lib".
	result, err = b.Build(ctx, []string{"lib"}, 0)
	if err != nil {
		t.Fatalf("Build target 'lib' returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Build target 'lib' exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
	}

	// Build target "main".
	result, err = b.Build(ctx, []string{"main"}, 0)
	if err != nil {
		t.Fatalf("Build target 'main' returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Build target 'main' exit code %d, stderr:\n%s", result.ExitCode, result.Stderr)
	}
}

