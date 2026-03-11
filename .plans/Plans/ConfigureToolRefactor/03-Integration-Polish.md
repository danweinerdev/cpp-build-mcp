---
title: "Integration Tests & Polish"
type: phase
plan: ConfigureToolRefactor
phase: 3
status: complete
created: 2026-03-10
updated: 2026-03-10
deliverable: "Integration/E2E test coverage and updated tool descriptions"
tasks:
  - id: "3.1"
    title: "Integration tests for load_presets"
    status: complete
    verification: "integration test creates a project with CMakePresets.json, calls load_presets, verifies configs are loaded, modifies presets file, calls load_presets again, verifies registry updated"
  - id: "3.2"
    title: "Integration tests for build --reconfigure"
    status: complete
    verification: "integration test calls build with reconfigure=true on a real CMake project, verifies cmake configure runs and build succeeds, test also covers reconfigure after CMakeLists.txt change"
  - id: "3.3"
    title: "E2E test updates"
    status: complete
    depends_on: ["3.1", "3.2"]
    verification: "e2e tests include load_presets tool in server setup, test the full JSON-RPC flow for load_presets and build --reconfigure"
  - id: "3.4"
    title: "Tool descriptions and research note cleanup"
    status: complete
    verification: "configure tool description clearly states it runs cmake, load_presets description explains it reloads config from disk, build description mentions reconfigure option, research note status updated to reflect implementation"
---

# Phase 3: Integration Tests & Polish

## Overview

Add integration and E2E test coverage for the new `load_presets` tool and `build --reconfigure` parameter, update tool descriptions for clarity, and close out the research note.

## 3.1: Integration tests for load_presets

### Subtasks
- [x] Test with cmake-presets fixture: call load_presets, verify configs match preset file
- [x] Test reload after modifying CMakePresets.json: add a preset, call load_presets, verify new config appears
- [x] Test reload after removing a preset: verify config is dropped from registry

### Notes
Use the existing `copyFixture` helper and `cmake-presets` test fixtures. Modify the copied preset file between load_presets calls to test reload behavior.

## 3.2: Integration tests for build --reconfigure

### Subtasks
- [x] Test build with reconfigure=true on a fresh (unconfigured) project — should configure and build successfully
- [x] Test build with reconfigure=true on an already-configured project — should re-run configure then build
- [x] Test build with reconfigure=true when CMakeLists.txt has an error — should return configure diagnostics

### Notes
Use the existing CMake project fixtures. The `requireCMake` helper ensures cmake is available.

## 3.3: E2E test updates

### Subtasks
- [x] Add `load_presets` tool registration to e2e test server setup
- [x] Add e2e test case calling `load_presets` via JSON-RPC
- [x] Add e2e test case calling `build` with `reconfigure: true` via JSON-RPC

## 3.4: Tool descriptions and research note cleanup

### Subtasks
- [x] Review and update `configure` tool description (already accurate, but confirm)
- [x] Ensure `load_presets` description clearly differentiates from `configure`
- [x] Update `build` tool description to mention `reconfigure` parameter
- [x] Update `.plans/Research/ConfigureToolRename.md` status to `archived`, note that Option D was implemented with the adjustment that `configure` was already running cmake

## Acceptance Criteria
- [ ] All integration tests pass with real cmake
- [ ] E2E tests cover both new features via JSON-RPC
- [ ] Tool descriptions clearly explain the three-tool workflow
- [ ] Research note closed out
- [ ] `go vet ./...` and `go test -race ./...` pass
