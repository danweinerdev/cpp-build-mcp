package diagnostics

import (
	"strings"
	"testing"
)

func TestGCCParser_Parse(t *testing.T) {
	parser := &GCCParser{}

	t.Run("simple error with location", func(t *testing.T) {
		stdout := `[
			{
				"kind": "error",
				"message": "expected ';' before '}' token",
				"option": "",
				"locations": [
					{
						"caret": {"file": "test.cpp", "line": 10, "column": 5, "display-column": 5, "byte-column": 5}
					}
				],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "test.cpp")
		assertDiagInt(t, "Line", d.Line, 10)
		assertDiagInt(t, "Column", d.Column, 5)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "expected ';' before '}' token")
		assertDiagField(t, "Source", d.Source, "gcc")
		assertDiagField(t, "RelatedTo", d.RelatedTo, "")
	})

	t.Run("warning with option code", func(t *testing.T) {
		stdout := `[
			{
				"kind": "warning",
				"message": "unused variable 'x'",
				"option": "-Wunused-variable",
				"locations": [
					{
						"caret": {"file": "main.cpp", "line": 7, "column": 9}
					}
				],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 7)
		assertDiagInt(t, "Column", d.Column, 9)
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Code", d.Code, "-Wunused-variable")
		assertDiagField(t, "Source", d.Source, "gcc")
	})

	t.Run("diagnostic with note children flattened", func(t *testing.T) {
		stdout := `[
			{
				"kind": "error",
				"message": "expected ';' before '}' token",
				"option": "-Werror",
				"locations": [
					{
						"caret": {"file": "test.cpp", "line": 10, "column": 5}
					}
				],
				"children": [
					{
						"kind": "note",
						"message": "in expansion of macro 'FOO'",
						"option": "",
						"locations": [
							{
								"caret": {"file": "test.cpp", "line": 5, "column": 1}
							}
						],
						"children": []
					}
				]
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics (parent + child note), got %d", len(diags))
		}

		// Parent diagnostic
		assertDiagSeverity(t, diags[0].Severity, SeverityError)
		assertDiagField(t, "Message[0]", diags[0].Message, "expected ';' before '}' token")
		assertDiagField(t, "Code[0]", diags[0].Code, "-Werror")
		assertDiagField(t, "File[0]", diags[0].File, "test.cpp")
		assertDiagInt(t, "Line[0]", diags[0].Line, 10)
		assertDiagField(t, "RelatedTo[0]", diags[0].RelatedTo, "")

		// Child note
		assertDiagSeverity(t, diags[1].Severity, SeverityNote)
		assertDiagField(t, "Message[1]", diags[1].Message, "in expansion of macro 'FOO'")
		assertDiagField(t, "File[1]", diags[1].File, "test.cpp")
		assertDiagInt(t, "Line[1]", diags[1].Line, 5)
		assertDiagInt(t, "Column[1]", diags[1].Column, 1)
		assertDiagField(t, "RelatedTo[1]", diags[1].RelatedTo, "test.cpp:10")
		assertDiagField(t, "Source[1]", diags[1].Source, "gcc")
	})

	t.Run("deeply nested children capped at depth 3", func(t *testing.T) {
		// Build a 5-level deep nesting: parent -> child1 -> child2 -> child3 -> child4
		// Only 4 levels (depth 0,1,2,3) should produce diagnostics; child4 (depth 4) should be dropped.
		stdout := `[
			{
				"kind": "error",
				"message": "level 0",
				"option": "",
				"locations": [{"caret": {"file": "a.cpp", "line": 1, "column": 1}}],
				"children": [
					{
						"kind": "note",
						"message": "level 1",
						"option": "",
						"locations": [{"caret": {"file": "a.cpp", "line": 2, "column": 1}}],
						"children": [
							{
								"kind": "note",
								"message": "level 2",
								"option": "",
								"locations": [{"caret": {"file": "a.cpp", "line": 3, "column": 1}}],
								"children": [
									{
										"kind": "note",
										"message": "level 3",
										"option": "",
										"locations": [{"caret": {"file": "a.cpp", "line": 4, "column": 1}}],
										"children": [
											{
												"kind": "note",
												"message": "level 4 should be dropped",
												"option": "",
												"locations": [{"caret": {"file": "a.cpp", "line": 5, "column": 1}}],
												"children": []
											}
										]
									}
								]
							}
						]
					}
				]
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}

		// Depth 0 (level 0) + Depth 1 (level 1) + Depth 2 (level 2) + Depth 3 (level 3) = 4
		// Depth 3 is at maxChildDepth, so its children are NOT expanded.
		if len(diags) != 4 {
			t.Fatalf("expected 4 diagnostics (depth cap at 3), got %d", len(diags))
		}

		assertDiagField(t, "Message[0]", diags[0].Message, "level 0")
		assertDiagField(t, "Message[1]", diags[1].Message, "level 1")
		assertDiagField(t, "Message[2]", diags[2].Message, "level 2")
		assertDiagField(t, "Message[3]", diags[3].Message, "level 3")

		// Verify the chain of RelatedTo references
		assertDiagField(t, "RelatedTo[0]", diags[0].RelatedTo, "")
		assertDiagField(t, "RelatedTo[1]", diags[1].RelatedTo, "a.cpp:1")
		assertDiagField(t, "RelatedTo[2]", diags[2].RelatedTo, "a.cpp:2")
		assertDiagField(t, "RelatedTo[3]", diags[3].RelatedTo, "a.cpp:3")
	})

	t.Run("empty stdout returns empty slice", func(t *testing.T) {
		diags, err := parser.Parse("", "some stderr output")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("malformed JSON returns fallback diagnostic", func(t *testing.T) {
		stdout := `[{"kind": "error", "message": broken json}]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 fallback diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Source", d.Source, "gcc")
		if !strings.HasPrefix(d.Message, "Failed to parse GCC output:") {
			t.Errorf("expected fallback message prefix, got %q", d.Message)
		}
	})

	t.Run("multiple diagnostics in one array", func(t *testing.T) {
		stdout := `[
			{
				"kind": "warning",
				"message": "unused variable 'a'",
				"option": "-Wunused-variable",
				"locations": [{"caret": {"file": "main.cpp", "line": 3, "column": 7}}],
				"children": []
			},
			{
				"kind": "warning",
				"message": "unused variable 'b'",
				"option": "-Wunused-variable",
				"locations": [{"caret": {"file": "main.cpp", "line": 4, "column": 7}}],
				"children": []
			},
			{
				"kind": "error",
				"message": "expected ';' after expression",
				"option": "",
				"locations": [{"caret": {"file": "main.cpp", "line": 10, "column": 1}}],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 3 {
			t.Fatalf("expected 3 diagnostics, got %d", len(diags))
		}

		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
		assertDiagField(t, "Message[0]", diags[0].Message, "unused variable 'a'")
		assertDiagField(t, "Code[0]", diags[0].Code, "-Wunused-variable")

		assertDiagSeverity(t, diags[1].Severity, SeverityWarning)
		assertDiagField(t, "Message[1]", diags[1].Message, "unused variable 'b'")

		assertDiagSeverity(t, diags[2].Severity, SeverityError)
		assertDiagField(t, "Message[2]", diags[2].Message, "expected ';' after expression")
	})

	t.Run("concatenated arrays merged", func(t *testing.T) {
		stdout := `[
			{
				"kind": "warning",
				"message": "warning in a.cpp",
				"option": "-Wextra",
				"locations": [{"caret": {"file": "a.cpp", "line": 1, "column": 1}}],
				"children": []
			}
		][
			{
				"kind": "error",
				"message": "error in b.cpp",
				"option": "",
				"locations": [{"caret": {"file": "b.cpp", "line": 10, "column": 5}}],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics from merged arrays, got %d", len(diags))
		}

		assertDiagField(t, "File[0]", diags[0].File, "a.cpp")
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
		assertDiagField(t, "Code[0]", diags[0].Code, "-Wextra")

		assertDiagField(t, "File[1]", diags[1].File, "b.cpp")
		assertDiagSeverity(t, diags[1].Severity, SeverityError)
		assertDiagInt(t, "Line[1]", diags[1].Line, 10)
	})

	t.Run("fatal error maps to error severity", func(t *testing.T) {
		stdout := `[
			{
				"kind": "fatal error",
				"message": "'nonexistent.h' file not found",
				"option": "",
				"locations": [{"caret": {"file": "missing.h", "line": 1, "column": 10}}],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		assertDiagSeverity(t, diags[0].Severity, SeverityError)
		assertDiagField(t, "Source", diags[0].Source, "gcc")
	})

	t.Run("stderr is ignored", func(t *testing.T) {
		stdout := `[
			{
				"kind": "warning",
				"message": "from stdout",
				"option": "",
				"locations": [{"caret": {"file": "test.cpp", "line": 1, "column": 1}}],
				"children": []
			}
		]`
		stderr := `[
			{
				"kind": "error",
				"message": "from stderr",
				"option": "",
				"locations": [{"caret": {"file": "test.cpp", "line": 99, "column": 99}}],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic (stderr ignored), got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "from stdout")
	})

	t.Run("multiple children on same parent", func(t *testing.T) {
		stdout := `[
			{
				"kind": "error",
				"message": "ambiguous call",
				"option": "",
				"locations": [{"caret": {"file": "call.cpp", "line": 20, "column": 3}}],
				"children": [
					{
						"kind": "note",
						"message": "candidate 1",
						"option": "",
						"locations": [{"caret": {"file": "call.cpp", "line": 5, "column": 1}}],
						"children": []
					},
					{
						"kind": "note",
						"message": "candidate 2",
						"option": "",
						"locations": [{"caret": {"file": "call.cpp", "line": 10, "column": 1}}],
						"children": []
					}
				]
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 3 {
			t.Fatalf("expected 3 diagnostics (1 parent + 2 notes), got %d", len(diags))
		}

		assertDiagField(t, "Message[0]", diags[0].Message, "ambiguous call")
		assertDiagField(t, "RelatedTo[0]", diags[0].RelatedTo, "")

		assertDiagField(t, "Message[1]", diags[1].Message, "candidate 1")
		assertDiagField(t, "RelatedTo[1]", diags[1].RelatedTo, "call.cpp:20")

		assertDiagField(t, "Message[2]", diags[2].Message, "candidate 2")
		assertDiagField(t, "RelatedTo[2]", diags[2].RelatedTo, "call.cpp:20")
	})

	t.Run("whitespace-only stdout returns empty slice", func(t *testing.T) {
		diags, err := parser.Parse("   \n\t  ", "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("diagnostic with no locations", func(t *testing.T) {
		stdout := `[
			{
				"kind": "error",
				"message": "no location info",
				"option": "",
				"locations": [],
				"children": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "")
		assertDiagInt(t, "Line", d.Line, 0)
		assertDiagInt(t, "Column", d.Column, 0)
		assertDiagField(t, "Message", d.Message, "no location info")
	})
}

func TestMapGCCSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  Severity
	}{
		{"error", SeverityError},
		{"fatal error", SeverityError},
		{"warning", SeverityWarning},
		{"note", SeverityNote},
		{"Error", SeverityError},      // case-insensitive
		{"WARNING", SeverityWarning},  // case-insensitive
		{"unknown", SeverityError},    // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapGCCSeverity(tt.input)
			if got != tt.want {
				t.Errorf("mapGCCSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
