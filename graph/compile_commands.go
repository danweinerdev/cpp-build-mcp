// Package graph provides compile_commands.json parsing and dependency graph construction.
package graph

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// GraphSummary holds a summary of the build graph derived from
// compile_commands.json.
type GraphSummary struct {
	Available        bool              `json:"available"`
	FileCount        int               `json:"file_count"`
	TranslationUnits []TranslationUnit `json:"translation_units,omitempty"`
	IncludeDirs      []string          `json:"include_dirs,omitempty"`
	Reason           string            `json:"reason,omitempty"`
}

// TranslationUnit represents a single source file in the build graph.
type TranslationUnit struct {
	File  string   `json:"file"`
	Flags []string `json:"flags"`
}

// compileEntry is the JSON structure of a single entry in compile_commands.json.
type compileEntry struct {
	Directory string `json:"directory"`
	Command   string `json:"command"`
	File      string `json:"file"`
}

// cppExtensions is the set of C/C++ file extensions used to count source files
// when compile_commands.json is unavailable.
var cppExtensions = map[string]bool{
	".c":   true,
	".cc":  true,
	".cpp": true,
	".cxx": true,
	".h":   true,
	".hpp": true,
	".hxx": true,
}

// ReadSummary reads and parses compile_commands.json from buildDir, extracting
// source files, include directories, and compiler flags. If the file is not
// found, it returns a summary with Available=false and a file count from
// walking sourceDir.
func ReadSummary(buildDir, sourceDir string) (*GraphSummary, error) {
	ccPath := filepath.Join(buildDir, "compile_commands.json")
	data, err := os.ReadFile(ccPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			count := countSourceFiles(sourceDir)
			return &GraphSummary{
				Available: false,
				FileCount: count,
				Reason:    "compile_commands.json not found",
			}, nil
		}
		return nil, err
	}

	var entries []compileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return &GraphSummary{
			Available: false,
			Reason:    "compile_commands.json parse error: " + err.Error(),
		}, nil
	}

	includeDirSet := make(map[string]bool)
	var units []TranslationUnit

	for _, entry := range entries {
		parts := splitCommand(entry.Command)
		var flags []string
		for i := 0; i < len(parts); i++ {
			p := parts[i]
			if p == "-I" || p == "-isystem" {
				if i+1 < len(parts) {
					includeDirSet[parts[i+1]] = true
					i++ // skip the next argument (the directory)
				}
			} else if strings.HasPrefix(p, "-I") {
				includeDirSet[p[2:]] = true
			} else if strings.HasPrefix(p, "-isystem") && len(p) > len("-isystem") {
				includeDirSet[p[len("-isystem"):]] = true
			} else {
				flags = append(flags, p)
			}
		}

		units = append(units, TranslationUnit{
			File:  entry.File,
			Flags: flags,
		})
	}

	var includeDirs []string
	for dir := range includeDirSet {
		includeDirs = append(includeDirs, dir)
	}

	return &GraphSummary{
		Available:        true,
		FileCount:        len(units),
		TranslationUnits: units,
		IncludeDirs:      includeDirs,
	}, nil
}

// splitCommand splits a compile command string into arguments, handling
// basic quoting.
func splitCommand(cmd string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range cmd {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == ' ' && !inSingle && !inDouble {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	// Skip the compiler name (first element) if present.
	if len(args) > 0 {
		args = args[1:]
	}
	// Remove the source file from flags (last arg matching a source extension
	// or -o and its argument, or -c).
	var filtered []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-o" {
			i++ // skip the output file
			continue
		}
		if a == "-c" {
			continue
		}
		// Skip the source file itself (it appears in the compile entry's File field).
		ext := filepath.Ext(a)
		if cppExtensions[ext] {
			continue
		}
		filtered = append(filtered, a)
	}

	return filtered
}

// countSourceFiles walks sourceDir and counts C/C++ source files.
func countSourceFiles(sourceDir string) int {
	count := 0
	filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if cppExtensions[ext] {
			count++
		}
		return nil
	})
	return count
}
