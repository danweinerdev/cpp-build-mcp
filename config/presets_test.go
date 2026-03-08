package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePresetsFile writes JSON content to a file named name in dir.
func writePresetsFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing presets file: %v", err)
	}
	return path
}

func TestReadPresetsFile(t *testing.T) {
	t.Run("valid file parses all fields", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja",
					"hidden": false,
					"inherits": "base"
				},
				{
					"name": "base",
					"binaryDir": "${sourceDir}/build/base",
					"generator": "Unix Makefiles",
					"hidden": true,
					"inherits": ["core", "platform"]
				}
			]
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}

		if pf.Version != 3 {
			t.Errorf("Version: got %d, want 3", pf.Version)
		}
		if len(pf.ConfigurePresets) != 2 {
			t.Fatalf("ConfigurePresets: got %d elements, want 2", len(pf.ConfigurePresets))
		}

		// First preset: debug.
		p0 := pf.ConfigurePresets[0]
		assertEqual(t, "preset[0].Name", p0.Name, "debug")
		assertEqual(t, "preset[0].BinaryDir", p0.BinaryDir, "${sourceDir}/build/debug")
		assertEqual(t, "preset[0].Generator", p0.Generator, "Ninja")
		assertBool(t, "preset[0].Hidden", p0.Hidden, false)

		// Second preset: base.
		p1 := pf.ConfigurePresets[1]
		assertEqual(t, "preset[1].Name", p1.Name, "base")
		assertEqual(t, "preset[1].BinaryDir", p1.BinaryDir, "${sourceDir}/build/base")
		assertEqual(t, "preset[1].Generator", p1.Generator, "Unix Makefiles")
		assertBool(t, "preset[1].Hidden", p1.Hidden, true)
	})

	t.Run("missing file returns nil without error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "CMakePresets.json")

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf != nil {
			t.Errorf("readPresetsFile() returned non-nil result for missing file: %+v", pf)
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{not valid json}`)

		pf, err := readPresetsFile(path)
		if err == nil {
			t.Fatal("readPresetsFile() should have returned an error for invalid JSON")
		}
		if pf != nil {
			t.Errorf("readPresetsFile() should return nil result on error, got: %+v", pf)
		}
	})

	t.Run("empty configurePresets array", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": []
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}
		if len(pf.ConfigurePresets) != 0 {
			t.Errorf("ConfigurePresets: got %d elements, want 0", len(pf.ConfigurePresets))
		}
		if pf.Version != 3 {
			t.Errorf("Version: got %d, want 3", pf.Version)
		}
	})

	t.Run("file with only buildPresets has no configurePresets", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"buildPresets": [
				{
					"name": "build-debug",
					"configurePreset": "debug"
				}
			]
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}
		if pf.ConfigurePresets != nil {
			t.Errorf("ConfigurePresets: got %v, want nil", pf.ConfigurePresets)
		}
	})

	t.Run("file with include field", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 4,
			"include": ["other-presets.json", "third-party/presets.json"],
			"configurePresets": [
				{
					"name": "default",
					"binaryDir": "build",
					"generator": "Ninja"
				}
			]
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}

		if pf.Version != 4 {
			t.Errorf("Version: got %d, want 4", pf.Version)
		}
		if len(pf.Include) != 2 {
			t.Fatalf("Include: got %d elements, want 2", len(pf.Include))
		}
		assertEqual(t, "Include[0]", pf.Include[0], "other-presets.json")
		assertEqual(t, "Include[1]", pf.Include[1], "third-party/presets.json")
		if len(pf.ConfigurePresets) != 1 {
			t.Fatalf("ConfigurePresets: got %d elements, want 1", len(pf.ConfigurePresets))
		}
		assertEqual(t, "preset[0].Name", pf.ConfigurePresets[0].Name, "default")
	})

	t.Run("inherits as string", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "derived",
					"inherits": "base"
				}
			]
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}

		preset := pf.ConfigurePresets[0]
		if preset.Inherits == nil {
			t.Fatal("Inherits should not be nil for string value")
		}

		// Verify the raw JSON can be unmarshaled as a string.
		var s string
		if err := json.Unmarshal(preset.Inherits, &s); err != nil {
			t.Fatalf("Inherits should unmarshal as string: %v", err)
		}
		assertEqual(t, "Inherits (string)", s, "base")
	})

	t.Run("inherits as array", func(t *testing.T) {
		dir := t.TempDir()
		path := writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "derived",
					"inherits": ["base1", "base2"]
				}
			]
		}`)

		pf, err := readPresetsFile(path)
		if err != nil {
			t.Fatalf("readPresetsFile() returned error: %v", err)
		}
		if pf == nil {
			t.Fatal("readPresetsFile() returned nil")
		}

		preset := pf.ConfigurePresets[0]
		if preset.Inherits == nil {
			t.Fatal("Inherits should not be nil for array value")
		}

		// Verify the raw JSON can be unmarshaled as a string slice.
		var arr []string
		if err := json.Unmarshal(preset.Inherits, &arr); err != nil {
			t.Fatalf("Inherits should unmarshal as []string: %v", err)
		}
		if len(arr) != 2 {
			t.Fatalf("Inherits: got %d elements, want 2", len(arr))
		}
		assertEqual(t, "Inherits[0]", arr[0], "base1")
		assertEqual(t, "Inherits[1]", arr[1], "base2")
	})
}

func TestResolveInherits(t *testing.T) {
	t.Run("single-level inherits", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "child", Inherits: json.RawMessage(`"parent"`)},
			{Name: "parent", BinaryDir: "/build/parent", Generator: "Ninja"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "child.BinaryDir", result[0].BinaryDir, "/build/parent")
		assertEqual(t, "child.Generator", result[0].Generator, "Ninja")
	})

	t.Run("multi-level inherits 3 deep", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "a", Inherits: json.RawMessage(`"b"`)},
			{Name: "b", Inherits: json.RawMessage(`"c"`)},
			{Name: "c", BinaryDir: "/build/c", Generator: "Ninja"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "a.BinaryDir", result[0].BinaryDir, "/build/c")
		assertEqual(t, "a.Generator", result[0].Generator, "Ninja")
		assertEqual(t, "b.BinaryDir", result[1].BinaryDir, "/build/c")
		assertEqual(t, "b.Generator", result[1].Generator, "Ninja")
	})

	t.Run("multi-parent array uses first non-empty value", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "child", Inherits: json.RawMessage(`["parent1", "parent2"]`)},
			{Name: "parent1", BinaryDir: "/build/p1"},
			{Name: "parent2", BinaryDir: "/build/p2", Generator: "Ninja"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		// BinaryDir from parent1 (first non-empty), Generator from parent2.
		assertEqual(t, "child.BinaryDir", result[0].BinaryDir, "/build/p1")
		assertEqual(t, "child.Generator", result[0].Generator, "Ninja")
	})

	t.Run("child overrides inherited values", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "child", BinaryDir: "/build/child", Generator: "Make", Inherits: json.RawMessage(`"parent"`)},
			{Name: "parent", BinaryDir: "/build/parent", Generator: "Ninja"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "child.BinaryDir", result[0].BinaryDir, "/build/child")
		assertEqual(t, "child.Generator", result[0].Generator, "Make")
	})

	t.Run("circular detection", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "a", Inherits: json.RawMessage(`"b"`)},
			{Name: "b", Inherits: json.RawMessage(`"a"`)},
		}

		_, err := resolveInherits(presets)
		if err == nil {
			t.Fatal("resolveInherits() should have returned an error for circular inherits")
		}
		if !strings.Contains(err.Error(), "circular") {
			t.Errorf("error should mention circular, got: %v", err)
		}
	})

	t.Run("generator inherits from parent", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "child", BinaryDir: "/build/child", Inherits: json.RawMessage(`"parent"`)},
			{Name: "parent", Generator: "Ninja Multi-Config"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "child.BinaryDir", result[0].BinaryDir, "/build/child")
		assertEqual(t, "child.Generator", result[0].Generator, "Ninja Multi-Config")
	})

	t.Run("no inherits field leaves preset unchanged", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "standalone", BinaryDir: "/build/standalone", Generator: "Ninja"},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "standalone.BinaryDir", result[0].BinaryDir, "/build/standalone")
		assertEqual(t, "standalone.Generator", result[0].Generator, "Ninja")
	})

	t.Run("unknown parent is skipped silently", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "child", Inherits: json.RawMessage(`"nonexistent"`)},
		}

		result, err := resolveInherits(presets)
		if err != nil {
			t.Fatalf("resolveInherits() returned error: %v", err)
		}

		assertEqual(t, "child.BinaryDir", result[0].BinaryDir, "")
		assertEqual(t, "child.Generator", result[0].Generator, "")
	})
}

func TestExpandBinaryDir(t *testing.T) {
	t.Run("sourceDir macro expansion", func(t *testing.T) {
		result, err := expandBinaryDir("${sourceDir}/build", "/src", "debug")
		if err != nil {
			t.Fatalf("expandBinaryDir() returned error: %v", err)
		}
		assertEqual(t, "expanded", result, "/src/build")
	})

	t.Run("presetName macro expansion", func(t *testing.T) {
		result, err := expandBinaryDir("${sourceDir}/build/${presetName}", "/src", "debug")
		if err != nil {
			t.Fatalf("expandBinaryDir() returned error: %v", err)
		}
		assertEqual(t, "expanded", result, "/src/build/debug")
	})

	t.Run("relative path join", func(t *testing.T) {
		result, err := expandBinaryDir("build/debug", "/src", "debug")
		if err != nil {
			t.Fatalf("expandBinaryDir() returned error: %v", err)
		}
		assertEqual(t, "expanded", result, "/src/build/debug")
	})

	t.Run("unresolvable macro returns error", func(t *testing.T) {
		_, err := expandBinaryDir("${sourceDir}/build/$env{HOME}", "/src", "debug")
		if err == nil {
			t.Fatal("expandBinaryDir() should have returned an error for unresolvable macro")
		}
		if !strings.Contains(err.Error(), "unresolvable") {
			t.Errorf("error should mention unresolvable, got: %v", err)
		}
	})

	t.Run("empty binaryDir returns empty string", func(t *testing.T) {
		result, err := expandBinaryDir("", "/src", "debug")
		if err != nil {
			t.Fatalf("expandBinaryDir() returned error: %v", err)
		}
		assertEqual(t, "expanded", result, "")
	})

	t.Run("absolute path stays absolute", func(t *testing.T) {
		result, err := expandBinaryDir("/absolute/build", "/src", "debug")
		if err != nil {
			t.Fatalf("expandBinaryDir() returned error: %v", err)
		}
		assertEqual(t, "expanded", result, "/absolute/build")
	})

	t.Run("unknown dollar-brace macro returns error", func(t *testing.T) {
		_, err := expandBinaryDir("${sourceDir}/build/${unknownMacro}", "/src", "debug")
		if err == nil {
			t.Fatal("expandBinaryDir() should have returned an error for unknown macro")
		}
	})
}
