---
title: "Integration Test Suite"
type: plan
status: complete
created: 2026-03-09
updated: 2026-03-09
tags: [testing, integration, compilers, cmake, ninja, diagnostics]
related: [Designs/IntegrationTestSuite, Research/IntegrationTestSuite, Retro/2026-03-09-v1-launch-and-real-world-validation]
phases:
  - id: 1
    title: "Test Infrastructure and Smoke Tests"
    status: complete
    doc: "01-Test-Infrastructure.md"
  - id: 2
    title: "Compiler Diagnostic Pipeline"
    status: complete
    doc: "02-Diagnostic-Pipeline.md"
    depends_on: [1]
  - id: 3
    title: "Failure Mode Tests"
    status: complete
    doc: "03-Failure-Modes.md"
    depends_on: [1]
  - id: 4
    title: "Progress, Presets, and Targets"
    status: complete
    doc: "04-Progress-Presets-Targets.md"
    depends_on: [1]
---

# Integration Test Suite

## Overview

Add integration tests that build real C++ projects with real compilers, validating the full pipeline from `Configure` through `Build` to diagnostic parsing. Directly addresses the v1 retro finding that three post-completion bugs were caught only by real-world usage, never by tests.

The plan creates 8 new purpose-built C/C++ test fixtures, each designed to produce a specific compiler behavior (error, warning, note, linker failure, configure failure, mixed severities). Tests exercise the complete input→output pipeline: cmake configure → real compilation → diagnostic parsing → assertion against expected output.

## Architecture

All integration tests live in a new `integration_test.go` at the package root (package `main`), using existing public APIs from `builder`, `diagnostics`, and `config`. Test fixtures are minimal CMake projects in `testdata/`. Each test copies its fixture to `t.TempDir()` for parallel safety and auto-cleanup.

Tests are table-driven per available compiler (Clang, GCC). Skip guards (`exec.LookPath` + `testing.Short()`) ensure graceful degradation. Diagnostic assertions use fuzzy matching (file suffix + severity + positive line) to tolerate cross-compiler differences.

See `Designs/IntegrationTestSuite/README.md` for the full architecture, design decisions, and input→output validation matrix.

## Key Decisions

1. **Top-level `integration_test.go`** — cross-package (builder + diagnostics), not in builder/cmake_test.go
2. **`t.TempDir()` copies** — parallel-safe, auto-cleaned, testdata/ stays read-only
3. **`exec.LookPath` + `testing.Short()`** — graceful skip, no build tags needed
4. **Fuzzy diagnostic assertions** — file suffix + severity + Line > 0, not exact messages
5. **Table-driven per-compiler** — each available toolchain gets its own subtest
6. **Default 250ms throttle for progress** — `progressMinInterval` is unexported; first/final lines always fire
7. **Purpose-built fixtures as specs** — C/C++ source IS the behavioral specification

## Dependencies

- cmake 3.16+ and ninja installed (skip if missing)
- At least one C++ compiler: clang++ or g++ (skip if missing)
- No new Go dependencies — uses existing `builder`, `diagnostics`, `config` packages
- No changes to existing code — purely additive (new file + new fixtures)
