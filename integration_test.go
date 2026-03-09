package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danweinerdev/cpp-build-mcp/builder"
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

// collectProgress returns a ProgressFunc that records each progress event and
// a pointer to the collected slice. Each call creates independent state.
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

