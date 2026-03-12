package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// ninjaProgressRe matches Ninja progress lines like "[1/803] Building CXX object ..."
// These appear in stdout when Ninja is the generator and must be stripped before
// parsing Clang JSON diagnostics.
var ninjaProgressRe = regexp.MustCompile(`(?m)^\[\d+/\d+\].*$`)

// ninjaFailedRe matches Ninja failure preamble lines such as
// "FAILED: [code=1] CMakeFiles/main.dir/a.cpp.o" that appear in build output
// before the compiler's diagnostic JSON. These lines contain '[' characters
// that can confuse JSON format detection.
var ninjaFailedRe = regexp.MustCompile(`(?m)^FAILED:.*$`)

// ninjaSummaryRe matches Ninja summary lines at the end of build output,
// e.g., "ninja: build stopped: subcommand failed." These appear after the
// compiler's diagnostic JSON output.
var ninjaSummaryRe = regexp.MustCompile(`(?m)^ninja:.*$`)

// compilerCountRe matches compiler summary lines like "1 error generated." or
// "2 warnings generated." that Clang appends after its diagnostic output.
var compilerCountRe = regexp.MustCompile(`(?m)^\d+ (?:error|warning)s? generated\.$`)

// makeProgressRe matches CMake Make-generator progress lines like
// "[ 50%] Building CXX object ..." or "[100%] Linking CXX executable main".
// These appear in stdout when Unix Makefiles is the generator and contain '['
// characters that confuse JSON format detection.
var makeProgressRe = regexp.MustCompile(`(?m)^\[\s*\d+%\].*$`)

// makeErrorRe matches GNU Make error lines like "make[2]: *** [target] Error 1"
// that appear after build failures. These contain '[' characters that can
// confuse JSON format detection.
var makeErrorRe = regexp.MustCompile(`(?m)^make\[?\d*\]?:.*$`)

// cmakeStatusRe matches CMake status lines like "-- The CXX compiler
// identification is Clang 19.0.0". These appear on stderr when a build
// triggers an automatic CMake reconfigure (because CMakeLists.txt changed)
// and must be stripped before parsing diagnostics.
var cmakeStatusRe = regexp.MustCompile(`(?m)^-- .*$`)

// stripNinjaNoise removes build system noise from s, leaving only the
// compiler's structured diagnostic output. This includes Ninja progress
// lines ("[1/42] Building ..."), failure preamble ("FAILED: ..."), summary
// ("ninja: build stopped ..."), compiler diagnostics count ("1 error
// generated."), and CMake status lines ("-- Configuring done") that appear
// when a build triggers an automatic reconfigure.
func stripNinjaNoise(s string) string {
	s = ninjaProgressRe.ReplaceAllString(s, "")
	s = ninjaFailedRe.ReplaceAllString(s, "")
	s = ninjaSummaryRe.ReplaceAllString(s, "")
	s = makeProgressRe.ReplaceAllString(s, "")
	s = makeErrorRe.ReplaceAllString(s, "")
	s = compilerCountRe.ReplaceAllString(s, "")
	s = cmakeStatusRe.ReplaceAllString(s, "")
	return s
}

// ClangParser parses Clang diagnostic output into structured Diagnostics.
//
// ClangParser auto-detects two formats: SARIF 2.1.0 (from
// -fdiagnostics-format=sarif, JSON object '{...}') and native Clang JSON
// (JSON array '[...]'). Stdout is checked first; if it has no structured
// content after stripping Ninja progress lines, stderr is used as a
// fallback. When Ninja runs multiple translation units in parallel, their
// output streams may be concatenated; ClangParser splits on object/array
// boundaries before parsing.
type ClangParser struct{}

// clangDiagnostic represents a single diagnostic entry in Clang's JSON output.
type clangDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Option   string `json:"option"`
}

// Parse parses Clang diagnostic output from stdout/stderr into []Diagnostic.
// It auto-detects the format: '{' → SARIF (from -fdiagnostics-format=sarif),
// '[' → native Clang JSON. Stdout is checked first; if it has no structured
// content after stripping Ninja progress lines, stderr is used as a fallback.
//
// Ninja build output may contain non-JSON text (FAILED: lines, compiler
// invocation lines) surrounding the actual JSON diagnostic output. After
// stripping Ninja progress lines, the parser also strips Ninja failure
// preamble lines so that format detection sees the JSON content.
func (p *ClangParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
	// Strip Ninja progress lines and failure preamble from both streams.
	stdout = stripNinjaNoise(stdout)
	stderr = stripNinjaNoise(stderr)

	// Select stream: stdout first, stderr fallback.
	var input string
	if hasStructuredContent(stdout) {
		input = strings.TrimSpace(stdout)
	} else if hasStructuredContent(stderr) {
		input = strings.TrimSpace(stderr)
	}
	if input == "" {
		return nil, nil
	}

	// Detect format and dispatch.
	if detectOutputFormat(input) == "sarif" {
		return parseSARIF(input)
	}
	return p.parseClangJSON(input)
}

// parseClangJSON handles the native Clang JSON array format ([...]).
func (p *ClangParser) parseClangJSON(input string) ([]Diagnostic, error) {
	chunks := splitJSONArrays(input)

	var result []Diagnostic
	for _, chunk := range chunks {
		var raw []clangDiagnostic
		if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
			slog.Warn("failed to parse Clang JSON diagnostics", "error", err)
			truncated := truncateOutput(input, 200)
			return []Diagnostic{
				{
					Severity: SeverityError,
					Message:  fmt.Sprintf("Failed to parse Clang output: %s", truncated),
					Source:   "clang",
				},
			}, nil
		}
		for _, d := range raw {
			result = append(result, Diagnostic{
				File:     d.File,
				Line:     d.Line,
				Column:   d.Column,
				Severity: mapClangSeverity(d.Severity),
				Message:  d.Message,
				Code:     d.Option,
				Source:   "clang",
			})
		}
	}

	return result, nil
}

// hasStructuredContent reports whether s contains a line whose first
// non-whitespace character is '{' or '[', indicating structured JSON content.
// Checking per-line (rather than anywhere in the string) avoids false
// positives from compiler invocation lines that may contain brackets.
func hasStructuredContent(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			return true
		}
	}
	return false
}

// detectOutputFormat scans s line-by-line for the first line whose leading
// non-whitespace character is '{' or '['. Returns "sarif" if '{' is found
// first (SARIF 2.1.0 from -fdiagnostics-format=sarif), "clang-json" if '['
// is found first (native Clang JSON array), or "" if neither is found.
// Per-line detection avoids misidentification from brackets in compiler
// invocation lines that may precede the diagnostic JSON output.
func detectOutputFormat(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		switch trimmed[0] {
		case '{':
			return "sarif"
		case '[':
			return "clang-json"
		}
	}
	return ""
}

// splitJSONArrays splits a string that may contain multiple concatenated JSON
// arrays (e.g., "[...][...]") into individual array strings. It tracks bracket
// depth to find the boundaries between top-level arrays.
func splitJSONArrays(s string) []string {
	var arrays []string
	depth := 0
	start := -1
	inString := false
	escaped := false

	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}

		switch ch {
		case '[':
			if depth == 0 {
				start = i
			}
			depth++
		case ']':
			depth--
			if depth == 0 && start >= 0 {
				arrays = append(arrays, s[start:i+1])
				start = -1
			}
		}
	}

	return arrays
}

// mapClangSeverity maps a Clang severity string to the Severity type.
// "fatal error" is mapped to SeverityError.
func mapClangSeverity(s string) Severity {
	switch strings.ToLower(s) {
	case "error", "fatal error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "note":
		return SeverityNote
	default:
		return SeverityError
	}
}

// truncateOutput truncates a string to maxLen characters, appending "..." if truncated.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
