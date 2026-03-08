---
title: "Full Toolset"
type: phase
plan: "BuildIntelligenceMCP"
phase: 3
status: planned
created: 2026-03-07
updated: 2026-03-07
deliverable: "All 7 MCP tools and the build_health resource fully functional for CMake+Clang projects"
tasks:
  - id: "3.1"
    title: "Implement get_warnings tool with filter"
    status: planned
    verification: "Returns warnings from state; filter by code prefix ('-Wunused' matches '-Wunused-variable'); filter by file substring ('src/core' matches 'src/core/foo.cpp'); case-insensitive OR match on code and file; no filter returns all warnings; empty warnings returns empty array"
  - id: "3.2"
    title: "Implement configure tool"
    status: planned
    verification: "Calls builder.Configure with cmake_args param; splits CMake output on 'CMake Error'/'CMake Warning' prefixes into messages array; error_count derived from prefix count; updates state phase to Configured on success; returns {success, error_count, messages}"
  - id: "3.3"
    title: "Implement clean tool"
    status: planned
    verification: "Calls builder.Clean with optional targets; returns {success, message}; resets state phase to Configured after clean; clean when Phase=Configured (no prior build) returns success without error and leaves Phase at Configured"
  - id: "3.4"
    title: "Implement get_changed_files tool"
    status: planned
    verification: "Git method: runs git diff --name-only against LastSuccessfulBuildTime (or HEAD if no prior build); mtime method: walks source_dir excluding build_dir, compares against LastSuccessfulBuildTime; auto-detects git vs mtime; response includes method field; git failure falls back to mtime; no prior successful build returns all source files"
  - id: "3.5"
    title: "Implement get_build_graph tool"
    status: planned
    verification: "Reads compile_commands.json from build_dir; returns {available: true, file_count, translation_units, include_dirs}; file not found returns {available: false, reason, file_count} with source count from directory walk; Make projects return degraded response; handles empty and malformed compile_commands.json"
  - id: "3.6"
    title: "Structural verification"
    status: planned
    depends_on: ["3.1", "3.2", "3.3", "3.4", "3.5"]
    verification: "go vet ./... clean; go test -race ./... passes"
---

# Phase 3: Full Toolset

## Overview

Implement the remaining five MCP tools: `get_warnings`, `configure`, `clean`, `get_changed_files`, and `get_build_graph`. After this phase, all seven tools are functional for CMake+Clang projects.

## 3.1: Implement get_warnings tool with filter

### Subtasks
- [ ] Register `get_warnings` tool with optional `filter` string param
- [ ] Handler reads `state.Warnings()`, applies filter if present
- [ ] Filter logic: case-insensitive substring match against `Diagnostic.Code` OR `Diagnostic.File`
- [ ] Return `{warnings: [...], count: N}`
- [ ] Tests: no filter, code filter, file filter, no matches, empty state

## 3.2: Implement configure tool

### Subtasks
- [ ] Register `configure` tool with optional `cmake_args` string array param
- [ ] Handler calls `builder.Configure(ctx, args)`
- [ ] Parse CMake stdout+stderr: split on lines starting with `CMake Error` or `CMake Warning` into message groups
- [ ] Count `CMake Error` prefixes for `error_count`
- [ ] Update state: `SetConfigured()` on success (exit code 0), leave unconfigured on failure
- [ ] Return `{success, error_count, messages}`
- [ ] Tests: successful configure, failed configure with CMake errors, already configured (reconfigure)

## 3.3: Implement clean tool

### Subtasks
- [ ] Register `clean` tool with optional `targets` string array param
- [ ] Handler calls `builder.Clean(ctx, targets)`
- [ ] On success: set state phase back to `Configured` (no longer `Built`)
- [ ] Return `{success: true, message: "Clean complete"}`
- [ ] Tests: full clean, targeted clean, clean when not built (Phase=Configured — should succeed and stay Configured)

## 3.4: Implement get_changed_files tool

### Subtasks
- [ ] Create `changes/detector.go` with `DetectChanges(cfg, lastSuccessfulBuild) ([]string, string, error)` (note: `changes/` is a new package not in the design component diagram — it's small enough to warrant its own package rather than forcing into `graph/` or `main.go`)
- [ ] Git detection: `os.LookPath("git")` + check `.git` exists, run `git diff --name-only` with timestamp
- [ ] Mtime detection: walk `source_dir` (exclude `build_dir`), filter by C/C++ extensions, compare mtime > `LastSuccessfulBuildTime`
- [ ] Auto-select: try git first, fall back to mtime on failure
- [ ] Return `{files, count, method}`
- [ ] Tests: mock git output, mtime with test files, git unavailable fallback, no prior build (all files)

## 3.5: Implement get_build_graph tool

### Subtasks
- [ ] Create `graph/compile_commands.go` with `ReadSummary(buildDir, sourceDir string) (*GraphSummary, error)`
- [ ] Parse `compile_commands.json`: extract unique source files, compiler flags, include dirs (from `-I` flags)
- [ ] Return compact summary: file count, deduped flags, deduped include dirs
- [ ] File not found: return degraded response with source file count from `sourceDir` walk
- [ ] Register `get_build_graph` MCP tool wired to this
- [ ] Tests: valid compile_commands.json, missing file, empty file, malformed JSON

## 3.6: Structural verification

### Subtasks
- [ ] Run `go vet ./...`
- [ ] Run `go test -race ./...`
- [ ] Fix any issues

## Acceptance Criteria
- [ ] All 7 MCP tools respond correctly over stdio
- [ ] `get_warnings` filter matches by code and file path
- [ ] `configure` parses CMake output into structured messages
- [ ] `get_changed_files` uses git when available, mtime as fallback
- [ ] `get_build_graph` reads compile_commands.json and degrades gracefully for Make
- [ ] All tests pass with `-race` flag
