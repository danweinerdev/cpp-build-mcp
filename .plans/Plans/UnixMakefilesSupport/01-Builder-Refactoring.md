---
title: "Builder Refactoring"
type: phase
plan: UnixMakefilesSupport
phase: 1
status: complete
created: 2026-03-11
updated: 2026-03-11
deliverable: "NewBuilder routes 'make' to CMakeBuilder, buildBuildArgs uses --parallel and nativeKeepGoingFlags"
tasks:
  - id: "1.1"
    title: "Update NewBuilder routing and unit tests"
    status: complete
    verification: "NewBuilder('make') returns *CMakeBuilder, NewBuilder('plain-make') returns *MakeBuilder, NewBuilder('bogus') error message lists ninja/make/plain-make. TestNewBuilderFactory passes with updated assertions."
  - id: "1.2"
    title: "Add nativeKeepGoingFlags and refactor buildBuildArgs"
    status: complete
    verification: "buildBuildArgs emits --parallel N (not -- -jN) for parallelism. DiagnosticSerialBuild with ninja emits --parallel 1 -- -k 0. DiagnosticSerialBuild with make emits --parallel 1 -- -k. Non-serial build emits no -- separator. TestNativeKeepGoingFlags covers ninja/empty/make/unknown. TestBuildBuildArgs updated assertions pass."
  - id: "1.3"
    title: "Clean up stale comments"
    status: complete
    depends_on: ["1.1", "1.2"]
    verification: "generatorCMakeName 'make' case has no unreachable-branch comment. CMakeBuilder struct comment no longer says 'make support is planned'. go vet ./... passes."
---

# Phase 1: Builder Refactoring

## Overview

Core production code changes to `builder/builder.go` and `builder/cmake.go`, plus atomic unit test updates in `builder/cmake_test.go`. These changes must land together because existing unit tests will fail as soon as routing or argument construction changes.

## 1.1: Update NewBuilder routing and unit tests

### Subtasks
- [x] In `builder.go`: change `case "make":` to return `NewCMakeBuilder(cfg)` instead of `NewMakeBuilder(cfg)`
- [x] Add `case "plain-make":` returning `NewMakeBuilder(cfg)`
- [x] Update error message on line 49 from `"supported: ninja, make"` to `"supported: ninja, make, plain-make"`
- [x] In `cmake_test.go` `TestNewBuilderFactory`: update `"make returns MakeBuilder"` subtest to assert `*CMakeBuilder`
- [x] Add `"plain-make returns MakeBuilder"` subtest asserting `*MakeBuilder`
- [x] Add `"unknown generator"` subtest confirming error message contains `plain-make`

### Notes
The `case "make"` and `case "ninja", ""` cases now both return `CMakeBuilder`. The generator-specific behavior (Ninja vs Unix Makefiles) is handled by `generatorCMakeName` inside `buildConfigureArgs`, which already has the `"make" → "Unix Makefiles"` mapping.

## 1.2: Add nativeKeepGoingFlags and refactor buildBuildArgs

### Subtasks
- [x] Add `nativeKeepGoingFlags(gen string) []string` function to `cmake.go` with cases: `"ninja"/"" → ["-k","0"]`, `"make" → ["-k"]`, `default → nil`
- [x] Refactor `buildBuildArgs` (lines 423–434): remove unconditional `args = append(args, "--")`, replace `fmt.Sprintf("-j%d", jobs)` with `"--parallel", strconv.Itoa(jobs)`, gate `--` on `len(nativeKeepGoingFlags(...)) > 0`
- [x] Add `TestNativeKeepGoingFlags` with subtests: `ninja`, `empty string`, `make`, `unknown`
- [x] Update `TestBuildBuildArgs` subtests: replace `-j4`/`-j1` assertions with `--parallel`/`4` and `--parallel`/`1` assertions
- [x] Remove the `assertContains(t, args, "--")` assertion from the "basic build args" subtest (line ~297) — `--` is no longer emitted in the non-serial case
- [x] Rewrite the "separator comes before jobs" subtest: assert `--parallel` appears and is ordered before `"4"`, assert `--` is absent in the non-serial case, and add a serial-case assertion that `--parallel` appears before `--`
- [x] Add a new subtest for `make` generator with DiagnosticSerialBuild: assert `--parallel 1 -- -k` (no `"0"`)

### Notes
`strconv` is already imported in `cmake.go`. The `fmt` package is still needed elsewhere, so no import churn. The key behavioral change: `cmake --build build --parallel 4` (no trailing `--`) is now the normal non-serial case for all generators. Note: `--parallel` requires CMake 3.12+ (2018) — the project already assumes this minimum version.

## 1.3: Clean up stale comments

### Subtasks
- [x] Remove the 2-line "Note: NewBuilder routes..." comment in `generatorCMakeName` `case "make":` (lines 364–366)
- [x] Update `CMakeBuilder` struct comment (line 38) from "make support is planned" to "It supports Ninja and Unix Makefiles generators"
- [x] Update `config.go` Generator field comment to include `plain-make` (reviewer finding)

## Acceptance Criteria
- [x] `go vet ./...` passes
- [x] `go test -race ./builder/...` passes — all unit tests green
- [x] `NewBuilder` correctly routes `"make"` to `*CMakeBuilder` and `"plain-make"` to `*MakeBuilder`
- [x] `buildBuildArgs` uses `--parallel` for parallelism and only emits `--` when keep-going flags are present
