package changes

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestDetectMtimeAllFiles(t *testing.T) {
	// Create a temp dir with some C/C++ files and a non-C++ file.
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	if err := os.Mkdir(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}

	// Create source files.
	files := map[string]string{
		"main.cpp":    "int main() {}",
		"util.h":      "#pragma once",
		"readme.txt":  "not a source file",
		"lib/core.cc": "void core() {}",
	}
	for path, content := range files {
		full := filepath.Join(tmpDir, path)
		dir := filepath.Dir(full)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	// Also create a C++ file inside the build directory — should be excluded.
	if err := os.WriteFile(filepath.Join(buildDir, "generated.cpp"), []byte("// gen"), 0o644); err != nil {
		t.Fatalf("write generated.cpp: %v", err)
	}

	// With zero time, all C/C++ files should be returned.
	got, method, err := DetectChanges(tmpDir, buildDir, time.Time{})
	if err != nil {
		t.Fatalf("DetectChanges: %v", err)
	}
	if method != "mtime" && method != "git" {
		t.Fatalf("expected method mtime or git, got %s", method)
	}

	// If git was used, it will return all tracked files (which may differ).
	// For a temp dir, git should fail and fall back to mtime.
	if method == "mtime" {
		sort.Strings(got)
		expected := []string{"lib/core.cc", "main.cpp", "util.h"}
		sort.Strings(expected)
		if len(got) != len(expected) {
			t.Fatalf("expected %d files, got %d: %v", len(expected), len(got), got)
		}
		for i, f := range expected {
			if got[i] != f {
				t.Fatalf("expected file %q at index %d, got %q", f, i, got[i])
			}
		}
	}
}

func TestDetectMtimeFilterByTime(t *testing.T) {
	tmpDir := t.TempDir()
	buildDir := filepath.Join(tmpDir, "build")
	if err := os.Mkdir(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}

	// Create a file with an old mtime.
	oldFile := filepath.Join(tmpDir, "old.cpp")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old.cpp: %v", err)
	}
	pastTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(oldFile, pastTime, pastTime); err != nil {
		t.Fatalf("chtimes old.cpp: %v", err)
	}

	// Record a "since" time between old and new files.
	since := time.Now().Add(-30 * time.Minute)

	// Create a new file (mtime will be now).
	newFile := filepath.Join(tmpDir, "new.cpp")
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatalf("write new.cpp: %v", err)
	}

	// Force mtime detection by using a non-git directory.
	files, err := detectMtime(tmpDir, buildDir, since)
	if err != nil {
		t.Fatalf("detectMtime: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d: %v", len(files), files)
	}
	if files[0] != "new.cpp" {
		t.Fatalf("expected new.cpp, got %s", files[0])
	}
}

func TestDetectGitUnavailable(t *testing.T) {
	// detectGit should fail when there is no .git directory.
	tmpDir := t.TempDir()
	_, err := detectGit(tmpDir, time.Time{})
	if err == nil {
		t.Fatal("expected error when .git does not exist")
	}
}
