---
title: "Polish"
type: phase
plan: "BuildIntelligenceMCP"
phase: 5
status: planned
created: 2026-03-07
updated: 2026-03-07
deliverable: "Production-ready server with graceful shutdown, suggest_fix tool, Ninja progress parsing, and integration documentation"
tasks:
  - id: "5.1"
    title: "Implement graceful subprocess kill"
    status: planned
    verification: "On timeout/cancel: sends SIGTERM first, waits grace period (3s), then SIGKILL; Dirty flag set after kill; next build auto-cleans; build://health returns DIRTY string when Dirty flag is set via SIGTERM kill path; tested with a long-running mock subprocess that handles SIGTERM"
  - id: "5.2"
    title: "Implement suggest_fix tool"
    status: planned
    verification: "Accepts error_index param; reads source file at Diagnostic.File; returns +/-10 lines of context around Diagnostic.Line; handles file not found, line out of range, and index out of bounds for error list; response includes file path, line range, and source snippet"
  - id: "5.3"
    title: "Parse Ninja progress for files_compiled"
    status: planned
    verification: "Parses Ninja [N/M] progress lines from build stderr; extracts final N as files_compiled count; returns 0 when no progress lines found (cache hit or Make); Make builds parse command echo lines as fallback count"
  - id: "5.4"
    title: "Create testdata fixtures"
    status: planned
    verification: "testdata/cmake/ has a minimal CMakeLists.txt + main.cpp that compiles with clang/gcc; testdata/cmake-error/ has intentional compile error; testdata/make/ has a minimal Makefile + main.cpp; all fixtures verified to work on Linux"
  - id: "5.5"
    title: "Write user-facing README and integration guide"
    status: planned
    depends_on: ["5.1", "5.2", "5.3"]
    verification: "README.md exists at repo root with sections: installation (go install), .cpp-build-mcp.json config reference with all fields and defaults, Claude Desktop / .mcp.json integration examples, worked example of configure() -> build() -> get_errors() loop, tool reference table"
  - id: "5.6"
    title: "Final structural verification and cleanup"
    status: planned
    depends_on: ["5.1", "5.2", "5.3", "5.4", "5.5"]
    verification: "go vet ./... clean; go test -race ./... passes; staticcheck ./... clean (if available); go build -o cpp-build-mcp . produces working binary; no unused imports or dead code"
---

# Phase 5: Polish

## Overview

Robustness improvements, the `suggest_fix` stretch tool, Ninja progress parsing for `files_compiled`, test fixtures, and final cleanup. After this phase, the server is production-ready.

## 5.1: Implement graceful subprocess kill

### Subtasks
- [ ] Use `cmd.Cancel` (Go 1.20+) to set custom cancel function
- [ ] Cancel function: send `SIGTERM`, wait 3 seconds, then `SIGKILL`
- [ ] Set `BuildState.Dirty = true` after any kill
- [ ] Verify next build calls `--clean-first` (CMake) or `make clean` (Make)
- [ ] Test with a subprocess that sleeps and verify SIGTERM is sent before SIGKILL

## 5.2: Implement suggest_fix tool

### Subtasks
- [ ] Register `suggest_fix` MCP tool with `error_index` int param
- [ ] Handler reads `state.Errors()`, indexes into it
- [ ] Read source file at `Diagnostic.File`, extract lines `[line-10, line+10]` (clamped to file bounds)
- [ ] Return `{file, start_line, end_line, source, diagnostic}` where `source` is the snippet text and `diagnostic` is the error
- [ ] Handle: index out of bounds, file not found, file unreadable
- [ ] Tests: valid index, out of bounds, file not found, error near start/end of file

## 5.3: Parse Ninja progress for files_compiled

### Subtasks
- [ ] After build completes, scan `BuildResult.Stderr` for Ninja `[N/M]` progress lines
- [ ] Extract the highest `N` seen as `files_compiled`
- [ ] For Make: count lines that look like compiler invocations (`gcc`, `g++`, `clang`, `clang++`, `cl.exe` as first token)
- [ ] Populate the `files_compiled` field in build tool response (field already present since Phase 2 as `0`)
- [ ] Tests: Ninja output with progress lines, no progress lines, Make output with compiler invocations

## 5.4: Create testdata fixtures

### Subtasks
- [ ] `testdata/cmake/CMakeLists.txt` + `testdata/cmake/main.cpp` — minimal valid project
- [ ] `testdata/cmake-error/CMakeLists.txt` + `testdata/cmake-error/main.cpp` — intentional compile error
- [ ] `testdata/make/Makefile` + `testdata/make/main.cpp` — minimal valid Makefile project
- [ ] Verify all fixtures compile on Linux with available toolchains
- [ ] Add `testdata/compile_commands.json` sample for graph tests
- [ ] Add `testdata/clang_output.json`, `testdata/gcc_output.json` samples for parser tests

## 5.5: Write user-facing README and integration guide

### Subtasks
- [ ] Create `README.md` at repo root
- [ ] Installation section: `go install` command, binary name
- [ ] Configuration section: `.cpp-build-mcp.json` reference with all fields, types, defaults, and env var overrides
- [ ] Integration section: Claude Desktop `claude_desktop_config.json` example, `.mcp.json` example
- [ ] Usage section: worked example of `configure()` -> `build()` -> `get_errors()` -> fix -> `build()` loop
- [ ] Tool reference table: all 8 tools with params and response shapes

## 5.6: Final structural verification and cleanup

### Subtasks
- [ ] Run `go vet ./...`
- [ ] Run `go test -race ./...`
- [ ] Run `staticcheck ./...` if available
- [ ] Build binary: `go build -o cpp-build-mcp .`
- [ ] Verify no unused imports, dead code, or TODO markers left
- [ ] Verify all tool responses stay under ~500 tokens in happy path

## Acceptance Criteria
- [ ] Graceful kill sends SIGTERM before SIGKILL and sets Dirty flag
- [ ] `suggest_fix` returns source context around errors without reading the whole file
- [ ] `build()` response includes `files_compiled` count
- [ ] All testdata fixtures compile on Linux
- [ ] `go vet`, `go test -race`, and `staticcheck` all pass
- [ ] Binary builds and starts cleanly
