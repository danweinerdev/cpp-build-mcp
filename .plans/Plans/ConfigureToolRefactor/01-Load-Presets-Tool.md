---
title: "load_presets Tool"
type: phase
plan: ConfigureToolRefactor
phase: 1
status: complete
created: 2026-03-10
updated: 2026-03-10
deliverable: "A load_presets MCP tool that reloads config/presets from disk and updates the registry"
tasks:
  - id: "1.1"
    title: "Registry reload method"
    status: complete
    verification: "reload handles: unchanged config preserves state, changed config resets state (full Config struct comparison, not field subset), new config added, removed config dropped, default config updated if removed, resolveToolchain mutation does not corrupt comparison (compare against fresh disk-loaded config), concurrent access safe under mutex and race detector"
  - id: "1.2"
    title: "load_presets handler and tool registration"
    status: complete
    depends_on: ["1.1"]
    verification: "handler calls registry reload, returns JSON response listing configs with their status (added/unchanged/removed/changed), go vet passes"
  - id: "1.3"
    title: "Unit tests"
    status: complete
    depends_on: ["1.2"]
    verification: "tests cover: reload adds new config, reload removes stale config, reload preserves state for unchanged config, reload resets state for changed config, reload updates default when old default removed, response JSON structure matches spec, go vet and race detector pass"
---

# Phase 1: load_presets Tool

## Overview

Add a `load_presets` MCP tool that re-reads CMakePresets.json and .cpp-build-mcp.json from disk and updates the in-memory config registry. This enables users to edit their preset files mid-session without restarting the server.

## 1.1: Registry reload method

### Subtasks
- [x] Add `reload(configs map[string]*config.Config, defaultName string) reloadResult` method to `configRegistry`
- [x] Compare incoming configs against existing instances by key fields (BuildDir, Generator, Preset, Toolchain)
- [x] Preserve existing `configInstance` (builder + store) for unchanged configs
- [x] Create new `configInstance` with fresh builder + store for changed or new configs
- [x] Remove instances not present in the new config map
- [x] Update `dflt` field; if old default was removed, use the new default
- [x] Return a `reloadResult` struct with Added/Removed/Changed/Unchanged counts and names
- [x] Run `resolveToolchain` for new/changed instances

### Notes
Compare the full `Config` struct by value (not a subset of fields) to detect changes. This avoids silent bugs when new config fields are added later. Use `reflect.DeepEqual` or a comparable struct copy.

Important: `resolveToolchain` in `main.go` mutates `inst.cfg.InjectDiagnosticFlags` as a side effect for gcc-legacy detection. The reload comparison must use the fresh disk-loaded config, not the possibly-mutated in-memory copy. Compare the new config against itself (pre-mutation) rather than against the existing instance's mutated config.

`registry.go` will need to import `builder` to call `builder.NewBuilder` for new/changed configs, and call `resolveToolchain` for new/changed instances.

## 1.2: load_presets handler and tool registration

### Subtasks
- [x] Register `load_presets` tool in `main()` with no parameters (it always reloads from the working directory)
- [x] Implement `handleLoadPresets` handler: call `config.LoadMulti(".")`, call `registry.reload()`, return JSON response
- [x] Define `loadPresetsResponse` struct: Configs (list of config summaries), Added/Removed/Changed/Unchanged counts
- [x] Handle `config.LoadMulti` errors gracefully (return tool error, don't crash)

### Notes
The tool takes no parameters — it always reloads from the current working directory (same as startup). The response includes the full config list plus a summary of what changed.

## 1.3: Unit tests

### Subtasks
- [x] Test `configRegistry.reload` directly with various scenarios
- [x] Test `handleLoadPresets` via `newTestServer` pattern used by existing tests
- [x] Verify state preservation: set up a configured+built instance, reload with same config, confirm phase is preserved
- [x] Verify state reset: modify a config field, reload, confirm phase resets to unconfigured
- [x] Run with `-race` flag

## Acceptance Criteria
- [ ] `load_presets` tool appears in MCP tool listing
- [ ] Calling `load_presets` re-reads config files and updates the registry
- [ ] Unchanged configs preserve their build state
- [ ] Changed/new configs start fresh
- [ ] Removed configs are dropped from the registry
- [ ] Concurrent reload under `go test -race` produces no race detector output
- [ ] `go vet ./...` and `go test -race ./...` pass
