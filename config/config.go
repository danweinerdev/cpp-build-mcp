// Package config provides configuration loading and defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const configFileName = ".cpp-build-mcp.json"

// Config holds the project configuration for the build MCP server.
type Config struct {
	BuildDir              string        `json:"build_dir"`
	SourceDir             string        `json:"source_dir"`
	Toolchain             string        `json:"toolchain"`              // "auto", "clang", "gcc", "msvc"
	Generator             string        `json:"generator"`              // "ninja", "make"
	CMakeArgs             []string      `json:"cmake_args"`
	// BuildTimeout is stored as time.Duration internally. Note: marshaling Config
	// directly to JSON produces nanosecond integers, not duration strings. The
	// on-disk config file format uses configJSON for human-readable round-tripping.
	BuildTimeout time.Duration `json:"build_timeout"`
	InjectDiagnosticFlags bool          `json:"inject_diagnostic_flags"`
	DiagnosticSerialBuild bool          `json:"diagnostic_serial_build"`
}

// defaults returns a Config populated with default values.
func defaults() Config {
	return Config{
		BuildDir:              "build",
		SourceDir:             ".",
		Toolchain:             "auto",
		Generator:             "ninja",
		CMakeArgs:             nil,
		BuildTimeout:          5 * time.Minute,
		InjectDiagnosticFlags: true,
		DiagnosticSerialBuild: false,
	}
}

// configJSON is the on-disk representation of the config file. It mirrors
// Config but uses a string for BuildTimeout so we can parse duration strings
// like "10m" or "2m30s".
type configJSON struct {
	BuildDir              *string  `json:"build_dir"`
	SourceDir             *string  `json:"source_dir"`
	Toolchain             *string  `json:"toolchain"`
	Generator             *string  `json:"generator"`
	CMakeArgs             []string `json:"cmake_args"`
	BuildTimeout          *string  `json:"build_timeout"`
	InjectDiagnosticFlags *bool    `json:"inject_diagnostic_flags"`
	DiagnosticSerialBuild *bool    `json:"diagnostic_serial_build"`
}

// configFileJSON is the top-level on-disk file structure. It extends the
// per-config fields with optional multi-config support via a "configs" map
// and a "default_config" selector.
type configFileJSON struct {
	// Embed all per-config fields so single-config files parse unchanged.
	configJSON

	// Configs maps named configurations to partial config overlays.
	// When present, each entry inherits top-level defaults and overrides them.
	Configs map[string]json.RawMessage `json:"configs"`

	// DefaultConfig names the default configuration when configs is present.
	// If omitted, the alphabetically first config name is used.
	DefaultConfig string `json:"default_config"`
}

// Load reads the project configuration from dir. It looks for a file named
// .cpp-build-mcp.json in the given directory, applies defaults for any
// missing fields, and then applies environment variable overrides.
//
// If the config file does not exist, defaults are used without error.
// If the file exists but contains invalid JSON, an error is returned.
func Load(dir string) (*Config, error) {
	cfg := defaults()

	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("no config file found, using defaults", "path", path)
		} else {
			return nil, fmt.Errorf("reading config file %s: %w", path, err)
		}
	} else {
		if err := applyJSON(&cfg, data); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
		slog.Debug("loaded config file", "path", path)
	}

	applyEnv(&cfg)

	return &cfg, nil
}

// LoadMulti reads the project configuration from dir and returns a map of
// named configurations and the name of the default configuration.
//
// If the config file contains a "configs" map, each entry is parsed as a
// partial overlay on top of the top-level defaults. Per-config fields
// (build_dir, cmake_args, etc.) replace top-level values rather than
// appending to them.
//
// If the config file does not contain a "configs" map, a single config named
// "default" is returned with default name "default".
//
// Environment variable overrides are applied only in single-config mode
// (no "configs" map). Multi-config env var handling is deferred to the caller.
func LoadMulti(dir string) (map[string]*Config, string, error) {
	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("no config file found, using defaults", "path", path)
			cfg := defaults()
			applyEnv(&cfg)
			return map[string]*Config{"default": &cfg}, "default", nil
		}
		return nil, "", fmt.Errorf("reading config file %s: %w", path, err)
	}

	// Probe for multi-config structure.
	var file configFileJSON
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, "", fmt.Errorf("parsing config file %s: invalid JSON: %w", path, err)
	}

	// Single-config mode: no "configs" map present.
	if file.Configs == nil {
		cfg := defaults()
		if err := applyJSON(&cfg, data); err != nil {
			return nil, "", fmt.Errorf("parsing config file %s: %w", path, err)
		}
		applyEnv(&cfg)
		slog.Debug("loaded single config file", "path", path)
		return map[string]*Config{"default": &cfg}, "default", nil
	}

	// Multi-config mode: build a base config from top-level fields, then
	// overlay each named config entry.
	base := defaults()
	if err := applyJSON(&base, data); err != nil {
		return nil, "", fmt.Errorf("parsing config file %s: %w", path, err)
	}

	configs := make(map[string]*Config, len(file.Configs))
	for name, raw := range file.Configs {
		// Value copy of base so each config is independent.
		entry := base
		if err := applyJSON(&entry, raw); err != nil {
			return nil, "", fmt.Errorf("parsing config %q in %s: %w", name, path, err)
		}
		configs[name] = &entry
	}

	// Determine default config name.
	defaultName := file.DefaultConfig
	if defaultName == "" {
		// Pick alphabetically first.
		names := make([]string, 0, len(configs))
		for name := range configs {
			names = append(names, name)
		}
		sort.Strings(names)
		defaultName = names[0]
	} else if _, ok := configs[defaultName]; !ok {
		return nil, "", fmt.Errorf("parsing config file %s: default_config %q not found in configs map", path, defaultName)
	}

	slog.Debug("loaded multi-config file", "path", path, "configs", len(configs), "default", defaultName)
	return configs, defaultName, nil
}

// applyJSON unmarshals raw JSON data onto cfg, overriding only the fields
// that are present in the JSON.
func applyJSON(cfg *Config, data []byte) error {
	var raw configJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	if raw.BuildDir != nil {
		cfg.BuildDir = *raw.BuildDir
	}
	if raw.SourceDir != nil {
		cfg.SourceDir = *raw.SourceDir
	}
	if raw.Toolchain != nil {
		cfg.Toolchain = *raw.Toolchain
	}
	if raw.Generator != nil {
		cfg.Generator = *raw.Generator
	}
	if raw.CMakeArgs != nil {
		cfg.CMakeArgs = raw.CMakeArgs
	}
	if raw.BuildTimeout != nil {
		d, err := time.ParseDuration(*raw.BuildTimeout)
		if err != nil {
			return fmt.Errorf("invalid build_timeout %q: %w", *raw.BuildTimeout, err)
		}
		cfg.BuildTimeout = d
	}
	if raw.InjectDiagnosticFlags != nil {
		cfg.InjectDiagnosticFlags = *raw.InjectDiagnosticFlags
	}
	if raw.DiagnosticSerialBuild != nil {
		cfg.DiagnosticSerialBuild = *raw.DiagnosticSerialBuild
	}

	return nil
}

// applyEnv overrides config values with environment variables when they are
// set and non-empty.
func applyEnv(cfg *Config) {
	if v := os.Getenv("CPP_BUILD_MCP_BUILD_DIR"); v != "" {
		cfg.BuildDir = v
	}
	if v := os.Getenv("CPP_BUILD_MCP_SOURCE_DIR"); v != "" {
		cfg.SourceDir = v
	}
	if v := os.Getenv("CPP_BUILD_MCP_TOOLCHAIN"); v != "" {
		cfg.Toolchain = v
	}
	if v := os.Getenv("CPP_BUILD_MCP_GENERATOR"); v != "" {
		cfg.Generator = v
	}
	if v := os.Getenv("CPP_BUILD_MCP_BUILD_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			slog.Warn("ignoring invalid CPP_BUILD_MCP_BUILD_TIMEOUT", "value", v, "error", err)
		} else {
			cfg.BuildTimeout = d
		}
	}
}
