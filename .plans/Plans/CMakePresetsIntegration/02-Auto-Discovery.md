---
title: "Auto-Discovery"
type: phase
plan: CMakePresetsIntegration
phase: 2
status: planned
created: 2026-03-08
updated: 2026-03-08
deliverable: "Zero-config auto-discovery of build configurations from CMakePresets.json; LoadMulti populates registry from presets when no .cpp-build-mcp.json configs map exists"
tasks:
  - id: "2.1"
    title: "Parse CMakePresets.json configurePresets"
    status: planned
    verification: "Valid CMakePresets.json with configurePresets array parses into presetMetadata slice. Missing file returns nil slice without error. Invalid JSON returns error. Schema version field is read (no version-specific behavior needed — only configurePresets is read, which exists in all versions). Each preset's name, binaryDir, generator, hidden, and inherits fields are extracted. Include field (v4+) is parsed for later warning detection. Presets with no configurePresets key return empty slice."
  - id: "2.2"
    title: "Inherits resolution and macro expansion"
    status: planned
    depends_on: ["2.1"]
    verification: "Preset inheriting binaryDir from a hidden base preset resolves correctly. Multi-level inherits (A inherits B inherits C) resolves transitively. Multi-inheritance via array uses first non-empty value for each field. Child preset with own binaryDir keeps it (parent's binaryDir ignored). Circular inherits detected and returns error naming the cycle. ${sourceDir} expands to the dir argument. ${presetName} expands to the preset's name. Relative binaryDir (no ${sourceDir} prefix) joined with dir via filepath.Join. Preset with $env{} macro in binaryDir skipped with slog.Warn. go test -race passes."
  - id: "2.3"
    title: "Filtering, normalization, and user presets merge"
    status: planned
    depends_on: ["2.1"]
    verification: "Hidden presets (hidden: true) excluded from output. Presets with multi-config generators ('Ninja Multi-Config', generators starting with 'Visual Studio') excluded with slog.Info. Generator names normalized: 'Ninja' → 'ninja', 'Unix Makefiles' → 'make'. CMakeUserPresets.json read if present; user presets shadow project presets with same name. Presets file with non-empty include field triggers slog.Warn naming the limitation. Presets without resolvable binaryDir skipped with slog.Warn. Duplicate binaryDir across presets produces error naming both. go test -race passes."
  - id: "2.4"
    title: "Wire preset discovery into LoadMulti"
    status: planned
    depends_on: ["2.2", "2.3"]
    verification: "LoadMulti with CMakePresets.json only (no .cpp-build-mcp.json) returns preset-derived configs with Preset field set, correct BuildDir, and normalized Generator. configs map in .cpp-build-mcp.json suppresses preset discovery entirely. All presets skipped falls back to single 'default' config with slog.Warn and env vars applied. Env vars suppressed in multi-preset mode with slog.Warn. Default config is alphabetically first preset name (or default_config from .cpp-build-mcp.json if set). After applyJSON merge, preset-derived BuildDir, Preset, and Generator are restored from preset metadata (not overridden by .cpp-build-mcp.json top-level build_dir). go vet, staticcheck, go test -race all pass."
---

# Phase 2: Auto-Discovery

## Overview

Implement the zero-config path: parse `CMakePresets.json` to auto-populate the config registry without any `.cpp-build-mcp.json`. After this phase, users with CMake presets files can start cpp-build-mcp with no configuration — it discovers preset names and build directories, and uses `cmake --preset` (from Phase 1) at configure time.

The key insight: cpp-build-mcp only parses presets for two fields (name and binaryDir). Everything else is delegated to cmake via `--preset`.

## 2.1: Parse CMakePresets.json configurePresets

### Subtasks
- [ ] Define unexported `presetMetadata` struct in `config/`: `Name string`, `BinaryDir string`, `Generator string`
- [ ] Define unexported JSON parsing structs for CMakePresets.json: `presetsFile` with `Version int`, `ConfigurePresets []configurePreset`, `Include []string` (for v4+ detection)
- [ ] Define `configurePreset` struct: `Name string`, `BinaryDir string`, `Generator string`, `Hidden bool`, `Inherits json.RawMessage` (string or array)
- [ ] Implement `readPresetsFile(path string) (*presetsFile, error)` — reads and unmarshals, returns nil without error if file doesn't exist
- [ ] Write tests: valid file parses, missing file returns nil, invalid JSON errors, empty configurePresets, file with only buildPresets, file with `include` field parses (field captured for warning)

### Notes
The `Inherits` field uses `json.RawMessage` because it can be either a string or an array of strings in CMakePresets.json. Parsing is deferred to the inherits resolution step (Task 2.2). Schema version is read but not acted on — `configurePresets` exists in all versions (v1+), and unknown fields are ignored by `encoding/json`. The `Include` field (v4+) is parsed but not followed — cpp-build-mcp does not recursively read included files. Task 2.3 emits a warning when `include` is non-empty.

## 2.2: Inherits resolution and macro expansion

### Subtasks
- [ ] Implement `resolveInherits(presets []configurePreset) ([]configurePreset, error)` — walks inherits chains, copies binaryDir and generator from parent when child has empty value
- [ ] Parse `Inherits` field: unmarshal as string (single parent) or `[]string` (multiple parents)
- [ ] For multiple parents, use first non-empty value for each field (binaryDir, generator) — matches cmake's left-to-right precedence
- [ ] Build a preset-name index for O(1) lookup during resolution
- [ ] Detect circular inherits via visited-set during traversal, return error naming the cycle
- [ ] Implement `expandBinaryDir(binaryDir, dir, presetName string) (string, error)` — replaces `${sourceDir}` with `dir` and `${presetName}` with preset name
- [ ] If expanded binaryDir still contains `${` or `$env{`, return error (caller will skip with warning)
- [ ] After expansion, if binaryDir is not absolute, join with dir via `filepath.Join(dir, binaryDir)`
- [ ] Write tests: single-level inherits, multi-level (3 deep), multi-parent array, child-overrides-inherited (child has own binaryDir, parent's ignored), circular detection, macro expansion, relative path join, unresolvable macro error

### Notes
Inherits resolution is scoped to binaryDir and generator only — these are the two fields cpp-build-mcp reads. All other inherited fields (cacheVariables, toolchainFile, environment) are irrelevant because `cmake --preset` resolves them natively. This keeps the resolver simple.

## 2.3: Filtering, normalization, and user presets merge

### Subtasks
- [ ] Implement hidden preset filter: exclude presets where `Hidden == true`
- [ ] Implement multi-config generator filter: exclude presets whose generator (after inherits resolution) matches `"Ninja Multi-Config"` or starts with `"Visual Studio"`, log `slog.Info` for each skipped preset
- [ ] Add `generatorNormalizeMap` in `config/`: `"Ninja"` → `"ninja"`, `"Unix Makefiles"` → `"make"`, unknown → `"ninja"` (default)
- [ ] Implement `readUserPresets(dir string) (*presetsFile, error)` — reads `CMakeUserPresets.json`, returns nil if not found
- [ ] Implement user presets merge: union configure presets, user presets replace project presets on name collision
- [ ] Filter presets without resolvable binaryDir (from macro expansion errors) with `slog.Warn`
- [ ] Validate binaryDir uniqueness across surviving presets — error naming both conflicting presets
- [ ] Emit `slog.Warn` when either presets file has a non-empty `Include` field: `"CMakePresets.json uses 'include' (v4+); included preset files are not read — some presets may not be discovered"`
- [ ] Assemble `loadPresetsMetadata(dir string) ([]presetMetadata, error)` — orchestrates: read project presets, read user presets, warn on include, merge, resolve inherits, expand macros, filter, normalize, validate
- [ ] Write tests: hidden filter, multi-config generator filter, user presets shadow, duplicate binaryDir error, generator normalization, include field triggers warning, full loadPresetsMetadata happy path

### Notes
The generator normalization map in `config/` is separate from the reverse map in `builder/` (Task 1.1's `generatorCMakeName`). They serve opposite directions: preset→config vs config→cmake. Keeping them in their respective packages avoids a cross-package dependency for a two-entry map.

## 2.4: Wire preset discovery into LoadMulti

### Subtasks
- [ ] In `LoadMulti`, after checking for `configs` map (existing multi-config path), call `loadPresetsMetadata(dir)` before falling through to single-config path
- [ ] When presets found and non-empty: create per-preset `Config` entries — set `BuildDir` from preset binaryDir, `Generator` from normalized preset generator, `Preset` from preset name, all other fields from `defaults()`
- [ ] If `.cpp-build-mcp.json` exists (without `configs` map), merge its top-level fields onto each preset-derived Config via `applyJSON`
- [ ] After `applyJSON` merge, restore preset-derived fields (`BuildDir`, `Preset`, `Generator`) from preset metadata — prevents `.cpp-build-mcp.json` top-level `build_dir` from overriding preset binaryDir
- [ ] Write test: `.cpp-build-mcp.json` with top-level `build_dir` does NOT override preset-derived `BuildDir`
- [ ] Suppress env vars in multi-preset mode (2+ presets) with existing `warnEnvVarsIgnored()` behavior
- [ ] When presets found but all filtered/skipped (zero usable): log `slog.Warn("CMakePresets.json found but no usable configure presets")`, fall through to single-config path (env vars apply)
- [ ] Default config name: use `configFileJSON.DefaultConfig` if set and present in presets, else alphabetically first preset name
- [ ] Write test: CMakePresets.json only (no .cpp-build-mcp.json), verify LoadMulti returns preset-derived configs
- [ ] Write test: CMakePresets.json + .cpp-build-mcp.json with top-level `build_timeout`, verify preset-derived configs inherit it
- [ ] Write test: .cpp-build-mcp.json with `configs` map suppresses preset discovery
- [ ] Write test: all presets skipped, verify single-config fallback with env vars
- [ ] Write test: default_config from .cpp-build-mcp.json applied to preset-derived mode
- [ ] Run `go vet ./...`, `staticcheck ./...`, `go test -race ./...`

### Notes
The insertion point in `LoadMulti` is between the `file.Configs == nil` check (existing multi-config path) and the single-config fallback. The new branch checks for presets before falling through. The `.cpp-build-mcp.json` top-level fields are merged via `applyJSON` onto each preset-derived Config — this reuses the existing overlay mechanism where only non-nil JSON fields override the target. After `applyJSON`, preset-derived fields (`BuildDir`, `Preset`, `Generator`) must be restored from the preset metadata to prevent `.cpp-build-mcp.json` top-level `build_dir` from overriding the preset's binaryDir. This guard is placed here (not deferred to Phase 3) because the problem is introduced in this task and would silently produce wrong behavior.

## Acceptance Criteria

- [ ] `loadPresetsMetadata` parses CMakePresets.json with inherits resolution, macro expansion, and filtering
- [ ] `LoadMulti` with CMakePresets.json only (no .cpp-build-mcp.json) returns preset-derived configs
- [ ] CMakeUserPresets.json is read and merged when present
- [ ] `configs` map in .cpp-build-mcp.json suppresses preset discovery
- [ ] All presets skipped falls back to single "default" config
- [ ] Env vars suppressed in multi-preset mode
- [ ] `go vet`, `go test -race`, and `staticcheck` all pass
