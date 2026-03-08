package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// compile_commands.json detection tests
// ---------------------------------------------------------------------------

func TestDetectToolchainClangFromCompileCommands(t *testing.T) {
	dir := t.TempDir()
	entries := []compileCommandEntry{
		{
			Directory: "/home/user/project/build",
			Command:   "/usr/bin/clang++ -c -o main.o main.cpp",
			File:      "main.cpp",
		},
	}
	writeCompileCommands(t, dir, entries)

	got := DetectToolchain(dir)
	if got != "clang" {
		t.Fatalf("expected clang, got %q", got)
	}
}

func TestDetectToolchainClangFromArguments(t *testing.T) {
	dir := t.TempDir()
	entries := []compileCommandEntry{
		{
			Directory: "/home/user/project/build",
			Arguments: []string{"/usr/lib/llvm-14/bin/clang", "-c", "main.c"},
			File:      "main.c",
		},
	}
	writeCompileCommands(t, dir, entries)

	got := DetectToolchain(dir)
	if got != "clang" {
		t.Fatalf("expected clang, got %q", got)
	}
}

func TestDetectToolchainMSVCFromCompileCommands(t *testing.T) {
	dir := t.TempDir()
	entries := []compileCommandEntry{
		{
			Directory: "C:\\project\\build",
			Command:   "cl.exe /c /Fo main.obj main.cpp",
			File:      "main.cpp",
		},
	}
	writeCompileCommands(t, dir, entries)

	got := DetectToolchain(dir)
	if got != "msvc" {
		t.Fatalf("expected msvc, got %q", got)
	}
}

func TestDetectToolchainMSVCFromPath(t *testing.T) {
	dir := t.TempDir()
	// Use the arguments form since Windows paths with spaces cannot be
	// reliably split from a flat command string.
	entries := []compileCommandEntry{
		{
			Directory: "C:\\project\\build",
			Arguments: []string{"C:\\Program Files\\MSVC\\bin\\cl.exe", "/c", "main.cpp"},
			File:      "main.cpp",
		},
	}
	writeCompileCommands(t, dir, entries)

	got := DetectToolchain(dir)
	if got != "msvc" {
		t.Fatalf("expected msvc, got %q", got)
	}
}

func TestDetectToolchainMissingCompileCommands(t *testing.T) {
	dir := t.TempDir()

	got := DetectToolchain(dir)
	// Without compile_commands.json and no CC env or cc binary,
	// the result depends on what's on the system PATH.
	// We just verify it doesn't panic and returns a valid string.
	validResults := map[string]bool{
		"clang": true, "gcc": true, "gcc-legacy": true, "msvc": true, "unknown": true,
	}
	if !validResults[got] {
		t.Fatalf("unexpected result %q", got)
	}
}

func TestDetectToolchainEmptyCompileCommands(t *testing.T) {
	dir := t.TempDir()

	// Write an empty JSON array.
	if err := os.WriteFile(filepath.Join(dir, "compile_commands.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("writing compile_commands.json: %v", err)
	}

	got := DetectToolchain(dir)
	// Empty array falls through to env/PATH detection.
	validResults := map[string]bool{
		"clang": true, "gcc": true, "gcc-legacy": true, "msvc": true, "unknown": true,
	}
	if !validResults[got] {
		t.Fatalf("unexpected result %q", got)
	}
}

func TestDetectToolchainInvalidJSON(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "compile_commands.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("writing compile_commands.json: %v", err)
	}

	got := DetectToolchain(dir)
	// Invalid JSON falls through to env/PATH detection.
	validResults := map[string]bool{
		"clang": true, "gcc": true, "gcc-legacy": true, "msvc": true, "unknown": true,
	}
	if !validResults[got] {
		t.Fatalf("unexpected result %q", got)
	}
}

// ---------------------------------------------------------------------------
// classifyCompiler unit tests (no subprocess)
// ---------------------------------------------------------------------------

func TestClassifyCompiler(t *testing.T) {
	tests := []struct {
		name     string
		compiler string
		want     string
	}{
		{"clang path", "/usr/bin/clang++", "clang"},
		{"clang bare", "clang", "clang"},
		{"clang with version", "clang-14", "clang"},
		{"cl.exe", "cl.exe", "msvc"},
		{"cl bare", "cl", "msvc"},
		{"cl.exe full path", "C:\\Program Files\\MSVC\\bin\\cl.exe", "msvc"},
		{"unknown compiler", "/usr/bin/cc", ""},
		{"unknown path", "/opt/custom/mycc", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// classifyCompiler calls probeGCCVersion for gcc/g++, which
			// runs a subprocess. We skip those cases here and test them
			// separately via ParseGCCMajorVersion.
			got := classifyCompiler(tt.compiler)
			if got != tt.want {
				t.Fatalf("classifyCompiler(%q) = %q, want %q", tt.compiler, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractCompiler tests
// ---------------------------------------------------------------------------

func TestExtractCompiler(t *testing.T) {
	t.Run("from arguments", func(t *testing.T) {
		entry := compileCommandEntry{
			Arguments: []string{"/usr/bin/g++", "-c", "main.cpp"},
		}
		got := extractCompiler(entry)
		if got != "/usr/bin/g++" {
			t.Fatalf("expected /usr/bin/g++, got %q", got)
		}
	})

	t.Run("from command string", func(t *testing.T) {
		entry := compileCommandEntry{
			Command: "/usr/bin/clang++ -c -o main.o main.cpp",
		}
		got := extractCompiler(entry)
		if got != "/usr/bin/clang++" {
			t.Fatalf("expected /usr/bin/clang++, got %q", got)
		}
	})

	t.Run("arguments takes precedence over command", func(t *testing.T) {
		entry := compileCommandEntry{
			Arguments: []string{"/usr/bin/g++", "-c"},
			Command:   "/usr/bin/clang++ -c main.cpp",
		}
		got := extractCompiler(entry)
		if got != "/usr/bin/g++" {
			t.Fatalf("expected /usr/bin/g++ (from arguments), got %q", got)
		}
	})

	t.Run("empty entry returns empty", func(t *testing.T) {
		entry := compileCommandEntry{}
		got := extractCompiler(entry)
		if got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// GCC version parsing tests
// ---------------------------------------------------------------------------

func TestParseGCCMajorVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    int
		wantErr bool
	}{
		{
			name:   "Ubuntu GCC 10",
			output: "gcc (Ubuntu 10.3.0-1ubuntu1) 10.3.0\nCopyright (C) 2020 Free Software Foundation, Inc.",
			want:   10,
		},
		{
			name:   "GCC 9 legacy",
			output: "gcc (GCC) 9.4.0\nCopyright (C) 2019 Free Software Foundation, Inc.",
			want:   9,
		},
		{
			name:   "GCC 13",
			output: "gcc (GCC) 13.2.1 20230801\nCopyright (C) 2023 Free Software Foundation, Inc.",
			want:   13,
		},
		{
			name:   "GCC 4 very old",
			output: "gcc (Ubuntu 4.8.5-4ubuntu8) 4.8.5",
			want:   4,
		},
		{
			name:   "g++ version",
			output: "g++ (Ubuntu 11.4.0-1ubuntu1~22.04) 11.4.0",
			want:   11,
		},
		{
			name:   "Fedora GCC",
			output: "gcc (GCC) 14.1.1 20240507 (Red Hat 14.1.1-1)",
			want:   14,
		},
		{
			name:    "no version found",
			output:  "some unknown output with no version",
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGCCMajorVersion(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected major version %d, got %d", tt.want, got)
			}
		})
	}
}

func TestParseGCCMajorVersionThreshold(t *testing.T) {
	// Verify the >= 10 threshold boundary.
	t.Run("version 10 is modern", func(t *testing.T) {
		major, err := ParseGCCMajorVersion("gcc (GCC) 10.0.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if major < 10 {
			t.Fatal("expected major >= 10")
		}
	})

	t.Run("version 9 is legacy", func(t *testing.T) {
		major, err := ParseGCCMajorVersion("gcc (GCC) 9.5.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if major >= 10 {
			t.Fatal("expected major < 10")
		}
	})
}

// ---------------------------------------------------------------------------
// detectFromCompileCommands unit tests
// ---------------------------------------------------------------------------

func TestDetectFromCompileCommandsGCCWithArguments(t *testing.T) {
	// This test will attempt to run gcc --version. If gcc is not available,
	// we still verify the function doesn't panic and returns a gcc variant.
	dir := t.TempDir()
	entries := []compileCommandEntry{
		{
			Directory: "/home/user/project/build",
			Arguments: []string{"/usr/bin/gcc", "-c", "main.c"},
			File:      "main.c",
		},
	}
	writeCompileCommands(t, dir, entries)

	got := detectFromCompileCommands(dir)
	// gcc detection may return "gcc" or "gcc-legacy" depending on system gcc,
	// or "gcc" if gcc is not installed (assume modern on probe failure).
	if got != "gcc" && got != "gcc-legacy" {
		t.Fatalf("expected gcc or gcc-legacy, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// writeCompileCommands writes a compile_commands.json file to the given
// directory.
func writeCompileCommands(t *testing.T, dir string, entries []compileCommandEntry) {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshaling compile_commands.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compile_commands.json"), data, 0o644); err != nil {
		t.Fatalf("writing compile_commands.json: %v", err)
	}
}
