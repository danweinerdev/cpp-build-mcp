---
title: "ClangParser Integration"
type: phase
plan: ClangSARIFSupport
phase: 2
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "ClangParser.Parse auto-detects SARIF vs native JSON, selects stream (stdout priority, stderr fallback), and delegates to the correct parser path"
tasks:
  - id: "2.1"
    title: "Format detection, stream selection, and Ninja stripping in ClangParser.Parse"
    status: complete
    verification: "ClangParser.Parse strips Ninja lines from both stdout and stderr. Detects SARIF (starts with '{') on stdout and parses it. Detects native JSON (starts with '[') on stdout and parses it via existing path. Falls back to stderr when stdout has no structured content after Ninja stripping. Returns nil,nil when neither stream has structured content (first non-ws char is not '{' or '[')."
  - id: "2.2"
    title: "Update existing tests and add SARIF integration tests"
    status: complete
    depends_on: ["2.1"]
    verification: "Existing 'stderr is ignored' test renamed to 'stderr not used when stdout has content' and still passes (stdout wins when both have content). New tests: SARIF on stdout, SARIF on stderr with empty stdout, Ninja progress + SARIF on stdout, Ninja-only stdout with SARIF on stderr falls back to stderr after stripping, neither stream has structured content returns nil. All existing clang_test.go tests pass unchanged (except the renamed stderr test)."
  - id: "2.3"
    title: "Structural verification"
    status: complete
    depends_on: ["2.1", "2.2"]
    verification: "go vet ./diagnostics/... and go test -race ./diagnostics/... pass. All pre-existing tests pass without modification (except the renamed stderr test)."
---

# Phase 2: ClangParser Integration

## Overview

Modify `diagnostics/clang.go` to add format auto-detection, stream selection, and Ninja stripping on both streams, wiring the SARIF parsing from Phase 1 into the existing `ClangParser.Parse` method. Update `diagnostics/clang_test.go` with integration tests and the renamed stderr test.

The key change: `Parse` now applies Ninja stripping to both streams, checks each for structured content (`{` or `[` as first non-whitespace), selects the stream, detects the format, and delegates.

## 2.1: Format detection, stream selection, and Ninja stripping in ClangParser.Parse

### Subtasks
- [ ] Add `hasStructuredContent(s string) bool` — returns true if first non-whitespace char is `{` or `[`
- [ ] Add `detectOutputFormat(s string) string` — returns `"sarif"` if first non-ws is `{`, `"clang-json"` if `[`, `""` otherwise
- [ ] Apply `ninjaProgressRe.ReplaceAllString` to stderr (in addition to existing stdout stripping)
- [ ] Apply `strings.TrimSpace` to stderr after stripping
- [ ] Modify `Parse` method flow:
  1. Strip Ninja lines from stdout (existing)
  2. Strip Ninja lines from stderr (new)
  3. If `hasStructuredContent(stdout)` → use stdout
  4. Else if `hasStructuredContent(stderr)` → use stderr
  5. Else → return nil, nil (no structured diagnostics in either stream)
  6. `detectOutputFormat(selected)`:
     - `"sarif"` → call `parseSARIF(selected)`
     - `"clang-json"` → existing `splitJSONArrays` + unmarshal path
     - `""` → return nil, nil (defensive; should not happen after steps 3-5)

### Notes
The existing code path for native JSON (`splitJSONArrays` → `json.Unmarshal` → `mapClangSeverity`) is unchanged. The SARIF path delegates entirely to `parseSARIF` from Phase 1.

Stream selection and format detection use the same `{`/`[` predicate, ensuring no gap between "this stream has content" and "this content has a detectable format."

Ninja stripping on stderr is safe: the regex matches `[N/M] Building...` patterns which are not valid SARIF or JSON content.

## 2.2: Update existing tests and add SARIF integration tests

### Subtasks
- [ ] Rename `"stderr is ignored"` test (line 356) to `"stderr not used when stdout has content"` — same assertions, just a name change reflecting the updated contract
- [ ] Add test: `"SARIF on stdout"` — full SARIF document on stdout, empty stderr → parses correctly
- [ ] Add test: `"SARIF on stderr with empty stdout"` — empty stdout, SARIF on stderr → falls back and parses
- [ ] Add test: `"Ninja progress + SARIF on stdout"` — Ninja lines + SARIF document on stdout → strips and parses
- [ ] Add test: `"Ninja-only stdout with SARIF on stderr"` — Ninja lines on stdout, SARIF on stderr → falls back to stderr after stripping
- [ ] Add test: `"neither stream has structured content"` — non-JSON text on both → returns nil, nil
- [ ] Verify all 12 existing `TestClangParser_Parse` subtests still pass

### Notes
SARIF test fixtures should be realistic but minimal — one `runs[]` entry with one `results[]` entry, using `file:///path/to/file.cpp` URIs. The integration tests validate the full `Parse` entry point, not just the SARIF parser (which is covered in Phase 1).

## 2.3: Structural verification

### Subtasks
- [ ] Run `go vet ./diagnostics/...`
- [ ] Run `go test -race ./diagnostics/...`
- [ ] Run `staticcheck ./diagnostics/...` if available
- [ ] Confirm all pre-existing tests pass (zero regressions)

## Acceptance Criteria
- [ ] `ClangParser.Parse` handles SARIF input on stdout and stderr
- [ ] `ClangParser.Parse` handles native JSON input on stdout (no regression)
- [ ] `ClangParser.Parse` returns nil, nil when no structured content in either stream
- [ ] Ninja stripping applies to both streams
- [ ] `"stderr not used when stdout has content"` test validates stdout priority
- [ ] All existing `clang_test.go` tests pass (only the stderr test is renamed)
- [ ] Full `go test ./...` green (parser + builder packages unaffected)
- [ ] `go vet` passes
