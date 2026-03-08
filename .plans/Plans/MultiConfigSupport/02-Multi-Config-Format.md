---
title: "Multi-Config File Format"
type: phase
plan: MultiConfigSupport
phase: 2
status: complete
created: 2026-03-07
updated: 2026-03-07
deliverable: "Config file supports a configs map with inheritance and validation; server creates multiple registry entries from config"
tasks:
  - id: "2.1"
    title: "Extend config parsing for configs map and inheritance"
    status: complete
    verification: "JSON with a 'configs' map parses into distinct Config values per entry. Top-level fields (source_dir, generator, toolchain, build_timeout, inject_diagnostic_flags, diagnostic_serial_build) are inherited by each config entry. Per-config fields (build_dir, cmake_args) override the top-level. JSON without a 'configs' key parses identically to today. Each config gets a value copy, not a shared pointer (verified by mutating one config's InjectDiagnosticFlags and confirming the other is unchanged)."
  - id: "2.2"
    title: "Validation and environment variable behavior"
    status: complete
    depends_on: ["2.1"]
    verification: "Two configs with the same build_dir produce an error at load time naming both configs. Env vars are ignored when configs map is present — test that CPP_BUILD_MCP_BUILD_DIR has no effect in multi-config mode. slog.Warn is emitted (captured via slog handler in test). Env vars still work normally in single-config mode."
  - id: "2.3"
    title: "Wire multi-config loading into main() and registry"
    status: complete
    depends_on: ["2.1", "2.2"]
    verification: "Server starts with a multi-config JSON file. list_configs returns all named configs with correct build_dirs and 'unconfigured' status. The default_config routes correctly when config param is omitted. configure(config:'release') only affects the release instance. go test -race passes."
---

# Phase 2: Multi-Config File Format

## Overview

Extend the config file format to support a `configs` map with top-level inheritance. When the map is present, the server creates multiple registry entries — one per named configuration. Backward compatibility is maintained: config files without a `configs` key behave identically to today.

## 2.1: Extend config parsing for configs map and inheritance

### Subtasks
- [x] Add `Configs map[string]json.RawMessage` field to the raw JSON struct in config loading (not to the public `Config` struct)
- [x] Add `DefaultConfig string` field (`json:"default_config"`) to the raw JSON struct
- [x] Implement `LoadMulti(dir string) (map[string]*Config, string, error)` that returns named configs and the default config name
- [x] When `configs` map is absent, `LoadMulti` returns `{"default": <single config>}` with default name `"default"`
- [x] When `configs` map is present, parse each entry as a partial config overlay on top-level defaults
- [x] Inheritance: `json.Unmarshal` top-level into a base `Config`, then for each entry in `configs`, copy the base by value and `json.Unmarshal` the entry's `RawMessage` over the copy
- [x] Write tests: single-config backward compat, multi-config with inheritance, override precedence
- [x] Write test: top-level `cmake_args: ["-DA=1"]` with per-config `cmake_args: ["-DB=2"]` produces `["-DB=2"]` only — replace semantics, not append

### Notes
`cmake_args` in a per-config entry **replaces** the top-level `cmake_args`, it does not append. This is the simplest and least surprising behavior — if a config needs extra args, it lists all of them. The value copy ensures each `Config` is independent so `resolveToolchain()` mutations are isolated.

## 2.2: Validation and environment variable behavior

### Subtasks
- [x] Add `build_dir` uniqueness validation in `LoadMulti` — iterate configs, check for duplicates, return error naming both conflicting config names
- [x] When `configs` map is present, skip `applyEnv()` and emit `slog.Warn("environment variable overrides ignored in multi-config mode")`
- [x] Write test: duplicate build_dir error message format
- [x] Write test: env var ignored in multi-config mode (set env, load, verify value is from JSON not env)
- [x] Write test: env var applied in single-config mode (existing behavior preserved)
- [x] Run `go vet ./...` and `go test -race ./...`

### Notes
The env var suppression is a safety measure: `CPP_BUILD_MCP_BUILD_DIR` applied to all instances would break the build_dir uniqueness invariant. The warning message tells users what happened if they expected env vars to work.

## 2.3: Wire multi-config loading into main() and registry

### Subtasks
- [x] Update `main()` to call `config.LoadMulti(dir)` instead of `config.Load(dir)`
- [x] Create registry entries for each returned config: instantiate `builder.NewBuilder()` and `state.NewStore()` per config
- [x] Set `registry.dflt` to the returned default config name
- [x] Run `resolveToolchain()` per config instance at registry construction time (each may detect a different toolchain; eager detection avoids race risk from concurrent builds both trying to detect lazily)
- [x] Write integration test: multi-config server with two named configs, verify `list_configs` returns both
- [x] Write test: configure one config, verify the other remains unconfigured
- [x] Write test: default config routing when no config param provided
- [x] Run `go vet ./...`, `staticcheck ./...`, `go test -race ./...`

### Notes
`config.Load(dir)` is kept as a thin wrapper over `LoadMulti(dir)` returning the single `"default"` config — the function is exported and removing it would be a breaking change. The key is that `main()` always goes through `LoadMulti()`.

## Acceptance Criteria

- [x] Multi-config JSON file with 2+ configs parses and creates registry entries
- [x] Top-level inheritance works correctly (source_dir, generator, toolchain carry through)
- [x] Duplicate build_dir is rejected at startup with a clear error
- [x] Env vars are suppressed in multi-config mode with a warning
- [x] Single-config JSON files work exactly as before
- [x] `go vet`, `go test -race`, and `staticcheck` all pass
