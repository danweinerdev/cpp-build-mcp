---
title: "Diagnostic Stream & Format Fix"
type: plan
status: complete
created: 2026-03-08
updated: 2026-03-08
tags: [diagnostics, clang, sarif, gcc, stderr, builder]
related: [Designs/ClangSARIFSupport]
phases:
  - id: 1
    title: "SARIF Parser Foundation"
    status: complete
    doc: "01-SARIF-Parser-Foundation.md"
  - id: 2
    title: "ClangParser Integration"
    status: complete
    doc: "02-ClangParser-Integration.md"
    depends_on: [1]
  - id: 3
    title: "Builder Flag Update"
    status: complete
    doc: "03-Builder-Flag-Update.md"
    depends_on: [2]
  - id: 4
    title: "GCC Stderr Fix"
    status: complete
    doc: "04-GCC-Stderr-Fix.md"
---

# Diagnostic Stream & Format Fix

## Overview

Empirical testing (Clang 21.1.8, GCC 15.2.1, Fedora 43) revealed two bugs:

**Bug 1 — Clang builds are broken.** The builder injects `-fdiagnostics-format=json`, which Clang **rejects** with a hard error: `error: invalid value 'json' in '-fdiagnostics-format json'`. Every Clang compilation unit fails. The correct flag is `-fdiagnostics-format=sarif`, which emits SARIF 2.1.0 on **stderr**.

**Bug 2 — GCC diagnostics silently lost.** GCC 15 writes `-fdiagnostics-format=json` output to **stderr**, but `GCCParser` reads only stdout. Structured diagnostics are silently dropped.

Both bugs are documented in `diagnostics/clang_sarif_evidence_test.go`.

This plan fixes both: SARIF support for Clang (Phases 1-3) and stderr fallback for GCC (Phase 4).

## Architecture

- **`diagnostics/`** — New `sarif.go` with SARIF types. Modified `clang.go` with format auto-detection and stderr fallback. Modified `gcc.go` with stderr fallback.
- **`builder/`** — Modified `cmake.go` and `make.go` to inject `-fdiagnostics-format=sarif -Wno-sarif-format-unstable` for Clang.

No interface changes. `DiagnosticParser`, `NewParser`, `Diagnostic`, `config.Config` unchanged.

## Key Decisions

1. **Auto-detect format** inside `ClangParser.Parse` — `{` = SARIF, `[` = native JSON.
2. **Stdout-first stream selection** in both parsers — stderr fallback when stdout is empty.
3. **Parallel implementations** — each parser has its own stream selection (no shared helper).
4. **make.go becomes toolchain-aware** — `diagnosticFlag()` selects SARIF for clang, JSON for everything else.
5. **GCC fix is 4 lines** — if stdout empty after trim, use stderr instead.

See `Designs/ClangSARIFSupport` for full decision rationale.

## Dependencies

- No external dependencies.
- `diagnostics/clang_sarif_evidence_test.go` documents the bugs with real compiler output.
- Phase 4 (GCC) has no dependency on Phases 1-3 at the code level, but is sequenced after them to keep the diagnostics package changes cohesive.
