package diagnostics

import "testing"

func TestNewParser(t *testing.T) {
	tests := []struct {
		name      string
		toolchain string
		wantType  string
	}{
		{"clang", "clang", "*diagnostics.ClangParser"},
		{"clang_uppercase", "Clang", "*diagnostics.ClangParser"},
		{"gcc", "gcc", "*diagnostics.GCCParser"},
		{"gcc_legacy", "gcc-legacy", "*diagnostics.RegexParser"},
		{"msvc", "msvc", "*diagnostics.RegexParser"},
		{"empty", "", "*diagnostics.RegexParser"},
		{"unknown", "icc", "*diagnostics.RegexParser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(tt.toolchain)
			got := typeString(p)
			if got != tt.wantType {
				t.Errorf("NewParser(%q) = %s, want %s", tt.toolchain, got, tt.wantType)
			}
		})
	}
}

func typeString(p DiagnosticParser) string {
	switch p.(type) {
	case *ClangParser:
		return "*diagnostics.ClangParser"
	case *GCCParser:
		return "*diagnostics.GCCParser"
	case *RegexParser:
		return "*diagnostics.RegexParser"
	default:
		return "unknown"
	}
}

func TestParse(t *testing.T) {
	// Clang routing: valid JSON produces diagnostics
	stdout := `[{"file":"a.cpp","line":1,"column":1,"severity":"error","message":"oops","option":""}]`
	diags, err := Parse("clang", stdout, "")
	if err != nil {
		t.Fatalf("Parse(clang) error: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Parse(clang) got %d diagnostics, want 1", len(diags))
	}
	if diags[0].Source != "clang" {
		t.Errorf("Source = %q, want %q", diags[0].Source, "clang")
	}

	// GCC routing: GCC JSON parser reads from stdout
	gccJSON := `[{"kind":"error","message":"bad","locations":[{"caret":{"file":"foo.cpp","line":1,"column":1}}],"children":[]}]`
	diags, err = Parse("gcc", gccJSON, "")
	if err != nil {
		t.Fatalf("Parse(gcc) error: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Parse(gcc) got %d diagnostics, want 1", len(diags))
	}
	if diags[0].Source != "gcc" {
		t.Errorf("Source = %q, want %q", diags[0].Source, "gcc")
	}

	// GCC-legacy routing: regex parser extracts from stderr
	diags, err = Parse("gcc-legacy", "", "foo.cpp:1:1: error: bad")
	if err != nil {
		t.Fatalf("Parse(gcc-legacy) error: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Parse(gcc-legacy) got %d diagnostics, want 1", len(diags))
	}
	if diags[0].Source != "compiler" {
		t.Errorf("Source = %q, want %q", diags[0].Source, "compiler")
	}

	// MSVC routing: regex parser extracts diagnostic
	diags, err = Parse("msvc", "", "foo.cpp(1): error C1234: bad")
	if err != nil {
		t.Fatalf("Parse(msvc) error: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("Parse(msvc) got %d diagnostics, want 1", len(diags))
	}
	if diags[0].Source != "msvc" {
		t.Errorf("Source = %q, want %q", diags[0].Source, "msvc")
	}
}
