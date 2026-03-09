package diagnostics

import (
	"strings"
	"testing"
)

func TestParseSARIF(t *testing.T) {
	t.Run("single error", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"error",
			"message":{"text":"use of undeclared identifier 'foo'"},
			"ruleId":"5350",
			"locations":[{"physicalLocation":{
				"artifactLocation":{"uri":"file:///src/main.cpp"},
				"region":{"startLine":10,"startColumn":5}
			}}]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/src/main.cpp")
		assertDiagInt(t, "Line", d.Line, 10)
		assertDiagInt(t, "Column", d.Column, 5)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "use of undeclared identifier 'foo'")
		assertDiagField(t, "Code", d.Code, "5350")
		assertDiagField(t, "Source", d.Source, "clang")
	})

	t.Run("single warning", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"warning",
			"message":{"text":"unused variable 'x'"},
			"ruleId":"7538",
			"locations":[{"physicalLocation":{
				"artifactLocation":{"uri":"file:///src/test.cpp"},
				"region":{"startLine":5,"startColumn":9}
			}}]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
	})

	t.Run("note level", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"note",
			"message":{"text":"candidate function not viable"},
			"ruleId":"",
			"locations":[]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagSeverity(t, diags[0].Severity, SeverityNote)
	})

	t.Run("unknown level maps to warning", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"none",
			"message":{"text":"informational"},
			"ruleId":"",
			"locations":[]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
	})

	t.Run("case insensitive level", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"ERROR",
			"message":{"text":"err"},
			"ruleId":"",
			"locations":[]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		assertDiagSeverity(t, diags[0].Severity, SeverityError)
	})

	t.Run("multiple results in one run", func(t *testing.T) {
		sarif := `{"runs":[{"results":[
			{"level":"warning","message":{"text":"w1"},"ruleId":"1","locations":[]},
			{"level":"error","message":{"text":"e1"},"ruleId":"2","locations":[]}
		]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics, got %d", len(diags))
		}
		assertDiagField(t, "Message[0]", diags[0].Message, "w1")
		assertDiagField(t, "Message[1]", diags[1].Message, "e1")
	})

	t.Run("multiple runs merged", func(t *testing.T) {
		sarif := `{"runs":[
			{"results":[{"level":"warning","message":{"text":"from run 0"},"ruleId":"","locations":[]}]},
			{"results":[{"level":"error","message":{"text":"from run 1"},"ruleId":"","locations":[]}]}
		]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics, got %d", len(diags))
		}
		assertDiagField(t, "Message[0]", diags[0].Message, "from run 0")
		assertDiagField(t, "Message[1]", diags[1].Message, "from run 1")
	})

	t.Run("empty results returns nil", func(t *testing.T) {
		sarif := `{"runs":[{"results":[]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil, got %v", diags)
		}
	})

	t.Run("missing locations", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"error",
			"message":{"text":"some error"},
			"ruleId":"123",
			"locations":[]
		}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "File", diags[0].File, "")
		assertDiagInt(t, "Line", diags[0].Line, 0)
		assertDiagInt(t, "Column", diags[0].Column, 0)
	})

	t.Run("malformed SARIF returns fallback", func(t *testing.T) {
		diags, err := parseSARIF(`{"runs": broken}`)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 fallback, got %d", len(diags))
		}
		assertDiagSeverity(t, diags[0].Severity, SeverityError)
		assertDiagField(t, "Source", diags[0].Source, "clang")
		if !strings.HasPrefix(diags[0].Message, "Failed to parse Clang SARIF output:") {
			t.Errorf("expected SARIF fallback message, got %q", diags[0].Message)
		}
	})

	t.Run("concatenated SARIF objects", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{"level":"warning","message":{"text":"w1"},"ruleId":"","locations":[]}]}]}{"runs":[{"results":[{"level":"error","message":{"text":"e1"},"ruleId":"","locations":[]}]}]}`

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics from concatenated objects, got %d", len(diags))
		}
		assertDiagField(t, "Message[0]", diags[0].Message, "w1")
		assertDiagField(t, "Message[1]", diags[1].Message, "e1")
	})

	t.Run("trailing text after SARIF is ignored", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{"level":"warning","message":{"text":"w1"},"ruleId":"","locations":[]}]}]}` + "\n\n1 warning generated.\n"

		diags, err := parseSARIF(sarif)
		if err != nil {
			t.Fatalf("parseSARIF() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "w1")
	})
}

func TestSplitJSONObjects(t *testing.T) {
	t.Run("single object", func(t *testing.T) {
		objects := splitJSONObjects(`{"a":1}`)
		if len(objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objects))
		}
	})

	t.Run("two objects", func(t *testing.T) {
		objects := splitJSONObjects(`{"a":1}{"b":2}`)
		if len(objects) != 2 {
			t.Fatalf("expected 2 objects, got %d", len(objects))
		}
	})

	t.Run("objects with whitespace between", func(t *testing.T) {
		objects := splitJSONObjects(`{"a":1}  {"b":2}`)
		if len(objects) != 2 {
			t.Fatalf("expected 2 objects, got %d", len(objects))
		}
	})

	t.Run("nested braces", func(t *testing.T) {
		objects := splitJSONObjects(`{"a":{"b":1}}`)
		if len(objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objects))
		}
	})

	t.Run("braces in strings", func(t *testing.T) {
		objects := splitJSONObjects(`{"msg":"has } brace"}`)
		if len(objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objects))
		}
	})

	t.Run("escaped quotes in strings", func(t *testing.T) {
		objects := splitJSONObjects(`{"msg":"has \" escaped"}`)
		if len(objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objects))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		objects := splitJSONObjects("")
		if len(objects) != 0 {
			t.Fatalf("expected 0 objects, got %d", len(objects))
		}
	})

	t.Run("trailing text ignored", func(t *testing.T) {
		objects := splitJSONObjects(`{"a":1}` + "\n1 warning generated.\n")
		if len(objects) != 1 {
			t.Fatalf("expected 1 object, got %d", len(objects))
		}
	})
}

func TestStripFileURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{"absolute path", "file:///usr/src/main.cpp", "/usr/src/main.cpp"},
		{"authority form left as-is", "file://host/path/file.cpp", "file://host/path/file.cpp"},
		{"relative path", "file:src/main.cpp", "src/main.cpp"},
		{"bare path", "/usr/src/main.cpp", "/usr/src/main.cpp"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFileURI(tt.uri)
			if got != tt.want {
				t.Errorf("stripFileURI(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestMapSARIFLevel(t *testing.T) {
	tests := []struct {
		level string
		want  Severity
	}{
		{"error", SeverityError},
		{"warning", SeverityWarning},
		{"note", SeverityNote},
		{"ERROR", SeverityError},
		{"Warning", SeverityWarning},
		{"none", SeverityWarning},
		{"unknown", SeverityWarning},
		{"", SeverityWarning},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			got := mapSARIFLevel(tt.level)
			if got != tt.want {
				t.Errorf("mapSARIFLevel(%q) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}
