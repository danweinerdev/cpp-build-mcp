package diagnostics

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// SARIF 2.1.0 types — minimal subset for fields we map to Diagnostic.

type sarifDocument struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID    string           `json:"ruleId"`
	Level     string           `json:"level"`
	Message   sarifMessage     `json:"message"`
	Locations []sarifLocation  `json:"locations"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
}

// parseSARIF parses one or more concatenated SARIF 2.1.0 documents into
// []Diagnostic. Multiple documents (from parallel Ninja builds) are split
// via splitJSONObjects before parsing.
func parseSARIF(data string) ([]Diagnostic, error) {
	chunks := splitJSONObjects(data)

	var result []Diagnostic
	for _, chunk := range chunks {
		var doc sarifDocument
		if err := json.Unmarshal([]byte(chunk), &doc); err != nil {
			slog.Warn("failed to parse Clang SARIF diagnostics", "error", err)
			truncated := truncateOutput(data, 200)
			return []Diagnostic{
				{
					Severity: SeverityError,
					Message:  fmt.Sprintf("Failed to parse Clang SARIF output: %s", truncated),
					Source:   "clang",
				},
			}, nil
		}
		for _, run := range doc.Runs {
			for _, r := range run.Results {
				d := Diagnostic{
					Severity: mapSARIFLevel(r.Level),
					Message:  r.Message.Text,
					Code:     r.RuleID,
					Source:   "clang",
				}
				if len(r.Locations) > 0 {
					loc := r.Locations[0].PhysicalLocation
					d.File = stripFileURI(loc.ArtifactLocation.URI)
					d.Line = loc.Region.StartLine
					d.Column = loc.Region.StartColumn
				}
				result = append(result, d)
			}
		}
	}

	return result, nil
}

// splitJSONObjects splits a string that may contain multiple concatenated JSON
// objects (e.g., "{...}{...}") into individual object strings. It tracks brace
// depth to find the boundaries between top-level objects. Non-JSON content
// between objects (whitespace, trailing text like "1 warning generated.") is
// naturally skipped.
func splitJSONObjects(s string) []string {
	var objects []string
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
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, s[start:i+1])
				start = -1
			}
		}
	}

	return objects
}

// stripFileURI converts a file:// URI to a bare filesystem path.
//   - file:///absolute/path → /absolute/path
//   - file://host/path → left as-is (authority form)
//   - file:relative/path → relative/path
//   - bare path → unchanged
func stripFileURI(uri string) string {
	if strings.HasPrefix(uri, "file:///") {
		return uri[len("file://"):]
	}
	if strings.HasPrefix(uri, "file://") {
		// Authority form (file://host/path) — leave as-is.
		return uri
	}
	if strings.HasPrefix(uri, "file:") {
		return uri[len("file:"):]
	}
	return uri
}

// mapSARIFLevel maps a SARIF level string to the Severity type.
// Unknown levels map to SeverityWarning (conservative but not alarmist).
func mapSARIFLevel(level string) Severity {
	switch strings.ToLower(level) {
	case "error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "note":
		return SeverityNote
	default:
		return SeverityWarning
	}
}
