package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// presetMetadata holds the minimal data cpp-build-mcp needs from a CMake
// configure preset. It is populated by resolving inheritance and macros in
// later processing steps (Tasks 2.2/2.3).
type presetMetadata struct {
	Name      string
	BinaryDir string
	Generator string
	Toolchain string // "clang", "gcc", "msvc", or "auto"
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
	Name          string          `json:"name"`
	BinaryDir     string          `json:"binaryDir"`
	Generator     string          `json:"generator"`
	ToolchainFile string          `json:"toolchainFile"`
	Hidden        bool            `json:"hidden"`
	Inherits      json.RawMessage `json:"inherits"`
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
			if presets[idx].ToolchainFile == "" {
				presets[idx].ToolchainFile = presets[pi].ToolchainFile
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

// generatorNormalizeMap maps CMake generator names to the short names used
// in cpp-build-mcp configuration.
var generatorNormalizeMap = map[string]string{
	"Ninja":          "ninja",
	"Unix Makefiles": "make",
}

// normalizeGenerator returns the normalized short name for a CMake generator.
// Unknown or empty generators default to "ninja".
func normalizeGenerator(gen string) string {
	if short, ok := generatorNormalizeMap[gen]; ok {
		return short
	}
	return "ninja"
}

// classifyPresetToolchain derives a toolchain name from preset metadata.
// It checks (in priority order):
//  1. toolchainFile path — e.g., ".../Toolchains/clang.cmake" → "clang"
//  2. Preset name — e.g., "clang-Debug" → "clang"
//
// Returns "auto" if no signal is found, which falls back to runtime
// detection via compile_commands.json.
func classifyPresetToolchain(toolchainFile, presetName string) string {
	// 1. Check toolchainFile (most reliable signal).
	if toolchainFile != "" {
		lower := strings.ToLower(toolchainFile)
		if strings.Contains(lower, "clang") {
			return "clang"
		}
		if strings.Contains(lower, "gcc") {
			return "gcc"
		}
		if strings.Contains(lower, "msvc") || strings.Contains(lower, "cl.exe") {
			return "msvc"
		}
	}

	// 2. Check preset name.
	lower := strings.ToLower(presetName)
	if strings.Contains(lower, "clang") {
		return "clang"
	}
	if strings.Contains(lower, "gcc") {
		return "gcc"
	}
	if strings.Contains(lower, "msvc") {
		return "msvc"
	}

	return "auto"
}

// isMultiConfigGenerator returns true if the generator is a multi-config
// generator that should be excluded from preset discovery.
func isMultiConfigGenerator(gen string) bool {
	if gen == "Ninja Multi-Config" {
		return true
	}
	if strings.HasPrefix(gen, "Visual Studio") {
		return true
	}
	return false
}

// readUserPresets reads CMakeUserPresets.json from dir.
// Returns (nil, nil) if the file does not exist.
func readUserPresets(dir string) (*presetsFile, error) {
	return readPresetsFile(filepath.Join(dir, "CMakeUserPresets.json"))
}

// mergePresets produces a union of configure presets from project and user
// files. When a user preset has the same name as a project preset, the user
// preset replaces the project preset.
func mergePresets(project, user []configurePreset) []configurePreset {
	byName := make(map[string]int, len(project))
	merged := make([]configurePreset, len(project))
	copy(merged, project)
	for i, p := range merged {
		byName[p.Name] = i
	}
	for _, u := range user {
		if idx, ok := byName[u.Name]; ok {
			merged[idx] = u // user replaces project
		} else {
			merged = append(merged, u)
			byName[u.Name] = len(merged) - 1
		}
	}
	return merged
}

// loadPresetsMetadata reads CMakePresets.json (and optionally
// CMakeUserPresets.json) from dir, resolves inheritance, filters out hidden
// and multi-config generator presets, expands binaryDir macros, normalizes
// generator names, and validates binaryDir uniqueness.
//
// Returns nil, nil if no CMakePresets.json exists in dir.
func loadPresetsMetadata(dir string) ([]presetMetadata, error) {
	// 1. Read project presets file.
	projectFile, err := readPresetsFile(filepath.Join(dir, "CMakePresets.json"))
	if err != nil {
		return nil, err
	}
	if projectFile == nil {
		return nil, nil // no presets file
	}

	// 2. Read user presets file.
	userFile, err := readUserPresets(dir)
	if err != nil {
		return nil, err
	}

	// 3. Warn on include fields.
	if len(projectFile.Include) > 0 {
		slog.Warn("CMakePresets.json uses 'include' (v4+); included preset files are not read — some presets may not be discovered")
	}
	if userFile != nil && len(userFile.Include) > 0 {
		slog.Warn("CMakeUserPresets.json uses 'include' (v4+); included preset files are not read — some presets may not be discovered")
	}

	// 4. Merge user presets into project presets.
	allPresets := projectFile.ConfigurePresets
	if userFile != nil {
		allPresets = mergePresets(allPresets, userFile.ConfigurePresets)
	}

	// 5. Resolve inherits.
	allPresets, err = resolveInherits(allPresets)
	if err != nil {
		return nil, err
	}

	// 6-8. Filter and build result.
	// Use a non-nil empty slice to distinguish "file exists but all presets
	// were filtered" from "no presets file" (which returns nil).
	result := make([]presetMetadata, 0)
	for _, p := range allPresets {
		// 6. Remove hidden presets.
		if p.Hidden {
			slog.Debug("skipping hidden preset", "preset", p.Name)
			continue
		}

		// 7. Remove multi-config generator presets.
		if isMultiConfigGenerator(p.Generator) {
			slog.Info("skipping multi-config generator preset", "preset", p.Name, "generator", p.Generator)
			continue
		}

		// 8a. Expand binaryDir.
		bd, err := expandBinaryDir(p.BinaryDir, dir, p.Name)
		if err != nil {
			slog.Warn("skipping preset with unresolvable binaryDir", "preset", p.Name, "error", err)
			continue
		}

		// 8b. Skip presets with empty binaryDir.
		if bd == "" {
			slog.Warn("preset has no binaryDir", "preset", p.Name)
			continue
		}

		// 8c. Normalize generator.
		gen := normalizeGenerator(p.Generator)

		// 8d. Classify toolchain from preset metadata.
		tc := classifyPresetToolchain(p.ToolchainFile, p.Name)

		result = append(result, presetMetadata{
			Name:      p.Name,
			BinaryDir: bd,
			Generator: gen,
			Toolchain: tc,
		})
	}

	// 9. Validate binaryDir uniqueness.
	bdOwner := make(map[string]string, len(result))
	// Sort result by name first so error messages are deterministic.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	for _, pm := range result {
		if prev, ok := bdOwner[pm.BinaryDir]; ok {
			return nil, fmt.Errorf(
				"presets %q and %q share binaryDir %q — each preset must have a unique binaryDir",
				prev, pm.Name, pm.BinaryDir)
		}
		bdOwner[pm.BinaryDir] = pm.Name
	}

	return result, nil
}
