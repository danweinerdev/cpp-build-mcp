package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestReadSummaryValidFile(t *testing.T) {
	tmpDir := t.TempDir()

	entries := []compileEntry{
		{
			Directory: "/home/user/project/build",
			Command:   "/usr/bin/clang++ -I/usr/include -isystem /opt/boost/include -DFOO=1 -std=c++17 -c /home/user/project/src/main.cpp -o main.o",
			File:      "/home/user/project/src/main.cpp",
		},
		{
			Directory: "/home/user/project/build",
			Command:   "/usr/bin/clang++ -I/usr/include -Wall -c /home/user/project/src/util.cpp -o util.o",
			File:      "/home/user/project/src/util.cpp",
		},
	}

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "compile_commands.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	summary, err := ReadSummary(tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("ReadSummary: %v", err)
	}

	if !summary.Available {
		t.Fatal("expected available=true")
	}
	if summary.FileCount != 2 {
		t.Fatalf("expected file_count 2, got %d", summary.FileCount)
	}
	if len(summary.TranslationUnits) != 2 {
		t.Fatalf("expected 2 translation units, got %d", len(summary.TranslationUnits))
	}
	if summary.TranslationUnits[0].File != "/home/user/project/src/main.cpp" {
		t.Fatalf("expected first file main.cpp, got %s", summary.TranslationUnits[0].File)
	}

	// Check include dirs were extracted.
	sort.Strings(summary.IncludeDirs)
	if len(summary.IncludeDirs) != 2 {
		t.Fatalf("expected 2 include dirs, got %d: %v", len(summary.IncludeDirs), summary.IncludeDirs)
	}
	// Should contain /usr/include and /opt/boost/include.
	found := map[string]bool{}
	for _, d := range summary.IncludeDirs {
		found[d] = true
	}
	if !found["/usr/include"] {
		t.Fatal("expected /usr/include in include dirs")
	}
	if !found["/opt/boost/include"] {
		t.Fatal("expected /opt/boost/include in include dirs")
	}

	// Verify flags: should not contain -I, -isystem, -c, -o, or source file.
	for _, unit := range summary.TranslationUnits {
		for _, f := range unit.Flags {
			if f == "-c" || f == "-o" {
				t.Fatalf("unexpected flag %q in unit %s", f, unit.File)
			}
		}
	}
}

func TestReadSummaryMissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some source files in the "source" directory.
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"a.cpp", "b.h", "c.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	buildDir := filepath.Join(tmpDir, "build-missing")

	summary, err := ReadSummary(buildDir, srcDir)
	if err != nil {
		t.Fatalf("ReadSummary: %v", err)
	}

	if summary.Available {
		t.Fatal("expected available=false")
	}
	if summary.Reason != "compile_commands.json not found" {
		t.Fatalf("expected reason 'compile_commands.json not found', got %q", summary.Reason)
	}
	// Should have counted 2 source files (a.cpp, b.h — not c.txt).
	if summary.FileCount != 2 {
		t.Fatalf("expected file_count 2, got %d", summary.FileCount)
	}
}

func TestReadSummaryEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Write an empty JSON array.
	if err := os.WriteFile(filepath.Join(tmpDir, "compile_commands.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	summary, err := ReadSummary(tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("ReadSummary: %v", err)
	}

	if !summary.Available {
		t.Fatal("expected available=true for empty array")
	}
	if summary.FileCount != 0 {
		t.Fatalf("expected file_count 0, got %d", summary.FileCount)
	}
	if len(summary.TranslationUnits) != 0 {
		t.Fatalf("expected 0 translation units, got %d", len(summary.TranslationUnits))
	}
}
