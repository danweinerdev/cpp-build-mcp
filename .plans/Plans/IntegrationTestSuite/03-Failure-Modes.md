---
title: "Failure Mode Tests"
type: phase
plan: IntegrationTestSuite
phase: 3
status: complete
created: 2026-03-09
updated: 2026-03-09
deliverable: "2 new fixtures and 2 test functions validating negative paths: linker errors (no structured diagnostics) and configure failures (cmake error, not compiler)"
tasks:
  - id: "3.1"
    title: "Linker error test and cmake-linker-error fixture"
    status: complete
    verification: "testdata/cmake-linker-error/ fixture exists with CMakeLists.txt, lib.h (declares undefined_function), main.cpp (calls it). TestIntegrationLinkerError runs per toolchain. Configure succeeds. Build fails (ExitCode != 0). diagnostics.Parse returns nil or empty slice (linker errors are not structured compiler diagnostics). BuildResult output contains the undefined symbol name. Works for both Clang and GCC linkers."
  - id: "3.2"
    title: "Configure error test and cmake-configure-error fixture"
    status: complete
    verification: "testdata/cmake-configure-error/ fixture exists with CMakeLists.txt containing message(FATAL_ERROR 'intentional configure failure'). TestIntegrationConfigureError runs (no per-toolchain loop needed — configure error is cmake, not compiler). Configure fails (ExitCode != 0). BuildResult.Stderr contains 'intentional configure failure'."
  - id: "3.3"
    title: "Structural verification"
    status: complete
    depends_on: ["3.1", "3.2"]
    verification: "go vet ./... passes. go test -race ./... passes all packages. All pre-existing tests still pass."
---

# Phase 3: Failure Mode Tests

## Overview

Validate the negative paths — what happens when the failure isn't a parseable compiler diagnostic. Linker errors are unstructured text. Configure errors are cmake messages, not compiler output. Both should be handled gracefully without producing phantom diagnostics.

## 3.1: Linker error test and cmake-linker-error fixture

### Subtasks
- [x] Create `testdata/cmake-linker-error/CMakeLists.txt`
- [x] Create `testdata/cmake-linker-error/lib.h`
- [x] Create `testdata/cmake-linker-error/main.cpp`
- [x] `TestIntegrationLinkerError` — `requireCMake`, `requireNinja`, loop `toolchainCases`
- [x] Configure with `InjectDiagnosticFlags: true` succeeds
- [x] Build fails (ExitCode != 0)
- [x] `parser.Parse(result.Stdout, result.Stderr)` returns nil or empty — no structured compiler diagnostics
- [x] Assert output contains `"undefined_function"` — the linker error is in raw output
- [x] Log stdout and stderr for debugging

### Notes
This tests an important boundary: compilation succeeds (all TUs compile), but linking fails. The diagnostic parser should not produce phantom diagnostics from linker error text.

Implementation note: on this system, ninja writes linker errors to stdout (not stderr), so the test checks both streams for the undefined symbol name.

## 3.2: Configure error test and cmake-configure-error fixture

### Subtasks
- [x] Create `testdata/cmake-configure-error/CMakeLists.txt`
- [x] Create `testdata/cmake-configure-error/main.cpp`
- [x] `TestIntegrationConfigureError` — `requireCMake`, `requireNinja` (no toolchain loop)
- [x] Configure fails (ExitCode != 0)
- [x] Assert `result.Stderr` contains `"intentional configure failure"`
- [x] Do NOT attempt Build — configuration failed

### Notes
The simplest test in the suite but covers an important edge case: configure-time failures.

## 3.3: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available

## Acceptance Criteria

- [x] Linker errors don't produce phantom diagnostics
- [x] Linker error text is available in build output
- [x] Configure failures return non-zero exit code with error message
- [x] `go vet`, `go test -race` pass
