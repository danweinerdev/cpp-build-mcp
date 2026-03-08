package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
