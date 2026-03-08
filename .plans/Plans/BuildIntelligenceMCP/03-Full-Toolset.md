---
title: "Full Toolset"
type: phase
plan: "BuildIntelligenceMCP"
phase: 3
status: complete
created: 2026-03-07
updated: 2026-03-07
completed: 2026-03-07
deliverable: "All 7 MCP tools and the build_health resource fully functional for CMake+Clang projects"
tasks:
  - id: "3.1"
    title: "Implement get_warnings tool with filter"
    status: complete
    verification: "Returns warnings from state; filter by code prefix ('-Wunused' matches '-Wunused-variable'); filter by file substring ('src/core' matches 'src/core/foo.cpp'); case-insensitive OR match on code and file; no filter returns all warnings; empty warnings returns empty array"
  - id: "3.2"
    title: "Implement configure tool"
    status: complete
    verification: "Calls builder.Configure with cmake_args param; splits CMake output on 'CMake Error'/'CMake Warning' prefixes into messages array; error_count derived from prefix count; updates state phase to Configured on success; returns {success, error_count, messages}"
  - id: "3.3"
    title: "Implement clean tool"
    status: complete
    verification: "Calls builder.Clean with optional targets; returns {success, message}; resets state phase to Configured after clean; clean when Phase=Configured (no prior build) returns success without error and leaves Phase at Configured"
  - id: "3.4"
    title: "Implement get_changed_files tool"
    status: complete
    verification: "Git method: runs git diff --name-only against LastSuccessfulBuildTime (or HEAD if no prior build); mtime method: walks source_dir excluding build_dir, compares against LastSuccessfulBuildTime; auto-detects git vs mtime; response includes method field; git failure falls back to mtime; no prior successful build returns all source files"
  - id: "3.5"
    title: "Implement get_build_graph tool"
    status: complete
    verification: "Reads compile_commands.json from build_dir; returns {available: true, file_count, translation_units, include_dirs}; file not found returns {available: false, reason, file_count} with source count from directory walk; Make projects return degraded response; handles empty and malformed compile_commands.json"
  - id: "3.6"
    title: "Structural verification"
    status: complete
    depends_on: ["3.1", "3.2", "3.3", "3.4", "3.5"]
    verification: "go vet ./... clean; go test -race ./... passes"
---

# Phase 3: Full Toolset

## Overview

Implement the remaining five MCP tools: `get_warnings`, `configure`, `clean`, `get_changed_files`, and `get_build_graph`. After this phase, all seven tools are functional for CMake+Clang projects.

## 3.1: Implement get_warnings tool with filter

### Subtasks
- [x] Register `get_warnings` tool with optional `filter` string param
- [x] Handler reads `state.Warnings()`, applies filter if present
- [x] Filter logic: case-insensitive substring match against `Diagnostic.Code` OR `Diagnostic.File`
- [x] Return `{warnings: [...], count: N}`
- [x] Tests: no filter, code filter, file filter, no matches, empty state

## 3.2: Implement configure tool

### Subtasks
- [x] Register `configure` tool with optional `cmake_args` string array param
- [x] Handler calls `builder.Configure(ctx, args)`
- [x] Parse CMake stdout+stderr: split on lines starting with `CMake Error` or `CMake Warning` into message groups
- [x] Count `CMake Error` prefixes for `error_count`
- [x] Update state: `SetConfigured()` on success (exit code 0), leave unconfigured on failure
- [x] Return `{success, error_count, messages}`
- [x] Tests: successful configure, failed configure with CMake errors, already configured (reconfigure)

## 3.3: Implement clean tool

### Subtasks
- [x] Register `clean` tool with optional `targets` string array param
- [x] Handler calls `builder.Clean(ctx, targets)`
- [x] On success: set state phase back to `Configured` (no longer `Built`)
- [x] Return `{success: true, message: "Clean complete"}`
- [x] Tests: full clean, targeted clean, clean when not built (Phase=Configured — should succeed and stay Configured)

## 3.4: Implement get_changed_files tool

### Subtasks
- [x] Create `changes/detector.go` with `DetectChanges(cfg, lastSuccessfulBuild) ([]string, string, error)`
- [x] Git detection: `os.LookPath("git")` + check `.git` exists, run `git diff --name-only` with timestamp
- [x] Mtime detection: walk `source_dir` (exclude `build_dir`), filter by C/C++ extensions, compare mtime > `LastSuccessfulBuildTime`
- [x] Auto-select: try git first, fall back to mtime on failure
- [x] Return `{files, count, method}`
- [x] Tests: mock git output, mtime with test files, git unavailable fallback, no prior build (all files)

## 3.5: Implement get_build_graph tool

### Subtasks
- [x] Create `graph/compile_commands.go` with `ReadSummary(buildDir, sourceDir string) (*GraphSummary, error)`
- [x] Parse `compile_commands.json`: extract unique source files, compiler flags, include dirs (from `-I` flags)
- [x] Return compact summary: file count, deduped flags, deduped include dirs
- [x] File not found: return degraded response with source file count from `sourceDir` walk
- [x] Register `get_build_graph` MCP tool wired to this
- [x] Tests: valid compile_commands.json, missing file, empty file, malformed JSON

## 3.6: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [x] Fix any issues

## Acceptance Criteria
- [x] All 7 MCP tools respond correctly over stdio
- [x] `get_warnings` filter matches by code and file path
- [x] `configure` parses CMake output into structured messages
- [x] `get_changed_files` uses git when available, mtime as fallback
- [x] `get_build_graph` reads compile_commands.json and degrades gracefully for Make
- [x] All tests pass with `-race` flag
