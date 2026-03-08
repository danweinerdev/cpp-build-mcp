---
title: "Diagnostics and Core Loop"
type: phase
plan: "BuildIntelligenceMCP"
phase: 2
status: complete
created: 2026-03-07
updated: 2026-03-07
completed: 2026-03-07
deliverable: "Working MCP server with build() and get_errors() tools over stdio — the core AI build loop"
tasks:
  - id: "2.1"
    title: "Implement shared Diagnostic types"
    status: complete
    verification: "Diagnostic struct serializes to JSON matching design spec; Severity constants cover error/warning/note; RelatedTo field omitted when empty"
  - id: "2.2"
    title: "Implement Clang JSON diagnostic parser"
    status: complete
    depends_on: ["2.1"]
    verification: "Parses valid Clang JSON array from stdout into []Diagnostic with correct file/line/col/severity/message/code; handles interleaved arrays (splits on [...] boundaries); empty stdout returns empty slice; malformed JSON returns single fallback Diagnostic with raw message; tested with real Clang output samples for unused variable, undefined reference, and template errors"
  - id: "2.3"
    title: "Implement diagnostic dispatcher"
    status: complete
    depends_on: ["2.2"]
    verification: "Routes to Clang parser for toolchain=clang; routes to regex fallback for unknown toolchain; accepts both stdout and stderr, passes correct stream per parser; logs parse failures via slog"
  - id: "2.4"
    title: "Wire MCP server with build and get_errors tools"
    status: complete
    depends_on: ["2.3"]
    verification: "MCP server starts via stdio; build tool accepts targets and jobs params, calls builder, parses diagnostics, updates state, returns {exit_code, error_count, warning_count, duration_ms, files_compiled: 0} (files_compiled is always 0 until Phase 5 adds parsing); get_errors returns {errors: []{file, line, col, severity, message, code}} from state; build when unconfigured returns tool error; build while in-progress returns tool error"
  - id: "2.5"
    title: "Wire build_health resource"
    status: complete
    depends_on: ["2.4"]
    verification: "Resource registered at build://health; returns correct string for each phase state: UNCONFIGURED, READY, OK (with counts), FAIL (with counts), DIRTY"
  - id: "2.6"
    title: "End-to-end test with mock builder"
    status: complete
    depends_on: ["2.4", "2.5"]
    verification: "io.Pipe-based test sends JSON-RPC build tool call, receives structured response; sends get_errors after failed build, receives diagnostic array; sends build when unconfigured, receives error; reads build://health resource, receives correct state string"
  - id: "2.7"
    title: "Structural verification"
    status: complete
    depends_on: ["2.2", "2.3", "2.4", "2.5", "2.6"]
    verification: "go vet ./... clean; go test -race ./... passes"
---

# Phase 2: Diagnostics and Core Loop

## Overview

Implement the critical path: Clang diagnostic parsing and the MCP server with `build()` + `get_errors()` tools. After this phase, an AI agent can connect to the server over stdio and run the core build-fix loop against a Clang/CMake project.

## 2.1: Implement shared Diagnostic types

### Subtasks
- [x] Create `diagnostics/types.go` with `Severity`, `Diagnostic`, `DiagnosticParser` interface
- [x] Verify JSON serialization matches design spec (field names, omitempty behavior)

## 2.2: Implement Clang JSON diagnostic parser

### Subtasks
- [x] Create `diagnostics/clang.go` implementing `DiagnosticParser`
- [x] Parse from **stdout** (Clang writes JSON diagnostics to stdout, not stderr)
- [x] Handle top-level JSON array: `[{"..."}]`
- [x] Split interleaved arrays: when parallel Ninja merges stdout from multiple TUs, multiple `[...]` arrays may be concatenated — scan for `][` boundaries and parse each independently
- [x] Map Clang JSON fields to `Diagnostic`: `file` -> `File`, `line` -> `Line`, `column` -> `Column`, `severity` -> `Severity`, `message` -> `Message`, `option` -> `Code` (e.g., `-Wunused-variable`)
- [x] Set `Source: "clang"` on all entries
- [x] On parse failure: log via `slog.Warn`, return single `Diagnostic{Severity: "error", Message: "Failed to parse Clang output: <truncated>"}`
- [x] Write table-driven tests with real Clang JSON samples

### Notes
- Test data should include: simple warning, error with column info, template instantiation error, empty output, malformed JSON, two concatenated arrays

## 2.3: Implement diagnostic dispatcher

### Subtasks
- [x] Create `diagnostics/parser.go` with `NewParser(toolchain string) DiagnosticParser`
- [x] For `"clang"` -> `ClangParser{}`
- [x] For `"gcc"` -> placeholder that falls back to regex (GCC parser built in Phase 4)
- [x] For `"msvc"` or `""` or unknown -> `RegexParser{}` (regex parser built in Phase 4, stub for now that returns empty)
- [x] `Parse(stdout, stderr)` delegates to the selected parser

## 2.4: Wire MCP server with build and get_errors tools

### Subtasks
- [x] In `main.go`: load config, create builder, create state store
- [x] Create `server.NewMCPServer("cpp-build-mcp", "0.1.0", server.WithToolCapabilities(true), server.WithResourceCapabilities(false, true))`
- [x] Register `build` tool: `mcp.NewTool("build", ...)` with optional `targets` (string array) and `jobs` (number) params
- [x] Build handler: check state (configured? in-progress?), call `builder.Build()`, parse diagnostics, update state, return JSON summary with `files_compiled: 0` placeholder (actual parsing added in Phase 5 task 5.3)
- [x] Register `get_errors` tool: no params, reads from state, returns `{errors: [...]}`
- [x] Cap `get_errors` at 20 diagnostics (design spec: deduplicate template noise)
- [x] Call `server.ServeStdio(s)`

## 2.5: Wire build_health resource

### Subtasks
- [x] Register `mcp.NewResource("build://health", "Build Health", ...)` with handler
- [x] Handler reads state phase and returns appropriate one-liner string
- [x] Test all state variants

## 2.6: End-to-end test with mock builder

### Subtasks
- [x] Create `builder/mock.go` implementing `Builder` with configurable `BuildResult` returns
- [x] Create E2E test using `io.Pipe` pairs for stdin/stdout
- [x] Start MCP server in goroutine with piped I/O
- [x] Write JSON-RPC requests, read responses, assert structure
- [x] Test: successful build, failed build with errors, unconfigured build, in-progress guard, health resource

## 2.7: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [x] Fix any issues

## Acceptance Criteria
- [x] Clang JSON parser correctly parses real compiler output samples
- [x] MCP server starts over stdio and responds to tool calls
- [x] `build()` -> `get_errors()` loop works end-to-end with mock builder
- [x] `build_health` resource returns correct state for all phases
- [x] State guards prevent builds when unconfigured or already in progress
- [x] All tests pass with `-race` flag
