---
title: "Progress, Presets, and Targets"
type: phase
plan: IntegrationTestSuite
phase: 4
status: complete
created: 2026-03-09
updated: 2026-03-09
deliverable: "2 new fixtures, 3 test functions validating progress callbacks from real Ninja output, preset-mode configure with nested binaryDir, and target-specific builds"
tasks:
  - id: "4.1"
    title: "Progress callback test and cmake-library fixture"
    status: complete
    verification: "testdata/cmake-library/ fixture exists with CMakeLists.txt defining a static library (lib.cpp/lib.h) and executable (main.cpp). TestIntegrationProgress runs per toolchain. Configure succeeds. SetProgressFunc called before Build. Build succeeds. Progress callback fired at least once (count > 0). Final callback has current == total. total > 1 (multiple TUs). Each toolchain subtest has its own collectProgress() and event slice — no shared state. go test -race passes."
  - id: "4.2"
    title: "Preset path resolution test and cmake-presets fixture"
    status: complete
    verification: "testdata/cmake-presets/ fixture exists with CMakePresets.json defining a preset with binaryDir '${sourceDir}/build/integration-test'. TestIntegrationPresets skips if cmake < 3.21 (requireCMakeMinVersion). Runs with single detected toolchain (not per-toolchain loop). Config constructed manually with Preset name, BuildDir set to absolute path <tempDir>/build/integration-test, InjectDiagnosticFlags=true. Configure with --preset succeeds. .cpp-build-mcp/DiagnosticFormat.cmake exists inside the nested build dir (proves MkdirAll created the path). Build succeeds."
  - id: "4.3"
    title: "Target build test"
    status: complete
    depends_on: ["4.1"]
    verification: "TestIntegrationTargetBuild uses the cmake-library fixture. After configure, Build with targets=['lib'] succeeds (ExitCode == 0). Build with targets=['main'] succeeds (ExitCode == 0). Validates target-passing plumbing reaches cmake correctly."
  - id: "4.4"
    title: "Structural verification"
    status: complete
    depends_on: ["4.1", "4.2", "4.3"]
    verification: "go vet ./... passes. go test -race ./... passes all packages. All pre-existing tests still pass. Full integration test suite runs end-to-end on a machine with cmake + ninja + at least one compiler."
---

# Phase 4: Progress, Presets, and Targets

## Overview

Validate the two highest-profile post-completion bugs (progress stream, preset path resolution) and multi-TU build behavior. These tests exercise features that unit tests fundamentally cannot: real Ninja progress output on stdout, and cmake's interaction with CMAKE_PROJECT_INCLUDE absolute paths in nested preset build directories.

## 4.1: Progress callback test and cmake-library fixture

### Subtasks
- [x] Create `testdata/cmake-library/CMakeLists.txt`
- [x] Create `testdata/cmake-library/lib.h`
- [x] Create `testdata/cmake-library/lib.cpp`
- [x] Create `testdata/cmake-library/main.cpp`
- [x] `TestIntegrationProgress` — `requireCMake`, `requireNinja`, loop `toolchainCases`
- [x] Per subtest: `copyFixture(t, "cmake-library")`, configure
- [x] `fn, events := collectProgress(t)` — each subtest gets its own
- [x] `b.SetProgressFunc(fn)` before Build
- [x] Build succeeds
- [x] Assert `len(*events) > 0` — at least one callback fired
- [x] Assert final callback has `current == total`
- [x] Assert `total > 1` — multiple TUs

### Notes
This directly validates the progress stream fix. The default 250ms throttle is fine — the first matching line always fires and the final N==M line always fires regardless of throttle.

## 4.2: Preset path resolution test and cmake-presets fixture

### Subtasks
- [x] Create `testdata/cmake-presets/CMakeLists.txt`
- [x] Create `testdata/cmake-presets/CMakePresets.json`
- [x] Create `testdata/cmake-presets/main.cpp`
- [x] `TestIntegrationPresets` — `requireCMake`, `requireNinja`, `requireCMakeMinVersion(t, 3, 21)`, `detectToolchain(t)`
- [x] `copyFixture(t, "cmake-presets")`, construct `config.Config` manually with Preset name
- [x] Configure with `--preset` succeeds (ExitCode == 0)
- [x] Assert `DiagnosticFormat.cmake` exists inside the nested build dir
- [x] Build succeeds (ExitCode == 0)

### Notes
This directly validates the preset path resolution bug fix. `requireCMakeMinVersion(t, 3, 21)` parses `cmake --version` output and skips with a clear message if the version is too old.

## 4.3: Target build test

### Subtasks
- [x] `TestIntegrationTargetBuild` — `requireCMake`, `requireNinja`, `detectToolchain(t)`
- [x] `copyFixture(t, "cmake-library")`, configure
- [x] `b.Build(ctx, []string{"lib"}, 0)` — assert ExitCode == 0
- [x] `b.Build(ctx, []string{"main"}, 0)` — assert ExitCode == 0

### Notes
This validates that the `--target` argument plumbing in `buildBuildArgs` works end-to-end.

## 4.4: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available
- [x] Confirm all pre-existing tests still pass
- [x] Run full integration suite end-to-end: `go test -v -run TestIntegration ./...`

## Acceptance Criteria

- [x] Progress callback fires with real Ninja `[N/M]` output
- [x] Final progress event has current == total with total > 1
- [x] Preset configure succeeds with nested binaryDir
- [x] DiagnosticFormat.cmake written to absolute path inside nested build dir
- [x] Target builds succeed for individual targets
- [x] `go test -race` passes (critical — progress goroutine + real I/O)
- [x] Full integration suite: 12 test categories, all passing
