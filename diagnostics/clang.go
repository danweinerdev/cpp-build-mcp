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
func (p *ClangParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
	// Strip Ninja progress lines from both streams before format detection.
	stdout = ninjaProgressRe.ReplaceAllString(stdout, "")
	stderr = ninjaProgressRe.ReplaceAllString(stderr, "")

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

// hasStructuredContent reports whether s contains structured JSON content
// (first non-whitespace character is '{' or '[').
func hasStructuredContent(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// detectOutputFormat returns "sarif" if the first non-whitespace character is '{',
// "clang-json" if it's '[', or "" otherwise.
func detectOutputFormat(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	switch trimmed[0] {
	case '{':
		return "sarif"
	case '[':
		return "clang-json"
	default:
		return ""
	}
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
