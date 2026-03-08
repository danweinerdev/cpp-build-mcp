---
title: "Broad Compiler Compatibility"
type: phase
plan: "BuildIntelligenceMCP"
phase: 4
status: planned
created: 2026-03-07
updated: 2026-03-07
deliverable: "GCC and MSVC diagnostic parsing, Make builder backend, and toolchain auto-detection"
tasks:
  - id: "4.1"
    title: "Implement GCC JSON diagnostic parser"
    status: planned
    verification: "Parses GCC JSON array from stdout; maps top-level elements to Diagnostic; flattens children with kind=note into separate Diagnostics with RelatedTo field; caps template expansion depth at 3; tested with real GCC 10+ JSON output including nested notes and template errors"
  - id: "4.2"
    title: "Implement regex fallback parser"
    status: planned
    verification: "Parses MSVC pattern 'file(line,col): error C1234: message'; parses legacy GCC pattern 'file:line:col: error: message'; extracts severity, file, line, column, message, code; multi-line errors handled (linker errors); no matches returns empty slice; tested with real MSVC and GCC sample output"
  - id: "4.3"
    title: "Update diagnostic dispatcher for all toolchains"
    status: planned
    depends_on: ["4.1", "4.2"]
    verification: "toolchain=gcc routes to GCC JSON parser; toolchain=gcc-legacy routes to regex parser; toolchain=msvc routes to regex parser; toolchain=auto detects compiler from build output or config; unknown toolchain falls back to regex"
  - id: "4.4"
    title: "Implement Make builder backend"
    status: planned
    verification: "MakeBuilder.Configure is a no-op that sets phase to Configured; MakeBuilder.Build runs 'make' with targets and -j flag (DiagnosticSerialBuild=true forces -j1); injects -fdiagnostics-format=json via CFLAGS/CXXFLAGS env vars only when InjectDiagnosticFlags=true; MakeBuilder.Clean runs 'make clean'; Dirty flag triggers 'make clean' before build; integration test with testdata/ Makefile project (skip if make not found)"
  - id: "4.5"
    title: "Implement toolchain auto-detection"
    status: planned
    depends_on: ["4.3", "4.4"]
    verification: "When toolchain=auto: checks compile_commands.json for compiler path; detects clang/gcc/msvc from compiler binary name; detects gcc version >= 10 for JSON support (returns gcc-legacy for < 10); when detected toolchain is gcc-legacy, sets InjectDiagnosticFlags=false to prevent injecting unsupported flags and logs warning via slog; falls back to regex if detection fails; factory selects correct builder based on generator field"
  - id: "4.6"
    title: "Structural verification"
    status: planned
    depends_on: ["4.1", "4.2", "4.3", "4.4", "4.5"]
    verification: "go vet ./... clean; go test -race ./... passes"
---

# Phase 4: Broad Compiler Compatibility

## Overview

Extend diagnostic parsing to GCC and MSVC, add the Make builder backend, and implement toolchain auto-detection. After this phase, the server works with any common C++ toolchain and build system.

## 4.1: Implement GCC JSON diagnostic parser

### Subtasks
- [ ] Create `diagnostics/gcc.go` implementing `DiagnosticParser`
- [ ] Parse GCC JSON from **stdout** (same stream as Clang)
- [ ] GCC JSON schema: array of objects with `kind`, `message`, `locations[]`, `children[]`
- [ ] Map: `locations[0].caret.file` -> `File`, `.line` -> `Line`, `.column` -> `Column`; `kind` -> `Severity`; `message` -> `Message`; `option` -> `Code`
- [ ] Flatten `children` where `kind: "note"`: create separate `Diagnostic` with `RelatedTo: "parent_file:parent_line"`
- [ ] Cap recursion depth at 3 for template expansion children
- [ ] Set `Source: "gcc"` on all entries
- [ ] Table-driven tests with real GCC JSON samples

### Notes
- GCC JSON differs from Clang: `locations` is an array with `caret`/`start`/`finish` objects, `children` can nest arbitrarily deep for template errors, `option` field is the warning flag name

## 4.2: Implement regex fallback parser

### Subtasks
- [ ] Create `diagnostics/regex.go` implementing `DiagnosticParser`
- [ ] Reads from **stderr** (human-readable output)
- [ ] MSVC regex: `^(.+)\((\d+),?(\d+)?\)\s*:\s*(error|warning|note)\s+(C\d+)\s*:\s*(.+)$`
- [ ] GCC/Clang regex: `^(.+):(\d+):(\d+):\s*(error|warning|note):\s*(.+)$`
- [ ] Try MSVC pattern first, then GCC pattern
- [ ] Set `Source` based on pattern matched
- [ ] Tests: MSVC output, legacy GCC output, mixed output, linker errors, no matches

## 4.3: Update diagnostic dispatcher for all toolchains

### Subtasks
- [ ] Update `NewParser` in `diagnostics/parser.go`
- [ ] `"gcc"` -> `GCCParser{}` (was placeholder)
- [ ] `"gcc-legacy"` -> `RegexParser{}` (GCC < 10, no JSON support)
- [ ] `"msvc"` -> `RegexParser{}`
- [ ] `""` or unknown -> `RegexParser{}` (was stub)
- [ ] Add `"auto"` handling that defers to auto-detection (task 4.5)

## 4.4: Implement Make builder backend

### Subtasks
- [ ] Create `builder/make.go` implementing `Builder`
- [ ] `Configure`: no-op, returns `BuildResult{ExitCode: 0}`, caller sets `Phase = Configured`
- [ ] `Build`: runs `make` with `-j<jobs>` flag and target args; when `cfg.DiagnosticSerialBuild` is true, override jobs to 1; sets `CFLAGS` and `CXXFLAGS` env vars with `-fdiagnostics-format=json` appended only when `cfg.InjectDiagnosticFlags` is true; captures stdout/stderr; uses `exec.CommandContext`
- [ ] `Clean`: runs `make clean`
- [ ] When `Dirty`: runs `make clean` first, then `make`
- [ ] Update factory `NewBuilder` to return `MakeBuilder` for `generator: "make"`
- [ ] Integration test with `testdata/Makefile` project (skip if make not found)

## 4.5: Implement toolchain auto-detection

### Subtasks
- [ ] Create `builder/detect.go` with `DetectToolchain(cfg *config.Config) string`
- [ ] Check `compile_commands.json` first entry for compiler path
- [ ] Match binary name: contains "clang" -> `"clang"`, contains "gcc"/"g++" -> `"gcc"`, contains "cl.exe"/"cl" -> `"msvc"`
- [ ] For GCC: probe `gcc --version` and parse major version; >= 10 -> `"gcc"`, < 10 -> `"gcc-legacy"` (routes to regex)
- [ ] If no compile_commands.json: try `$CC --version` or `cc --version`
- [ ] When detected toolchain is `"gcc-legacy"`: set `cfg.InjectDiagnosticFlags = false` to prevent injecting `-fdiagnostics-format=json` into a compiler that doesn't support it; log warning via `slog.Warn`
- [ ] Fallback: `"unknown"` (routes to regex parser)
- [ ] Tests: mock compile_commands entries, version string parsing, gcc-legacy disables flag injection

## 4.6: Structural verification

### Subtasks
- [ ] Run `go vet ./...`
- [ ] Run `go test -race ./...`
- [ ] Fix any issues

## Acceptance Criteria
- [ ] GCC JSON parser correctly handles nested children and template depth cap
- [ ] Regex parser handles both MSVC and legacy GCC output formats
- [ ] Make builder injects diagnostic flags via env vars and executes builds
- [ ] Auto-detection identifies Clang, GCC (with version check), and MSVC from compile_commands.json
- [ ] All tests pass with `-race` flag
