package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// presetMetadata holds the minimal data cpp-build-mcp needs from a CMake
// configure preset. It is populated by resolving inheritance and macros in
// later processing steps (Tasks 2.2/2.3).
type presetMetadata struct {
	Name      string
	BinaryDir string
	Generator string
}

// presetsFile represents the top-level structure of a CMakePresets.json or
// CMakeUserPresets.json file. Only the fields relevant to cpp-build-mcp are
// included.
type presetsFile struct {
	Version          int               `json:"version"`
	Include          []string          `json:"include"`
	ConfigurePresets []configurePreset `json:"configurePresets"`
}

// configurePreset represents a single entry in the configurePresets array.
// The Inherits field uses json.RawMessage because the CMake spec allows it to
// be either a single string or an array of strings.
type configurePreset struct {
	Name      string          `json:"name"`
	BinaryDir string          `json:"binaryDir"`
	Generator string          `json:"generator"`
	Hidden    bool            `json:"hidden"`
	Inherits  json.RawMessage `json:"inherits"`
}

// readPresetsFile reads and parses a CMakePresets.json (or CMakeUserPresets.json)
// file at the given path.
//
// If the file does not exist, it returns (nil, nil) — a missing presets file
// is not an error. Invalid JSON returns an error.
func readPresetsFile(path string) (*presetsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading presets file %s: %w", path, err)
	}

	var pf presetsFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing presets file %s: %w", path, err)
	}

	return &pf, nil
}

// parseInherits parses the Inherits field of a configurePreset. The CMake spec
// allows it to be either a single string or an array of strings.
// If raw is nil or empty, it returns nil (no parents).
func parseInherits(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}

	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}

	return nil, fmt.Errorf("inherits: expected string or []string, got %s", string(raw))
}

// resolveInherits walks the inherits chains for all presets and copies
// BinaryDir and Generator from parent presets when the child has empty values.
// For multiple parents (array form), the first non-empty value wins
// (left-to-right precedence). The slice is modified in-place and returned.
//
// Circular inherits chains are detected and cause an error to be returned.
func resolveInherits(presets []configurePreset) ([]configurePreset, error) {
	byName := make(map[string]int, len(presets))
	for i, p := range presets {
		byName[p.Name] = i
	}

	resolved := make([]bool, len(presets))
	inStack := make([]bool, len(presets))

	var resolve func(idx int) error
	resolve = func(idx int) error {
		if resolved[idx] {
			return nil
		}
		if inStack[idx] {
			return fmt.Errorf("circular inherits involving %q", presets[idx].Name)
		}
		inStack[idx] = true

		parents, err := parseInherits(presets[idx].Inherits)
		if err != nil {
			return err
		}

		for _, parentName := range parents {
			pi, ok := byName[parentName]
			if !ok {
				continue // unknown parent — skip silently
			}
			if err := resolve(pi); err != nil {
				return err
			}
			if presets[idx].BinaryDir == "" {
				presets[idx].BinaryDir = presets[pi].BinaryDir
			}
			if presets[idx].Generator == "" {
				presets[idx].Generator = presets[pi].Generator
			}
		}

		inStack[idx] = false
		resolved[idx] = true
		return nil
	}

	for i := range presets {
		if err := resolve(i); err != nil {
			return nil, err
		}
	}
	return presets, nil
}

// expandBinaryDir replaces macros in binaryDir and normalizes the resulting
// path. The supported macros are ${sourceDir} and ${presetName}. If the
// expanded result still contains unresolved macros (${...} or $env{...}), an
// error is returned. If the expanded path is relative, it is joined with dir.
func expandBinaryDir(binaryDir, dir, presetName string) (string, error) {
	if binaryDir == "" {
		return "", nil
	}

	expanded := strings.ReplaceAll(binaryDir, "${sourceDir}", dir)
	expanded = strings.ReplaceAll(expanded, "${presetName}", presetName)

	if strings.Contains(expanded, "${") || strings.Contains(expanded, "$env{") {
		return "", fmt.Errorf("unresolvable macro in binaryDir %q (expanded: %q)", binaryDir, expanded)
	}

	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(dir, expanded)
	}

	return expanded, nil
}
