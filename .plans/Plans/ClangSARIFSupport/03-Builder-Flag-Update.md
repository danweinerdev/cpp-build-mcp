---
title: "Builder Flag Update"
type: phase
plan: ClangSARIFSupport
phase: 3
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "cmake.go and make.go inject -fdiagnostics-format=sarif -Wno-sarif-format-unstable for Clang toolchain, with updated tests"
tasks:
  - id: "3.1"
    title: "Update cmake.go flag injection for Clang"
    status: complete
    verification: "buildConfigureArgs with Toolchain='clang' and InjectDiagnosticFlags=true produces -DCMAKE_C_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable and -DCMAKE_CXX_FLAGS=... equivalent. GCC toolchain still does not inject flags. Inject disabled still produces no flags."
  - id: "3.2"
    title: "Update cmake_test.go assertions"
    status: complete
    depends_on: ["3.1"]
    verification: "'clang toolchain injects diagnostic flags' test asserts SARIF flag strings. 'clang toolchain with inject disabled does not inject' test asserts SARIF flag strings are absent. 'preset mode with diagnostic flags' test asserts SARIF flags. GCC test unchanged. All cmake_test.go tests pass."
  - id: "3.3"
    title: "Make make.go toolchain-aware"
    status: complete
    verification: "New diagnosticFlag() method returns SARIF flag string for Toolchain='clang', JSON flag string for all other toolchains (gcc, auto, empty). buildEnv uses diagnosticFlag() instead of const diagFlag."
  - id: "3.4"
    title: "Update make_test.go with toolchain-specific tests"
    status: complete
    depends_on: ["3.3"]
    verification: "Existing 'injects CFLAGS and CXXFLAGS when enabled' test updated with Toolchain='gcc' and still asserts JSON flag. Existing 'appends to existing CFLAGS and CXXFLAGS' test updated with Toolchain='gcc' and still asserts JSON flag. New test: Toolchain='clang' with injection enabled asserts SARIF flag in CFLAGS and CXXFLAGS. New test: empty Toolchain with injection enabled asserts JSON flag (default case). Injection disabled test unchanged."
  - id: "3.5"
    title: "Structural verification"
    status: complete
    depends_on: ["3.1", "3.2", "3.3", "3.4"]
    verification: "go vet ./... and go test -race ./... pass across all packages. Full test suite green — zero regressions in diagnostics, builder, or main packages."
---

# Phase 3: Builder Flag Update

## Overview

Update the builder layer to inject the correct diagnostic format flag based on toolchain. CMake builder (`cmake.go`) changes the flag string; Make builder (`make.go`) gains toolchain awareness via a new `diagnosticFlag()` method. All builder tests are updated to assert the correct flags.

This phase is sequenced after the parser phases so the full stack can be validated end-to-end: builder injects SARIF flag → Clang emits SARIF → parser detects and parses SARIF.

## 3.1: Update cmake.go flag injection for Clang

### Subtasks
- [ ] Change `cmake.go:134` from `"-DCMAKE_C_FLAGS=-fdiagnostics-format=json"` to `"-DCMAKE_C_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable"`
- [ ] Change `cmake.go:135` from `"-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json"` to `"-DCMAKE_CXX_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable"`
- [ ] Update the comment at `cmake.go:132` if it references JSON format
- [ ] Note: the `cmake.go:132` guard uses plain `==` comparison (not `strings.ToLower`), consistent with existing codebase convention. The `make.go` implementation uses `strings.ToLower` defensively. Both are acceptable — toolchain values come from `resolveToolchain` which already normalizes.

### Notes
The `b.cfg.Toolchain == "clang"` guard at line 132 already ensures this only applies to Clang. GCC flag injection is handled by the GCC parser path and is not affected.

`exec.CommandContext` passes each slice element as one OS argument. CMake splits `CMAKE_C_FLAGS` on whitespace when invoking the compiler, so both flags (`-fdiagnostics-format=sarif` and `-Wno-sarif-format-unstable`) reach the compiler correctly.

## 3.2: Update cmake_test.go assertions

### Subtasks
- [ ] Update `"clang toolchain injects diagnostic flags"` test (line 113): change `assertContains` from `"-fdiagnostics-format=json"` to `"-fdiagnostics-format=sarif -Wno-sarif-format-unstable"` for both C and CXX flags
- [ ] Update `"clang toolchain with inject disabled does not inject"` test (line 127): change `assertNotContains` to check for the SARIF flag string
- [ ] Update `"preset mode with diagnostic flags"` test (line 197): change `assertContains` for SARIF flags
- [ ] Verify `"gcc toolchain does not inject diagnostic flags"` test (line 141) still passes unchanged
- [ ] Verify all other `buildConfigureArgs` tests still pass

### Notes
The assertContains/assertNotContains helpers do substring matching on the args slice elements, so the full string `-DCMAKE_C_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable` must be asserted.

## 3.3: Make make.go toolchain-aware

### Subtasks
- [ ] Add `diagnosticFlag() string` method to `MakeBuilder`:
  ```go
  func (b *MakeBuilder) diagnosticFlag() string {
      switch strings.ToLower(b.cfg.Toolchain) {
      case "clang":
          return "-fdiagnostics-format=sarif -Wno-sarif-format-unstable"
      default:
          return "-fdiagnostics-format=json"
      }
  }
  ```
- [ ] Replace `const diagFlag = "-fdiagnostics-format=json"` (line 101) with `diagFlag := b.diagnosticFlag()`
- [ ] Update the comment at `make.go:92-94` to mention toolchain-aware flag selection

### Notes
The `strings` package is already imported in `make.go`. The default case covers `"gcc"`, `"auto"`, `""`, and any unknown toolchain — all want JSON format.

For Make builds, this change takes effect immediately on the next `Build` call (no configure step). Only TUs that are actually recompiled will use the new flag.

## 3.4: Update make_test.go with toolchain-specific tests

### Subtasks
- [ ] Update `"injects CFLAGS and CXXFLAGS when enabled"` (line 124): add `Toolchain: "gcc"` to config, keep JSON flag assertions
- [ ] Update `"appends to existing CFLAGS and CXXFLAGS"` (line 142): add `Toolchain: "gcc"` to config, keep JSON flag assertions
- [ ] Add new test: `"clang toolchain injects SARIF flags"` — config with `Toolchain: "clang"`, `InjectDiagnosticFlags: true`, assert CFLAGS and CXXFLAGS contain `-fdiagnostics-format=sarif -Wno-sarif-format-unstable`
- [ ] Add new test: `"empty toolchain injects JSON flags"` — config with no Toolchain set, `InjectDiagnosticFlags: true`, assert JSON flag (default case)
- [ ] Verify `TestMakeBuilderNoInjection` (line 162) still passes unchanged

### Notes
The existing tests implicitly relied on the empty-toolchain default being JSON — adding explicit `Toolchain: "gcc"` makes the intent clear and guards against future default changes.

## 3.5: Structural verification

### Subtasks
- [ ] Run `go vet ./...`
- [ ] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available
- [ ] Confirm zero test regressions across all packages

## Acceptance Criteria
- [ ] `cmake.go` injects SARIF flags for Clang toolchain
- [ ] `make.go` selects SARIF or JSON flag based on toolchain
- [ ] All cmake_test.go and make_test.go tests pass with updated assertions
- [ ] GCC and disabled-injection paths are unaffected (no regressions)
- [ ] Full `go test ./...` passes across all packages
- [ ] `go vet` passes
