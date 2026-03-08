---
title: "Registry Foundation"
type: phase
plan: MultiConfigSupport
phase: 1
status: complete
created: 2026-03-07
updated: 2026-03-07
deliverable: "Config registry abstraction with all tool handlers routing through it; single 'default' instance; fully backward compatible"
tasks:
  - id: "1.1"
    title: "Promote SetDirty to Builder interface"
    status: complete
    verification: "SetDirty(bool) is in the Builder interface definition. CMakeBuilder and MakeBuilder satisfy it. The type switch in handleBuild is gone — dirty flag propagation uses the interface method. go vet and all existing tests pass."
  - id: "1.2"
    title: "Implement configRegistry and configInstance"
    status: complete
    verification: "get('default') returns the instance. get('unknown') returns an error whose message lists available config names. defaultInstance() returns the default. list() returns all instances with name, build_dir, and status. go test -race passes (registry uses sync.RWMutex)."
  - id: "1.3"
    title: "Refactor mcpServer and handlers to use registry"
    status: complete
    depends_on: ["1.1", "1.2"]
    verification: "mcpServer has a registry field, no builder/store/cfg fields. All 8 tool handlers resolve config via registry.get(); handleBuildHealth uses registry.defaultInstance() (MCP resources have no parameters — aggregate behavior comes in Phase 3). main() creates a registry with one 'default' entry. Test setup helpers (newTestServer, startE2E) are updated to construct mcpServer via configRegistry. All existing test assertions pass unchanged — the default routing is transparent."
  - id: "1.4"
    title: "Add config parameter to tools and list_configs tool"
    status: complete
    depends_on: ["1.3"]
    verification: "All 8 tools accept an optional 'config' string parameter. New list_configs tool returns [{name:'default', build_dir, status}] in single-config mode. Tools with explicit config='default' route correctly. Tools without config param route to default. go vet, go test -race, and staticcheck all clean."
---

# Phase 1: Registry Foundation

## Overview

Introduce the `configRegistry` abstraction and refactor all tool handlers to route through it. After this phase, the server internally uses a registry with a single `"default"` entry. External behavior is identical to the current server — full backward compatibility. This phase is the foundation that Phase 2 builds on to add multi-config file parsing.

## 1.1: Promote SetDirty to Builder interface

### Subtasks
- [x] Add `SetDirty(dirty bool)` to the `Builder` interface in `builder/builder.go`
- [x] Verify `CMakeBuilder` and `MakeBuilder` already satisfy the interface (they do — both have `SetDirty`)
- [x] Remove the type switch on `builder.Builder` in `handleBuild` that calls `SetDirty` — replace with direct `srv.builder.SetDirty(true)` / `srv.builder.SetDirty(false)` via the interface
- [x] Run `go vet ./...` and `go test -race ./...`

### Notes
This is a prerequisite cleanup. The type switch in `handleBuild` currently checks for `*CMakeBuilder` and `*MakeBuilder` individually. Promoting `SetDirty` to the interface eliminates this and makes dirty-flag propagation work naturally with per-config builder instances.

## 1.2: Implement configRegistry and configInstance

### Subtasks
- [x] Define `configInstance` struct: `name string`, `cfg *config.Config`, `builder builder.Builder`, `store *state.Store`
- [x] Define `configRegistry` struct: `mu sync.RWMutex`, `instances map[string]*configInstance`, `dflt string`
- [x] Implement `get(name string) (*configInstance, error)` — returns error with available config names on unknown
- [x] Implement `defaultInstance() *configInstance`
- [x] Implement `list() []ConfigSummary` with `ConfigSummary{Name, BuildDir, Status}`
- [x] Implement `storeStatusToken(store *state.Store) string` helper that maps store state to compact tokens: `"unconfigured"`, `"configured"`, `"built"`, `"building"`, `"dirty"` — used by `list()` and later by aggregate health (Phase 3)
- [x] Write unit tests for all registry methods including error case and status token mapping
- [x] Run `go test -race ./...` to verify RWMutex correctness

### Notes
The registry stays in `main.go` (or a `registry.go` file in the main package) to avoid exporting internal types. The `get()` method must return an error message that includes the list of available config names — this helps the AI agent self-correct when it uses a wrong config name.

## 1.3: Refactor mcpServer and handlers to use registry

### Subtasks
- [x] Replace `mcpServer.builder`, `mcpServer.store`, `mcpServer.cfg` fields with `mcpServer.registry *configRegistry`
- [x] Add a helper method `resolveConfig(req mcp.CallToolRequest) (*configInstance, error)` that extracts the optional `config` string param and calls `registry.get()` (or `registry.defaultInstance()` if absent)
- [x] Refactor `handleBuild` to use `resolveConfig` + instance fields
- [x] Refactor `handleConfigure` to use `resolveConfig` + instance fields
- [x] Refactor `handleClean` to use `resolveConfig` + instance fields
- [x] Refactor `handleGetErrors` to use `resolveConfig` + instance fields
- [x] Refactor `handleGetWarnings` to use `resolveConfig` + instance fields
- [x] Refactor `handleSuggestFix` to use `resolveConfig` + instance fields
- [x] Refactor `handleGetChangedFiles` to use `resolveConfig` + instance fields
- [x] Refactor `handleGetBuildGraph` to use `resolveConfig` + instance fields
- [x] Refactor `handleBuildHealth` to use `registry.defaultInstance()` directly (MCP resources don't take parameters; aggregate behavior comes in Phase 3)
- [x] Update `main()` to create a `configRegistry` with one `"default"` entry from the loaded config
- [x] Refactor `resolveToolchain()` signature to `resolveToolchain(inst *configInstance) string` — reads from `inst.cfg` instead of `srv.cfg`, runs eagerly at registry construction time (not lazily at first build) to avoid race risk with concurrent builds
- [x] Update `newTestServer()` in `main_test.go` to construct `mcpServer` via `configRegistry` with a `"default"` entry
- [x] Update `startE2E()` in `e2e_test.go` to construct `mcpServer` via `configRegistry` with a `"default"` entry
- [x] Run all existing tests — test setup code changes but all test assertions must pass unchanged

### Notes
The critical property is backward compatibility. Since no existing tool call provides a `config` parameter, every handler must fall through to the default instance. The test assertions are the proof: if all assertions pass without changes, the refactor is correct. Test setup helpers (`newTestServer`, `startE2E`) do require structural changes to construct the registry-based `mcpServer` — this is expected and unavoidable.

## 1.4: Add config parameter to tools and list_configs tool

### Subtasks
- [x] Add optional `config` string parameter to all 8 tool registrations (configure, build, get_errors, get_warnings, suggest_fix, clean, get_changed_files, get_build_graph)
- [x] Implement `handleListConfigs` handler returning `{configs: [...], default_config: string}`
- [x] Register `list_configs` tool with MCP server
- [x] Add test: `list_configs` returns one entry `{name: "default", build_dir: "build", status: "unconfigured"}`
- [x] Add test: tool call with explicit `config: "default"` routes correctly
- [x] Add test: tool call with `config: "nonexistent"` returns MCP error with available names
- [x] Update E2E test to register `list_configs` tool
- [x] Run `go vet ./...`, `staticcheck ./...`, `go test -race ./...`

### Notes
The `config` parameter description should be: `"Configuration name (omit for default)"`. The `list_configs` response helps the AI agent discover available configurations and their current states.

## Acceptance Criteria

- [x] `mcpServer` no longer has `builder`, `store`, or `cfg` fields — only `registry`
- [x] `SetDirty` is on the `Builder` interface, no type switches remain
- [x] All existing test assertions pass unchanged (test setup helpers updated for registry construction)
- [x] `list_configs` tool works and returns the default config
- [x] `go vet`, `go test -race`, and `staticcheck` all pass
