---
title: "Compiler Diagnostic Pipeline"
type: phase
plan: IntegrationTestSuite
phase: 2
status: complete
created: 2026-03-09
updated: 2026-03-09
deliverable: "5 new test fixtures and 5 test functions validating the full configure→build→parse pipeline for errors, warnings, multi-error, mixed-severity, and note-level diagnostics across Clang and GCC"
tasks:
  - id: "2.1"
    title: "Error diagnostic test with existing fixture"
    status: complete
    verification: "TestIntegrationErrorDiagnostics runs per available toolchain. Copies testdata/cmake-error/ to temp dir. Configure with InjectDiagnosticFlags=true succeeds. Build fails (ExitCode != 0). diagnostics.NewParser(toolchain).Parse(stdout, stderr) returns at least 1 diagnostic. At least one diagnostic has File ending in 'main.cpp', Severity == SeverityError, and Line > 0. Works for both Clang (SARIF) and GCC (JSON) output formats."
  - id: "2.2"
    title: "Warning diagnostic test and cmake-warning fixture"
    status: complete
    verification: "testdata/cmake-warning/ fixture exists with CMakeLists.txt enabling -Wall and main.cpp containing an unused variable. TestIntegrationWarningDiagnostics runs per toolchain. Configure with InjectDiagnosticFlags=true succeeds. Build succeeds (ExitCode == 0). diagnostics.Parse returns at least 1 diagnostic. At least one has File ending in 'main.cpp', Severity == SeverityWarning, Line > 0. Works for both Clang and GCC."
  - id: "2.3"
    title: "Multi-error diagnostic test and cmake-multi-error fixture"
    status: complete
    verification: "testdata/cmake-multi-error/ fixture exists with CMakeLists.txt, a.cpp (undeclared identifier), b.cpp (undeclared identifier). TestIntegrationMultiError runs per toolchain. Config sets DiagnosticSerialBuild=true (forces -j1 + keep-going). Build fails. diagnostics.Parse returns at least 2 diagnostics. Diagnostics reference both 'a.cpp' and 'b.cpp' (assertDiagnosticFound for each). All have Severity == SeverityError."
  - id: "2.4"
    title: "Mixed-severity diagnostic test and cmake-mixed-diagnostics fixture"
    status: complete
    verification: "testdata/cmake-mixed-diagnostics/ fixture exists with CMakeLists.txt enabling -Wall, good.cpp (unused variable → warning), bad.cpp (undeclared identifier → error). TestIntegrationMixedDiagnostics runs per toolchain with DiagnosticSerialBuild=true. Build fails. diagnostics.Parse returns at least 2 diagnostics. assertDiagnosticFound finds error in 'bad.cpp' and warning in 'good.cpp'."
  - id: "2.5"
    title: "Note-level diagnostic test and cmake-note fixture"
    status: complete
    verification: "testdata/cmake-note/ fixture exists with CMakeLists.txt and main.cpp triggering an error+note (overload resolution failure). TestIntegrationNoteDiagnostics runs per toolchain. Build fails. diagnostics.Parse returns at least 1 diagnostic. Test logs whether a note-severity diagnostic was found — not a hard failure since note emission is compiler-dependent. At minimum asserts len(diags) > 0 (the error itself was parsed)."
  - id: "2.6"
    title: "Structural verification"
    status: complete
    depends_on: ["2.1", "2.2", "2.3", "2.4", "2.5"]
    verification: "go vet ./... passes. go test -race ./... passes all packages including new integration tests. All pre-existing tests still pass."
---

# Phase 2: Compiler Diagnostic Pipeline

## Overview

The bulk of the input→output validation. Each test function exercises the full pipeline: configure → build → `diagnostics.Parse(toolchain, stdout, stderr)` → assert expected diagnostics. Five new fixtures are crafted to produce specific diagnostic categories that both Clang and GCC emit predictably.

## 2.1: Error diagnostic test with existing fixture

### Subtasks
- [x] `TestIntegrationErrorDiagnostics` — `requireCMake`, `requireNinja`, loop `toolchainCases`
- [x] Per subtest: `copyFixture(t, "cmake-error")`, config with `InjectDiagnosticFlags: true`
- [x] Configure succeeds (ExitCode == 0)
- [x] Build fails (ExitCode != 0)
- [x] `parser := diagnostics.NewParser(tc.toolchain)`, `diags, err := parser.Parse(result.Stdout, result.Stderr)`
- [x] Assert `err == nil`, `len(diags) > 0`
- [x] `assertDiagnosticFound(t, diags, "main.cpp", diagnostics.SeverityError)`
- [x] Log all diagnostics at `t.Logf` level for debugging

### Notes
This is the most important test in the suite — it validates the complete pipeline for the most common case (compile error). The existing `testdata/cmake-error/main.cpp` uses `undeclared_variable` which is a hard error on every compiler. Both Clang (SARIF on stderr) and GCC (JSON on stderr) produce structured output that the respective parsers handle.

## 2.2: Warning diagnostic test and cmake-warning fixture

### Subtasks
- [x] Create `testdata/cmake-warning/CMakeLists.txt`
- [x] Create `testdata/cmake-warning/main.cpp`
- [x] `TestIntegrationWarningDiagnostics` — configure, build (succeeds), parse, assert warning in `main.cpp`
- [x] Verify ExitCode == 0 (warnings don't fail the build)
- [x] `assertDiagnosticFound(t, diags, "main.cpp", diagnostics.SeverityWarning)`

### Notes
`-Wall` triggers `-Wunused-variable` on both Clang and GCC. The warning appears in structured output because `InjectDiagnosticFlags: true` adds the diagnostic format flag.

## 2.3: Multi-error diagnostic test and cmake-multi-error fixture

### Subtasks
- [x] Create `testdata/cmake-multi-error/CMakeLists.txt`
- [x] Create `testdata/cmake-multi-error/a.cpp`
- [x] Create `testdata/cmake-multi-error/b.cpp`
- [x] `TestIntegrationMultiError` — config with `DiagnosticSerialBuild: true`, build fails, parse
- [x] Assert at least 2 diagnostics returned
- [x] `assertDiagnosticFound(t, diags, "a.cpp", diagnostics.SeverityError)`
- [x] `assertDiagnosticFound(t, diags, "b.cpp", diagnostics.SeverityError)`

### Notes
`DiagnosticSerialBuild: true` forces `-j1` so Ninja compiles one TU at a time. This prevents diagnostic output from interleaving across TUs, which could confuse the parser. Both errors are in separate TUs — the parser should see each TU's output independently. Both use `undeclared_*` identifiers which are unambiguous hard errors on all compilers.

Implementation note: `-k 0` (keep-going) was added to `buildBuildArgs` when DiagnosticSerialBuild is true, so Ninja continues past the first failure and captures diagnostics from all TUs.

## 2.4: Mixed-severity diagnostic test and cmake-mixed-diagnostics fixture

### Subtasks
- [x] Create `testdata/cmake-mixed-diagnostics/CMakeLists.txt`
- [x] Create `testdata/cmake-mixed-diagnostics/good.cpp`
- [x] Create `testdata/cmake-mixed-diagnostics/bad.cpp`
- [x] `TestIntegrationMixedDiagnostics` — config with `DiagnosticSerialBuild: true`, build fails, parse, assert both severities present
- [x] `assertDiagnosticFound(t, diags, "bad.cpp", diagnostics.SeverityError)`
- [x] `assertDiagnosticFound(t, diags, "good.cpp", diagnostics.SeverityWarning)`

### Notes
`DiagnosticSerialBuild: true` forces `-j1` so `good.cpp` compiles first (with warning), then `bad.cpp` fails. The warning from `good.cpp` should be in the output alongside the error from `bad.cpp`. This validates that successful-TU diagnostics aren't lost when the overall build fails.

## 2.5: Note-level diagnostic test and cmake-note fixture

### Subtasks
- [x] Create `testdata/cmake-note/CMakeLists.txt`
- [x] Create `testdata/cmake-note/main.cpp` — overload resolution failure
- [x] `TestIntegrationNoteDiagnostics` — build fails, parse
- [x] Assert `len(diags) > 0` — the error itself was parsed
- [x] Check if any diagnostic has `Severity == SeverityNote`, log result
- [x] Do NOT hard-fail if no note found — note emission varies by compiler version

### Notes
Note-level diagnostics are the least predictable across compilers. This test validates that notes flow through the parser when present, but doesn't hard-fail if the specific compiler version doesn't emit them. The primary assertion is that the parent error is captured — the note is bonus.

## 2.6: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available
- [x] Confirm all pre-existing tests still pass

## Acceptance Criteria

- [x] 5 new test fixtures exist in `testdata/`
- [x] 5 new test functions validate the configure→build→parse pipeline
- [x] Error diagnostics parsed correctly for both Clang and GCC
- [x] Warning diagnostics parsed from successful builds
- [x] Multi-error diagnostics reference distinct source files
- [x] Mixed-severity diagnostics capture both errors and warnings
- [x] All tests skip gracefully when tools are missing
- [x] `go vet`, `go test -race` pass
