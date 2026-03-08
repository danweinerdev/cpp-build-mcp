package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// maxChildDepth is the maximum recursion depth when flattening children.
// GCC template expansion can produce arbitrarily deep child trees; we cap
// at 3 levels to keep output manageable.
const maxChildDepth = 3

// GCCParser parses GCC's JSON diagnostic output (GCC 10+ with
// -fdiagnostics-format=json) into structured Diagnostics.
//
// GCC writes JSON diagnostics to stdout as a JSON array. Like Clang,
// when Ninja runs multiple translation units in parallel their stdout
// streams may be concatenated, producing multiple adjacent arrays.
// GCCParser reuses splitJSONArrays to handle this.
type GCCParser struct{}

// gccLocation represents the caret/start/finish location within a GCC
// diagnostic's locations array entry.
type gccLocation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// gccLocationEntry represents a single entry in the "locations" array.
// Each entry can contain "caret", "start", and "finish" sub-objects.
type gccLocationEntry struct {
	Caret gccLocation `json:"caret"`
}

// gccDiagnostic represents a single diagnostic entry in GCC's JSON output.
type gccDiagnostic struct {
	Kind      string             `json:"kind"`
	Message   string             `json:"message"`
	Option    string             `json:"option"`
	Locations []gccLocationEntry `json:"locations"`
	Children  []gccDiagnostic    `json:"children"`
}

// Parse parses GCC JSON diagnostic output from stdout into []Diagnostic.
// The stderr parameter is ignored because GCC writes JSON diagnostics to stdout.
func (p *GCCParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}

	chunks := splitJSONArrays(stdout)

	var result []Diagnostic
	for _, chunk := range chunks {
		var raw []gccDiagnostic
		if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
			slog.Warn("failed to parse GCC JSON diagnostics", "error", err)
			truncated := truncateOutput(stdout, 200)
			return []Diagnostic{
				{
					Severity: SeverityError,
					Message:  fmt.Sprintf("Failed to parse GCC output: %s", truncated),
					Source:   "gcc",
				},
			}, nil
		}
		for _, d := range raw {
			result = append(result, flattenGCCDiagnostic(d, "", 0)...)
		}
	}

	return result, nil
}

// flattenGCCDiagnostic converts a gccDiagnostic and its children into a flat
// slice of Diagnostic values. Children with kind "note" are emitted as
// separate entries with RelatedTo pointing to the parent's file:line.
// Recursion is capped at maxChildDepth levels.
func flattenGCCDiagnostic(d gccDiagnostic, parentRef string, depth int) []Diagnostic {
	file, line, column := extractGCCLocation(d)

	diag := Diagnostic{
		File:      file,
		Line:      line,
		Column:    column,
		Severity:  mapGCCSeverity(d.Kind),
		Message:   d.Message,
		Code:      d.Option,
		Source:    "gcc",
		RelatedTo: parentRef,
	}

	result := []Diagnostic{diag}

	if depth >= maxChildDepth {
		return result
	}

	// Build the parent reference for children: "file:line"
	childRef := ""
	if file != "" {
		childRef = fmt.Sprintf("%s:%d", file, line)
	}

	for _, child := range d.Children {
		result = append(result, flattenGCCDiagnostic(child, childRef, depth+1)...)
	}

	return result
}

// extractGCCLocation extracts file, line, and column from the first location
// entry's caret position. Returns zero values if no locations are present.
func extractGCCLocation(d gccDiagnostic) (string, int, int) {
	if len(d.Locations) == 0 {
		return "", 0, 0
	}
	caret := d.Locations[0].Caret
	return caret.File, caret.Line, caret.Column
}

// mapGCCSeverity maps a GCC kind string to the Severity type.
// "fatal error" is mapped to SeverityError.
func mapGCCSeverity(kind string) Severity {
	switch strings.ToLower(kind) {
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
