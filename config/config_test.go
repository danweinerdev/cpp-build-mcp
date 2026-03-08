package config

import (
	"os"
	"path/filepath"
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
