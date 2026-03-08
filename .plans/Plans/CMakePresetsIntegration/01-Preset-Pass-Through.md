---
title: "Preset Pass-Through"
type: phase
plan: CMakePresetsIntegration
phase: 1
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "Users can reference CMake presets from .cpp-build-mcp.json; generator hardcode bug fixed; cmake --preset used at configure time"
tasks:
  - id: "1.1"
    title: "Fix generator hardcoding in buildConfigureArgs"
    status: complete
    verification: "buildConfigureArgs uses cfg.Generator resolved to cmake's full name. cfg.Generator='ninja' produces '-G', 'Ninja'. cfg.Generator='make' produces '-G', 'Unix Makefiles'. Empty generator defaults to 'Ninja'. Existing cmake_test.go tests updated and passing. go vet and go test -race pass."
  - id: "1.2"
    title: "Add Preset field to Config and configJSON"
    status: complete
    verification: "Config struct has Preset string field. configJSON has Preset *string field. applyJSON handles Preset. JSON with 'preset':'debug' parses correctly via Load and LoadMulti. Preset field preserved through single-config and multi-config paths. Existing tests pass unchanged."
  - id: "1.3"
    title: "Add --preset branch to buildConfigureArgs"
    status: complete
    depends_on: ["1.1", "1.2"]
    verification: "When cfg.Preset is non-empty, buildConfigureArgs produces ['--preset', name, '-DCMAKE_EXPORT_COMPILE_COMMANDS=ON', ...diagFlags, ...cmakeArgs, ...extraArgs] without -S, -B, or -G flags. When cfg.Preset is empty, existing behavior is unchanged. Diagnostic flags included in preset mode when toolchain is clang. cfg.CMakeArgs passed through in preset mode. Tests cover both preset and non-preset paths."
  - id: "1.4"
    title: "End-to-end preset reference tests"
    status: complete
    depends_on: ["1.3"]
    verification: "Integration test: .cpp-build-mcp.json with per-config preset field, verify list_configs shows configs and configure dispatches to the correct builder. Config.Preset is set on the resulting config. Test: single-config mode with top-level preset field, Config.Preset populated. Test: configs map entry with preset field alongside build_dir, both fields set on resulting Config. Note: --preset flag construction is verified by unit tests in task 1.3; E2E tests verify config parsing and routing, not argument construction. go vet, staticcheck, go test -race all pass across all packages."
---

# Phase 1: Preset Pass-Through

## Overview

Fix the generator hardcoding bug, add a `Preset` field to the config system, and implement the `--preset` branch in `buildConfigureArgs`. After this phase, users with `CMakePresets.json` can write a minimal `.cpp-build-mcp.json` referencing preset names, and cmake handles all preset complexity.

## 1.1: Fix generator hardcoding in buildConfigureArgs

### Subtasks
- [x] Add `generatorCMakeName(gen string) string` helper in `builder/cmake.go` that maps normalized names to cmake's full names (`"ninja"` → `"Ninja"`, `"make"` → `"Unix Makefiles"`, `""` → `"Ninja"`)
- [x] Replace hardcoded `"-G", "Ninja"` at `cmake.go:64` with `"-G", generatorCMakeName(b.cfg.Generator)`
- [x] Update `TestBuildConfigureArgs` in `cmake_test.go` — the test at line 78 asserts `"-G", "Ninja"` and must be updated to reflect the new behavior
- [x] Add test: `cfg.Generator = "make"` produces `"-G", "Unix Makefiles"`
- [x] Add test: `cfg.Generator = ""` (empty) produces `"-G", "Ninja"` (default fallback)
- [x] Run `go vet ./...` and `go test -race ./...`

### Notes
This is a pre-existing bug independent of presets. The fix is a prerequisite because the `--preset` branch (Task 1.3) must not emit `-G` at all, while the non-preset branch must emit the correct generator name. Both branches need the generator to be resolved correctly.

## 1.2: Add Preset field to Config and configJSON

### Subtasks
- [x] Add `Preset string` field with `json:"preset"` tag to `Config` struct in `config/config.go`
- [x] Add `Preset *string` field with `json:"preset"` tag to `configJSON` struct
- [x] Add `Preset` handling to `applyJSON` — same pattern as existing fields: `if raw.Preset != nil { cfg.Preset = *raw.Preset }`
- [x] Verify `configFileJSON` struct inherits `Preset` from embedded `configJSON` (it embeds `configJSON`, so the field is automatically available at the top level of `.cpp-build-mcp.json`)
- [x] Add test in `config_test.go`: single-config JSON with `"preset": "debug"` parses correctly
- [x] Add test: `LoadMulti` with configs map entries containing `"preset"` field
- [x] Add test: `Preset` field defaults to empty string when not specified
- [x] Run `go test -race ./config/...`

### Notes
The `Preset` field lives on the public `Config` struct because it affects builder behavior (`buildConfigureArgs` reads it). The `configJSON` pointer field enables optional parsing — presets from `.cpp-build-mcp.json` are explicit, while presets from auto-discovery (Phase 2) set the field programmatically.

## 1.3: Add --preset branch to buildConfigureArgs

### Subtasks
- [x] In `buildConfigureArgs`, add a branch: `if b.cfg.Preset != ""`
- [x] Preset branch produces: `["--preset", b.cfg.Preset, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON"]`
- [x] Append diagnostic flags in preset mode (same clang toolchain check as non-preset path)
- [x] Append `b.cfg.CMakeArgs` in preset mode (user-specified `-D` flags override preset cacheVariables)
- [x] Append `extraArgs` in preset mode
- [x] Do NOT emit `-S`, `-B`, or `-G` in preset mode — cmake resolves these from the preset
- [x] Non-preset branch remains unchanged (uses the fixed generator from Task 1.1)
- [x] Add `slog.Warn` in `buildConfigureArgs` when `Preset` is set but `BuildDir` equals the default (`"build"`) — the user likely forgot to set `build_dir` to match the preset's `binaryDir` (Phase 2 auto-discovery eliminates this issue, but Phase 1 manual references need the hint)
- [x] Write tests: preset mode args structure, non-preset mode regression, diagnostic flags in preset mode, CMakeArgs pass-through in preset mode
- [x] Run `go test -race ./builder/...`

### Notes
The `-DCMAKE_EXPORT_COMPILE_COMMANDS=ON` flag is always appended in preset mode because the preset may not include it, and cli `-D` flags override preset cacheVariables. `CMakeArgs` from `.cpp-build-mcp.json` are passed through because the user explicitly set them — research confirmed this works correctly with `--preset`.

**Phase 1 constraint:** When using manual preset references (before Phase 2 auto-discovery), the user must set `build_dir` to match the preset's `binaryDir`. Without this, `cmake --build` targets the wrong directory. A warning is emitted when `Preset` is set with the default `BuildDir` to surface this mismatch early.

**Generator interaction:** `Preset` only affects `CMakeBuilder.buildConfigureArgs`. If `Generator == "make"`, `NewBuilder` routes to `MakeBuilder` (whose `Configure` is a no-op), so `Preset` has no effect. This is acceptable — preset support is cmake-specific. E2E tests should not combine `preset` with `generator: "make"`.

## 1.4: End-to-end preset reference tests

### Subtasks
- [x] Write integration test in `main_test.go`: multi-config `.cpp-build-mcp.json` with `"preset": "debug"` on a config entry, verify `list_configs` shows the config and configure dispatches to the correct `fakeBuilder`
- [x] Write test: single-config `.cpp-build-mcp.json` with top-level `"preset": "mypreset"`, verify `Config.Preset` is populated on the loaded config
- [x] Write test: configs map entry with both `"preset": "debug"` and `"build_dir": "build/debug"`, verify both fields are set on the resulting Config
- [x] Run `go vet ./...`, `staticcheck ./...`, `go test -race ./...`

### Notes
These tests use `fakeBuilder` and verify config parsing and routing — not argument construction. `fakeBuilder` receives only the `cmake_args` tool parameter (the `extraArgs` passed by the handler), not the full args built by `buildConfigureArgs`. The `--preset` flag is injected inside `CMakeBuilder.buildConfigureArgs`, which is tested by unit tests in task 1.3. E2E tests here verify that `Config.Preset` is correctly parsed from JSON, that the right builder is dispatched to, and that both `Preset` and `BuildDir` coexist correctly.

## Acceptance Criteria

- [x] `buildConfigureArgs` uses `cfg.Generator` (not hardcoded `"Ninja"`) for the `-G` flag
- [x] `Config.Preset` field exists and parses from `.cpp-build-mcp.json`
- [x] `buildConfigureArgs` with `Preset != ""` produces `--preset <name>` without `-S`/`-B`/`-G`
- [x] Existing tests pass unchanged (backward compatible)
- [x] `go vet`, `go test -race`, and `staticcheck` all pass
