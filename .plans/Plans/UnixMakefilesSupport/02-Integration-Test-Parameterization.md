---
title: "Integration Test Parameterization"
type: phase
plan: UnixMakefilesSupport
phase: 2
status: complete
created: 2026-03-11
updated: 2026-03-11
deliverable: "12 integration tests parameterized to run with both Ninja and Unix Makefiles generators"
tasks:
  - id: "2.1"
    title: "Add generatorCase type and generatorCases helper"
    status: complete
    verification: "generatorCase struct has name, generator, and require fields. generatorCases(t) returns ninja case always; appends make case only when make is on PATH. Helper compiles and is used by at least one test."
  - id: "2.2"
    title: "Parameterize toolchainCases-based tests"
    status: complete
    depends_on: ["2.1"]
    verification: "Each of the 9 tests produces subtests like TestName/ninja/clang and TestName/make/clang. Generator loop is outermost, toolchain loop is inner. gc.require(t) is called inside the generator subtest. Generator: gc.generator replaces hardcoded 'ninja'. All 9 tests pass with both generators when make is available; make subtests skip gracefully when make is absent."
  - id: "2.3"
    title: "Parameterize detectToolchain-based tests"
    status: complete
    depends_on: ["2.1"]
    verification: "Each of the 3 reconfigure tests produces subtests like TestName/ninja and TestName/make. gc.require(t) is called inside the generator subtest. Generator: gc.generator replaces hardcoded 'ninja'. All 3 tests pass with both generators."
  - id: "2.4"
    title: "Structural verification"
    status: complete
    depends_on: ["2.2", "2.3"]
    verification: "go vet ./... passes. go test -race -run TestIntegration ./... passes with both generators. No test regressions — total test count increases by 12+ (one make subtest per parameterized test, times toolchain count)."
---

# Phase 2: Integration Test Parameterization

## Overview

Wrap 12 integration tests with a `generatorCases` loop so they run with both Ninja and Unix Makefiles. The remaining tests stay Ninja-only per the design's Decision 3 rationale.

## 2.1: Add generatorCase type and generatorCases helper

### Subtasks
- [x] Add `generatorCase` struct to `integration_test.go`: `name string`, `generator string`, `require func(*testing.T)`
- [x] Add `generatorCases(t *testing.T) []generatorCase` function that returns ninja always, appends make when `exec.LookPath("make")` succeeds
- [x] Ninja case uses `require: requireNinja`, make case uses `require: requireMake`

### Notes
This follows the same pattern as the existing `toolchainCases` and `presetGeneratorCases` helpers. The `require` field allows each subtest to skip independently. `requireMake` already exists in `integration_test.go` (lines 60–67) — do not re-add it.

## 2.2: Parameterize toolchainCases-based tests

### Subtasks
- [x] TestIntegrationSmoke — wrap in generatorCases loop, replace `requireNinja(t)` with `gc.require(t)`, replace `Generator: "ninja"` with `Generator: gc.generator`
- [x] TestIntegrationDiagnosticInjection — same pattern
- [x] TestIntegrationErrorDiagnostics — same pattern
- [x] TestIntegrationWarningDiagnostics — same pattern
- [x] TestIntegrationMultiError — same pattern
- [x] TestIntegrationMixedDiagnostics — same pattern
- [x] TestIntegrationLinkerError — same pattern
- [x] TestIntegrationConfigureError — same pattern
- [x] TestIntegrationTargetBuild — same pattern (uses `detectToolchain` but has a single toolchain, not a toolchainCases loop — still gets the outer generatorCases loop)

### Notes
During testing, discovered that Unix Makefiles output includes `[ 50%]` progress lines and `make[N]: ***` error lines that confused the diagnostic parser. Fixed by adding `makeProgressRe` and `makeErrorRe` to `stripNinjaNoise()` in `diagnostics/clang.go`.

## 2.3: Parameterize detectToolchain-based tests

### Subtasks
- [x] TestIntegrationBuildReconfigureFresh — wrap in generatorCases loop, replace `requireNinja(t)` with `gc.require(t)`, replace `Generator: "ninja"` with `Generator: gc.generator`
- [x] TestIntegrationBuildReconfigureAfterChange — same pattern
- [x] TestIntegrationBuildReconfigureWithCMakeError — same pattern

## 2.4: Structural verification

### Subtasks
- [x] Run `go vet ./...` — passes clean
- [x] Run `go test -race -run TestIntegration ./...` — all subtests pass
- [x] Confirm make subtests appear in output (e.g. `TestIntegrationSmoke/make/clang`)
- [x] Confirm make subtests skip gracefully on systems without `make`

## Acceptance Criteria
- [x] All 12 parameterized tests produce both `ninja` and `make` subtests
- [x] All tests pass with `go test -race ./...`
- [x] `go vet ./...` clean
- [x] Tests that should remain Ninja-only (Progress, NoteDiagnostics, ConfigureUnknownCommand, CMakeReconfigureError, Presets, LoadPresets) are untouched
