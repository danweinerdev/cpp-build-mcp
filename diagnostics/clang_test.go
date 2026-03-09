package diagnostics

import (
	"strings"
	"testing"
)

func TestClangParser_Parse(t *testing.T) {
	parser := &ClangParser{}

	t.Run("simple warning", func(t *testing.T) {
		stdout := `[
			{
				"file": "test.cpp",
				"line": 5,
				"column": 7,
				"severity": "warning",
				"message": "unused variable 'x'",
				"option": "-Wunused-variable",
				"ranges": [],
				"fixits": []
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
		assertDiagInt(t, "Line", d.Line, 5)
		assertDiagInt(t, "Column", d.Column, 7)
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'x'")
		assertDiagField(t, "Code", d.Code, "-Wunused-variable")
		assertDiagField(t, "Source", d.Source, "clang")
	})

	t.Run("error with column info", func(t *testing.T) {
		stdout := `[
			{
				"file": "main.cpp",
				"line": 12,
				"column": 3,
				"severity": "error",
				"message": "use of undeclared identifier 'foo'",
				"option": "",
				"ranges": [],
				"fixits": []
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
		assertDiagInt(t, "Line", d.Line, 12)
		assertDiagInt(t, "Column", d.Column, 3)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "use of undeclared identifier 'foo'")
		assertDiagField(t, "Source", d.Source, "clang")
	})

	t.Run("template instantiation error", func(t *testing.T) {
		stdout := `[
			{
				"file": "template.hpp",
				"line": 42,
				"column": 15,
				"severity": "error",
				"message": "no matching function for call to 'process'",
				"option": "",
				"ranges": [],
				"fixits": []
			},
			{
				"file": "template.hpp",
				"line": 42,
				"column": 15,
				"severity": "note",
				"message": "candidate function not viable: requires 2 arguments, but 1 was provided",
				"option": "",
				"ranges": [],
				"fixits": []
			}
		]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics, got %d", len(diags))
		}

		assertDiagSeverity(t, diags[0].Severity, SeverityError)
		assertDiagField(t, "Message[0]", diags[0].Message, "no matching function for call to 'process'")
		assertDiagSeverity(t, diags[1].Severity, SeverityNote)
		assertDiagField(t, "Message[1]", diags[1].Message, "candidate function not viable: requires 2 arguments, but 1 was provided")
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

	t.Run("whitespace-only stdout returns empty slice", func(t *testing.T) {
		diags, err := parser.Parse("   \n\t  ", "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("malformed JSON returns fallback diagnostic", func(t *testing.T) {
		stdout := `[{"file": "test.cpp", "line": broken}]`

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 fallback diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Source", d.Source, "clang")
		if !strings.HasPrefix(d.Message, "Failed to parse Clang output:") {
			t.Errorf("expected fallback message prefix, got %q", d.Message)
		}
	})

	t.Run("two concatenated arrays merged", func(t *testing.T) {
		stdout := `[
			{
				"file": "a.cpp",
				"line": 1,
				"column": 1,
				"severity": "warning",
				"message": "warning in a.cpp",
				"option": "-Wextra",
				"ranges": [],
				"fixits": []
			}
		][
			{
				"file": "b.cpp",
				"line": 10,
				"column": 5,
				"severity": "error",
				"message": "error in b.cpp",
				"option": "",
				"ranges": [],
				"fixits": []
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
				"file": "missing.h",
				"line": 1,
				"column": 10,
				"severity": "fatal error",
				"message": "'nonexistent.h' file not found",
				"option": "",
				"ranges": [],
				"fixits": []
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
		assertDiagField(t, "Source", diags[0].Source, "clang")
	})

	t.Run("multiple diagnostics in one array", func(t *testing.T) {
		stdout := `[
			{
				"file": "main.cpp",
				"line": 3,
				"column": 7,
				"severity": "warning",
				"message": "unused variable 'a'",
				"option": "-Wunused-variable",
				"ranges": [],
				"fixits": []
			},
			{
				"file": "main.cpp",
				"line": 4,
				"column": 7,
				"severity": "warning",
				"message": "unused variable 'b'",
				"option": "-Wunused-variable",
				"ranges": [],
				"fixits": []
			},
			{
				"file": "main.cpp",
				"line": 10,
				"column": 1,
				"severity": "error",
				"message": "expected ';' after expression",
				"option": "",
				"ranges": [],
				"fixits": []
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

		assertDiagSeverity(t, diags[1].Severity, SeverityWarning)
		assertDiagField(t, "Message[1]", diags[1].Message, "unused variable 'b'")

		assertDiagSeverity(t, diags[2].Severity, SeverityError)
		assertDiagField(t, "Message[2]", diags[2].Message, "expected ';' after expression")
	})

	t.Run("ninja progress lines stripped from stdout", func(t *testing.T) {
		stdout := "[1/803] Building CXX object CMakeFiles/foo.dir/foo.cpp.o\n" +
			"[2/803] Building CXX object CMakeFiles/bar.dir/bar.cpp.o\n" +
			`[
			{
				"file": "foo.cpp",
				"line": 5,
				"column": 7,
				"severity": "warning",
				"message": "unused variable 'x'",
				"option": "-Wunused-variable",
				"ranges": [],
				"fixits": []
			}
		]` + "\n[3/803] Building CXX object CMakeFiles/baz.dir/baz.cpp.o\n"

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "foo.cpp")
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'x'")
	})

	t.Run("only ninja progress lines returns empty", func(t *testing.T) {
		stdout := "[1/10] Building CXX object CMakeFiles/foo.dir/foo.cpp.o\n" +
			"[2/10] Building CXX object CMakeFiles/bar.dir/bar.cpp.o\n" +
			"[3/10] Linking CXX executable foo\n"

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("ninja progress interleaved with multiple json arrays", func(t *testing.T) {
		stdout := "[1/5] Building CXX object a.cpp.o\n" +
			`[
			{
				"file": "a.cpp",
				"line": 1,
				"column": 1,
				"severity": "warning",
				"message": "warning in a",
				"option": "-Wextra",
				"ranges": [],
				"fixits": []
			}
		]` + "\n[2/5] Building CXX object b.cpp.o\n" +
			`[
			{
				"file": "b.cpp",
				"line": 10,
				"column": 5,
				"severity": "error",
				"message": "error in b",
				"option": "",
				"ranges": [],
				"fixits": []
			}
		]` + "\n[3/5] Linking CXX executable main\n"

		diags, err := parser.Parse(stdout, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics, got %d", len(diags))
		}

		assertDiagField(t, "File[0]", diags[0].File, "a.cpp")
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
		assertDiagField(t, "File[1]", diags[1].File, "b.cpp")
		assertDiagSeverity(t, diags[1].Severity, SeverityError)
	})

	t.Run("stderr not used when stdout has content", func(t *testing.T) {
		stdout := `[
			{
				"file": "test.cpp",
				"line": 1,
				"column": 1,
				"severity": "warning",
				"message": "from stdout",
				"option": "",
				"ranges": [],
				"fixits": []
			}
		]`
		stderr := `[
			{
				"file": "test.cpp",
				"line": 99,
				"column": 99,
				"severity": "error",
				"message": "from stderr",
				"option": "",
				"ranges": [],
				"fixits": []
			}
		]`

		diags, err := parser.Parse(stdout, stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic (stdout wins), got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "from stdout")
	})

	t.Run("SARIF on stdout parsed correctly", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"warning",
			"message":{"text":"unused variable 'x'"},
			"ruleId":"7538",
			"locations":[{"physicalLocation":{
				"artifactLocation":{"uri":"file:///src/test.cpp"},
				"region":{"startLine":5,"startColumn":9}
			}}]
		}]}]}`

		diags, err := parser.Parse(sarif, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/src/test.cpp")
		assertDiagInt(t, "Line", d.Line, 5)
		assertDiagInt(t, "Column", d.Column, 9)
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'x'")
		assertDiagField(t, "Code", d.Code, "7538")
		assertDiagField(t, "Source", d.Source, "clang")
	})

	t.Run("SARIF on stderr with empty stdout", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"error",
			"message":{"text":"use of undeclared identifier 'foo'"},
			"ruleId":"5350",
			"locations":[{"physicalLocation":{
				"artifactLocation":{"uri":"file:///src/main.cpp"},
				"region":{"startLine":10,"startColumn":5}
			}}]
		}]}]}`

		diags, err := parser.Parse("", sarif)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic from stderr fallback, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/src/main.cpp")
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "use of undeclared identifier 'foo'")
	})

	t.Run("real Clang stderr format with leading blank and trailing summary", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"warning",
			"message":{"text":"unused variable 'x'"},
			"ruleId":"7538",
			"locations":[]
		}]}]}`
		stderr := "\n" + sarif + "\n\n1 warning generated.\n"

		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'x'")
	})

	t.Run("ninja progress stripped from stderr before SARIF", func(t *testing.T) {
		sarif := `{"runs":[{"results":[{
			"level":"warning",
			"message":{"text":"w1"},
			"ruleId":"",
			"locations":[]
		}]}]}`
		stderr := "[1/5] Building CXX object a.cpp.o\n" + sarif

		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "w1")
	})

	t.Run("neither stream has structured content returns nil", func(t *testing.T) {
		diags, err := parser.Parse("some text", "error: something")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil, got %v", diags)
		}
	})
}

func TestStripNinjaNoise(t *testing.T) {
	t.Run("strips progress lines", func(t *testing.T) {
		input := "[1/5] Building CXX object a.cpp.o\n[{\"file\":\"a.cpp\"}]\n[2/5] Building CXX object b.cpp.o"
		got := stripNinjaNoise(input)
		if strings.Contains(got, "[1/5]") || strings.Contains(got, "[2/5]") {
			t.Errorf("progress lines not stripped: %q", got)
		}
		if !strings.Contains(got, `[{"file":"a.cpp"}]`) {
			t.Errorf("JSON content should be preserved: %q", got)
		}
	})

	t.Run("strips FAILED lines", func(t *testing.T) {
		input := "FAILED: CMakeFiles/main.dir/a.cpp.o\n[{\"file\":\"a.cpp\"}]"
		got := stripNinjaNoise(input)
		if strings.Contains(got, "FAILED:") {
			t.Errorf("FAILED line not stripped: %q", got)
		}
		if !strings.Contains(got, `[{"file":"a.cpp"}]`) {
			t.Errorf("JSON content should be preserved: %q", got)
		}
	})

	t.Run("strips ninja summary lines", func(t *testing.T) {
		input := "[{\"file\":\"a.cpp\"}]\nninja: build stopped: subcommand failed."
		got := stripNinjaNoise(input)
		if strings.Contains(got, "ninja:") {
			t.Errorf("ninja summary not stripped: %q", got)
		}
		if !strings.Contains(got, `[{"file":"a.cpp"}]`) {
			t.Errorf("JSON content should be preserved: %q", got)
		}
	})

	t.Run("strips compiler count lines", func(t *testing.T) {
		input := "[{\"file\":\"a.cpp\"}]\n1 error generated.\n2 warnings generated."
		got := stripNinjaNoise(input)
		if strings.Contains(got, "error generated") || strings.Contains(got, "warnings generated") {
			t.Errorf("compiler count lines not stripped: %q", got)
		}
	})

	t.Run("strips all noise types together", func(t *testing.T) {
		input := "[1/3] Building CXX object a.cpp.o\n" +
			"FAILED: CMakeFiles/main.dir/a.cpp.o\n" +
			`[{"file":"a.cpp","line":1,"column":1,"severity":"error","message":"err","option":""}]` + "\n" +
			"1 error generated.\n" +
			"ninja: build stopped: subcommand failed."
		got := stripNinjaNoise(input)
		if strings.Contains(got, "[1/3]") {
			t.Error("progress line not stripped")
		}
		if strings.Contains(got, "FAILED:") {
			t.Error("FAILED line not stripped")
		}
		if strings.Contains(got, "error generated") {
			t.Error("compiler count not stripped")
		}
		if strings.Contains(got, "ninja:") {
			t.Error("ninja summary not stripped")
		}
		if !strings.Contains(got, `"severity":"error"`) {
			t.Error("JSON diagnostic content should be preserved")
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		got := stripNinjaNoise("")
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestSplitJSONArrays(t *testing.T) {
	t.Run("single array", func(t *testing.T) {
		arrays := splitJSONArrays(`[{"a":1}]`)
		if len(arrays) != 1 {
			t.Fatalf("expected 1 array, got %d", len(arrays))
		}
	})

	t.Run("two arrays", func(t *testing.T) {
		arrays := splitJSONArrays(`[{"a":1}][{"b":2}]`)
		if len(arrays) != 2 {
			t.Fatalf("expected 2 arrays, got %d", len(arrays))
		}
	})

	t.Run("arrays with whitespace between", func(t *testing.T) {
		arrays := splitJSONArrays(`[{"a":1}]  [{"b":2}]`)
		if len(arrays) != 2 {
			t.Fatalf("expected 2 arrays, got %d", len(arrays))
		}
	})

	t.Run("nested brackets in strings", func(t *testing.T) {
		arrays := splitJSONArrays(`[{"msg":"has ] bracket"}]`)
		if len(arrays) != 1 {
			t.Fatalf("expected 1 array, got %d", len(arrays))
		}
	})

	t.Run("escaped quotes in strings", func(t *testing.T) {
		arrays := splitJSONArrays(`[{"msg":"has \" escaped"}]`)
		if len(arrays) != 1 {
			t.Fatalf("expected 1 array, got %d", len(arrays))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		arrays := splitJSONArrays("")
		if len(arrays) != 0 {
			t.Fatalf("expected 0 arrays, got %d", len(arrays))
		}
	})
}

func TestTruncateOutput(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		got := truncateOutput("hello", 10)
		if got != "hello" {
			t.Fatalf("expected %q, got %q", "hello", got)
		}
	})

	t.Run("long string truncated with ellipsis", func(t *testing.T) {
		input := strings.Repeat("a", 250)
		got := truncateOutput(input, 200)
		if len(got) != 203 { // 200 + "..."
			t.Fatalf("expected length 203, got %d", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Fatal("expected truncated string to end with '...'")
		}
	})
}

// assertDiagField checks string field equality on a diagnostic.
func assertDiagField(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", field, got, want)
	}
}

// assertDiagInt checks integer field equality on a diagnostic.
func assertDiagInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", field, got, want)
	}
}

// assertDiagSeverity checks severity equality on a diagnostic.
func assertDiagSeverity(t *testing.T, got, want Severity) {
	t.Helper()
	if got != want {
		t.Errorf("Severity: got %q, want %q", got, want)
	}
}
