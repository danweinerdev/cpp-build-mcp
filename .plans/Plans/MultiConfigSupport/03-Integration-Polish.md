---
title: "Integration and Polish"
type: phase
plan: MultiConfigSupport
phase: 3
status: complete
created: 2026-03-07
updated: 2026-03-08
deliverable: "Aggregate health, config field in responses, E2E tests proving state isolation, updated documentation"
tasks:
  - id: "3.1"
    title: "Aggregate health resource"
    status: complete
    verification: "Single config: build://health returns the existing format unchanged (backward compatible). Multiple configs: returns pipe-separated format 'debug: OK | release: FAIL(3 errors) | asan: UNCONFIGURED'. Dirty state renders as 'debug: DIRTY'. Format matches design spec exactly."
  - id: "3.2"
    title: "Config field in responses and cross-config routing"
    status: complete
    verification: "build, get_errors, get_warnings, suggest_fix, configure, clean, get_changed_files responses all include a 'config' field matching the resolved config name. get_build_graph reads compile_commands.json from the per-config build_dir (not a hardcoded path). get_changed_files uses the per-config last-successful-build timestamp. Tests verify each tool's response contains the config field."
  - id: "3.3"
    title: "E2E integration tests and documentation"
    status: complete
    depends_on: ["3.1", "3.2"]
    verification: "E2E test with two named fakeBuilder configs proves: configuring one doesn't affect the other, building one doesn't change the other's state, errors from one config don't appear in the other's get_errors. README multi-config section includes config file example and session walkthrough. go vet, go test -race, staticcheck all pass."
---

# Phase 3: Integration and Polish

## Overview

Complete the multi-config feature with aggregate health reporting, config provenance in tool responses, comprehensive E2E testing, and documentation. After this phase, multi-config support is production-ready.

## 3.1: Aggregate health resource

### Subtasks
- [x] Update `handleBuildHealth` to iterate all registry instances when multiple configs exist
- [x] Use `aggregateHealthToken()` to generate compact per-config tokens (`OK`, `FAIL(N errors)`, `UNCONFIGURED`, `DIRTY`, `READY`, `BUILDING`) for the aggregate format
- [x] Single config: return the existing format verbatim (e.g., `"OK: 0 errors, 2 warnings, last build 30s ago"`)
- [x] Multiple configs: return pipe-separated format: `"debug: OK | release: FAIL(3 errors) | asan: UNCONFIGURED"`
- [x] Handle dirty state: `"debug: DIRTY"`
- [x] Write test: single config returns existing format
- [x] Write test: multiple configs with mixed states return correct aggregate format
- [x] Write test: dirty config renders as DIRTY in aggregate

### Notes
The aggregate format is designed to be scannable in a single line. The AI agent can call `list_configs` for more detail, or `get_errors(config: "release")` for the specific errors. A new `aggregateHealthToken()` was created instead of reusing `storeStatusToken()` — the existing helper returns lowercase tokens and maps `PhaseBuilt` to `"built"`, which doesn't match the required uppercase `"OK"`/`"FAIL(N errors)"` format.

## 3.2: Config field in responses and cross-config routing

### Subtasks
- [x] Add `config` string field to `buildResponse`, `configureResponse`, `cleanResponse`, `suggestFixResponse`, `changedFilesResponse` structs
- [x] Add `config` field to `get_errors`, `get_warnings`, and `get_build_graph` response JSON
- [x] Set the config field in each handler to the resolved config name
- [x] Verify `get_build_graph` passes `instance.cfg.BuildDir` to `graph.ReadSummary` (should already work from Phase 1 refactor — confirm with test)
- [x] Verify `get_changed_files` uses `instance.store.LastSuccessfulBuildTime()` and `instance.cfg.SourceDir`/`instance.cfg.BuildDir` (should already work — confirm with test)
- [x] Write tests: each tool's response JSON contains `"config"` field
- [x] Write test: two configs with different build_dirs, `get_build_graph` reads from the correct one

### Notes
The `config` field in responses is important for the AI agent's mental model — it confirms which configuration produced the result, especially when the agent is switching between configs rapidly.

## 3.3: E2E integration tests and documentation

### Subtasks
- [x] Write E2E test: create two named configs ("debug", "release") with fakeBuilders
- [x] Test: configure("debug") sets debug to Configured, release remains Unconfigured
- [x] Test: build("debug") with errors, get_errors("release") returns empty (state isolation)
- [x] Test: build both configs, verify independent error/warning counts
- [x] Test: list_configs returns both with correct statuses after operations
- [x] Test: health resource returns aggregate with both configs' states
- [x] Update README.md with multi-config section:
  - Config file example with `configs` map
  - Session walkthrough showing cross-config workflow
  - Updated config reference table with new fields
  - Updated tool reference with `config` parameter
- [x] Run `go vet ./...`, `staticcheck ./...`, `go test -race ./...`

### Notes
The E2E tests are the ultimate proof of state isolation — the core property that makes multi-config work. Each config must be completely independent: its own Builder, Store, and Config instance.

## Acceptance Criteria

- [x] `build://health` shows aggregate status across all configs
- [x] All tool responses include `config` field
- [x] E2E tests prove complete state isolation between configs
- [x] README documents multi-config feature with examples
- [x] `go vet`, `go test -race`, and `staticcheck` all pass
- [x] Single-config mode behavior is completely unchanged
