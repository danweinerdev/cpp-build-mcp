// Package changes detects source files that have changed since the last
// successful build using either git or filesystem mtime.
package changes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cppExtensions is the set of C/C++ file extensions to consider when detecting
// changed files via mtime.
var cppExtensions = map[string]bool{
	".c":   true,
	".cc":  true,
	".cpp": true,
	".cxx": true,
	".h":   true,
	".hpp": true,
	".hxx": true,
}

// DetectChanges returns the list of source files that have changed since the
// given timestamp. It first attempts to use git; if git is unavailable or the
// source directory is not a git repository, it falls back to mtime-based
// detection.
//
// If since is the zero time (no prior build), all source files are returned.
//
// The method return value is "git" or "mtime" indicating which detection
// strategy was used.
func DetectChanges(sourceDir, buildDir string, since time.Time) (files []string, method string, err error) {
	files, err = detectGit(sourceDir, since)
	if err == nil {
		return files, "git", nil
	}

	// Git detection failed; fall back to mtime.
	files, err = detectMtime(sourceDir, buildDir, since)
	if err != nil {
		return nil, "mtime", err
	}
	return files, "mtime", nil
}

// detectGit uses git to find changed files. It requires that git is installed
// and that sourceDir is inside a git repository.
func detectGit(sourceDir string, since time.Time) ([]string, error) {
	// Check that git is available.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}

	// Check that .git exists in sourceDir.
	if _, err := os.Stat(filepath.Join(sourceDir, ".git")); err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	if since.IsZero() {
		// No prior build: list all tracked files.
		cmd = exec.Command(gitPath, "ls-files")
	} else {
		// List files changed since the timestamp.
		sinceStr := since.Format(time.RFC3339)
		cmd = exec.Command(gitPath, "diff", "--name-only", "--diff-filter=ACMR",
			"--since="+sinceStr, "HEAD")
	}
	cmd.Dir = sourceDir

	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// detectMtime walks the source directory and returns C/C++ files whose mtime
// is after since. If since is zero, all C/C++ files are returned.
func detectMtime(sourceDir, buildDir string, since time.Time) ([]string, error) {
	absBuildDir, err := filepath.Abs(buildDir)
	if err != nil {
		return nil, err
	}

	var files []string
	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip the build directory.
		absPath, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		if info.IsDir() && absPath == absBuildDir {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !cppExtensions[ext] {
			return nil
		}

		if since.IsZero() || info.ModTime().After(since) {
			// Use relative path from sourceDir for consistency.
			rel, relErr := filepath.Rel(sourceDir, path)
			if relErr != nil {
				rel = path
			}
			files = append(files, rel)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}
