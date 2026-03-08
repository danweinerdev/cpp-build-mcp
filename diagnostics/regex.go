package diagnostics

import (
	"regexp"
	"strconv"
	"strings"
)

// msvcPattern matches MSVC-style diagnostic lines:
//
//	file.cpp(line,col): severity CODE: message
//	file.cpp(line): severity CODE: message
var msvcPattern = regexp.MustCompile(
	`^(.+)\((\d+),?(\d+)?\)\s*:\s*(fatal error|error|warning|note)\s+(C\d+)\s*:\s*(.+)$`,
)

// gccPattern matches GCC/Clang legacy diagnostic lines:
//
//	file.cpp:line:col: severity: message
var gccPattern = regexp.MustCompile(
	`^(.+?):(\d+):(\d+):\s*(fatal error|error|warning|note):\s*(.+)$`,
)

// RegexParser parses human-readable compiler diagnostic output from stderr
// using regex patterns. It supports MSVC and GCC/Clang legacy formats.
type RegexParser struct{}

// Parse parses stderr for compiler diagnostics using regex patterns.
// The stdout parameter is ignored. Each line of stderr is tested against
// the MSVC pattern first, then the GCC/Clang pattern. Lines that match
// neither pattern are silently ignored.
func (p *RegexParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return nil, nil
	}

	lines := strings.Split(stderr, "\n")
	var diags []Diagnostic

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if d, ok := parseMSVC(line); ok {
			diags = append(diags, d)
			continue
		}

		if d, ok := parseGCC(line); ok {
			diags = append(diags, d)
		}
	}

	if diags == nil {
		return nil, nil
	}
	return diags, nil
}

// parseMSVC attempts to parse a line as an MSVC diagnostic.
func parseMSVC(line string) (Diagnostic, bool) {
	m := msvcPattern.FindStringSubmatch(line)
	if m == nil {
		return Diagnostic{}, false
	}

	lineNum, _ := strconv.Atoi(m[2])
	col := 0
	if m[3] != "" {
		col, _ = strconv.Atoi(m[3])
	}

	return Diagnostic{
		File:     m[1],
		Line:     lineNum,
		Column:   col,
		Severity: mapRegexSeverity(m[4]),
		Code:     m[5],
		Message:  m[6],
		Source:   "msvc",
	}, true
}

// parseGCC attempts to parse a line as a GCC/Clang legacy diagnostic.
func parseGCC(line string) (Diagnostic, bool) {
	m := gccPattern.FindStringSubmatch(line)
	if m == nil {
		return Diagnostic{}, false
	}

	lineNum, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])

	return Diagnostic{
		File:     m[1],
		Line:     lineNum,
		Column:   col,
		Severity: mapRegexSeverity(m[4]),
		Message:  m[5],
		Source:   "compiler",
	}, true
}

// mapRegexSeverity maps a severity string from regex-parsed output to a Severity constant.
func mapRegexSeverity(s string) Severity {
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
