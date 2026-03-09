---
title: "Phase 3 Debrief: Builder Flag Update"
type: debrief
plan: "ClangSARIFSupport"
phase: 3
phase_title: "Builder Flag Update"
status: complete
created: 2026-03-09
---

# Phase 3 Debrief: Builder Flag Update

## Decisions Made

- **SARIF flag for Clang, JSON for everything else** — clear toolchain-conditioned dispatch in both cmake.go and make.go.
- **DiagnosticFormat.cmake injection via CMAKE_PROJECT_INCLUDE** — probes compiler for diagnostic format support at configure time, sets CMake variables for downstream consumers (like Fusion).

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| cmake.go injects SARIF flags for Clang | Met | |
| make.go selects SARIF or JSON by toolchain | Met | |
| All cmake_test.go and make_test.go pass | Met | |
| GCC and disabled-injection paths unaffected | Met | |
| go vet passes | Met | |
| DiagnosticFormat.cmake written before configure | Met | After fix — see below |
| CMAKE_PROJECT_INCLUDE uses absolute path | Met | After fix — see below |

## Deviations

- **CMAKE_PROJECT_INCLUDE path resolution required post-fix.** The initial implementation used a relative path for `CMAKE_PROJECT_INCLUDE`. CMake resolves this relative to the *source directory*, not the working directory, so preset-based builds with different binary dirs (e.g., `tmp/Linux/X64/Clang/Debug`) couldn't find the module. Fix: `diagnosticModulePath()` now returns an absolute path via `filepath.Abs()`. Committed in the same batch as the SARIF parser work.

- **Build directory creation for preset-derived paths.** Preset-based builds produce binary dirs like `tmp/Linux/X64/Clang/Debug` that don't exist until cmake creates them. But `CMAKE_PROJECT_INCLUDE` is evaluated *during* `project()` — before cmake creates the binary dir tree. Fix: `writeDiagnosticModule()` uses `os.MkdirAll` to create the path before configure runs.

## Risks & Issues Encountered

- **CMAKE_PROJECT_INCLUDE relative path bug.** Not caught by unit tests because tests verified argument construction (the string was correct), not whether cmake could actually resolve the path. Only surfaced during real-world testing with Fusion's preset-based build.

- **Missing directory for preset builds.** The `writeDiagnosticModule()` function originally assumed the build directory existed. With presets, it doesn't exist until cmake creates it. `os.MkdirAll` was the straightforward fix.

## Lessons Learned

- **Path resolution bugs only surface when source dir ≠ working dir ≠ build dir.** Unit tests typically run from a fixed directory with co-located source and build. Preset-based builds with deeply nested binary dirs are the real-world scenario that breaks relative paths. Integration tests against a real CMake project with presets would catch this.

- **CMAKE_PROJECT_INCLUDE has subtle resolution semantics.** The cmake docs say it's resolved relative to the source directory, but most examples show absolute paths. Always use absolute paths for any cmake variable that references an external file.

- **Write-before-use ordering is critical for injected cmake modules.** CMAKE_PROJECT_INCLUDE is evaluated at `project()` time, which happens before cmake creates the binary directory tree. Any injected module must be written to disk before cmake is invoked, and the target directory must be created explicitly.

## Impact on Subsequent Phases

- Phase 4 (GCC stderr fix) was unaffected — it operates in the diagnostics layer, not the builder layer.
- The absolute path and MkdirAll fixes were also committed and are now part of the stable implementation.
