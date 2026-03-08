package diagnostics

import "strings"

// NewParser returns a DiagnosticParser for the given toolchain name.
// Supported toolchains: "clang" (JSON parser), "gcc" (regex parser for
// legacy output), and everything else (regex fallback for MSVC and others).
func NewParser(toolchain string) DiagnosticParser {
	switch strings.ToLower(toolchain) {
	case "clang":
		return &ClangParser{}
	case "gcc":
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
