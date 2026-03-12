package diagnostics

import "testing"

func TestRegexParser_Parse(t *testing.T) {
	parser := &RegexParser{}

	t.Run("MSVC error with column", func(t *testing.T) {
		stderr := "main.cpp(10,5): error C2065: 'foo': undeclared identifier"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 10)
		assertDiagInt(t, "Column", d.Column, 5)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Code", d.Code, "C2065")
		assertDiagField(t, "Message", d.Message, "'foo': undeclared identifier")
		assertDiagField(t, "Source", d.Source, "msvc")
	})

	t.Run("MSVC warning without column", func(t *testing.T) {
		stderr := "main.cpp(10): warning C4996: deprecated function"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 10)
		assertDiagInt(t, "Column", d.Column, 0)
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Code", d.Code, "C4996")
		assertDiagField(t, "Message", d.Message, "deprecated function")
		assertDiagField(t, "Source", d.Source, "msvc")
	})

	t.Run("GCC/Clang error", func(t *testing.T) {
		stderr := "main.cpp:10:5: error: use of undeclared identifier 'x'"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 10)
		assertDiagInt(t, "Column", d.Column, 5)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "use of undeclared identifier 'x'")
		assertDiagField(t, "Source", d.Source, "compiler")
	})

	t.Run("GCC/Clang warning", func(t *testing.T) {
		stderr := "main.cpp:20:1: warning: unused variable 'y'"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 20)
		assertDiagInt(t, "Column", d.Column, 1)
		assertDiagSeverity(t, d.Severity, SeverityWarning)
		assertDiagField(t, "Message", d.Message, "unused variable 'y'")
		assertDiagField(t, "Source", d.Source, "compiler")
	})

	t.Run("GCC fatal error", func(t *testing.T) {
		stderr := "main.cpp:1:10: fatal error: nosuchfile.h: No such file"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 1)
		assertDiagInt(t, "Column", d.Column, 10)
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Message", d.Message, "nosuchfile.h: No such file")
		assertDiagField(t, "Source", d.Source, "compiler")
	})

	t.Run("mixed MSVC and GCC output", func(t *testing.T) {
		stderr := "main.cpp(10,5): error C2065: 'foo': undeclared identifier\n" +
			"other.cpp:20:1: warning: unused variable 'y'"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 2 {
			t.Fatalf("expected 2 diagnostics, got %d", len(diags))
		}

		assertDiagField(t, "Source[0]", diags[0].Source, "msvc")
		assertDiagField(t, "File[0]", diags[0].File, "main.cpp")
		assertDiagSeverity(t, diags[0].Severity, SeverityError)

		assertDiagField(t, "Source[1]", diags[1].Source, "compiler")
		assertDiagField(t, "File[1]", diags[1].File, "other.cpp")
		assertDiagSeverity(t, diags[1].Severity, SeverityWarning)
	})

	t.Run("non-matching lines ignored", func(t *testing.T) {
		stderr := "LINK : fatal error LNK1104: cannot open file 'kernel32.lib'\n" +
			"   |         ^~~~~~\n" +
			"   = note: some context\n" +
			"1 error generated."
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice for non-matching lines, got %d diagnostics", len(diags))
		}
	})

	t.Run("empty stderr returns nil", func(t *testing.T) {
		diags, err := parser.Parse("ignored stdout", "")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("note diagnostic", func(t *testing.T) {
		stderr := "main.cpp:5:1: note: declared here"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagField(t, "File", d.File, "main.cpp")
		assertDiagInt(t, "Line", d.Line, 5)
		assertDiagInt(t, "Column", d.Column, 1)
		assertDiagSeverity(t, d.Severity, SeverityNote)
		assertDiagField(t, "Message", d.Message, "declared here")
		assertDiagField(t, "Source", d.Source, "compiler")
	})

	t.Run("stdout is ignored", func(t *testing.T) {
		diags, err := parser.Parse("main.cpp:1:1: error: from stdout", "main.cpp:2:2: warning: from stderr")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic (stdout ignored), got %d", len(diags))
		}
		assertDiagField(t, "Message", diags[0].Message, "from stderr")
		assertDiagInt(t, "Line", diags[0].Line, 2)
	})

	t.Run("whitespace-only stderr returns nil", func(t *testing.T) {
		diags, err := parser.Parse("", "   \n\t  \n")
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if diags != nil {
			t.Fatalf("expected nil slice, got %v", diags)
		}
	})

	t.Run("make noise lines ignored in stderr", func(t *testing.T) {
		stderr := "[ 50%] Building CXX object CMakeFiles/main.dir/main.cpp.o\n" +
			"main.cpp:10:5: error: use of undeclared identifier 'x'\n" +
			"make[2]: *** [CMakeFiles/main.dir/main.cpp.o] Error 1\n" +
			"make: *** [all] Error 2"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic (make noise ignored), got %d", len(diags))
		}
		assertDiagField(t, "File", diags[0].File, "main.cpp")
		assertDiagInt(t, "Line", diags[0].Line, 10)
		assertDiagSeverity(t, diags[0].Severity, SeverityError)
		assertDiagField(t, "Message", diags[0].Message, "use of undeclared identifier 'x'")
	})

	t.Run("MSVC fatal error", func(t *testing.T) {
		stderr := "main.cpp(1,10): fatal error C1083: Cannot open include file"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagSeverity(t, d.Severity, SeverityError)
		assertDiagField(t, "Code", d.Code, "C1083")
		assertDiagField(t, "Source", d.Source, "msvc")
	})

	t.Run("MSVC note", func(t *testing.T) {
		stderr := "main.cpp(5,1): note C4000: see declaration"
		diags, err := parser.Parse("", stderr)
		if err != nil {
			t.Fatalf("Parse() returned error: %v", err)
		}
		if len(diags) != 1 {
			t.Fatalf("expected 1 diagnostic, got %d", len(diags))
		}

		d := diags[0]
		assertDiagSeverity(t, d.Severity, SeverityNote)
		assertDiagField(t, "Source", d.Source, "msvc")
	})
}
