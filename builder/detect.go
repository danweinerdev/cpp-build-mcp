package builder

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// compileCommandEntry represents a single entry in compile_commands.json.
// It supports both the "command" string form and the "arguments" array form.
type compileCommandEntry struct {
	Directory string   `json:"directory"`
	Command   string   `json:"command"`
	Arguments []string `json:"arguments"`
	File      string   `json:"file"`
}

// DetectToolchain inspects the build directory to determine which compiler
// toolchain is in use. It checks compile_commands.json first, then falls back
// to probing the CC environment variable or cc on PATH.
//
// Return values: "clang", "gcc", "gcc-legacy", "msvc", or "unknown".
func DetectToolchain(buildDir string) string {
	tc := detectFromCompileCommands(buildDir)
	if tc != "" {
		return tc
	}

	tc = detectFromEnv()
	if tc != "" {
		return tc
	}

	return "unknown"
}

// detectFromCompileCommands reads compile_commands.json and extracts the
// compiler from the first entry. Returns "" if the file is missing, empty,
// or unparseable.
func detectFromCompileCommands(buildDir string) string {
	path := filepath.Join(buildDir, "compile_commands.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var entries []compileCommandEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Debug("failed to parse compile_commands.json for toolchain detection", "error", err)
		return ""
	}

	if len(entries) == 0 {
		return ""
	}

	compiler := extractCompiler(entries[0])
	if compiler == "" {
		return ""
	}

	return classifyCompiler(compiler)
}

// extractCompiler gets the compiler binary path from a compile command entry.
// It checks the arguments array first, then falls back to splitting the
// command string.
func extractCompiler(entry compileCommandEntry) string {
	if len(entry.Arguments) > 0 {
		return entry.Arguments[0]
	}

	if entry.Command != "" {
		parts := strings.Fields(entry.Command)
		if len(parts) > 0 {
			return parts[0]
		}
	}

	return ""
}

// classifyCompiler determines the toolchain from a compiler binary path.
func classifyCompiler(compiler string) string {
	// filepath.Base handles OS-native separators. For cross-platform paths
	// (e.g., Windows paths in compile_commands.json read on Linux), also
	// split on backslashes.
	base := filepath.Base(compiler)
	if idx := strings.LastIndex(base, "\\"); idx >= 0 {
		base = base[idx+1:]
	}
	base = strings.ToLower(base)

	if strings.Contains(base, "clang") {
		return "clang"
	}

	if strings.Contains(base, "gcc") || strings.Contains(base, "g++") {
		return probeGCCVersion(compiler)
	}

	if base == "cl.exe" || base == "cl" {
		return "msvc"
	}

	return ""
}

// gccVersionRegexp matches GCC version strings like "10.3.0" or "9.4.0" in
// gcc --version output.
var gccVersionRegexp = regexp.MustCompile(`\b(\d+)\.\d+\.\d+\b`)

// probeGCCVersion runs the compiler with --version and parses the major
// version number. Returns "gcc" for GCC >= 10 (JSON diagnostics support),
// "gcc-legacy" for older versions.
func probeGCCVersion(compiler string) string {
	major, err := runGCCVersion(compiler)
	if err != nil {
		slog.Debug("failed to probe GCC version, assuming modern GCC", "compiler", compiler, "error", err)
		return "gcc"
	}

	if major >= 10 {
		return "gcc"
	}
	return "gcc-legacy"
}

// runGCCVersion executes <compiler> --version and extracts the major version
// number. This function is separated from probeGCCVersion so that unit tests
// can test version string parsing via ParseGCCMajorVersion without running
// a subprocess.
func runGCCVersion(compiler string) (int, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(compiler, "--version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout // some compilers print to stderr

	if err := cmd.Run(); err != nil {
		return 0, err
	}

	return ParseGCCMajorVersion(stdout.String())
}

// ParseGCCMajorVersion extracts the major version number from GCC --version
// output. Example input: "gcc (Ubuntu 10.3.0-1ubuntu1) 10.3.0"
// Returns the major version (e.g., 10) or an error if no version is found.
func ParseGCCMajorVersion(output string) (int, error) {
	// Look at the first line for the version.
	firstLine := output
	if idx := strings.IndexByte(output, '\n'); idx >= 0 {
		firstLine = output[:idx]
	}

	match := gccVersionRegexp.FindStringSubmatch(firstLine)
	if match == nil {
		// Try the full output if first line didn't match.
		match = gccVersionRegexp.FindStringSubmatch(output)
	}
	if match == nil {
		return 0, &versionParseError{output: firstLine}
	}

	major, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, &versionParseError{output: firstLine}
	}

	return major, nil
}

// versionParseError is returned when GCC version output cannot be parsed.
type versionParseError struct {
	output string
}

func (e *versionParseError) Error() string {
	return "could not parse GCC version from: " + e.output
}

// detectFromEnv tries the CC environment variable (or "cc") to determine
// the toolchain. Returns "" if detection fails.
func detectFromEnv() string {
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}

	var stdout bytes.Buffer
	cmd := exec.Command(cc, "--version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	if err := cmd.Run(); err != nil {
		return ""
	}

	output := strings.ToLower(stdout.String())

	if strings.Contains(output, "clang") {
		return "clang"
	}
	if strings.Contains(output, "gcc") || strings.Contains(output, "g++") {
		// For env-based detection, try to parse version too.
		major, err := ParseGCCMajorVersion(stdout.String())
		if err != nil {
			return "gcc"
		}
		if major >= 10 {
			return "gcc"
		}
		return "gcc-legacy"
	}
	if strings.Contains(output, "microsoft") || strings.Contains(output, "cl.exe") {
		return "msvc"
	}

	return ""
}
