---
title: "Test Infrastructure and Smoke Tests"
type: phase
plan: IntegrationTestSuite
phase: 1
status: complete
created: 2026-03-09
updated: 2026-03-09
deliverable: "integration_test.go with test helpers, skip guards, table-driven smoke test (configure+build+clean) per compiler, and diagnostic injection verification. All existing tests still pass."
tasks:
  - id: "1.1"
    title: "Create integration_test.go with skip guards and helpers"
    status: complete
    verification: "requireCMake skips when cmake not in PATH. requireNinja skips when ninja not in PATH. requireCompiler skips when named binary not in PATH and returns the path when found. requireCMakeMinVersion skips when cmake version is below threshold (parses cmake --version output). All four skip with testing.Short(). copyFixture copies a testdata/ directory to t.TempDir(), returning the path; files in the copy match the source. assertDiagnosticFound asserts at least one diagnostic has the given file suffix, severity, and Line > 0; fails with clear message showing actual diagnostics when no match. collectProgress returns a ProgressFunc and a pointer to a slice; calling the func appends events; concurrent-safe per subtest (each call creates independent state). go vet passes."
  - id: "1.2"
    title: "Create toolchain table-driven test runner"
    status: complete
    depends_on: ["1.1"]
    verification: "toolchainCases returns a slice of {name, toolchain, compiler} for each available compiler (clang if clang++ found, gcc if g++ found). Each entry's compiler field is the LookPath result. When no compilers are found, the calling test skips. Subtests are created per entry. The pattern is reusable by all subsequent test functions. detectToolchain returns a single toolchainCase (first available compiler) for tests that don't need per-compiler subtests; skips if none found."
  - id: "1.3"
    title: "Smoke test — configure, build, clean"
    status: complete
    depends_on: ["1.2"]
    verification: "TestIntegrationSmoke runs per available toolchain. Copies testdata/cmake/ to temp dir. Configure with InjectDiagnosticFlags=false: ExitCode == 0, Duration > 0. Build: ExitCode == 0, compile_commands.json exists in build dir. Clean: ExitCode == 0. All three steps succeed for both Clang and GCC (when available). go test -race passes."
  - id: "1.4"
    title: "Diagnostic injection test"
    status: complete
    depends_on: ["1.2"]
    verification: "TestIntegrationDiagnosticInjection runs per available toolchain. Copies testdata/cmake/ to temp dir. Configure with InjectDiagnosticFlags=true: ExitCode == 0. .cpp-build-mcp/DiagnosticFormat.cmake exists in build dir (verifies writeDiagnosticModule + MkdirAll). Configure stderr contains '[cpp-build-mcp] Diagnostic format:' (module executed). For GCC toolchain: stderr contains 'json'. For Clang toolchain: stderr contains 'sarif'. Build succeeds after injection."
  - id: "1.5"
    title: "Structural verification"
    status: complete
    depends_on: ["1.3", "1.4"]
    verification: "go vet ./... reports no issues. go test -race ./... passes all packages. staticcheck ./... reports no issues (if available). All pre-existing 506+ tests still pass."
---

# Phase 1: Test Infrastructure and Smoke Tests

## Overview

Create `integration_test.go` with all shared infrastructure (helpers, skip guards, toolchain table) and the first two test categories: smoke tests and diagnostic injection. This phase establishes the patterns all subsequent phases use and immediately validates the retro's top action items (real cmake builds, DiagnosticFormat.cmake injection).

## 1.1: Create integration_test.go with skip guards and helpers

### Subtasks
- [x] Create `integration_test.go` in package `main` at project root
- [x] Implement `requireCMake(t)` — `exec.LookPath("cmake")` + `testing.Short()` skip
- [x] Implement `requireNinja(t)` — `exec.LookPath("ninja")` + `testing.Short()` skip
- [x] Implement `requireCompiler(t, name)` — `exec.LookPath(name)` skip, returns path
- [x] Implement `requireCMakeMinVersion(t, major, minor)` — runs `cmake --version`, parses version, skips if below threshold. Used by preset tests (CMake 3.21 required for presets v3).
- [x] Implement `copyFixture(t, fixtureName) string` — copies `testdata/<fixtureName>/` to `t.TempDir()`, returns the copy path. Uses `filepath.WalkDir` + `os.MkdirAll` + `os.ReadFile`/`os.WriteFile`.
- [x] Implement `assertDiagnosticFound(t, diags, fileSuffix, severity)` — iterates `diags`, succeeds if any has `strings.HasSuffix(d.File, fileSuffix)` and matching severity and `d.Line > 0`. On failure, logs all diagnostics for debugging.
- [x] Implement `collectProgress(t) (builder.ProgressFunc, *[]progressEvent)` — returns a func that appends `{current, total, message}` to a dedicated slice. Each call returns independent state.
- [x] Define `progressEvent` struct with `current`, `total int` and `message string` fields

### Notes
The `copyFixture` helper is critical for test isolation. It copies files, not symlinks, so each test has its own writable copy. Build dirs are created as `filepath.Join(copiedDir, "build")` by the tests themselves.

## 1.2: Create toolchain table-driven test runner

### Subtasks
- [x] Define `toolchainCase` struct: `name string`, `toolchain string`, `compiler string`
- [x] Implement `toolchainCases(t) []toolchainCase` — checks for `clang++` and `g++`, returns entries for each found. When neither is found, calls `t.Skip`.
- [x] Implement `detectToolchain(t) toolchainCase` — returns the first available toolchain (prefers clang, falls back to gcc). Skips if none found. Used by tests that don't need per-compiler subtests (e.g., 4.2 presets, 4.3 target builds).
- [x] Pattern: each test function calls `toolchainCases(t)`, loops, creates `t.Run(tc.name, ...)` subtests

### Notes
The table is evaluated at test time, not compile time. This allows the same binary to produce different results on different machines.

## 1.3: Smoke test — configure, build, clean

### Subtasks
- [x] `TestIntegrationSmoke` — calls `requireCMake`, `requireNinja`, loops `toolchainCases`
- [x] Per toolchain subtest: `copyFixture(t, "cmake")`, create `config.Config` with absolute paths, `InjectDiagnosticFlags: false`, `BuildTimeout: 2*time.Minute`
- [x] Subtest `configure`: `b.Configure(ctx, nil)`, assert ExitCode == 0, Duration > 0
- [x] Subtest `build`: `b.Build(ctx, nil, 0)`, assert ExitCode == 0, assert `compile_commands.json` exists via `os.Stat`
- [x] Subtest `clean`: `b.Clean(ctx, nil)`, assert ExitCode == 0

### Notes
This is the minimal real-cmake smoke test from the retro's first action item. `InjectDiagnosticFlags: false` keeps this test simple — injection is tested separately in 1.4.

## 1.4: Diagnostic injection test

### Subtasks
- [x] `TestIntegrationDiagnosticInjection` — calls `requireCMake`, `requireNinja`, loops `toolchainCases`
- [x] Per toolchain subtest: `copyFixture(t, "cmake")`, create `config.Config` with `InjectDiagnosticFlags: true`
- [x] Configure, assert ExitCode == 0
- [x] Assert `filepath.Join(buildDir, ".cpp-build-mcp", "DiagnosticFormat.cmake")` exists — proves `writeDiagnosticModule` + `MkdirAll` worked
- [x] Assert configure output contains `[cpp-build-mcp] Diagnostic format:` — proves the module executed during configure
- [x] For `toolchain == "gcc"`: assert output contains `json`
- [x] For `toolchain == "clang"`: assert output contains `sarif`
- [x] Build after injection succeeds (ExitCode == 0) — proves the injected flags don't break compilation

### Notes
This test validates the full DiagnosticFormat.cmake pipeline: file written → cmake finds it via CMAKE_PROJECT_INCLUDE (absolute path) → module probes compiler → correct format detected → flags injected → build succeeds with injected flags.

Implementation note: cmake `message(STATUS ...)` outputs to stdout, not stderr. The test checks combined stdout+stderr. Also, explicit `-DCMAKE_CXX_COMPILER` and `-DCMAKE_C_COMPILER` are passed via extraArgs to ensure the correct compiler is exercised per toolchain.

## 1.5: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available
- [x] Confirm all pre-existing tests pass (zero regressions)

## Acceptance Criteria

- [x] `integration_test.go` exists at project root with all helpers
- [x] `TestIntegrationSmoke` passes for each available compiler
- [x] `TestIntegrationDiagnosticInjection` validates injection for each compiler
- [x] Tests skip gracefully when cmake/ninja/compiler not found
- [x] `requireCMakeMinVersion` skips when cmake version is below threshold
- [x] Tests skip when `-short` is passed
- [x] `go vet`, `go test -race` pass
- [x] All pre-existing tests pass unchanged
