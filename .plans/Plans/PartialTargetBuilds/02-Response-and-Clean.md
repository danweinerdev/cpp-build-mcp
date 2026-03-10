---
title: "Response Enhancements and Clean Hardening"
type: phase
plan: PartialTargetBuilds
phase: 2
status: complete
created: 2026-03-09
updated: 2026-03-09
deliverable: "build response includes targets_requested field, clean tool no longer accepts targets parameter, handleClean rejects unconfigured projects. All existing tests updated or replaced as needed."
tasks:
  - id: "2.1"
    title: "Add targets_requested to build response"
    status: complete
    verification: "buildResponse struct has TargetsRequested []string with json tag targets_requested,omitempty. handleBuild sets the field to the extracted targets slice (nil when none provided). build(targets: ['app']) response JSON includes 'targets_requested': ['app']. Full build response JSON does not contain 'targets_requested' key at all. Unit test: marshal with nil slice omits field, marshal with ['x'] includes field."
  - id: "2.2"
    title: "Remove targets parameter from clean tool"
    status: complete
    verification: "clean tool registration in main.go no longer includes WithArray('targets', ...). handleClean no longer extracts targets from request. Clean is called with nil targets (existing behavior — CMakeBuilder.buildCleanArgs always ignores targets anyway). Unit test confirms clean tool schema has only config parameter."
  - id: "2.3"
    title: "Add PhaseConfigured precondition to handleClean"
    status: complete
    verification: "handleClean checks store.GetPhase() >= state.PhaseConfigured before calling builder.Clean. Returns tool error 'Project not configured. Call configure() first.' when phase is Unconfigured. store.SetClean() is only called when the clean actually succeeds on a configured project. Unit test: clean on unconfigured store returns tool error. Unit test: clean on configured store succeeds."
  - id: "2.4"
    title: "Add GetPhase method to Store if missing"
    status: complete
    verification: "state.Store.GetPhase() state.Phase returns the current phase under read lock. If this method already exists, this task is a no-op."
  - id: "2.5"
    title: "Structural verification"
    status: complete
    depends_on: ["2.1", "2.2", "2.3", "2.4"]
    verification: "go vet ./... clean. go test -race ./... passes all packages. All pre-existing tests pass (some may need updating if they assert on clean tool schema or buildResponse shape)."
---

# Phase 2: Response Enhancements and Clean Hardening

## Overview

Polish the partial build experience: echo requested targets in the build response so the agent knows what it asked for, remove the misleading `targets` parameter from the `clean` tool, and fix a pre-existing bug where `handleClean` on an unconfigured project incorrectly advances the state machine.

## 2.1: Add targets_requested to build response

### Subtasks
- [x] Add `TargetsRequested []string \`json:"targets_requested,omitempty"\`` to `buildResponse` struct
- [x] In `handleBuild`, set `resp.TargetsRequested = targets` (the variable is already nil when no targets provided)
- [x] Unit test: `json.Marshal` of `buildResponse{TargetsRequested: nil}` does not contain `targets_requested`
- [x] Unit test: `json.Marshal` of `buildResponse{TargetsRequested: []string{"app"}}` contains `"targets_requested":["app"]`
- [x] Verify existing build handler tests still pass (response shape is a superset)

### Notes
The key subtlety: `var targets []string` (nil) vs `targets := []string{}` (non-nil empty). The existing code uses `var targets []string` and appends only when targets are present, so `nil` is the natural zero-value. `omitempty` on a nil slice omits the field. On a non-nil empty slice, it would serialize as `[]`.

## 2.2: Remove targets parameter from clean tool

### Subtasks
- [x] Remove `mcp.WithArray("targets", ...)` from the `clean` tool registration
- [x] Remove the targets extraction block from `handleClean` (lines that extract `rawTargets`)
- [x] Pass `nil` to `inst.builder.Clean(ctx, nil)` explicitly
- [x] Update any tests that reference the clean tool's targets parameter

### Notes
`CMakeBuilder.buildCleanArgs()` already ignores the targets argument — it hardcodes `--target clean`. This change just removes the misleading schema parameter so the AI doesn't think per-target clean is available.

## 2.3: Add PhaseConfigured precondition to handleClean

### Subtasks
- [x] At the top of `handleClean` (after `resolveConfig`), check `inst.store.GetPhase() < state.PhaseConfigured`
- [x] If unconfigured, return `mcp.NewToolResultError("Project not configured. Call configure() first.")`
- [x] Unit test: new store (phase Unconfigured) → clean returns tool error
- [x] Unit test: store after SetConfigured → clean succeeds
- [x] Verify `store.SetClean()` is only called after a successful clean on a configured project

### Notes
This fixes a pre-existing bug: without this guard, `handleClean` calls `store.SetClean()` even on an unconfigured project. `SetClean()` sets the phase to `PhaseConfigured`, which would make `build` callable before any configure step has run. The guard prevents this state machine violation.

## 2.4: Add GetPhase method to Store if missing

### Subtasks
- [x] Check if `state.Store` already has a `GetPhase()` or equivalent method — **already exists at state/store.go:207**
- [x] No-op: method already existed

### Notes
The `StartBuild` method internally checks phase, but there may not be a public accessor. If one exists, this task is a no-op.

## 2.5: Structural verification

### Subtasks
- [x] `go vet ./...`
- [x] `go test -race ./...`
- [x] Review any test failures from schema changes (clean targets removed, buildResponse shape change)
- [x] Confirm all pre-existing tests pass

## Acceptance Criteria

- [x] `build(targets: ["app"])` response includes `"targets_requested": ["app"]`
- [x] `build()` (no targets) response does not contain `targets_requested` key
- [x] `clean` tool schema has only `config` parameter
- [x] `clean` on unconfigured project returns tool error
- [x] `clean` on configured project succeeds and calls `SetClean`
- [x] All existing tests pass (updated as needed)
