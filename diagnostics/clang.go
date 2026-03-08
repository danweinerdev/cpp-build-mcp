package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ClangParser parses Clang's JSON diagnostic output into structured Diagnostics.
//
// Clang writes JSON diagnostics to stdout as a JSON array. When Ninja runs
// multiple translation units in parallel, their stdout streams may be
// concatenated, producing multiple adjacent arrays (e.g., "[...][...]").
// ClangParser handles this by splitting on array boundaries before parsing.
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

// Parse parses Clang JSON diagnostic output from stdout into []Diagnostic.
// The stderr parameter is ignored because Clang writes JSON diagnostics to stdout.
func (p *ClangParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}

	chunks := splitJSONArrays(stdout)

	var result []Diagnostic
	for _, chunk := range chunks {
		var raw []clangDiagnostic
		if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
			slog.Warn("failed to parse Clang JSON diagnostics", "error", err)
			truncated := truncateOutput(stdout, 200)
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
