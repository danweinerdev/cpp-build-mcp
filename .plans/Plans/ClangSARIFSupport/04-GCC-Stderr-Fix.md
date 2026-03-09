---
title: "GCC Stderr Fix"
type: phase
plan: ClangSARIFSupport
phase: 4
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "GCCParser.Parse reads from stderr when stdout is empty, fixing the GCC 15 diagnostic loss bug"
tasks:
  - id: "4.1"
    title: "Add stderr fallback to GCCParser.Parse"
    status: complete
    verification: "GCCParser.Parse returns diagnostics when JSON is on stderr and stdout is empty. GCCParser.Parse still returns diagnostics when JSON is on stdout (no regression). GCCParser.Parse returns nil when both streams are empty."
  - id: "4.2"
    title: "Update gcc_test.go"
    status: complete
    depends_on: ["4.1"]
    verification: "Existing 'stderr is ignored' test renamed to 'stderr not used when stdout has content' — same assertions, stdout wins when both have content. New test: JSON on stderr with empty stdout returns parsed diagnostics. New test: both streams empty returns nil. All existing gcc_test.go tests pass unchanged (except the renamed test)."
  - id: "4.3"
    title: "Update evidence test expectations"
    status: complete
    depends_on: ["4.1"]
    verification: "TestGCCParserStreamMismatch/GCC_JSON_on_stderr_is_missed_by_GCCParser now expects diagnostics (not nil) since the bug is fixed. Test renamed to reflect the fixed behavior."
  - id: "4.4"
    title: "Structural verification"
    status: complete
    depends_on: ["4.1", "4.2", "4.3"]
    verification: "go vet ./diagnostics/... and go test -race ./diagnostics/... pass. Full go test ./... passes across all packages."
---

# Phase 4: GCC Stderr Fix

## Overview

GCC 15 writes `-fdiagnostics-format=json` output to **stderr**, not stdout. The existing `GCCParser.Parse` reads only stdout, causing structured diagnostics to be silently lost. This is confirmed in `diagnostics/clang_sarif_evidence_test.go:TestGCCParserStreamMismatch`.

The fix is minimal: if stdout is empty after trim, use stderr instead. Same stdout-first/stderr-fallback pattern as the ClangParser changes in Phase 2, but simpler since GCC doesn't need format detection or Ninja stripping.

## 4.1: Add stderr fallback to GCCParser.Parse

### Subtasks
- [ ] Change `gcc.go:Parse` to select input stream:
  ```go
  input := strings.TrimSpace(stdout)
  if input == "" {
      input = strings.TrimSpace(stderr)
  }
  if input == "" {
      return nil, nil
  }
  ```
- [ ] Replace all subsequent `stdout` references in Parse with `input`
- [ ] Update the function comment to document stderr fallback behavior

### Notes
The existing Parse function at `gcc.go:49-77` does `stdout = strings.TrimSpace(stdout)` then checks if empty. The change introduces an `input` variable that tries stdout first, then stderr. The rest of the function (splitJSONArrays, unmarshal, flattenGCCDiagnostic) operates on `input` unchanged.

No Ninja stripping needed: GCC builds don't interleave Ninja progress lines with JSON output because GCC writes to stderr (not stdout where Ninja progress appears). The stdout path similarly has no Ninja stripping — this is pre-existing behavior and out of scope for this phase.

## 4.2: Update gcc_test.go

### Subtasks
- [ ] Rename `"stderr is ignored"` test (line 329) to `"stderr not used when stdout has content"` — same body, same assertions
- [ ] Add test: `"JSON on stderr with empty stdout"` — pass GCC JSON as stderr, empty string as stdout → expect parsed diagnostics
- [ ] Add test: `"JSON on stderr with whitespace-only stdout"` — pass `"   \n\t  "` as stdout, GCC JSON as stderr → falls back to stderr
- [ ] Verify all 13 existing `TestGCCParser_Parse` subtests pass (only the stderr test is renamed)

### Notes
The renamed test validates stdout priority: when both streams have content, stdout wins. This is the same contract as the renamed ClangParser test in Phase 2.

## 4.3: Update evidence test expectations

### Subtasks
- [ ] In `clang_sarif_evidence_test.go`, update `TestGCCParserStreamMismatch`:
  - Rename `"GCC JSON on stderr is missed by GCCParser"` to `"GCC JSON on stderr is parsed by GCCParser"`
  - Change assertion from `diags == nil` to `len(diags) == 1`
  - Assert `diags[0].Message == "unused variable 'unused_var'"` and `diags[0].Severity == SeverityWarning` (mirroring the `"GCC JSON on stdout works"` sub-test)
- [ ] Verify `"GCC JSON on stdout works"` test still passes unchanged

### Notes
The evidence test was written to document the bug. After the fix, it documents the correct behavior. The test becomes a regression guard rather than a bug witness.

## 4.4: Structural verification

### Subtasks
- [ ] Run `go vet ./diagnostics/...`
- [ ] Run `go test -race ./diagnostics/...`
- [ ] Run `go test ./...` (full suite)
- [ ] Run `staticcheck ./diagnostics/...` if available

## Acceptance Criteria
- [ ] `GCCParser.Parse` returns diagnostics when JSON is on stderr
- [ ] `GCCParser.Parse` still returns diagnostics when JSON is on stdout (no regression)
- [ ] `"stderr not used when stdout has content"` test validates stdout priority
- [ ] All existing `gcc_test.go` tests pass (only the stderr test is renamed)
- [ ] Evidence test updated to reflect fixed behavior
- [ ] `go vet` and `go test -race` pass
