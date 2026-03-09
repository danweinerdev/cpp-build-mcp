---
title: "SARIF Parser Foundation"
type: phase
plan: ClangSARIFSupport
phase: 1
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "New sarif.go with SARIF 2.1.0 types, parsing, URI stripping, object splitting, and full unit test coverage in sarif_test.go"
tasks:
  - id: "1.1"
    title: "SARIF types and parseSARIF function"
    status: complete
    verification: "parseSARIF converts a SARIF document with error, warning, and note results into []Diagnostic with correct File, Line, Column, Severity, Message, Code, and Source fields. Empty results array returns nil. Missing locations produces Diagnostic with empty File and zero Line/Column. Multiple runs are merged."
  - id: "1.2"
    title: "splitJSONObjects function"
    status: complete
    verification: "splitJSONObjects splits concatenated SARIF objects ({...}{...}) correctly. Handles single object, two objects, objects with inter-object whitespace, nested braces within objects, escaped quotes in strings, and empty input. Mirrors splitJSONArrays behavior for {} instead of []."
  - id: "1.3"
    title: "stripFileURI function"
    status: complete
    verification: "stripFileURI converts file:///absolute/path to /absolute/path, leaves file://host/path as-is, strips file: prefix from file:relative/path, and leaves bare paths unchanged."
  - id: "1.4"
    title: "mapSARIFLevel function"
    status: complete
    verification: "mapSARIFLevel maps 'error' to SeverityError, 'warning' to SeverityWarning, 'note' to SeverityNote, unknown strings to SeverityWarning. Case-insensitive."
  - id: "1.5"
    title: "Malformed SARIF fallback"
    status: complete
    depends_on: ["1.1"]
    verification: "parseSARIF returns a single fallback Diagnostic with Severity error, Source 'clang', and truncated output message when given invalid JSON that starts with '{'."
  - id: "1.6"
    title: "Structural verification"
    status: complete
    depends_on: ["1.1", "1.2", "1.3", "1.4", "1.5"]
    verification: "go vet ./diagnostics/... and go test -race ./diagnostics/... pass with zero warnings and zero failures."
---

# Phase 1: SARIF Parser Foundation

## Overview

Create `diagnostics/sarif.go` and `diagnostics/sarif_test.go` as standalone additions. No existing code is modified in this phase — `ClangParser` integration happens in Phase 2.

The SARIF types are minimal: only the fields that map to `Diagnostic` are defined. The `parseSARIF` function handles the full pipeline: split concatenated objects → unmarshal → extract results → strip URIs → map severity.

## 1.1: SARIF types and parseSARIF function

### Subtasks
- [ ] Define minimal SARIF structs: `sarifDocument`, `sarifRun`, `sarifResult`, `sarifMessage`, `sarifLocation`, `sarifPhysicalLocation`, `sarifArtifactLocation`, `sarifRegion`
- [ ] Implement `parseSARIF(data string) ([]Diagnostic, error)` — splits objects, unmarshals each, iterates `runs[].results[]`, builds `[]Diagnostic`
- [ ] Set `Source: "clang"` on every produced Diagnostic
- [ ] Map `ruleId` to `Diagnostic.Code`
- [ ] Write tests: single error, single warning, note level, multiple results in one run, multiple runs merged, empty results returns nil, missing locations → empty File/zero Line/Column

### Notes
The `assertDiagField`, `assertDiagInt`, `assertDiagSeverity` helpers from `clang_test.go` are reusable in `sarif_test.go` since both are `package diagnostics`.

SARIF test fixtures use inline JSON strings (same convention as `clang_test.go`), not external files.

## 1.2: splitJSONObjects function

### Subtasks
- [ ] Implement `splitJSONObjects(s string) []string` using brace-depth tracking — mirrors `splitJSONArrays` but tracks `{`/`}` instead of `[`/`]`
- [ ] Handle: string literals with escaped quotes, nested braces, inter-object whitespace
- [ ] Write tests: single object, two objects, objects with whitespace between, nested braces in strings, escaped quotes, empty input
- [ ] Write end-to-end test: `parseSARIF` with concatenated SARIF objects (`{...}{...}`) merges all results
- [ ] Write end-to-end test: `parseSARIF` with whitespace between concatenated objects merges all results

### Notes
Direct port of `splitJSONArrays` (clang.go:82-122) with `{`/`}` replacing `[`/`]`. Same string/escape tracking logic.

## 1.3: stripFileURI function

### Subtasks
- [ ] Implement `stripFileURI(uri string) string`
- [ ] If URI matches `file:///` (triple slash = empty authority + absolute path), strip `file://` to leave the absolute path (e.g., `file:///usr/src/main.cpp` → `/usr/src/main.cpp`)
- [ ] If URI matches `file://` followed by a non-`/` character (authority form, e.g., `file://host/path`), leave as-is — stripping would lose the hostname context
- [ ] If URI matches `file:` without `//` (bare scheme), strip `file:` prefix (e.g., `file:relative/path` → `relative/path`)
- [ ] If URI has no `file:` prefix (bare path), return unchanged
- [ ] Write tests for each case

### Notes
The conditional logic must distinguish between `file:///` (triple slash), `file://X` where X is not `/` (authority form), and `file:` (bare scheme). The simplest implementation checks `strings.HasPrefix(uri, "file:///")` first, then `strings.HasPrefix(uri, "file://")` (authority — leave as-is), then `strings.HasPrefix(uri, "file:")` (bare scheme).

## 1.4: mapSARIFLevel function

### Subtasks
- [ ] Implement `mapSARIFLevel(level string) Severity` with `strings.ToLower`
- [ ] Map: `"error"` → `SeverityError`, `"warning"` → `SeverityWarning`, `"note"` → `SeverityNote`, default → `SeverityWarning`
- [ ] Write tests for each level including unknown string
- [ ] Write test: `"ERROR"` maps to `SeverityError` (case-insensitivity exercised)
- [ ] Write test: `"none"` maps to `SeverityWarning` (SARIF informational level, treated same as unknown)

### Notes
Default is `SeverityWarning` (not `SeverityError` as in `mapClangSeverity`). See Design Decision 8 for rationale.

## 1.5: Malformed SARIF fallback

### Subtasks
- [ ] When `json.Unmarshal` fails in `parseSARIF`, return single fallback `Diagnostic{Severity: SeverityError, Message: "Failed to parse Clang SARIF output: <truncated>", Source: "clang"}`
- [ ] Reuse `truncateOutput` from `clang.go` (same package, unexported but accessible)
- [ ] Write test with malformed JSON that starts with `{`

### Notes
Follows the exact same pattern as the existing Clang JSON fallback in `clang.go:52-61`.

## 1.6: Structural verification

### Subtasks
- [ ] Run `go vet ./diagnostics/...`
- [ ] Run `go test -race ./diagnostics/...`
- [ ] Run `staticcheck ./diagnostics/...` if available

## Acceptance Criteria
- [ ] `diagnostics/sarif.go` exists with all types and functions
- [ ] `diagnostics/sarif_test.go` exists with tests covering every row of the design's SARIF unit test matrix
- [ ] No existing files modified — `clang.go`, `clang_test.go`, `parser.go`, `types.go` are untouched
- [ ] `go vet` and `go test -race` pass on `./diagnostics/...`
- [ ] Full `go test ./...` green (sarif.go additions are isolated; no other packages are touched)
