---
title: "Integration Test Suite"
type: design
status: approved
created: 2026-03-09
updated: 2026-03-09
tags: [testing, integration, compilers, cmake, ninja, diagnostics]
related:
  - Research/IntegrationTestSuite
  - Retro/2026-03-09-v1-launch-and-real-world-validation
  - Plans/BuildProgressNotifications
  - Plans/ClangSARIFSupport
---

# Integration Test Suite

## Overview

Add integration tests that build real C++ projects with real compilers, validating the full pipeline from `Configure` through `Build` to diagnostic parsing. This directly addresses the v1 retro finding that three post-completion bugs (progress stream, CMAKE_PROJECT_INCLUDE paths, build dir creation) were caught only by real-world usage, never by tests.

The existing test infrastructure has strong unit coverage (506 tests, 3.2:1 test:production ratio) and e2e tests that validate MCP protocol plumbing, but every builder test uses either argument-construction helpers (no cmake invocation) or `fakeBuilder` (no subprocess). The integration tests fill the gap between unit tests with synthetic input and manual testing against production projects.

**Non-goal:** replacing the existing unit and e2e tests. Integration tests are slower and compiler-dependent — they complement, not replace, the fast synthetic tests.

## Architecture

### Components

```
testdata/
├── cmake/                         # Existing — clean C++ executable (success path)
│   ├── CMakeLists.txt
│   └── main.cpp
├── cmake-error/                   # Existing — intentional compile error
│   ├── CMakeLists.txt
│   └── main.cpp
│
│ ── Purpose-built behavior fixtures ──
│
├── cmake-warning/                 # New — compiles but emits warnings
│   ├── CMakeLists.txt             #   -Wall enabled
│   └── main.cpp                   #   unused variable, signed/unsigned compare
├── cmake-multi-error/             # New — multiple errors across multiple TUs
│   ├── CMakeLists.txt
│   ├── a.cpp                      #   undeclared identifier
│   └── b.cpp                      #   undeclared identifier
├── cmake-note/                    # New — error with related note/fixit
│   ├── CMakeLists.txt
│   └── main.cpp                   #   overload resolution failure → note
├── cmake-linker-error/            # New — compiles but fails to link
│   ├── CMakeLists.txt
│   ├── main.cpp                   #   calls undefined_function()
│   └── lib.h                      #   declares but never defines it
├── cmake-configure-error/         # New — CMakeLists.txt with fatal error
│   ├── CMakeLists.txt             #   message(FATAL_ERROR ...)
│   └── main.cpp
├── cmake-library/                 # New — static library + executable
│   ├── CMakeLists.txt
│   ├── lib.cpp
│   ├── lib.h
│   └── main.cpp
├── cmake-presets/                 # New — project with CMakePresets.json
│   ├── CMakeLists.txt
│   ├── CMakePresets.json          #   nested binaryDir
│   └── main.cpp
└── cmake-mixed-diagnostics/       # New — errors AND warnings in same build
    ├── CMakeLists.txt
    ├── good.cpp                   #   compiles with warning (unused var)
    └── bad.cpp                    #   compile error (undeclared)

integration_test.go                # New — top-level integration tests
```

### Test Fixtures

Each fixture is a minimal, self-contained CMake project designed to produce a specific, predictable outcome. The C/C++ source files are crafted so both Clang and GCC produce the same category of diagnostic (error, warning, note) even though exact messages differ.

**Existing fixtures:**

- **`testdata/cmake/`** — Clean C++ executable. Compiles and links without errors or warnings. The baseline success path.
- **`testdata/cmake-error/`** — Single compile error (`undeclared_variable`). Produces exactly one error diagnostic referencing `main.cpp`.

**New behavior-targeted fixtures:**

**`testdata/cmake-warning/`** — Compiles successfully but produces warnings. Uses an unused variable (`int unused = 42;`) — universally warned about by both Clang and GCC with `-Wall`. The CMakeLists.txt adds `-Wall` explicitly (DiagnosticFormat.cmake doesn't inject warning flags). Tests: successful build with parseable warnings, warnings have correct severity and file reference.

**`testdata/cmake-multi-error/`** — Two TUs (`a.cpp`, `b.cpp`) each with an undeclared identifier error (`undeclared_a` and `undeclared_b` respectively). Tests: multiple diagnostics returned from a single build, diagnostics reference different files, `-j1` mode (`DiagnosticSerialBuild`) doesn't lose diagnostics. With `-j1`, Ninja compiles one file at a time so diagnostic output isn't interleaved — both errors should still be captured.

**`testdata/cmake-note/`** — Code that triggers a note-level diagnostic (e.g., overload resolution failure where the compiler emits an error + a note explaining why a candidate was rejected, or a deprecated function use that emits warning + note). Tests: note-severity diagnostics are parsed and returned alongside the parent error/warning.

**`testdata/cmake-linker-error/`** — Compiles successfully but fails at link time. `main.cpp` calls `undefined_function()` declared in `lib.h` but never defined. Tests: build fails (exit code != 0) but no structured *compiler* diagnostics are produced — the linker error is in stderr as plain text. Validates that the diagnostic parser returns nil/empty when there's no structured output (the error is in `BuildResult.Stderr` as raw text, not in parsed diagnostics).

**`testdata/cmake-configure-error/`** — `CMakeLists.txt` contains `message(FATAL_ERROR "intentional configure failure")`. Tests: `Configure()` returns non-zero exit code, `BuildResult.Stderr` contains the error message. No diagnostics to parse — this validates the configure-failure path.

**`testdata/cmake-library/`** — Static library (`lib.cpp` / `lib.h`) consumed by an executable (`main.cpp`). Multiple TUs mean multiple `[N/M]` progress lines from Ninja. Tests: target-based builds (`--target lib`, `--target main`), progress callback fires with `total > 1`, `compile_commands.json` contains entries for both TUs.

**`testdata/cmake-presets/`** — Project with `CMakePresets.json` defining a preset with `binaryDir` set to `${sourceDir}/build/integration-test` — a nested path that differs from the source dir. Tests: preset-mode configure, absolute path injection for `CMAKE_PROJECT_INCLUDE`, build dir creation via `os.MkdirAll`.

**`testdata/cmake-mixed-diagnostics/`** — Two TUs: `good.cpp` compiles with a warning (unused variable), `bad.cpp` has a compile error (undeclared identifier). Build fails because of `bad.cpp`, but `good.cpp`'s warning should still be captured. Tests: mixed-severity diagnostics from a single build, both error and warning diagnostics present in parsed output, diagnostics reference their respective source files.

### Fixture Design Principles

1. **Predictable across compilers.** Each fixture uses language constructs that produce the same *category* of diagnostic on both Clang and GCC (e.g., undeclared identifier is always an error, unused variable is always a warning with `-Wall`). Exact messages and column numbers vary — assertions use fuzzy matching (Decision 4).

2. **Minimal source.** Each `.cpp` file is under 10 lines. The goal is to trigger a specific diagnostic, not to be a realistic project. Smaller source = faster builds = faster tests.

3. **One behavior per fixture where possible.** `cmake-warning/` tests warnings. `cmake-linker-error/` tests linker failures. `cmake-mixed-diagnostics/` is the exception — it tests the interaction between multiple diagnostic severities in a single build.

4. **CMakeLists.txt controls the build, not the source.** Warning flags (`-Wall`) are in CMakeLists.txt, not in pragmas or attributes. This matches how real projects work and ensures DiagnosticFormat.cmake injection interacts correctly with project-defined flags.

### Test Structure

All integration tests live in `integration_test.go` at the package root (package `main`), not in `builder/cmake_test.go`. Rationale: these tests exercise the full stack across `builder`, `diagnostics`, and `config` packages — they need to import from multiple packages and validate cross-package behavior. The existing `TestCMakeBuilderIntegration` in `builder/cmake_test.go` remains as a package-level smoke test.

Each test copies the source fixture into `t.TempDir()` so builds never pollute the repository. Build directories are subdirectories of the temp dir.

### Skip Guards

```go
func requireCMake(t *testing.T) {
    t.Helper()
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }
    if _, err := exec.LookPath("cmake"); err != nil {
        t.Skip("cmake not found")
    }
}

func requireNinja(t *testing.T) {
    t.Helper()
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }
    if _, err := exec.LookPath("ninja"); err != nil {
        t.Skip("ninja not found")
    }
}

func requireCompiler(t *testing.T, name string) string {
    t.Helper()
    path, err := exec.LookPath(name)
    if err != nil {
        t.Skipf("%s not found", name)
    }
    return path
}

// requireCMakeMinVersion skips if cmake version is below major.minor.
// Parses output of "cmake --version" (e.g., "cmake version 3.28.1").
func requireCMakeMinVersion(t *testing.T, major, minor int) {
    t.Helper()
    // Parse cmake --version and compare against threshold
}

// detectToolchain returns the first available toolchainCase (prefers clang,
// falls back to gcc). Skips if neither is found. Used by tests that don't
// need per-compiler subtests (e.g., presets, target builds).
func detectToolchain(t *testing.T) toolchainCase {
    t.Helper()
    if path, err := exec.LookPath("clang++"); err == nil {
        return toolchainCase{name: "clang", toolchain: "clang", compiler: path}
    }
    if path, err := exec.LookPath("g++"); err == nil {
        return toolchainCase{name: "gcc", toolchain: "gcc", compiler: path}
    }
    t.Skip("no C++ compiler found (need clang++ or g++)")
    return toolchainCase{}
}
```

Tests also check `testing.Short()` and skip — integration tests are slow relative to unit tests (seconds, not milliseconds).

### Data Flow

```
Test setup:
  1. Skip if cmake/ninja/compiler not available or -short
  2. Copy testdata/<fixture>/ to t.TempDir()
  3. Create config.Config with absolute paths
  4. Create CMakeBuilder

Test execution:
  Configure(ctx, extraArgs)
      │
      ├── writeDiagnosticModule() → <buildDir>/.cpp-build-mcp/DiagnosticFormat.cmake
      ├── cmake -S <src> -B <build> -G Ninja -DCMAKE_PROJECT_INCLUDE=<abs path>
      └── BuildResult{Stdout, Stderr, ExitCode}

  Build(ctx, targets, jobs)
      │
      ├── cmake --build <build> -- -j<N>
      ├── ProgressFunc receives [N/M] from stdout tee
      └── BuildResult{Stdout, Stderr, ExitCode}

Test assertions:
  ├── ExitCode == expected
  ├── ProgressFunc called with valid (current, total)    [progress tests]
  ├── diagnostics.Parse(toolchain, stdout, stderr)       [diagnostic tests]
  │     ├── len(diags) > 0
  │     ├── diags[0].File contains expected filename
  │     ├── diags[0].Severity == expected
  │     └── diags[0].Line > 0
  └── File existence checks                              [injection tests]
        ├── <buildDir>/.cpp-build-mcp/DiagnosticFormat.cmake exists
        └── <buildDir>/compile_commands.json exists
```

### Interfaces

No new interfaces or types exported from any package. Integration tests use existing public APIs:

- `builder.NewCMakeBuilder(cfg)` — create builder
- `b.Configure(ctx, args)`, `b.Build(ctx, targets, jobs)` — exercise builder
- `b.SetProgressFunc(fn)` — set progress callback (concrete method, not interface)
- `diagnostics.NewParser(toolchain)` — create parser
- `parser.Parse(stdout, stderr)` — parse real compiler output

New unexported test helpers in `integration_test.go`:

```go
// copyFixture copies a testdata directory to dst, returning the destination path.
func copyFixture(t *testing.T, fixture string) string

// assertDiagnosticFound checks that at least one diagnostic matches the expected
// file suffix, severity, and has a positive line number. Cross-compiler tolerant.
func assertDiagnosticFound(t *testing.T, diags []diagnostics.Diagnostic, fileSuffix string, severity diagnostics.Severity)

// collectProgress returns a ProgressFunc that appends events to a slice.
func collectProgress(t *testing.T) (builder.ProgressFunc, *[]progressEvent)
```

## Design Decisions

### Decision 1: Top-level `integration_test.go` vs. per-package tests

**Context:** Integration tests need to exercise both `builder` and `diagnostics` packages together. The existing `TestCMakeBuilderIntegration` is in `builder/cmake_test.go` (package `builder`) and cannot import `diagnostics`.

**Options Considered:**
1. Top-level `integration_test.go` in package `main` — can import all packages
2. Extend `builder/cmake_test.go` — builder-only assertions, no diagnostic parsing
3. New `integration/` package — separate Go package with its own tests

**Decision:** Option 1 — top-level `integration_test.go`.

**Rationale:** The highest-value integration tests validate the full pipeline: configure → build → parse diagnostics. This requires importing both `builder` and `diagnostics`. Package `main` already imports both. A separate `integration/` package adds directory structure for no benefit. The existing `TestCMakeBuilderIntegration` stays in `builder/cmake_test.go` for package-level regression coverage.

### Decision 2: `t.TempDir()` copies vs. building in-place

**Context:** Test fixtures live in `testdata/`. CMake builds produce artifacts (build dirs, object files). These must not pollute the repository.

**Options Considered:**
1. Copy fixture to `t.TempDir()`, build there — clean, parallel-safe
2. Build in `testdata/<fixture>/build/`, gitignore it — simpler but not parallel-safe
3. Inline temp files (like existing `TestCMakeBuilderIntegration`) — no shared fixtures

**Decision:** Option 1 — copy to temp dir.

**Rationale:** `t.TempDir()` is auto-cleaned, parallel-safe (`t.Parallel()`), and keeps `testdata/` as read-only source fixtures. The copy overhead is negligible for these tiny projects. The `copyFixture` helper makes this a one-liner per test.

### Decision 3: Skip strategy — `exec.LookPath` + `testing.Short()`

**Context:** Integration tests require cmake, ninja, and a C++ compiler. Not all environments have these. Tests should be frictionless locally but skippable in constrained CI.

**Options Considered:**
1. `//go:build integration` build tag — requires explicit `-tags integration`
2. `exec.LookPath` skip — graceful degradation, runs wherever tools exist
3. `testing.Short()` skip — opt-out via `-short`
4. LookPath + Short — both guards

**Decision:** Option 4 — both `exec.LookPath` and `testing.Short()`.

**Rationale:** LookPath ensures tests don't fail on machines without cmake/clang. `testing.Short()` lets CI or developers explicitly skip slow tests with `-short`. The combination means: `go test ./...` runs integration tests when tools are available, `go test -short ./...` skips them, and missing tools cause graceful skips with a clear message.

**Edge case:** If the build environment has a compiler cmake can find but neither `clang++` nor `g++` is in PATH, the smoke test runs with `toolchain: "auto"` but diagnostic-specific tests skip. This is acceptable — the auto-detection path gets coverage, and compiler-specific behavior is a unit-test concern.

### Decision 4: Cross-compiler diagnostic assertions

**Context:** Clang and GCC produce different diagnostic messages, column numbers, error codes, and even stream behavior. Integration tests must work with either compiler.

**Options Considered:**
1. Exact field matching per compiler — fragile, breaks on compiler version updates
2. Fuzzy matching — assert file suffix, severity, positive line; ignore message/column/code
3. Compiler-specific subtests — separate assertion sets per toolchain

**Decision:** Option 2 — fuzzy matching with `assertDiagnosticFound`.

**Rationale:** The goal is to validate the pipeline works (configure → build → parse), not to regression-test specific compiler output. Asserting that a diagnostic was found with the right file, severity, and a valid line number proves the full stack works. Exact message matching would break on every compiler point release. If compiler-specific behavior needs testing, dedicated unit tests with captured output (like the existing `clang_sarif_evidence_test.go`) are the right tool.

### Decision 5: Toolchain detection — detect available vs. test all

**Context:** We support Clang, GCC, and MSVC. A given machine may have one, two, or all three.

**Options Considered:**
1. `detectToolchain` picks one available compiler, run tests once
2. Table-driven subtests, one per available compiler
3. Fixed to one compiler with skip

**Decision:** Option 2 — table-driven subtests per available compiler.

**Rationale:** The bugs that motivated this suite were toolchain-specific (Clang SARIF vs GCC JSON, different stream behavior). Testing with only one compiler defeats the purpose. Table-driven subtests naturally skip unavailable compilers and test all available ones. Each subtest gets its own temp dir and is `t.Parallel()`-safe.

### Decision 6: Preset fixture design

**Context:** The CMAKE_PROJECT_INCLUDE path resolution bug surfaced with preset-based builds using nested `binaryDir`. The preset fixture must reproduce this scenario.

**Decision:** `testdata/cmake-presets/CMakePresets.json` defines a preset with `binaryDir` set to `${sourceDir}/build/integration-test` — a nested path that differs from the source dir. The test copies the fixture to `t.TempDir()`, configures with `--preset`, and verifies:
1. The `DiagnosticFormat.cmake` module was written to an absolute path inside the nested build dir
2. Configure succeeded (cmake found the module)
3. Build succeeded

**Rationale:** This directly reproduces the conditions that caused the path resolution bug. The `${sourceDir}` macro is one that cmake presets expand natively — cpp-build-mcp's `expandBinaryDir` also handles it.

### Decision 7: Progress callback validation

**Context:** The progress stream bug (scanner on stderr instead of stdout) was the highest-profile post-completion bug. The integration test must validate that `ProgressFunc` actually fires during a real Ninja build.

**Decision:** The progress test uses the `testdata/cmake-library/` fixture (multiple TUs for multiple `[N/M]` lines) and asserts:
1. At least one callback was received
2. The final callback has `current == total`
3. `total > 1` (multiple TUs produced multiple progress lines)

The default 250ms throttle is accepted — no need to override `progressMinInterval`. The `cmake-library` fixture has multiple TUs and the build takes long enough (compile + link) that the first and final callbacks will fire regardless. The test validates that the scanner sees real Ninja output, not that every line triggers a callback.

**Note on `progressMinInterval` access:** This field is unexported on `CMakeBuilder` and inaccessible from `package main`. Rather than adding an exported setter just for tests, we accept the default throttle. The 250ms window is fine because: (a) the first matching line always fires (lastNotify is zero-value), (b) the final `N==M` line always fires regardless of throttle, and (c) we only need `count > 0`, not `count == total`. If more precise throttle testing is ever needed, it belongs in `builder/cmake_test.go` where the field is accessible (and is already tested there).

**Rationale:** These assertions prove the scanner is reading from the correct stream (stdout) and parsing real Ninja output. Each parallel subtest gets its own `collectProgress()` call and its own event slice — no shared state across subtests.

### Decision 8: Preset test — verifying directory creation vs. cmake side effects

**Context:** The `os.MkdirAll` fix in `writeDiagnosticModule()` creates the build directory tree before cmake runs. But cmake also creates this directory during configure. A naive "file exists after configure" assertion can't distinguish "our code created it" from "cmake created it."

**Decision:** The preset test constructs a `config.Config` manually with the preset name and the expanded `binaryDir` as an absolute path (computed from the temp dir). It does NOT call `config.LoadMulti()` — `expandBinaryDir` coverage is a unit-test concern (already covered in `config/presets_test.go`). After `Configure()` returns, the test asserts `DiagnosticFormat.cmake` exists. The directory creation is verified indirectly: if `writeDiagnosticModule()` doesn't call `MkdirAll`, cmake would fail to find the module at the `CMAKE_PROJECT_INCLUDE` path and configure would either fail or not inject diagnostics. The configure success + module existence assertion is sufficient because the module is written *before* cmake runs — cmake doesn't create the `.cpp-build-mcp/` subdirectory itself.

**Rationale:** The `.cpp-build-mcp/` directory inside the build dir is created exclusively by `writeDiagnosticModule()`, not by cmake. So "file exists after configure" does prove our code created the directory. The key insight is that cmake creates `build/integration-test/` (the top-level build dir) but never creates `build/integration-test/.cpp-build-mcp/` — that's our subdirectory.

### Decision 9: Purpose-built fixtures as behavior specifications

**Context:** Existing tests use either synthetic JSON strings (unit tests) or `fakeBuilder` (e2e tests) — the real compiler is never in the loop. We need fixtures that produce specific, predictable compiler output to validate the full input→output pipeline.

**Options Considered:**
1. Static fixtures only — pre-recorded compiler output, no real builds
2. Purpose-built C/C++ projects — crafted source that triggers specific diagnostics
3. Both — static for unit tests, real projects for integration

**Decision:** Option 2 for integration tests — real C/C++ projects designed to produce known diagnostic categories.

**Rationale:** The entire point of integration tests is to validate real compiler interaction. Each fixture is a specification: "this code should produce this category of diagnostic." The C/C++ source uses language constructs with well-defined diagnostic behavior across compilers:

- **Undeclared identifier** → error (universally)
- **Unused variable with `-Wall`** → warning (Clang and GCC both)
- **Undefined symbol at link time** → linker error (not a compiler diagnostic)
- **`message(FATAL_ERROR)`** → configure failure (cmake, not compiler)
- **Mixed TUs: one with error, one with warning** → both severities in output

The fixtures are the specification. If a fixture stops producing the expected diagnostic category on a new compiler version, that's valuable signal — it means our parser assumptions may need updating. Static captured output would hide this.

## Error Handling

| Scenario | Handling |
|----------|----------|
| cmake/ninja/compiler not found | `t.Skip()` with clear message — test is not a failure |
| Configure fails unexpectedly | `t.Fatalf` with full stderr — immediate, visible failure |
| Build fails when success expected | `t.Fatalf` with exit code and stderr |
| Build succeeds when failure expected (error fixture) | `t.Fatal("expected build to fail")` |
| No diagnostics parsed from error build | `t.Fatal("expected diagnostics from failed build")` |
| Diagnostics parsed from linker error | `t.Fatal("expected no structured diagnostics from linker error")` |
| Wrong diagnostic severity | `t.Errorf` — non-fatal, logs the mismatch for investigation |
| Diagnostics reference wrong file | `t.Errorf` — non-fatal, may indicate parser or interleaving issue |
| Progress callback never fires | `t.Fatal("expected at least one progress callback")` |
| `copyFixture` fails | `t.Fatalf` — can't proceed without source files |
| DiagnosticFormat.cmake not written | `t.Fatal` with path — injection mechanism broken |
| Note-level diagnostic not found | `t.Log` warning, not failure — compiler-dependent whether notes are emitted |

## Testing Strategy

The integration tests themselves ARE the testing strategy — they exist to fill the gap the retro identified. The validation hierarchy is:

### Test Categories

**1. Smoke tests** — Configure + Build + Clean succeeds with each available compiler.
- Fixture: `testdata/cmake/`
- Toolchains: table-driven (clang, gcc — whichever are available)
- Assertions: ExitCode == 0, Duration > 0, `compile_commands.json` exists, Clean succeeds

**2. Diagnostic injection tests** — `InjectDiagnosticFlags: true`, DiagnosticFormat.cmake executes correctly.
- Fixture: `testdata/cmake/`
- Assertions: `.cpp-build-mcp/DiagnosticFormat.cmake` exists in build dir, configure stderr contains `[cpp-build-mcp] Diagnostic format:`, format is `json` (GCC) or `sarif` (Clang)

**3. Error diagnostic tests** — Build fails, diagnostics parsed from real compiler output.
- Fixture: `testdata/cmake-error/`
- Pipeline: Configure (succeeds) → Build (fails) → `diagnostics.Parse(toolchain, stdout, stderr)`
- Assertions: ExitCode != 0, `Parse()` returns at least one diagnostic, diagnostic references `main.cpp`, severity is error, Line > 0

**4. Warning diagnostic tests** — Build succeeds, warnings parsed.
- Fixture: `testdata/cmake-warning/`
- Pipeline: Configure → Build (succeeds) → `diagnostics.Parse(toolchain, stdout, stderr)`
- Assertions: ExitCode == 0, diagnostics include at least one warning referencing expected file, warning Line > 0

**5. Multi-error diagnostic tests** — Multiple errors across multiple TUs.
- Fixture: `testdata/cmake-multi-error/`
- Config: `DiagnosticSerialBuild: true` (forces `-j1` to avoid interleaved output)
- Pipeline: Configure → Build (fails) → Parse
- Assertions: at least 2 diagnostics returned, diagnostics reference both `a.cpp` and `b.cpp`, all severity error

**6. Mixed-severity diagnostic tests** — Errors and warnings from the same build.
- Fixture: `testdata/cmake-mixed-diagnostics/`
- Config: `DiagnosticSerialBuild: true` (forces `-j1` to ensure warning-producing TU compiles before error-producing TU halts the build)
- Pipeline: Configure → Build (fails) → Parse
- Assertions: at least 2 diagnostics, at least one error (from `bad.cpp`), at least one warning (from `good.cpp`), diagnostics reference their respective files

**7. Note-level diagnostic tests** — Notes/fixits parsed alongside errors.
- Fixture: `testdata/cmake-note/`
- Pipeline: Configure → Build (fails) → Parse
- Assertions: diagnostics include at least one note-severity entry. If the compiler doesn't produce notes for this fixture, test is lenient (some GCC versions may not emit notes for the chosen code pattern — assert `len(diags) > 0` at minimum)

**8. Linker error tests** — Build fails at link, not compile.
- Fixture: `testdata/cmake-linker-error/`
- Pipeline: Configure → Build (fails)
- Assertions: ExitCode != 0, `diagnostics.Parse()` returns nil or empty (no structured compiler diagnostics — linker errors are unstructured text in stderr), `BuildResult.Stderr` contains the undefined symbol name

**9. Configure error tests** — Configure itself fails.
- Fixture: `testdata/cmake-configure-error/`
- Pipeline: Configure (fails)
- Assertions: ExitCode != 0, `BuildResult.Stderr` contains `"intentional configure failure"`. No diagnostic parsing — configure errors are not compiler diagnostics.

**10. Progress tests** — ProgressFunc fires with real Ninja `[N/M]` from stdout.
- Fixture: `testdata/cmake-library/` (multiple TUs)
- Default 250ms throttle accepted (see Decision 7)
- Each compiler subtest gets its own `collectProgress()` and event slice
- Assertions: callback count > 0, final callback has current == total, total > 1

**11. Preset tests** — Preset-mode configure with nested binaryDir.
- Fixture: `testdata/cmake-presets/`
- Assertions: DiagnosticFormat.cmake written to absolute path inside nested build dir, configure succeeds, build succeeds

**12. Target build tests** — Building specific targets.
- Fixture: `testdata/cmake-library/`
- Assertions: `--target lib` succeeds (exit 0), `--target main` succeeds (exit 0). We validate the target-passing plumbing (arguments reach cmake correctly), not which artifacts were or were not produced — artifact naming is platform-dependent.

### Input-to-Output Validation Matrix

The core principle: each fixture produces a **known input** (specific compiler output), and the test validates the **expected output** (parsed diagnostics with correct fields). This table maps the complete input→output pipeline:

| Fixture | Build Outcome | Compiler Output | Parser Input | Expected Diagnostics |
|---------|--------------|-----------------|--------------|---------------------|
| `cmake/` | Success | No errors/warnings | stdout+stderr (clean) | None (nil) |
| `cmake-error/` | Failure | 1 error on stdout or stderr | Parse(toolchain, stdout, stderr) | 1+ error in `main.cpp` |
| `cmake-warning/` | Success | 1+ warning on stdout or stderr | Parse(toolchain, stdout, stderr) | 1+ warning in `main.cpp` |
| `cmake-multi-error/` | Failure | 2+ errors across TUs | Parse(toolchain, stdout, stderr) | 2+ errors in `a.cpp` and `b.cpp` |
| `cmake-mixed-diagnostics/` | Failure | Error + warning from different TUs | Parse(toolchain, stdout, stderr) | Error in `bad.cpp` + warning in `good.cpp` |
| `cmake-note/` | Failure | Error + note | Parse(toolchain, stdout, stderr) | Error + note (compiler-dependent) |
| `cmake-linker-error/` | Failure | Linker error (unstructured) | Parse returns empty | None — error is in raw stderr |
| `cmake-configure-error/` | Configure fails | CMake error in stderr | N/A | N/A — not compiler output |

### Structural Verification

- `go vet ./...` — catches format mismatches, unreachable code
- `go test -race ./...` — integration tests exercise real concurrent I/O (MultiWriter + scanner goroutine)
- `staticcheck ./...` — additional correctness checks

The `-race` flag is particularly valuable here: the progress scanner goroutine runs concurrently with real cmake/Ninja I/O, which is the exact production scenario the unit tests couldn't exercise.

## Migration / Rollout

### Phased Implementation

**Phase 1: Test infrastructure + smoke tests.** Add `copyFixture`, skip guard helpers, `assertDiagnosticFound`, `collectProgress`, and the basic configure+build+clean smoke test with table-driven toolchains. Add diagnostic injection test. This immediately validates the retro's top action items.

**Phase 2: Compiler diagnostic pipeline.** Add the error, warning, multi-error, mixed-severity, and note diagnostic tests. Add the `cmake-warning/`, `cmake-multi-error/`, `cmake-note/`, and `cmake-mixed-diagnostics/` fixtures. This is the bulk of the input→output validation — each fixture produces a known compiler output category and the test verifies it flows through the parser correctly.

**Phase 3: Failure-mode tests.** Add linker error and configure error tests. Add the `cmake-linker-error/` and `cmake-configure-error/` fixtures. These validate the negative paths — what happens when the failure isn't a parseable compiler diagnostic.

**Phase 4: Progress, presets, and targets.** Add the progress callback test (`cmake-library/` fixture), preset path resolution test (`cmake-presets/` fixture), and target build tests. These directly validate the two highest-profile post-completion bugs and multi-TU build behavior.

### Backward Compatibility

No existing code is modified. All changes are additive:
- New file: `integration_test.go`
- New fixtures: `testdata/cmake-warning/`, `testdata/cmake-multi-error/`, `testdata/cmake-note/`, `testdata/cmake-linker-error/`, `testdata/cmake-configure-error/`, `testdata/cmake-library/`, `testdata/cmake-presets/`, `testdata/cmake-mixed-diagnostics/`
- Existing `TestCMakeBuilderIntegration` in `builder/cmake_test.go` is untouched

### CI Considerations

Integration tests are automatically skipped when tools are missing (`exec.LookPath`) or when `-short` is passed. No CI configuration changes required for the tests to be harmless. To enable them in CI, ensure the runner has `cmake`, `ninja-build`, and at least one of `clang`/`gcc` installed.
