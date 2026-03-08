package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
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
		if p0.Inherits == nil {
			t.Fatal("preset[0].Inherits should be non-nil (has inherits field)")
		}

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

	t.Run("three-node circular detection", func(t *testing.T) {
		presets := []configurePreset{
			{Name: "a", Inherits: json.RawMessage(`"b"`)},
			{Name: "b", Inherits: json.RawMessage(`"c"`)},
			{Name: "c", Inherits: json.RawMessage(`"a"`)},
		}

		_, err := resolveInherits(presets)
		if err == nil {
			t.Fatal("resolveInherits() should have returned an error for 3-node circular inherits")
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

func TestNormalizeGenerator(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Ninja", "ninja"},
		{"Unix Makefiles", "make"},
		{"", "ninja"},
		{"CustomGen", "ninja"},
	}

	for _, tc := range cases {
		t.Run("input="+tc.input, func(t *testing.T) {
			got := normalizeGenerator(tc.input)
			assertEqual(t, "normalizeGenerator", got, tc.want)
		})
	}
}

func TestIsMultiConfigGenerator(t *testing.T) {
	t.Run("Ninja Multi-Config excluded", func(t *testing.T) {
		if !isMultiConfigGenerator("Ninja Multi-Config") {
			t.Error("expected Ninja Multi-Config to be multi-config")
		}
	})

	t.Run("Visual Studio 17 2022 excluded", func(t *testing.T) {
		if !isMultiConfigGenerator("Visual Studio 17 2022") {
			t.Error("expected Visual Studio 17 2022 to be multi-config")
		}
	})

	t.Run("Ninja is not multi-config", func(t *testing.T) {
		if isMultiConfigGenerator("Ninja") {
			t.Error("expected Ninja to not be multi-config")
		}
	})

	t.Run("Unix Makefiles is not multi-config", func(t *testing.T) {
		if isMultiConfigGenerator("Unix Makefiles") {
			t.Error("expected Unix Makefiles to not be multi-config")
		}
	})
}

func TestMergePresets(t *testing.T) {
	t.Run("user preset shadows project preset", func(t *testing.T) {
		project := []configurePreset{
			{Name: "A", BinaryDir: "/project/build/a", Generator: "Ninja"},
			{Name: "B", BinaryDir: "/project/build/b", Generator: "Ninja"},
		}
		user := []configurePreset{
			{Name: "A", BinaryDir: "/user/build/a", Generator: "Unix Makefiles"},
		}

		merged := mergePresets(project, user)

		if len(merged) != 2 {
			t.Fatalf("merged: got %d elements, want 2", len(merged))
		}
		// User's A replaces project's A.
		assertEqual(t, "merged[0].Name", merged[0].Name, "A")
		assertEqual(t, "merged[0].BinaryDir", merged[0].BinaryDir, "/user/build/a")
		assertEqual(t, "merged[0].Generator", merged[0].Generator, "Unix Makefiles")
		// B is unchanged.
		assertEqual(t, "merged[1].Name", merged[1].Name, "B")
		assertEqual(t, "merged[1].BinaryDir", merged[1].BinaryDir, "/project/build/b")
	})

	t.Run("user preset adds new preset", func(t *testing.T) {
		project := []configurePreset{
			{Name: "A", BinaryDir: "/build/a"},
		}
		user := []configurePreset{
			{Name: "C", BinaryDir: "/build/c"},
		}

		merged := mergePresets(project, user)

		if len(merged) != 2 {
			t.Fatalf("merged: got %d elements, want 2", len(merged))
		}
		assertEqual(t, "merged[0].Name", merged[0].Name, "A")
		assertEqual(t, "merged[1].Name", merged[1].Name, "C")
	})

	t.Run("empty user presets returns project presets", func(t *testing.T) {
		project := []configurePreset{
			{Name: "A", BinaryDir: "/build/a"},
		}

		merged := mergePresets(project, nil)

		if len(merged) != 1 {
			t.Fatalf("merged: got %d elements, want 1", len(merged))
		}
		assertEqual(t, "merged[0].Name", merged[0].Name, "A")
	})

	t.Run("partial overlap replaces collision and appends new", func(t *testing.T) {
		project := []configurePreset{
			{Name: "A", BinaryDir: "/project/a"},
			{Name: "B", BinaryDir: "/project/b"},
		}
		user := []configurePreset{
			{Name: "A", BinaryDir: "/user/a"},
			{Name: "C", BinaryDir: "/user/c"},
		}

		merged := mergePresets(project, user)

		if len(merged) != 3 {
			t.Fatalf("merged: got %d elements, want 3", len(merged))
		}
		// A replaced by user version.
		assertEqual(t, "merged[0].Name", merged[0].Name, "A")
		assertEqual(t, "merged[0].BinaryDir", merged[0].BinaryDir, "/user/a")
		// B unchanged from project.
		assertEqual(t, "merged[1].Name", merged[1].Name, "B")
		assertEqual(t, "merged[1].BinaryDir", merged[1].BinaryDir, "/project/b")
		// C appended from user.
		assertEqual(t, "merged[2].Name", merged[2].Name, "C")
		assertEqual(t, "merged[2].BinaryDir", merged[2].BinaryDir, "/user/c")
	})
}

func TestLoadPresetsMetadata(t *testing.T) {
	t.Run("hidden presets excluded", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "base",
					"binaryDir": "${sourceDir}/build/base",
					"generator": "Ninja",
					"hidden": true
				},
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja",
					"hidden": false
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "debug")
	})

	t.Run("multi-config generator presets excluded with slog info", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "ninja-mc",
					"binaryDir": "${sourceDir}/build/ninja-mc",
					"generator": "Ninja Multi-Config"
				},
				{
					"name": "vs2022",
					"binaryDir": "${sourceDir}/build/vs",
					"generator": "Visual Studio 17 2022"
				},
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja"
				}
			]
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "debug")

		logOutput := buf.String()
		if !strings.Contains(logOutput, "ninja-mc") {
			t.Errorf("expected slog.Info mentioning ninja-mc, got: %q", logOutput)
		}
		if !strings.Contains(logOutput, "vs2022") {
			t.Errorf("expected slog.Info mentioning vs2022, got: %q", logOutput)
		}
	})

	t.Run("user presets shadow project presets", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/project",
					"generator": "Ninja"
				}
			]
		}`)
		writePresetsFile(t, dir, "CMakeUserPresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/user",
					"generator": "Ninja"
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "debug")
		// User's binaryDir should win.
		wantBD := filepath.Join(dir, "build/user")
		assertEqual(t, "result[0].BinaryDir", result[0].BinaryDir, wantBD)
	})

	t.Run("duplicate binaryDir returns error naming both presets", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "alpha",
					"binaryDir": "${sourceDir}/build/same",
					"generator": "Ninja"
				},
				{
					"name": "beta",
					"binaryDir": "${sourceDir}/build/same",
					"generator": "Ninja"
				}
			]
		}`)

		_, err := loadPresetsMetadata(dir)
		if err == nil {
			t.Fatal("loadPresetsMetadata() should have returned an error for duplicate binaryDir")
		}

		errMsg := err.Error()
		if !strings.Contains(errMsg, `"alpha"`) || !strings.Contains(errMsg, `"beta"`) {
			t.Errorf("error should name both presets, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "binaryDir") {
			t.Errorf("error should mention binaryDir, got: %s", errMsg)
		}
	})

	t.Run("generator normalization", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "ninja-preset",
					"binaryDir": "${sourceDir}/build/ninja",
					"generator": "Ninja"
				},
				{
					"name": "make-preset",
					"binaryDir": "${sourceDir}/build/make",
					"generator": "Unix Makefiles"
				},
				{
					"name": "empty-gen",
					"binaryDir": "${sourceDir}/build/empty",
					"generator": ""
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 3 {
			t.Fatalf("got %d presets, want 3", len(result))
		}

		// Results are sorted by name.
		byName := make(map[string]presetMetadata, len(result))
		for _, pm := range result {
			byName[pm.Name] = pm
		}

		assertEqual(t, "empty-gen.Generator", byName["empty-gen"].Generator, "ninja")
		assertEqual(t, "make-preset.Generator", byName["make-preset"].Generator, "make")
		assertEqual(t, "ninja-preset.Generator", byName["ninja-preset"].Generator, "ninja")
	})

	t.Run("include field triggers warning", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 4,
			"include": ["other.json"],
			"configurePresets": [
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja"
				}
			]
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}

		logOutput := buf.String()
		if !strings.Contains(logOutput, "include") {
			t.Errorf("expected warning about include field, got: %q", logOutput)
		}
	})

	t.Run("user presets include field triggers warning", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja"
				}
			]
		}`)
		writePresetsFile(t, dir, "CMakeUserPresets.json", `{
			"version": 4,
			"include": ["user-extra.json"],
			"configurePresets": []
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}

		logOutput := buf.String()
		if !strings.Contains(logOutput, "CMakeUserPresets.json") {
			t.Errorf("expected warning mentioning CMakeUserPresets.json, got: %q", logOutput)
		}
		if !strings.Contains(logOutput, "include") {
			t.Errorf("expected warning about include field, got: %q", logOutput)
		}
	})

	t.Run("preset with unresolvable binaryDir skipped with warning", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "bad-macro",
					"binaryDir": "${sourceDir}/build/$env{HOME}",
					"generator": "Ninja"
				},
				{
					"name": "good",
					"binaryDir": "${sourceDir}/build/good",
					"generator": "Ninja"
				}
			]
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "good")

		logOutput := buf.String()
		if !strings.Contains(logOutput, "bad-macro") {
			t.Errorf("expected warning mentioning bad-macro preset, got: %q", logOutput)
		}
	})

	t.Run("preset with empty binaryDir skipped with warning", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "no-bd",
					"generator": "Ninja"
				},
				{
					"name": "has-bd",
					"binaryDir": "${sourceDir}/build/has",
					"generator": "Ninja"
				}
			]
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "has-bd")

		logOutput := buf.String()
		if !strings.Contains(logOutput, "no binaryDir") {
			t.Errorf("expected warning about missing binaryDir, got: %q", logOutput)
		}
	})

	t.Run("no CMakePresets.json returns nil", func(t *testing.T) {
		dir := t.TempDir()

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil result when no presets file, got: %+v", result)
		}
	})

	t.Run("empty configurePresets returns non-nil empty slice", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": []
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}
		// Non-nil empty slice signals "file exists but no usable presets".
		if result == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected 0 presets, got %d", len(result))
		}
	})

	t.Run("only buildPresets returns non-nil empty slice", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"buildPresets": [
				{
					"name": "build-debug",
					"configurePreset": "debug"
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}
		// File exists but configurePresets is nil (absent from JSON).
		// The allPresets slice will be nil/empty, producing a non-nil empty result.
		if result == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected 0 presets, got %d", len(result))
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{not valid json!!!}`)

		_, err := loadPresetsMetadata(dir)
		if err == nil {
			t.Fatal("loadPresetsMetadata() should have returned an error for invalid JSON")
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "CMakePresets.json") {
			t.Errorf("error should mention CMakePresets.json, got: %s", errMsg)
		}
	})

	t.Run("hidden preset logs debug with preset name", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "my-hidden-base",
					"binaryDir": "${sourceDir}/build/base",
					"generator": "Ninja",
					"hidden": true
				},
				{
					"name": "visible",
					"binaryDir": "${sourceDir}/build/visible",
					"generator": "Ninja"
				}
			]
		}`)

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		origLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		defer slog.SetDefault(origLogger)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 1 {
			t.Fatalf("got %d presets, want 1", len(result))
		}
		assertEqual(t, "result[0].Name", result[0].Name, "visible")

		logOutput := buf.String()
		if !strings.Contains(logOutput, "my-hidden-base") {
			t.Errorf("expected debug log mentioning hidden preset name, got: %q", logOutput)
		}
		if !strings.Contains(logOutput, "hidden") {
			t.Errorf("expected debug log mentioning 'hidden', got: %q", logOutput)
		}
	})

	t.Run("all presets filtered returns non-nil empty slice", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "base",
					"binaryDir": "${sourceDir}/build/base",
					"generator": "Ninja",
					"hidden": true
				},
				{
					"name": "vs",
					"binaryDir": "${sourceDir}/build/vs",
					"generator": "Visual Studio 17 2022"
				},
				{
					"name": "no-bd",
					"generator": "Ninja"
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}
		// All presets filtered by different reasons - should still return non-nil empty.
		if result == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(result) != 0 {
			t.Errorf("expected 0 presets, got %d", len(result))
		}
	})

	t.Run("full happy path", func(t *testing.T) {
		dir := t.TempDir()
		writePresetsFile(t, dir, "CMakePresets.json", `{
			"version": 3,
			"configurePresets": [
				{
					"name": "base",
					"binaryDir": "${sourceDir}/build/base",
					"generator": "Ninja",
					"hidden": true
				},
				{
					"name": "debug",
					"binaryDir": "${sourceDir}/build/debug",
					"generator": "Ninja",
					"inherits": "base"
				},
				{
					"name": "release",
					"binaryDir": "${sourceDir}/build/release",
					"generator": "Unix Makefiles",
					"inherits": "base"
				}
			]
		}`)

		result, err := loadPresetsMetadata(dir)
		if err != nil {
			t.Fatalf("loadPresetsMetadata() returned error: %v", err)
		}

		if len(result) != 2 {
			t.Fatalf("got %d presets, want 2", len(result))
		}

		// Results sorted by name: debug, release.
		assertEqual(t, "result[0].Name", result[0].Name, "debug")
		assertEqual(t, "result[0].BinaryDir", result[0].BinaryDir, filepath.Join(dir, "build/debug"))
		assertEqual(t, "result[0].Generator", result[0].Generator, "ninja")

		assertEqual(t, "result[1].Name", result[1].Name, "release")
		assertEqual(t, "result[1].BinaryDir", result[1].BinaryDir, filepath.Join(dir, "build/release"))
		assertEqual(t, "result[1].Generator", result[1].Generator, "make")
	})
}
