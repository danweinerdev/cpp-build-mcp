package diagnostics

import "strings"

// NewParser returns a DiagnosticParser for the given toolchain name.
// Supported toolchains: "clang" (Clang JSON parser), "gcc" (GCC JSON
// parser for GCC 10+), "gcc-legacy" (regex for GCC < 10), "msvc" (regex),
// and everything else (regex fallback).
func NewParser(toolchain string) DiagnosticParser {
	switch strings.ToLower(toolchain) {
	case "clang":
		return &ClangParser{}
	case "gcc":
		return &GCCParser{}
	case "gcc-legacy", "msvc", "":
		return &RegexParser{}
	default:
		return &RegexParser{}
	}
}

// Parse is a convenience function that creates a parser for the given
// toolchain and parses the build output.
func Parse(toolchain, stdout, stderr string) ([]Diagnostic, error) {
	return NewParser(toolchain).Parse(stdout, stderr)
}
