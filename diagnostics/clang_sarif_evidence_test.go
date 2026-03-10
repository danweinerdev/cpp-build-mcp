package diagnostics

import (
	"strings"
	"testing"
)

// TestClangSARIFEvidence documents the actual output from Clang 21 and GCC 15
// on Linux (Fedora 43) to validate design assumptions for SARIF support.
//
// Findings from empirical testing (2026-03-08):
//
// 1. Clang REJECTS -fdiagnostics-format=json with a hard error:
//    "error: invalid value 'json' in '-fdiagnostics-format json'"
//    This means the current cpp-build-mcp flag injection BREAKS Clang builds.
//
// 2. Clang SARIF output goes to STDERR (not stdout).
//    stdout is completely empty.
//
// 3. Clang stderr contains: blank line + SARIF JSON + blank line + "N warning(s) generated."
//    The SARIF JSON is a single line, not pretty-printed.
//
// 4. GCC JSON output also goes to STDERR (not stdout).
//    The existing GCCParser reads from stdout and would miss it.
//    However, GCC JSON mode works because gcc_test.go tests pass — need to verify
//    if the real GCC integration path routes stderr correctly.
//
// 5. Clang uses ruleId as a numeric string ("7538"), not a -W flag name.
//    GCC uses ruleId as the -W flag ("-Wunused-variable").

// clangSARIFWarning is the actual SARIF output from:
//
//	clang++ -fsyntax-only -Wall -fdiagnostics-format=sarif -Wno-sarif-format-unstable /tmp/test_diag.cpp
//
// for the file:
//
//	int main() {
//	    int unused_var = 42;
//	    return 0;
//	}
const clangSARIFWarning = `{"$schema":"https://docs.oasis-open.org/sarif/sarif/v2.1.0/cos02/schemas/sarif-schema-2.1.0.json","runs":[{"artifacts":[{"length":54,"location":{"index":0,"uri":"file:///tmp/test_diag.cpp"},"mimeType":"text/plain","roles":["resultFile"]}],"columnKind":"unicodeCodePoints","results":[{"level":"warning","locations":[{"physicalLocation":{"artifactLocation":{"index":0,"uri":"file:///tmp/test_diag.cpp"},"region":{"endColumn":19,"endLine":2,"startColumn":9,"startLine":2}}},{"physicalLocation":{"artifactLocation":{"index":0,"uri":"file:///tmp/test_diag.cpp"},"region":{"endColumn":9,"startColumn":9,"startLine":2}}}],"message":{"text":"unused variable 'unused_var'"},"ruleId":"7538","ruleIndex":0}],"tool":{"driver":{"fullName":"","informationUri":"https://clang.llvm.org/docs/UsersManual.html","language":"en-US","name":"clang","rules":[{"defaultConfiguration":{"enabled":true,"level":"warning","rank":-1},"fullDescription":{"text":""},"id":"7538","name":""}],"version":"21.1.8"}}}],"version":"2.1.0"}`

// clangSARIFError is the actual SARIF output for an undeclared identifier error.
const clangSARIFError = `{"$schema":"https://docs.oasis-open.org/sarif/sarif/v2.1.0/cos02/schemas/sarif-schema-2.1.0.json","runs":[{"artifacts":[{"length":76,"location":{"index":0,"uri":"file:///tmp/test_diag_error.cpp"},"mimeType":"text/plain","roles":["resultFile"]}],"columnKind":"unicodeCodePoints","results":[{"level":"error","locations":[{"physicalLocation":{"artifactLocation":{"index":0,"uri":"file:///tmp/test_diag_error.cpp"},"region":{"endColumn":19,"endLine":3,"startColumn":5,"startLine":3}}},{"physicalLocation":{"artifactLocation":{"index":0,"uri":"file:///tmp/test_diag_error.cpp"},"region":{"endColumn":5,"startColumn":5,"startLine":3}}}],"message":{"text":"use of undeclared identifier 'undefined_func'"},"ruleId":"5350","ruleIndex":0}],"tool":{"driver":{"fullName":"","informationUri":"https://clang.llvm.org/docs/UsersManual.html","language":"en-US","name":"clang","rules":[{"defaultConfiguration":{"enabled":true,"level":"error","rank":50},"fullDescription":{"text":""},"id":"5350","name":""}],"version":"21.1.8"}}}],"version":"2.1.0"}`

// clangSARIFStderrFull is the complete stderr from Clang SARIF mode:
// blank line, SARIF JSON, blank line, "1 warning generated."
const clangSARIFStderrFull = "\n" + clangSARIFWarning + "\n\n1 warning generated.\n"

func TestClangParserSARIFIntegration(t *testing.T) {
	parser := &ClangParser{}

	t.Run("SARIF on stdout parsed correctly", func(t *testing.T) {
		// Real Clang SARIF warning output parsed via format auto-detection.
		diags, err := parser.Parse(clangSARIFWarning, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/tmp/test_diag.cpp")
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'unused_var'")
		assertDiagField(t, "Code", d.Code, "7538")
		assertDiagField(t, "Source", d.Source, "clang")
	})

	t.Run("SARIF on stderr with empty stdout", func(t *testing.T) {
		// Clang SARIF goes to stderr. Parser falls back to stderr when
		// stdout is empty.
		diags, err := parser.Parse("", clangSARIFStderrFull)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic from stderr fallback, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/tmp/test_diag.cpp")
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'unused_var'")
	})

	t.Run("non-structured stderr returns nil", func(t *testing.T) {
		// When Clang rejects a flag, stderr has plain text, not JSON.
		// Neither stream has structured content → nil.
		stderr := `error: invalid value 'json' in '-fdiagnostics-format json'`
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil (no structured content), got %d diags", len(diags))
		}
	})
}

func TestGCCParserStderrFallback(t *testing.T) {
	// GCC 15 writes -fdiagnostics-format=json output to STDERR, not stdout.
	// GCCParser now falls back to stderr when stdout is empty.
	gccJSONStderr := `[{"kind": "warning",
  "message": "unused variable 'unused_var'",
  "option": "-Wunused-variable",
  "option_url": "https://gcc.gnu.org/onlinedocs/gcc-15.2.0/gcc/Warning-Options.html#index-Wno-unused-variable",
  "children": [],
  "column-origin": 1,
  "locations": [{"caret": {"file": "/tmp/test_diag.cpp",
                           "line": 2,
                           "display-column": 9,
                           "byte-column": 9,
                           "column": 9},
                 "finish": {"file": "/tmp/test_diag.cpp",
                            "line": 2,
                            "display-column": 18,
                            "byte-column": 18,
                            "column": 18}}],
  "escape-source": false}]`

	gccParser := &GCCParser{}

	t.Run("GCC JSON on stderr is parsed by GCCParser", func(t *testing.T) {
		diags, err := gccParser.Parse("", gccJSONStderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'unused_var'")
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
	})

	t.Run("GCC JSON on stdout works", func(t *testing.T) {
		diags, err := gccParser.Parse(gccJSONStderr, "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'unused_var'")
		assertDiagSeverity(t, diags[0].Severity, SeverityWarning)
	})
}

func TestSARIFFormatDetection(t *testing.T) {
	// These tests validate the design's format detection heuristic:
	// first non-whitespace '{' = SARIF, '[' = native JSON.

	t.Run("SARIF starts with brace", func(t *testing.T) {
		if clangSARIFWarning[0] != '{' {
			t.Errorf("expected SARIF to start with '{', got %q", string(clangSARIFWarning[0]))
		}
	})

	t.Run("SARIF stderr has leading blank line", func(t *testing.T) {
		// The real Clang output has a leading \n before the SARIF JSON.
		// TrimSpace is required before format detection.
		trimmed := strings.TrimSpace(clangSARIFStderrFull)
		if trimmed[0] != '{' {
			t.Errorf("after TrimSpace, expected '{', got %q", string(trimmed[0]))
		}
	})

	t.Run("SARIF stderr has trailing text after JSON", func(t *testing.T) {
		// "1 warning generated." appears after the SARIF JSON.
		// splitJSONObjects must handle this trailing text.
		if !strings.Contains(clangSARIFStderrFull, "1 warning generated.") {
			t.Error("expected trailing 'N warning generated.' in stderr")
		}
	})

	t.Run("Clang ruleId is numeric not -W flag", func(t *testing.T) {
		// Clang uses numeric ruleIds like "7538", not "-Wunused-variable".
		// This maps to Diagnostic.Code but is less human-readable than GCC's format.
		if !strings.Contains(clangSARIFWarning, `"ruleId":"7538"`) {
			t.Error("expected numeric ruleId in Clang SARIF")
		}
	})
}

func TestClangParserSARIFWithCMakeReconfigure(t *testing.T) {
	// When CMakeLists.txt changes, cmake --build triggers a reconfigure
	// before the actual build. The reconfigure output (-- prefixed lines)
	// gets mixed into the stderr stream alongside clang's SARIF diagnostics.
	parser := &ClangParser{}

	t.Run("SARIF on stderr with cmake reconfigure noise", func(t *testing.T) {
		stderr := "[1/5] Re-running CMake...\n" +
			"-- The CXX compiler identification is Clang 19.0.0\n" +
			"-- Git submodule detected\n" +
			"-- Fusion installation enabled\n" +
			"-- Configuring done (0.5s)\n" +
			"-- Generating done (0.0s)\n" +
			"-- Build files have been written to: /tmp/build\n" +
			"[2/5] Building CXX object CMakeFiles/main.dir/test.cpp.o\n" +
			"\n" + clangSARIFWarning + "\n\n" +
			"1 warning generated.\n" +
			"[3/5] Linking CXX executable main\n" +
			"ninja: build stopped: subcommand failed."

		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		d := diags[0]
		assertDiagField(t, "File", d.File, "/tmp/test_diag.cpp")
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'unused_var'")
	})

	t.Run("SARIF on stderr with cmake noise on stdout", func(t *testing.T) {
		// CMake reconfigure output on stdout, SARIF on stderr.
		// Parser should skip stdout noise and find SARIF on stderr.
		stdout := "-- Configuring done\n-- Generating done\n-- Build files have been written to: /tmp/build\n"
		stderr := "\n" + clangSARIFWarning + "\n\n1 warning generated.\n"

		diags, err := parser.Parse(stdout, stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'unused_var'")
	})
}

func TestGCCParserWithCMakeReconfigure(t *testing.T) {
	// Same scenario as above but with GCC JSON diagnostics.
	gccJSON := `[{"kind": "warning",
  "message": "unused variable 'x'",
  "option": "-Wunused-variable",
  "children": [],
  "column-origin": 1,
  "locations": [{"caret": {"file": "test.cpp", "line": 2, "column": 9}}]}]`

	parser := &GCCParser{}

	t.Run("GCC JSON on stderr with cmake noise on stdout", func(t *testing.T) {
		stdout := "-- Configuring done\n-- Generating done\n"
		diags, err := parser.Parse(stdout, gccJSON)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'x'")
	})

	t.Run("GCC JSON on stderr with cmake noise on both streams", func(t *testing.T) {
		stdout := "-- The CXX compiler identification is GCC 15.2.0\n-- Configuring done\n"
		stderr := "-- Build files have been written to: /tmp/build\n" + gccJSON + "\n"
		diags, err := parser.Parse(stdout, stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "unused variable 'x'")
	})
}
