// Package diagnostics provides structured compiler diagnostic parsing.
package diagnostics

// Severity represents the severity level of a compiler diagnostic.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityNote    Severity = "note"
)

// Diagnostic represents a single compiler diagnostic (error, warning, or note).
type Diagnostic struct {
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Column    int      `json:"column"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
	Code      string   `json:"code,omitempty"`
	Source    string   `json:"source,omitempty"`
	RelatedTo string   `json:"related_to,omitempty"`
}
