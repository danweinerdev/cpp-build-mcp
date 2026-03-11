---
title: "build --reconfigure Parameter"
type: phase
plan: ConfigureToolRefactor
phase: 2
status: complete
created: 2026-03-10
updated: 2026-03-10
deliverable: "A reconfigure boolean parameter on the build tool that runs cmake configure before building"
tasks:
  - id: "2.1"
    title: "Add reconfigure parameter to build tool"
    status: complete
    verification: "build tool schema includes reconfigure boolean, go vet passes"
  - id: "2.2"
    title: "Implement reconfigure logic in handleBuild"
    status: complete
    depends_on: ["2.1"]
    verification: "reconfigure=true runs cmake configure then builds, reconfigure=true on unconfigured project auto-configures and builds (Configure + SetConfigured must run BEFORE StartBuild since StartBuild requires PhaseConfigured), reconfigure=false preserves existing behavior (requires prior configure), configure failure aborts build and returns configure diagnostics via shared helper, cmake_args from config are passed through to configure step"
  - id: "2.3"
    title: "Unit tests"
    status: complete
    depends_on: ["2.2"]
    verification: "tests cover: build with reconfigure=true calls Configure then Build, build with reconfigure=false skips configure, reconfigure on unconfigured project succeeds, reconfigure with configure failure returns error without building, reconfigure=true passes through config-level cmake_args, go vet and race detector pass"
---

# Phase 2: build --reconfigure Parameter

## Overview

Add a `reconfigure` boolean parameter to the `build` tool. When true, cmake configure runs before the build step, making a prior `configure` call optional. This is the common "something changed in CMakeLists.txt, rebuild from scratch" workflow.

## 2.1: Add reconfigure parameter to build tool

### Subtasks
- [x] Add `mcp.WithBool("reconfigure", mcp.Description("Re-run CMake configure before building."))` to build tool registration
- [x] Update build tool description to mention the reconfigure option

## 2.2: Implement reconfigure logic in handleBuild

### Subtasks
- [x] Extract `reconfigure` boolean from request arguments
- [x] **Ordering constraint**: when `reconfigure=true`, run `Configure()` + `SetConfigured()` BEFORE calling `StartBuild()`. This is critical because `StartBuild()` requires `PhaseConfigured` (see `state/store.go:71`) — placing configure after `StartBuild()` would always fail on unconfigured projects.
- [x] Extract a shared `runConfigureStep(ctx, inst)` helper from `handleConfigure` that runs cmake, parses output, stores diagnostics, and returns structured results. Use this helper in both `handleConfigure` and `handleBuild` to avoid divergence.
- [x] On configure success: `SetConfigured()` is called by the shared helper, proceed to `StartBuild()`
- [x] On configure failure: return configure error response with structured diagnostics (don't proceed to build)
- [x] If `reconfigure` is false (default), preserve existing behavior — `StartBuild()` still requires `PhaseConfigured`

### Notes
The shared `runConfigureStep` helper should encapsulate the full configure flow from `handleConfigure` lines 591-633: extract cmake_args, call `builder.Configure()`, parse cmake messages/diagnostics, call `SetConfigured()` on success, store diagnostics on failure. Both `handleConfigure` and `handleBuild` (when `reconfigure=true`) call this same function.

The response on configure failure should use the same `configureResponse` format so the client gets structured cmake error information rather than an opaque error string.

## 2.3: Unit tests

### Subtasks
- [x] Test reconfigure=true triggers `Configure` then `Build` on fakeBuilder
- [x] Test reconfigure=false does not call `Configure`
- [x] Test reconfigure=true on unconfigured project succeeds (no prior configure needed)
- [x] Test reconfigure=true with configure failure: `Build` not called, error returned with diagnostics
- [x] Test that config-level `CMakeArgs` are passed to the configure step
- [x] Run with `-race` flag

## Acceptance Criteria
- [ ] `build` tool schema includes `reconfigure` boolean parameter
- [ ] `build --reconfigure=true` runs cmake configure then builds
- [ ] `build --reconfigure=true` works on unconfigured projects (no prior configure needed)
- [ ] Configure failure during `build --reconfigure` returns structured diagnostics
- [ ] `build --reconfigure=false` (default) preserves existing behavior
- [ ] `go vet ./...` and `go test -race ./...` pass
