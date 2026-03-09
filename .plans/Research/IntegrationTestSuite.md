---
title: "Integration Test Suite with Real Compiler Projects"
type: research
status: draft
created: 2026-03-09
updated: 2026-03-09
tags: [testing, integration, compilers, cmake, ninja]
related:
  - Retro/2026-03-09-v1-launch-and-real-world-validation
  - Plans/BuildProgressNotifications
  - Plans/ClangSARIFSupport
---

# Integration Test Suite with Real Compiler Projects

## Motivation

Three post-completion bugs (progress scanner on wrong stream, CMAKE_PROJECT_INCLUDE relative paths, build dir creation for presets) all passed unit and e2e tests but failed against real builds. The root cause: fakeBuilder-based tests validate plumbing, not integration with real tools.

We need a test suite that builds real C++ projects with real compilers.

## Scope

### Test Projects (in `testdata/` or similar)

- **Minimal executable** — single `main.cpp`, verifies configure + build + progress callbacks work end-to-end
- **Library + executable** — static/shared lib consumed by an exe, verifies target-based builds and dependency graphs
- **Multi-file with errors** — deliberate compilation errors and warnings, verifies diagnostic parsing produces correct `Diagnostic` structs from real compiler output (not synthetic JSON strings)
- **Preset-based project** — `CMakePresets.json` with multiple configurations (Debug/Release, different generators), verifies preset discovery, path resolution, and binary dir creation

### Compiler Coverage

Test against every toolchain we support:
- **Clang** — SARIF diagnostic format, `-fdiagnostics-format=sarif`
- **GCC** — JSON diagnostic format, `-fdiagnostics-format=json`, stderr output
- **MSVC** (if CI supports it) — regex fallback parser

### What to Validate

- `Configure` succeeds and creates build system files
- `Build` succeeds and produces artifacts
- `ProgressFunc` receives real `[N/M]` callbacks from Ninja (stream assumption)
- `CMAKE_PROJECT_INCLUDE` injection writes the module and cmake finds it (path resolution)
- Diagnostic parsing from real compiler output matches expected `Diagnostic` fields
- `Build` with deliberate errors produces non-zero exit code and parseable diagnostics
- `Clean` removes build artifacts
- Preset-based builds resolve `binaryDir` correctly

### CI Considerations

- Tests that require compilers should be gated behind a build tag (e.g., `//go:build integration`) or skip via `testing.Short()`
- CI matrix: at minimum Clang + GCC on Linux
- Keep test projects minimal to avoid long compile times — the goal is correctness, not scale
