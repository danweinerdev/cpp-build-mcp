---
title: "Target Discovery"
type: phase
plan: PartialTargetBuilds
phase: 1
status: planned
created: 2026-03-09
updated: 2026-03-09
deliverable: "list_targets MCP tool that enumerates user-defined CMake targets, with unit tests for both Ninja and Makefile generator output parsing. Builder interface extended with ListTargets method."
tasks:
  - id: "1.1"
    title: "Extend Builder interface with ListTargets"
    status: planned
    verification: "Builder interface in builder/builder.go includes ListTargets(ctx context.Context) ([]TargetInfo, error). TargetInfo struct has Name string field with json tag. ErrTargetsNotSupported sentinel error is exported. go vet passes."
  - id: "1.2"
    title: "Implement parseTargetList parser"
    status: planned
    depends_on: ["1.1"]
    verification: "parseTargetList(stdout string) []TargetInfo correctly parses both Ninja format (bare lines, optional ': phony' suffix) and Makefile format ('... name' prefix with optional parenthetical). Internal targets are filtered: all, clean, help, depend, edit_cache, rebuild_cache, install, install/local, install/strip, list_install_components, package, package_source, test, RUN_TESTS, NightlyMemoryCheck, targets containing '/', targets ending in .o or .obj. Empty input returns empty slice. Unit tests use inline fixture strings from the design's Parser Specification."
  - id: "1.3"
    title: "Implement CMakeBuilder.ListTargets"
    status: planned
    depends_on: ["1.2"]
    verification: "CMakeBuilder.ListTargets runs cmake --build <buildDir> --target help, passes stdout to parseTargetList, returns result. Non-zero exit code returns error with stderr content. Unit test with mock exec verifies the command args."
  - id: "1.4"
    title: "Implement MakeBuilder.ListTargets"
    status: planned
    depends_on: ["1.1"]
    verification: "MakeBuilder.ListTargets returns ErrTargetsNotSupported. Unit test asserts errors.Is(err, ErrTargetsNotSupported)."
  - id: "1.5"
    title: "Add list_targets MCP tool"
    status: planned
    depends_on: ["1.3", "1.4"]
    verification: "list_targets tool registered in main.go with config? string parameter. Handler checks store.GetPhase() >= PhaseConfigured (returns tool error if not). Handler checks store.IsBuilding() (returns tool error if build in progress). On success, returns JSON {config, targets: [{name}], count}. On ErrTargetsNotSupported, returns tool error with clear message. Unit tests with mock builder: success case, unconfigured case, build-in-progress case, ErrTargetsNotSupported case."
  - id: "1.6"
    title: "Add IsBuilding method to Store"
    status: planned
    verification: "state.Store.IsBuilding() bool returns the current BuildInProgress flag under read lock. Unit test verifies it returns false initially, true between StartBuild/FinishBuild."
  - id: "1.7"
    title: "Structural verification"
    status: planned
    depends_on: ["1.5", "1.6"]
    verification: "go vet ./... clean. go test -race ./... passes all packages. All pre-existing tests pass unchanged."
---

# Phase 1: Target Discovery

## Overview

Add the `list_targets` MCP tool end-to-end: extend the `Builder` interface, implement the cmake `--target help` parser with internal target filtering, wire up the MCP handler with precondition guards, and add comprehensive unit tests.

## 1.1: Extend Builder interface with ListTargets

### Subtasks
- [ ] Add `TargetInfo` struct to `builder/builder.go`: `Name string \`json:"name"\``
- [ ] Add `ErrTargetsNotSupported = errors.New("target listing not supported for this build system")` to `builder/builder.go`
- [ ] Add `ListTargets(ctx context.Context) ([]TargetInfo, error)` to the `Builder` interface

### Notes
This is an interface-breaking change. Both `CMakeBuilder` and `MakeBuilder` must implement the new method before the code compiles.

## 1.2: Implement parseTargetList parser

### Subtasks
- [ ] Create `parseTargetList(stdout string) []TargetInfo` in `builder/cmake.go`
- [ ] Detect Ninja format: lines that are bare target names (no `... ` prefix)
- [ ] Detect Makefile format: lines starting with `... ` — strip prefix and parenthetical suffix
- [ ] Strip optional `: phony` or `: PHONY` suffix (some Ninja versions)
- [ ] Build internal target exclusion set: `all`, `clean`, `help`, `depend`, `edit_cache`, `rebuild_cache`, `install`, `install/local`, `install/strip`, `list_install_components`, `package`, `package_source`, `test`, `RUN_TESTS`, `NightlyMemoryCheck`
- [ ] Exclude targets containing `/` (CMakeFiles directory targets)
- [ ] Exclude targets ending in `.o` or `.obj` (object file targets)
- [ ] Skip empty lines and the Makefile header line (`The following are some...`)
- [ ] Unit tests in `builder/cmake_test.go` with inline fixture strings for both formats
- [ ] Unit test: empty input returns `[]TargetInfo{}`
- [ ] Unit test: all internal targets filtered, only user targets remain

### Notes
The Ninja and Makefile format fixtures come directly from the design's Parser Specification section. Use those exact strings as test input.

## 1.3: Implement CMakeBuilder.ListTargets

### Subtasks
- [ ] Implement `CMakeBuilder.ListTargets(ctx)`: run `exec.CommandContext(ctx, "cmake", "--build", b.cfg.BuildDir, "--target", "help")`
- [ ] Capture stdout, pass to `parseTargetList`
- [ ] On non-zero exit code, return `fmt.Errorf("cmake --target help failed: %s", stderr)`
- [ ] Unit test: verify command args are `["--build", "<dir>", "--target", "help"]`

### Notes
Reuses the `run` method pattern from Build/Configure/Clean but does not need progress scanning or timeout wrapping. Consider a simpler `exec.CommandContext` call directly since `run` has progress/timeout features that don't apply here.

## 1.4: Implement MakeBuilder.ListTargets

### Subtasks
- [ ] Implement `MakeBuilder.ListTargets(ctx)`: `return nil, ErrTargetsNotSupported`
- [ ] Unit test: `errors.Is(err, builder.ErrTargetsNotSupported)` returns true

## 1.5: Add list_targets MCP tool

### Subtasks
- [ ] Register `list_targets` tool in `main.go` with `mcp.WithString("config", ...)` parameter
- [ ] Define `listTargetsResponse` struct: `Config string`, `Targets []TargetInfo`, `Count int`
- [ ] Implement `handleListTargets` handler:
  - Resolve config via `resolveConfig(req)`
  - Check `inst.store.GetPhase() >= state.PhaseConfigured` — return tool error if not
  - Check `inst.store.IsBuilding()` — return tool error if true
  - Call `inst.builder.ListTargets(ctx)`
  - If `errors.Is(err, builder.ErrTargetsNotSupported)`, return tool error with clear message
  - If other error, return tool error with stderr content
  - Marshal and return response
- [ ] Unit test: mock builder returns 3 targets → response has count 3
- [ ] Unit test: unconfigured state → tool error "not configured"
- [ ] Unit test: build in progress → tool error "build in progress"
- [ ] Unit test: ErrTargetsNotSupported → tool error about Make

## 1.6: Add IsBuilding method to Store

### Subtasks
- [ ] Add `IsBuilding() bool` to `state/store.go` — acquires read lock, returns `s.state.BuildInProgress`
- [ ] Unit test: false initially, true after `StartBuild`, false after `FinishBuild`

### Notes
`StartBuild` already sets `BuildInProgress = true` under write lock. `IsBuilding` needs a read lock to check the flag. This is a simple accessor.

## 1.7: Structural verification

### Subtasks
- [ ] `go vet ./...`
- [ ] `go test -race ./...`
- [ ] Confirm all pre-existing tests pass

## Acceptance Criteria

- [ ] `list_targets` tool appears in MCP tool list
- [ ] Ninja format parser extracts user targets, filters internals
- [ ] Makefile format parser extracts user targets, filters internals
- [ ] Handler rejects calls before configure
- [ ] Handler rejects calls during a build
- [ ] MakeBuilder returns `ErrTargetsNotSupported`
- [ ] All existing tests pass unchanged
