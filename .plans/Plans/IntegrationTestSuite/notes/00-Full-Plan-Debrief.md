---
title: "Full Plan Debrief: Integration Test Suite"
type: debrief
plan: "IntegrationTestSuite"
phase: "all"
phase_title: "All Phases (1-4)"
status: complete
created: 2026-03-09
---

# Full Plan Debrief: Integration Test Suite

All 4 phases executed in a single session. 12 test categories, 8 fixtures, ~950 lines of test code, 3 production bugs discovered and fixed.

## Decisions Made

1. **Explicit compiler flags via `-DCMAKE_CXX_COMPILER` / `-DCMAKE_C_COMPILER`.** The plan assumed `config.Config.Toolchain` would force cmake to use a specific compiler. It doesn't — Toolchain only controls which diagnostic format flag to inject. We added explicit `-DCMAKE_*_COMPILER` extraArgs to every per-toolchain test so the configured toolchain actually matches the compiler cmake uses.

2. **C compiler derivation from C++ compiler path.** `DiagnosticFormat.cmake` calls `check_c_compiler_flag`, which requires a C compiler. Derived C compiler name from basename of C++ compiler (clang++ → clang, g++ → gcc), validated via `exec.LookPath`. Initially done inline with `strings.Replace` on the full path; refactored to a `deriveCCompiler` helper operating on basename only after code review flagged the fragility.

3. **`project(... C CXX)` in all fixtures.** Original fixtures only enabled CXX. `DiagnosticFormat.cmake` probes both C and CXX compilers via `check_c_compiler_flag`/`check_cxx_compiler_flag`. Without C enabled, the C probe would fail. Added `C` to all fixture `project()` declarations.

4. **Combined stdout+stderr for configure output assertions.** CMake `message(STATUS ...)` goes to stdout, not stderr. Rather than asserting on a specific stream, tests check `result.Stdout + result.Stderr` combined. Resilient to cmake version differences.

5. **`configureOK` flag for smoke test cascade prevention.** Code review flagged that if configure fails, the build and clean subtests would fail with confusing errors. Added a boolean flag set at end of configure; build/clean subtests skip if false.

6. **`-k 0` (keep-going) for DiagnosticSerialBuild.** Discovered during Phase 2 that Ninja stops at the first error by default. Multi-error and mixed-severity tests only saw one TU's diagnostics. Added `-k 0` to cmake builder and `-k` to make builder when DiagnosticSerialBuild is true. This was a production bug fix, not just a test concern.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| 12 test categories covering full pipeline | Met | Smoke, injection, error, warning, multi-error, mixed, note, linker error, configure error, progress, presets, target build |
| 8 new test fixtures | Met | cmake-warning, cmake-multi-error, cmake-mixed-diagnostics, cmake-note, cmake-linker-error, cmake-configure-error, cmake-library, cmake-presets |
| Table-driven per-compiler subtests | Met | Both Clang and GCC exercised where available |
| Skip guards for missing tools | Met | requireCMake, requireNinja, requireCompiler, requireCMakeMinVersion, testing.Short() |
| Fuzzy diagnostic assertions | Met | File suffix + severity + Line > 0 |
| Progress callback validation | Met | count > 0, final current == total, total > 1 |
| Preset path resolution validation | Met | DiagnosticFormat.cmake exists in nested build dir |
| go vet + go test -race pass | Met | All 7 packages pass |
| No regressions in existing tests | Met | All pre-existing tests unmodified and passing |

## Deviations

1. **Plan said "No changes to existing code — purely additive."** In practice, 3 production bugs required production code changes:
   - `diagnostics/clang.go`: Added `stripNinjaNoise()` with 3 new regexes, tightened `hasStructuredContent` and `detectOutputFormat`
   - `diagnostics/gcc.go`: Added `stripNinjaNoise()` call
   - `builder/cmake.go`: Added `-k 0` for DiagnosticSerialBuild
   - `builder/make.go`: Added `-k` for DiagnosticSerialBuild
   - `testdata/cmake/CMakeLists.txt` and `testdata/cmake-error/CMakeLists.txt`: Added `C` language to `project()`

2. **Design doc said `config.Config.Toolchain` controls compiler selection.** It only controls diagnostic format flag selection. Required explicit cmake variable overrides in tests.

3. **Phase 2 notes described `cmake-multi-error/b.cpp` as "type mismatch" (`int* p = 42`).** Actual implementation uses `undeclared_b` (undeclared identifier) — more reliably produces the same diagnostic category across compilers.

4. **Linker error output location.** Phase 3 design assumed linker errors appear in stderr. On this system, Ninja writes linker errors to stdout. Tests check both streams.

## Risks & Issues Encountered

1. **Ninja `FAILED:` lines contain `[` brackets.** These confused `hasStructuredContent` and `detectOutputFormat`, which scanned for `[` anywhere in the string. Lines like `FAILED: [code=1] CMakeFiles/main.dir/a.cpp.o` triggered false positive JSON detection. Fixed by: (a) adding `stripNinjaNoise()` to remove FAILED/summary/count lines, (b) tightening format detection to check per-line first non-whitespace character only.

2. **GCC parser had zero Ninja noise stripping.** The Clang parser stripped progress lines, but the GCC parser passed raw Ninja output directly to JSON parsing. Added `stripNinjaNoise()` call to GCC parser.

3. **Ninja default stop-on-first-error.** Without `-k 0`, `DiagnosticSerialBuild: true` with `-j1` would only compile until the first TU failed. Multi-error and mixed-severity tests only saw half the expected diagnostics. Fixed by adding keep-going flags to both cmake and make builders.

4. **`hasStructuredContent` overcorrection.** First fix changed to `strings.ContainsAny(s, "{[")` — too broad, matching brackets in compiler invocation lines. Code review caught this. Final fix: per-line first non-whitespace character check.

5. **File-not-read errors during review fixes.** Attempted to edit `builder/cmake_test.go` and `builder/make_test.go` without reading them first after context compaction. Required re-reading files before applying edits.

## Lessons Learned

1. **Integration tests find real bugs that unit tests structurally cannot.** All 3 production bugs (Ninja noise, keep-going, format detection) were invisible to unit tests because they only manifest with real build system output. The v1 retro's finding — that post-completion bugs were caught only by real-world usage — was validated again: these bugs existed in shipped code.

2. **`DiagnosticFormat.cmake` has implicit requirements on fixture configuration.** The cmake module probes both C and CXX compilers. Any test fixture using `InjectDiagnosticFlags: true` must enable both languages in its `project()` declaration. This constraint was not documented anywhere in the codebase or design docs.

3. **`config.Config.Toolchain` is a parser hint, not a compiler selector.** The Toolchain field tells the diagnostic parser which format to expect (SARIF vs JSON). It does not influence which compiler cmake uses. Tests requiring a specific compiler must pass explicit `-DCMAKE_*_COMPILER` flags. This is a documentation gap in the config package.

4. **Ninja's stream behavior varies by context.** Progress lines go to stdout. Compiler diagnostics may go to stdout or stderr depending on the compiler and format. Linker errors may appear on either stream depending on the linker. The safest approach is to check both streams.

5. **Code review cycles are high-value for test code.** The review caught the `hasStructuredContent` overcorrection (too broad), the fragile C compiler derivation pattern, the missing cascade guards, and the missing unit tests for new production code. Without review, at least 2 of these would have been latent issues.

6. **`-k 0` is essential for diagnostic collection.** Any system that collects diagnostics from multiple TUs must use keep-going mode. Without it, only the first failing TU's diagnostics are visible. This applies to the `DiagnosticSerialBuild` feature in general, not just tests.

## Impact on Subsequent Phases

No subsequent phases — all 4 phases are complete. However, the production bug fixes have implications:

- **`stripNinjaNoise` is shared between Clang and GCC parsers.** Future parser changes must maintain this function. The unit tests added in `TestStripNinjaNoise` and `TestGCCParser_NinjaNoiseStripped` guard against regressions.
- **`-k 0` in cmake builder and `-k` in make builder** are now load-bearing for multi-TU diagnostic collection. The unit test assertions in `cmake_test.go` and `make_test.go` verify this.
- **`hasStructuredContent` and `detectOutputFormat`** now use per-line detection. If a future build system produces JSON content that doesn't start at column 0 of a line, these functions may need adjustment.
- **`deriveCCompiler` pattern** should be extracted if more tests need it, or the limitation of `Config.Toolchain` should be documented/fixed at the config level.
