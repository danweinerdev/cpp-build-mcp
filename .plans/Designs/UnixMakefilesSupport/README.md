---
title: "Unix Makefiles Generator Support"
type: design
status: implemented
created: 2026-03-11
updated: 2026-03-11
tags: [builder, generator, make, cmake, testing, generalization]
related: []
---

# Unix Makefiles Generator Support

## Overview

CMake projects using the "Unix Makefiles" generator are broken. When `generator: "make"` is configured, `NewBuilder` routes to `MakeBuilder` (plain Make, no CMake) instead of `CMakeBuilder` with `-G "Unix Makefiles"`. This means:

- `configure` is a silent no-op (returns success without running cmake)
- `build` runs bare `make` instead of `cmake --build`
- No `compile_commands.json` is generated
- Diagnostic injection via `CMAKE_PROJECT_INCLUDE` doesn't happen

The only way to use Unix Makefiles currently is through CMake presets, which bypass `NewBuilder` routing. Only 2 of 22 integration tests exercise Make builds (both preset-based).

## Architecture

### Current Builder Routing

```
NewBuilder(cfg)
  ├── generator: "ninja" or "" → CMakeBuilder  (runs cmake -G Ninja)
  └── generator: "make"        → MakeBuilder   (runs plain make, no cmake)
```

### Proposed Builder Routing

```
NewBuilder(cfg)
  ├── generator: "ninja" or "" → CMakeBuilder  (runs cmake -G Ninja)
  ├── generator: "make"        → CMakeBuilder  (runs cmake -G "Unix Makefiles")
  └── generator: "plain-make"  → MakeBuilder   (runs plain make, no cmake)
```

### Components Affected

1. **`builder.NewBuilder`** (`builder/builder.go`) — Route `"make"` to `CMakeBuilder` instead of `MakeBuilder`. Add `"plain-make"` for the legacy plain-Make behavior. Update the error message on line 49 from `"supported: ninja, make"` to `"supported: ninja, make, plain-make"`.

2. **`CMakeBuilder.buildBuildArgs`** (`builder/cmake.go`) — Already supports Unix Makefiles via `generatorCMakeName("make")` → `"Unix Makefiles"`. The comment even notes the branch is "not reachable via CMakeBuilder in production" — this change makes it reachable. Remove the unreachable-branch comment. Additionally, refactor the native tool flags after `--` (lines 423–434) to generalize for future generators:
   - Switch parallelism from `-- -jN` to CMake's built-in `--parallel N` (generator-agnostic since CMake 3.12).
   - Extract keep-going flags into a `nativeKeepGoingFlags(generator string) []string` helper. Only emit `--` when native flags are present.

3. **Integration tests** (`integration_test.go`) — Expand coverage to run core test scenarios with both Ninja and Unix Makefiles generators.

### Data Flow

With the fix, a `generator: "make"` config follows the same CMake path as Ninja:

```
configure → cmake -S . -B build -G "Unix Makefiles" -DCMAKE_EXPORT_COMPILE_COMMANDS=ON
build     → cmake --build build [--target t] [--parallel N] [-- <native flags>]
clean     → cmake --build build --target clean
```

This is identical to Ninja except the generated build system uses Makefiles instead of `build.ninja`.

### Interfaces

No MCP tool API changes. The `generator` config field gains clearer semantics:

| Value | Behavior |
|-------|----------|
| `"ninja"` or `""` | CMake + Ninja (default, unchanged) |
| `"make"` | CMake + Unix Makefiles (fixed — was plain Make) |
| `"plain-make"` | Plain Make without CMake (new name for old `"make"` behavior) |

## Design Decisions

### Decision 1: Route `"make"` to CMakeBuilder

**Context:** `generator: "make"` currently creates a `MakeBuilder` (plain Make). Users setting this likely expect CMake with Unix Makefiles, not a raw `make` invocation.

**Options Considered:**
1. Add a new value `"unix-makefiles"` for CMake + Make, keep `"make"` as plain Make
2. Route `"make"` to CMakeBuilder, add `"plain-make"` for the old behavior
3. Auto-detect: if CMakeLists.txt exists, use CMakeBuilder; otherwise MakeBuilder

**Decision:** Option 2

**Rationale:** "make" is the natural name users will type when they want CMake with Makefiles. The existing `MakeBuilder` usage is likely minimal since most CMake projects use Ninja. Adding `"plain-make"` preserves backward compatibility for anyone who actually uses raw Make. Auto-detection adds implicit behavior that's hard to reason about.

### Decision 2: Parameterized integration tests for generator coverage

**Context:** 20 of 22 integration tests are hardcoded to Ninja. Duplicating them for Make would be 40+ tests with copy-paste code.

**Options Considered:**
1. Duplicate each test with a Make variant
2. Parameterize existing tests with a generator table (subtest per generator)
3. Add a separate parallel test suite for Make

**Decision:** Option 2

**Rationale:** Subtest parameterization (`t.Run("ninja", ...)` / `t.Run("make", ...)`) is idiomatic Go, avoids code duplication, and gives clear per-generator pass/fail in test output. Tests that are generator-agnostic (like preset discovery) don't need parameterization.

### Decision 3: Which tests to parameterize

**Context:** Not all tests need both generators. Some test CMake-specific features that work identically regardless of generator.

**Tests to parameterize** (core build pipeline):
- TestIntegrationSmoke
- TestIntegrationDiagnosticInjection
- TestIntegrationErrorDiagnostics
- TestIntegrationWarningDiagnostics
- TestIntegrationMultiError
- TestIntegrationMixedDiagnostics
- TestIntegrationLinkerError
- TestIntegrationConfigureError
- TestIntegrationBuildReconfigureFresh
- TestIntegrationBuildReconfigureAfterChange
- TestIntegrationBuildReconfigureWithCMakeError
- TestIntegrationTargetBuild

**Tests to leave Ninja-only** (generator-irrelevant or Ninja-specific):
- TestIntegrationNoteDiagnostics (CMake message parsing, generator-irrelevant)
- TestIntegrationConfigureUnknownCommand (CMake error parsing, generator-irrelevant)
- TestIntegrationProgress (parses Ninja's `[N/M]` progress lines; Unix Makefiles emits different output without structured progress)
- TestIntegrationPresets, TestIntegrationPreset* (preset-specific, already tested with Make via `presetGeneratorCases`)
- TestIntegrationLoadPresets* (config reload, generator-irrelevant)
- TestIntegrationBuildCMakeReconfigureError (hardcoded Ninja — tests Ninja-specific auto-reconfigure when `build.ninja` detects stale CMake inputs; Unix Makefiles doesn't have this behavior)

### Decision 4: Generalize native build tool flags for future generators

**Context:** `buildBuildArgs` currently passes `-jN` and `-k 0` (Ninja-specific) after the `--` separator. Unix Makefiles needs `-k` (no argument) instead of `-k 0`. Future generators like Visual Studio / MSBuild will need entirely different flags (`/m:N`, `/p:ContinueOnError=...`). Parallelism is handled identically by all CMake generators via `--parallel N`.

**Options Considered:**
1. Keep `-- -jN` and add a generator switch for the keep-going flag only
2. Switch to `--parallel N` for parallelism, extract keep-going into a generator-keyed helper
3. Move all native flags into a full generator-specific strategy/interface

**Decision:** Option 2

**Rationale:** `cmake --build --parallel N` has been generator-agnostic since CMake 3.12 (2018) and eliminates the need for generator-specific parallelism flags entirely. The only remaining native flag is keep-going behavior, which differs per generator. Extracting it into `nativeKeepGoingFlags(generator) []string` is minimal, directly useful now (Ninja vs Make differ), and trivially extensible for MSBuild later. A full strategy interface is overkill for a single flag.

**Current code** (`cmake.go:423–434`):
```go
args = append(args, "--")
if b.cfg.DiagnosticSerialBuild {
    jobs = 1
    args = append(args, "-k", "0")
}
if jobs > 0 {
    args = append(args, fmt.Sprintf("-j%d", jobs))
}
```

**Proposed code:**
```go
if b.cfg.DiagnosticSerialBuild {
    jobs = 1
}
if jobs > 0 {
    args = append(args, "--parallel", strconv.Itoa(jobs))
}
if b.cfg.DiagnosticSerialBuild {
    if flags := nativeKeepGoingFlags(b.cfg.Generator); len(flags) > 0 {
        args = append(args, "--")
        args = append(args, flags...)
    }
}

// nativeKeepGoingFlags returns the generator-specific "keep going" flags
// passed after -- to the native build tool.
func nativeKeepGoingFlags(gen string) []string {
    switch gen {
    case "ninja", "":
        return []string{"-k", "0"}
    case "make":
        return []string{"-k"}
    default:
        return nil
    }
}
```

## Error Handling

No new error modes. The Unix Makefiles path through `CMakeBuilder` uses the same error handling as Ninja:
- Configure failure → `configureResponse` with structured CMake diagnostics
- Build failure → `buildResponse` with parsed compiler diagnostics
- Missing `make` → cmake configure fails with a clear error about the missing generator

## Testing Strategy

### Test Parameterization Pattern

Generator cases compose with the existing `toolchainCases` loop. The outer loop selects the generator; the inner loop selects the compiler. Each `generatorCase` has a `require` function that skips the subtest if the generator tool is missing.

```go
type generatorCase struct {
    name      string             // subtest name: "ninja" or "make"
    generator string             // config.Config Generator value
    require   func(t *testing.T) // skip if generator tool is missing
}

// generatorCases returns test cases for available generators.
func generatorCases(t *testing.T) []generatorCase {
    t.Helper()
    cases := []generatorCase{{name: "ninja", generator: "ninja", require: requireNinja}}
    if _, err := exec.LookPath("make"); err == nil {
        cases = append(cases, generatorCase{name: "make", generator: "make", require: requireMake})
    }
    return cases
}

func TestIntegrationSmoke(t *testing.T) {
    requireCMake(t)
    for _, gc := range generatorCases(t) {
        t.Run(gc.name, func(t *testing.T) {
            gc.require(t) // skip if ninja/make not on PATH
            for _, tc := range toolchainCases(t) {
                t.Run(tc.name, func(t *testing.T) {
                    // existing test body, unchanged except:
                    //   Generator: gc.generator  (was hardcoded "ninja")
                    cfg := &config.Config{
                        SourceDir: srcDir,
                        BuildDir:  buildDir,
                        Toolchain: tc.toolchain,
                        Generator: gc.generator,
                        // ...
                    }
                    // ...
                })
            }
        })
    }
}
```

This produces subtests like `TestIntegrationSmoke/ninja/clang`, `TestIntegrationSmoke/make/gcc`, etc.

### Structural Verification

- `go vet ./...` on every change
- `go test -race ./...` for all tests
- Integration tests gated by `requireMake(t)` so they skip gracefully when `make` is not installed

### Coverage Matrix

| Test | Ninja | Make |
|------|-------|------|
| Smoke (configure + build) | yes | **new** |
| Diagnostic injection | yes | **new** |
| Error diagnostics | yes | **new** |
| Warning diagnostics | yes | **new** |
| Multi-error | yes | **new** |
| Mixed diagnostics | yes | **new** |
| Linker error | yes | **new** |
| Configure error | yes | **new** |
| Reconfigure fresh | yes | **new** |
| Reconfigure after change | yes | **new** |
| Reconfigure cmake error | yes | **new** |
| Target build | yes | **new** |
| Note diagnostics | yes | — |
| Configure unknown command | yes | — |
| Progress notifications | yes | — |
| Presets | yes | yes (existing) |
| Load presets | yes | — |

## Migration / Rollout

### Breaking Change

`generator: "make"` changes meaning from "plain Make" to "CMake + Unix Makefiles". Users with plain Makefile projects (no CMakeLists.txt) who set `generator: "make"` will need to change to `generator: "plain-make"`.

### Mitigation

- The `"plain-make"` path is added simultaneously, so no functionality is lost
- Plain Make projects are likely rare (most C/C++ projects tracked by this tool use CMake)
- If a user has `generator: "make"` and no CMakeLists.txt, `cmake` will fail with a clear error about missing CMakeLists.txt — not a silent failure
