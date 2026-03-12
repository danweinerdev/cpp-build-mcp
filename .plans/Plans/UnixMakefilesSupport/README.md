---
title: "Unix Makefiles Generator Support"
type: plan
status: complete
created: 2026-03-11
updated: 2026-03-11
tags: [builder, generator, make, cmake, testing, generalization]
related: [Designs/UnixMakefilesSupport]
phases:
  - id: 1
    title: "Builder Refactoring"
    status: complete
    doc: "01-Builder-Refactoring.md"
  - id: 2
    title: "Integration Test Parameterization"
    status: complete
    doc: "02-Integration-Test-Parameterization.md"
    depends_on: [1]
---

# Unix Makefiles Generator Support

## Overview

Fix the broken `generator: "make"` routing so it creates a `CMakeBuilder` (running `cmake -G "Unix Makefiles"`) instead of a `MakeBuilder` (plain Make, no CMake). Generalize the native build tool flags in `buildBuildArgs` for future generator support (Visual Studio / MSBuild). Add integration test coverage for Unix Makefiles equivalent to the existing Ninja coverage.

## Architecture

```
NewBuilder(cfg)
  ├── "ninja" or "" → CMakeBuilder  (cmake -G Ninja)
  ├── "make"        → CMakeBuilder  (cmake -G "Unix Makefiles")
  └── "plain-make"  → MakeBuilder   (plain make, no cmake)
```

`buildBuildArgs` switches from Ninja-specific `-- -jN` to generator-agnostic `--parallel N`. Keep-going flags (`-k 0` for Ninja, `-k` for Make) are extracted into `nativeKeepGoingFlags(generator)` so future generators (MSBuild) add a case without touching `buildBuildArgs`.

Integration tests gain a `generatorCases` loop that wraps the existing `toolchainCases` loop, producing subtests like `TestIntegrationSmoke/ninja/clang` and `TestIntegrationSmoke/make/gcc`.

## Key Decisions

1. **Route `"make"` to CMakeBuilder** — "make" is the natural name for CMake + Unix Makefiles. Legacy plain-Make users switch to `"plain-make"`.
2. **`--parallel N` instead of `-- -jN`** — Generator-agnostic since CMake 3.12. Only keep-going flags remain generator-specific after `--`.
3. **Parameterized subtests** — `generatorCases(t)` + `toolchainCases(t)` avoids duplicating 12 tests.

## Dependencies

- CMake 3.12+ for `--parallel` support (widely available since 2018)
- `make` on PATH for integration test Make subtests (skipped gracefully if missing)
