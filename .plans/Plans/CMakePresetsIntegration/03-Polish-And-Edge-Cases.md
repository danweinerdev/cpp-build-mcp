---
title: "Polish and Edge Cases"
type: phase
plan: CMakePresetsIntegration
phase: 3
status: planned
created: 2026-03-08
updated: 2026-03-08
deliverable: "Hybrid mode fully working, all edge cases handled, E2E tests covering zero-config and hybrid scenarios"
tasks:
  - id: "3.1"
    title: "Hybrid mode: presets with .cpp-build-mcp.json overrides"
    status: planned
    verification: "CMakePresets.json + .cpp-build-mcp.json with top-level build_timeout, inject_diagnostic_flags, and diagnostic_serial_build: all three fields applied to every preset-derived config. Preset names and binaryDir values not overridden by .cpp-build-mcp.json top-level fields. Single-config mode with top-level preset field (no CMakePresets.json) works: Config.Preset set, configure uses --preset. .cpp-build-mcp.json with default_config set to a valid preset name uses it as default. default_config set to a name not in presets produces an error."
  - id: "3.2"
    title: "Edge case hardening"
    status: planned
    verification: "Empty configurePresets array (valid file, no presets) → fallback to single default with warning. All presets filtered (all hidden or all multi-config generators) → fallback with distinct warning message. Preset binaryDir not specified (v3+ allows this) → preset skipped with slog.Warn. CMakePresets.json exists but is not valid JSON → startup error with descriptive message. CMakePresets.json with only buildPresets/testPresets (no configurePresets) → fallback to single default. Single preset after filtering → single-config mode with env vars applied (not multi-config suppression)."
  - id: "3.3"
    title: "Integration tests and structural verification"
    status: planned
    depends_on: ["3.1", "3.2"]
    verification: "E2E test: server starts with CMakePresets.json only (no .cpp-build-mcp.json), list_configs returns preset-derived configs with correct names, build_dirs, and 'unconfigured' status. E2E test: configure(config:'debug') on a preset-derived config, verify the correct fakeBuilder is dispatched to (--preset arg construction verified by unit tests in Phase 1 task 1.3). E2E test: hybrid mode (CMakePresets.json + .cpp-build-mcp.json top-level overrides), verify preset configs have overridden build_timeout. go vet, staticcheck, go test -race all pass across all packages."
---

# Phase 3: Polish and Edge Cases

## Overview

Ensure the hybrid mode (presets + `.cpp-build-mcp.json` overrides) works correctly, harden all edge cases identified in the design, and add E2E integration tests covering the full zero-config and hybrid scenarios.

## 3.1: Hybrid mode: presets with .cpp-build-mcp.json overrides

### Subtasks
- [ ] Verify top-level `build_timeout` from `.cpp-build-mcp.json` is applied to all preset-derived configs (should work from Task 2.4's `applyJSON` merge, but needs explicit test)
- [ ] Verify `inject_diagnostic_flags` and `diagnostic_serial_build` override defaults on preset-derived configs
- [ ] Verify preset-derived `BuildDir` and `Preset` fields are NOT overridden by `.cpp-build-mcp.json` top-level `build_dir` (the per-preset binaryDir takes precedence) — guard implemented in Task 2.4, this task verifies it with additional scenarios
- [ ] Test: single-config mode with top-level `"preset": "mypreset"` (no CMakePresets.json file) — `Config.Preset` is set, configure uses `--preset`
- [ ] Test: `default_config` in `.cpp-build-mcp.json` with presets — valid name selects default, invalid name errors

### Notes
The `applyJSON` post-override guard (restoring `BuildDir`, `Preset`, `Generator` after `applyJSON`) is implemented in Task 2.4. This task focuses on verifying the behavior with additional hybrid scenarios and testing server-specific field merging (`build_timeout`, `inject_diagnostic_flags`, `diagnostic_serial_build`).

## 3.2: Edge case hardening

### Subtasks
- [ ] Test: empty `configurePresets` array (`"configurePresets": []`) → fallback to single default with `slog.Warn`
- [ ] Test: all presets filtered (all hidden) → fallback with warning mentioning hidden filter
- [ ] Test: all presets filtered (all multi-config generators) → fallback with warning mentioning generator filter
- [ ] Test: preset with `binaryDir` not specified (field absent, v3+ allows this) → preset skipped with `slog.Warn`
- [ ] Test: `CMakePresets.json` exists but is not valid JSON → startup error returned from `LoadMulti`
- [ ] Test: `CMakePresets.json` with only `buildPresets` key (no `configurePresets`) → fallback to single default
- [ ] Test: single preset after filtering → treated as single-config mode (env vars apply, no multi-config suppression)
- [ ] Verify all warning messages include enough context for the user to understand what happened (preset name, filter reason)

### Notes
The "single preset after filtering" case is important: if only one preset survives, it behaves like single-config mode. Env vars apply (not suppressed), and the config name is the preset name (not "default"). This matches the existing behavior where a single-entry `configs` map still creates a named config.

## 3.3: Integration tests and structural verification

### Subtasks
- [ ] E2E test: create a temp dir with `CMakePresets.json` containing two configure presets (debug, release), start server, verify `list_configs` returns both configs with correct `build_dir` values and `"unconfigured"` status
- [ ] E2E test: on the preset-derived server, call `configure(config: "debug")`, verify the correct `fakeBuilder` was dispatched to and `Config.Preset` is `"debug"` (the `--preset` flag construction is verified by unit tests in Phase 1 task 1.3; `fakeBuilder` only sees `extraArgs`, not full built args)
- [ ] E2E test: create temp dir with both `CMakePresets.json` and `.cpp-build-mcp.json` (top-level `build_timeout: "10m"`), verify preset configs have `build_timeout` of 10 minutes
- [ ] Run `go vet ./...` across all packages
- [ ] Run `staticcheck ./...` across all packages
- [ ] Run `go test -race ./...` across all packages
- [ ] Verify all existing tests pass unchanged (backward compatibility)

### Notes
E2E tests write temp files and construct `mcpServer` instances directly (same pattern as existing E2E tests). They use `fakeBuilder` to avoid invoking cmake. Note: `fakeBuilder.lastConfigureArgs` captures only the `extraArgs` passed by the handler (the `cmake_args` tool parameter), not the full args built by `buildConfigureArgs`. The `--preset` flag is injected inside `CMakeBuilder.buildConfigureArgs` and is tested by unit tests in Phase 1 task 1.3. E2E tests here verify config routing, `Config.Preset` population, and server-level behavior.

## Acceptance Criteria

- [ ] Hybrid mode: preset-derived configs inherit server-specific overrides from `.cpp-build-mcp.json`
- [ ] Preset-derived `BuildDir` not overridden by `.cpp-build-mcp.json` top-level `build_dir`
- [ ] All edge cases produce correct behavior (error, warning, or fallback as specified)
- [ ] E2E tests cover zero-config, preset configure, and hybrid mode
- [ ] All existing tests pass unchanged (backward compatible)
- [ ] `go vet`, `go test -race`, and `staticcheck` all pass
