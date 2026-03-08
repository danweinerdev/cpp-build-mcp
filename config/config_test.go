package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Run("valid JSON with all fields", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "out",
			"source_dir": "src",
			"toolchain": "clang",
			"generator": "make",
			"cmake_args": ["-DCMAKE_EXPORT_COMPILE_COMMANDS=ON", "-DBUILD_TESTS=ON"],
			"build_timeout": "10m",
			"inject_diagnostic_flags": false,
			"diagnostic_serial_build": true
		}`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "out")
		assertEqual(t, "SourceDir", cfg.SourceDir, "src")
		assertEqual(t, "Toolchain", cfg.Toolchain, "clang")
		assertEqual(t, "Generator", cfg.Generator, "make")
		if len(cfg.CMakeArgs) != 2 {
			t.Fatalf("CMakeArgs: got %d elements, want 2", len(cfg.CMakeArgs))
		}
		assertEqual(t, "CMakeArgs[0]", cfg.CMakeArgs[0], "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")
		assertEqual(t, "CMakeArgs[1]", cfg.CMakeArgs[1], "-DBUILD_TESTS=ON")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 10*time.Minute)
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, false)
		assertBool(t, "DiagnosticSerialBuild", cfg.DiagnosticSerialBuild, true)
	})

	t.Run("missing file returns defaults", func(t *testing.T) {
		dir := t.TempDir()

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "build")
		assertEqual(t, "SourceDir", cfg.SourceDir, ".")
		assertEqual(t, "Toolchain", cfg.Toolchain, "auto")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		if cfg.CMakeArgs != nil {
			t.Errorf("CMakeArgs: got %v, want nil", cfg.CMakeArgs)
		}
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 5*time.Minute)
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, true)
		assertBool(t, "DiagnosticSerialBuild", cfg.DiagnosticSerialBuild, false)
	})

	t.Run("env var overrides take precedence over JSON", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "out",
			"source_dir": "src",
			"toolchain": "gcc",
			"generator": "make",
			"build_timeout": "3m"
		}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")
		t.Setenv("CPP_BUILD_MCP_SOURCE_DIR", "env-src")
		t.Setenv("CPP_BUILD_MCP_TOOLCHAIN", "clang")
		t.Setenv("CPP_BUILD_MCP_GENERATOR", "ninja")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "15m")

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "env-build")
		assertEqual(t, "SourceDir", cfg.SourceDir, "env-src")
		assertEqual(t, "Toolchain", cfg.Toolchain, "clang")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 15*time.Minute)
	})

	t.Run("env vars override defaults when no file exists", func(t *testing.T) {
		dir := t.TempDir()

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "custom-build")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "30s")

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "custom-build")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 30*time.Second)
		// Non-overridden fields keep defaults.
		assertEqual(t, "Toolchain", cfg.Toolchain, "auto")
	})

	t.Run("empty env vars do not override", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"build_dir": "out"}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "")

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "out")
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{not valid json}`)

		_, err := Load(dir)
		if err == nil {
			t.Fatal("Load() should have returned an error for invalid JSON")
		}
		// Verify the error message is descriptive.
		if got := err.Error(); got == "" {
			t.Fatal("error message should not be empty")
		}
	})

	t.Run("partial JSON fills remaining fields with defaults", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"toolchain": "gcc",
			"build_timeout": "2m"
		}`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// Explicitly set fields.
		assertEqual(t, "Toolchain", cfg.Toolchain, "gcc")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 2*time.Minute)

		// Fields not in JSON should be defaults.
		assertEqual(t, "BuildDir", cfg.BuildDir, "build")
		assertEqual(t, "SourceDir", cfg.SourceDir, ".")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, true)
		assertBool(t, "DiagnosticSerialBuild", cfg.DiagnosticSerialBuild, false)
	})

	t.Run("BuildTimeout parsing with various duration strings", func(t *testing.T) {
		cases := []struct {
			input string
			want  time.Duration
		}{
			{"10m", 10 * time.Minute},
			{"30s", 30 * time.Second},
			{"2m30s", 2*time.Minute + 30*time.Second},
			{"1h", time.Hour},
			{"500ms", 500 * time.Millisecond},
		}

		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				dir := t.TempDir()
				writeConfig(t, dir, `{"build_timeout": "`+tc.input+`"}`)

				cfg, err := Load(dir)
				if err != nil {
					t.Fatalf("Load() returned error: %v", err)
				}

				assertDuration(t, "BuildTimeout", cfg.BuildTimeout, tc.want)
			})
		}
	})

	t.Run("invalid BuildTimeout in JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"build_timeout": "not-a-duration"}`)

		_, err := Load(dir)
		if err == nil {
			t.Fatal("Load() should have returned an error for invalid build_timeout")
		}
	})

	t.Run("preset field parsed from JSON", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"preset": "debug"
		}`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "Preset", cfg.Preset, "debug")
		// Other fields should keep defaults.
		assertEqual(t, "BuildDir", cfg.BuildDir, "build")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
	})

	t.Run("preset defaults to empty string when not specified", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "out"
		}`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "Preset", cfg.Preset, "")
	})

	t.Run("preset defaults to empty string with no config file", func(t *testing.T) {
		dir := t.TempDir()

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		assertEqual(t, "Preset", cfg.Preset, "")
	})

	t.Run("invalid BuildTimeout in env var is ignored", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"build_timeout": "3m"}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "not-a-duration")

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// The JSON value should be preserved since the env var was invalid.
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 3*time.Minute)
	})
}

func TestLoadMulti(t *testing.T) {
	t.Run("single config backward compat", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "out",
			"source_dir": "src",
			"toolchain": "clang",
			"generator": "make",
			"cmake_args": ["-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"],
			"build_timeout": "10m",
			"inject_diagnostic_flags": false,
			"diagnostic_serial_build": true
		}`)

		configs, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "default")
		if len(configs) != 1 {
			t.Fatalf("got %d configs, want 1", len(configs))
		}
		cfg, ok := configs["default"]
		if !ok {
			t.Fatal("missing 'default' config entry")
		}

		assertEqual(t, "BuildDir", cfg.BuildDir, "out")
		assertEqual(t, "SourceDir", cfg.SourceDir, "src")
		assertEqual(t, "Toolchain", cfg.Toolchain, "clang")
		assertEqual(t, "Generator", cfg.Generator, "make")
		if len(cfg.CMakeArgs) != 1 {
			t.Fatalf("CMakeArgs: got %d elements, want 1", len(cfg.CMakeArgs))
		}
		assertEqual(t, "CMakeArgs[0]", cfg.CMakeArgs[0], "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 10*time.Minute)
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, false)
		assertBool(t, "DiagnosticSerialBuild", cfg.DiagnosticSerialBuild, true)
	})

	t.Run("missing file returns defaults", func(t *testing.T) {
		dir := t.TempDir()

		configs, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "default")
		if len(configs) != 1 {
			t.Fatalf("got %d configs, want 1", len(configs))
		}
		cfg := configs["default"]
		assertEqual(t, "BuildDir", cfg.BuildDir, "build")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, true)
	})

	t.Run("multi config with inheritance", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"source_dir": ".",
			"generator": "ninja",
			"toolchain": "clang",
			"build_timeout": "10m",
			"inject_diagnostic_flags": true,
			"configs": {
				"debug": {
					"build_dir": "build/debug",
					"cmake_args": ["-DCMAKE_BUILD_TYPE=Debug"]
				},
				"release": {
					"build_dir": "build/release",
					"cmake_args": ["-DCMAKE_BUILD_TYPE=Release"]
				}
			},
			"default_config": "debug"
		}`)

		configs, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "debug")
		if len(configs) != 2 {
			t.Fatalf("got %d configs, want 2", len(configs))
		}

		debug := configs["debug"]
		release := configs["release"]

		// Per-config overrides.
		assertEqual(t, "debug.BuildDir", debug.BuildDir, "build/debug")
		assertEqual(t, "release.BuildDir", release.BuildDir, "build/release")
		if len(debug.CMakeArgs) != 1 || debug.CMakeArgs[0] != "-DCMAKE_BUILD_TYPE=Debug" {
			t.Errorf("debug.CMakeArgs: got %v, want [-DCMAKE_BUILD_TYPE=Debug]", debug.CMakeArgs)
		}
		if len(release.CMakeArgs) != 1 || release.CMakeArgs[0] != "-DCMAKE_BUILD_TYPE=Release" {
			t.Errorf("release.CMakeArgs: got %v, want [-DCMAKE_BUILD_TYPE=Release]", release.CMakeArgs)
		}

		// Inherited top-level fields.
		assertEqual(t, "debug.SourceDir", debug.SourceDir, ".")
		assertEqual(t, "debug.Generator", debug.Generator, "ninja")
		assertEqual(t, "debug.Toolchain", debug.Toolchain, "clang")
		assertDuration(t, "debug.BuildTimeout", debug.BuildTimeout, 10*time.Minute)
		assertBool(t, "debug.InjectDiagnosticFlags", debug.InjectDiagnosticFlags, true)

		assertEqual(t, "release.SourceDir", release.SourceDir, ".")
		assertEqual(t, "release.Generator", release.Generator, "ninja")
		assertEqual(t, "release.Toolchain", release.Toolchain, "clang")
		assertDuration(t, "release.BuildTimeout", release.BuildTimeout, 10*time.Minute)
		assertBool(t, "release.InjectDiagnosticFlags", release.InjectDiagnosticFlags, true)
	})

	t.Run("cmake_args replace semantics not append", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"cmake_args": ["-DA=1"],
			"configs": {
				"custom": {
					"cmake_args": ["-DB=2"]
				}
			}
		}`)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		cfg := configs["custom"]
		if len(cfg.CMakeArgs) != 1 {
			t.Fatalf("CMakeArgs: got %d elements, want 1 (replace, not append)", len(cfg.CMakeArgs))
		}
		assertEqual(t, "CMakeArgs[0]", cfg.CMakeArgs[0], "-DB=2")
	})

	t.Run("per-config override precedence", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "default-build",
			"generator": "make",
			"toolchain": "gcc",
			"build_timeout": "3m",
			"inject_diagnostic_flags": true,
			"diagnostic_serial_build": false,
			"configs": {
				"custom": {
					"build_dir": "custom-build",
					"generator": "ninja",
					"build_timeout": "15m",
					"inject_diagnostic_flags": false,
					"diagnostic_serial_build": true
				}
			}
		}`)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		cfg := configs["custom"]
		assertEqual(t, "BuildDir", cfg.BuildDir, "custom-build")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		assertEqual(t, "Toolchain", cfg.Toolchain, "gcc") // inherited, not overridden
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 15*time.Minute)
		assertBool(t, "InjectDiagnosticFlags", cfg.InjectDiagnosticFlags, false)
		assertBool(t, "DiagnosticSerialBuild", cfg.DiagnosticSerialBuild, true)
	})

	t.Run("value copy isolation", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"inject_diagnostic_flags": true,
			"cmake_args": ["-DSHARED=1"],
			"configs": {
				"a": {"build_dir": "build-a"},
				"b": {"build_dir": "build-b"}
			}
		}`)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		// Mutate scalar field on one config and verify the other is unaffected.
		configs["a"].InjectDiagnosticFlags = false

		assertBool(t, "a.InjectDiagnosticFlags", configs["a"].InjectDiagnosticFlags, false)
		assertBool(t, "b.InjectDiagnosticFlags", configs["b"].InjectDiagnosticFlags, true)

		// Mutate inherited CMakeArgs slice on one config and verify the other
		// is unaffected (no slice aliasing).
		if len(configs["a"].CMakeArgs) != 1 || len(configs["b"].CMakeArgs) != 1 {
			t.Fatalf("expected both configs to inherit 1 cmake arg, got a=%d b=%d",
				len(configs["a"].CMakeArgs), len(configs["b"].CMakeArgs))
		}
		configs["a"].CMakeArgs[0] = "-DMUTATED=1"

		assertEqual(t, "a.CMakeArgs[0]", configs["a"].CMakeArgs[0], "-DMUTATED=1")
		assertEqual(t, "b.CMakeArgs[0]", configs["b"].CMakeArgs[0], "-DSHARED=1")
	})

	t.Run("default_config omitted picks alphabetically first", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"zebra": {"build_dir": "z"},
				"alpha": {"build_dir": "a"},
				"mango": {"build_dir": "m"}
			}
		}`)

		_, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "alpha")
	})

	t.Run("empty configs map returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {}
		}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for empty configs map")
		}
	})

	t.Run("default_config not in configs map returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"debug": {}
			},
			"default_config": "nonexistent"
		}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for invalid default_config")
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{not valid json}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for invalid JSON")
		}
	})

	t.Run("invalid per-config entry returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"bad": {"build_timeout": "not-a-duration"}
			}
		}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for invalid per-config entry")
		}
	})

	t.Run("single config mode applies env vars", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{"build_dir": "out"}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "BuildDir", configs["default"].BuildDir, "env-build")
	})

	t.Run("multi config mode does not apply env vars", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "base",
			"configs": {
				"dev": {"build_dir": "dev-build"}
			}
		}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		// Env vars should NOT be applied in multi-config mode.
		assertEqual(t, "BuildDir", configs["dev"].BuildDir, "dev-build")
	})

	t.Run("preset field in multi-config entries", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"preset": "base-preset",
			"configs": {
				"debug": {
					"build_dir": "build/debug",
					"preset": "debug-preset"
				},
				"release": {
					"build_dir": "build/release"
				}
			},
			"default_config": "debug"
		}`)

		configs, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "debug")

		// debug overrides preset from per-config.
		assertEqual(t, "debug.Preset", configs["debug"].Preset, "debug-preset")
		assertEqual(t, "debug.BuildDir", configs["debug"].BuildDir, "build/debug")
		// release inherits preset from top-level.
		assertEqual(t, "release.Preset", configs["release"].Preset, "base-preset")
		assertEqual(t, "release.BuildDir", configs["release"].BuildDir, "build/release")
	})

	t.Run("preset in single-config via LoadMulti", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"preset": "my-preset",
			"build_dir": "out"
		}`)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "Preset", configs["default"].Preset, "my-preset")
	})

	t.Run("top-level cmake_args inherited when per-config omits them", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"cmake_args": ["-DA=1", "-DB=2"],
			"configs": {
				"inheritor": {
					"build_dir": "build/inheritor"
				}
			}
		}`)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		cfg := configs["inheritor"]
		if len(cfg.CMakeArgs) != 2 {
			t.Fatalf("CMakeArgs: got %d elements, want 2 (inherited from top-level)", len(cfg.CMakeArgs))
		}
		assertEqual(t, "CMakeArgs[0]", cfg.CMakeArgs[0], "-DA=1")
		assertEqual(t, "CMakeArgs[1]", cfg.CMakeArgs[1], "-DB=2")
	})
}

func TestLoadMulti_DuplicateBuildDir(t *testing.T) {
	t.Run("two configs with same build_dir produce error naming both", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"debug": {"build_dir": "build"},
				"release": {"build_dir": "build"}
			}
		}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for duplicate build_dir")
		}

		errMsg := err.Error()
		// Names should be sorted, so "debug" comes before "release".
		wantMsg := `configurations "debug" and "release" share build_dir "build" — each configuration must have a unique build_dir`
		if errMsg != wantMsg {
			t.Errorf("error message mismatch:\ngot:  %s\nwant: %s", errMsg, wantMsg)
		}
	})

	t.Run("duplicate build_dir from inherited default", func(t *testing.T) {
		dir := t.TempDir()
		// Both configs inherit build_dir "build" from defaults (neither overrides it).
		writeConfig(t, dir, `{
			"configs": {
				"alpha": {},
				"beta": {}
			}
		}`)

		_, _, err := LoadMulti(dir)
		if err == nil {
			t.Fatal("LoadMulti() should have returned an error for duplicate inherited build_dir")
		}

		errMsg := err.Error()
		if !strings.Contains(errMsg, `"alpha"`) || !strings.Contains(errMsg, `"beta"`) {
			t.Errorf("error should name both configs, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, `"build"`) {
			t.Errorf("error should name the shared build_dir, got: %s", errMsg)
		}
	})

	t.Run("unique build_dirs pass validation", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"debug": {"build_dir": "build/debug"},
				"release": {"build_dir": "build/release"}
			}
		}`)

		_, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}
	})
}

func TestLoadMulti_EnvVarWarningInMultiConfig(t *testing.T) {
	t.Run("env vars ignored and warning emitted in multi-config mode", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "base",
			"source_dir": "src",
			"toolchain": "gcc",
			"generator": "make",
			"build_timeout": "3m",
			"configs": {
				"dev": {
					"build_dir": "dev-build",
					"source_dir": "dev-src"
				}
			}
		}`)

		// Set env vars that would normally override config values.
		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")
		t.Setenv("CPP_BUILD_MCP_SOURCE_DIR", "env-src")
		t.Setenv("CPP_BUILD_MCP_TOOLCHAIN", "clang")
		t.Setenv("CPP_BUILD_MCP_GENERATOR", "ninja")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "15m")

		// Capture slog output.
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		cfg := configs["dev"]

		// Verify env vars were NOT applied — values should come from JSON.
		assertEqual(t, "BuildDir", cfg.BuildDir, "dev-build")
		assertEqual(t, "SourceDir", cfg.SourceDir, "dev-src")
		assertEqual(t, "Toolchain", cfg.Toolchain, "gcc")
		assertEqual(t, "Generator", cfg.Generator, "make")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 3*time.Minute)

		// Verify slog.Warn was emitted.
		logOutput := buf.String()
		if !strings.Contains(logOutput, "environment variable overrides ignored in multi-config mode") {
			t.Errorf("expected warning about ignored env vars in log output, got: %q", logOutput)
		}
	})

	t.Run("no warning when no env vars are set in multi-config mode", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"dev": {"build_dir": "dev-build"}
			}
		}`)

		// Ensure none of the relevant env vars are set.
		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "")
		t.Setenv("CPP_BUILD_MCP_SOURCE_DIR", "")
		t.Setenv("CPP_BUILD_MCP_TOOLCHAIN", "")
		t.Setenv("CPP_BUILD_MCP_GENERATOR", "")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "")

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		_, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		logOutput := buf.String()
		if strings.Contains(logOutput, "environment variable overrides ignored") {
			t.Errorf("unexpected warning when no env vars are set: %q", logOutput)
		}
	})

	t.Run("warning emitted when only one env var is set", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"configs": {
				"dev": {"build_dir": "dev-build"}
			}
		}`)

		// Set only one env var.
		t.Setenv("CPP_BUILD_MCP_TOOLCHAIN", "clang")

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		_, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		logOutput := buf.String()
		if !strings.Contains(logOutput, "environment variable overrides ignored in multi-config mode") {
			t.Errorf("expected warning about ignored env vars, got: %q", logOutput)
		}
	})
}

func TestLoadMulti_EnvVarsAppliedInSingleConfigMode(t *testing.T) {
	t.Run("env vars applied in single-config mode via LoadMulti", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, `{
			"build_dir": "out",
			"source_dir": "src",
			"toolchain": "gcc",
			"generator": "make",
			"build_timeout": "3m"
		}`)

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")
		t.Setenv("CPP_BUILD_MCP_SOURCE_DIR", "env-src")
		t.Setenv("CPP_BUILD_MCP_TOOLCHAIN", "clang")
		t.Setenv("CPP_BUILD_MCP_GENERATOR", "ninja")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "15m")

		configs, defaultName, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		assertEqual(t, "defaultName", defaultName, "default")
		cfg := configs["default"]

		// Env vars should override JSON values in single-config mode.
		assertEqual(t, "BuildDir", cfg.BuildDir, "env-build")
		assertEqual(t, "SourceDir", cfg.SourceDir, "env-src")
		assertEqual(t, "Toolchain", cfg.Toolchain, "clang")
		assertEqual(t, "Generator", cfg.Generator, "ninja")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 15*time.Minute)
	})

	t.Run("env vars applied when no config file exists via LoadMulti", func(t *testing.T) {
		dir := t.TempDir()

		t.Setenv("CPP_BUILD_MCP_BUILD_DIR", "env-build")
		t.Setenv("CPP_BUILD_MCP_BUILD_TIMEOUT", "30s")

		configs, _, err := LoadMulti(dir)
		if err != nil {
			t.Fatalf("LoadMulti() returned error: %v", err)
		}

		cfg := configs["default"]
		assertEqual(t, "BuildDir", cfg.BuildDir, "env-build")
		assertDuration(t, "BuildTimeout", cfg.BuildTimeout, 30*time.Second)
		// Non-overridden fields keep defaults.
		assertEqual(t, "Toolchain", cfg.Toolchain, "auto")
	})
}

// writeConfig writes JSON content to .cpp-build-mcp.json in dir.
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, ".cpp-build-mcp.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
}

// assertEqual checks string equality and reports the field name on mismatch.
func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", field, got, want)
	}
}

// assertDuration checks duration equality and reports the field name on mismatch.
func assertDuration(t *testing.T, field string, got, want time.Duration) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", field, got, want)
	}
}

// assertBool checks bool equality and reports the field name on mismatch.
func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", field, got, want)
	}
}
