---
title: "Foundation"
type: phase
plan: "BuildIntelligenceMCP"
phase: 1
status: complete
created: 2026-03-07
updated: 2026-03-07
deliverable: "Compilable Go module with config loading, state management, and CMake builder that can execute builds and capture output"
tasks:
  - id: "1.1"
    title: "Scaffold Go module and directory structure"
    status: complete
    verification: "go build ./... succeeds; go mod tidy produces no changes; mcp-go dependency resolves"
  - id: "1.2"
    title: "Implement config loading"
    status: complete
    depends_on: ["1.1"]
    verification: "Loads .cpp-build-mcp.json with all fields; falls back to defaults when file missing; env vars override JSON values; invalid JSON returns descriptive error"
  - id: "1.3"
    title: "Implement StateStore with phase machine"
    status: complete
    depends_on: ["1.1"]
    verification: "Phase transitions Unconfigured->Configured->Built enforced; build() rejected when unconfigured; BuildInProgress guard prevents concurrent builds; Dirty flag set/cleared correctly; LastSuccessfulBuildTime only updated on exit code 0; concurrent goroutine reads pass race detector"
  - id: "1.4"
    title: "Implement Builder interface and CMake backend"
    status: complete
    depends_on: ["1.2"]
    verification: "Builder interface defined with Configure/Build/Clean; CMakeBuilder.Configure runs cmake with injected flags (-DCMAKE_EXPORT_COMPILE_COMMANDS=ON, diagnostic flags only for toolchain=clang — auto/unknown skip injection until Phase 4 auto-detection); CMakeBuilder.Build runs cmake --build and captures stdout/stderr separately; DiagnosticSerialBuild=true forces -j1; CMakeBuilder.Clean runs cmake --build --target clean; context timeout cancels subprocess; BuildResult contains exit code, stdout, stderr, duration"
  - id: "1.5"
    title: "Structural verification"
    status: complete
    depends_on: ["1.2", "1.3", "1.4"]
    verification: "go vet ./... clean; go test -race ./... passes; all tests pass"
---

# Phase 1: Foundation

## Overview

Set up the Go module, directory layout, and the three foundational packages: config, state, and builder. After this phase, the project compiles, has the CMake builder wired up, and the state machine enforces lifecycle invariants. No MCP server yet — that comes in Phase 2.

## 1.1: Scaffold Go module and directory structure

### Subtasks
- [x] `go mod init github.com/danweinerdev/cpp-build-mcp`
- [x] `go get github.com/mark3labs/mcp-go`
- [x] Create directory skeleton: `builder/`, `diagnostics/`, `graph/`, `state/`, `config/`
- [x] Create empty `main.go` with `package main` and `func main()`
- [x] Verify `go build ./...` succeeds

## 1.2: Implement config loading

### Subtasks
- [x] Define `Config` struct in `config/config.go` with fields: `BuildDir`, `SourceDir`, `Toolchain`, `Generator`, `CMakeArgs`, `BuildTimeout`, `InjectDiagnosticFlags`, `DiagnosticSerialBuild`
- [x] Load from `.cpp-build-mcp.json` via `encoding/json`
- [x] Apply defaults (BuildDir=`build`, SourceDir=`.`, Toolchain=`auto`, Generator=`ninja`, BuildTimeout=`5m`, InjectDiagnosticFlags=`true`)
- [x] Override with env vars: `CPP_BUILD_MCP_BUILD_DIR`, `CPP_BUILD_MCP_SOURCE_DIR`, `CPP_BUILD_MCP_TOOLCHAIN`, `CPP_BUILD_MCP_GENERATOR`, `CPP_BUILD_MCP_BUILD_TIMEOUT`
- [x] Write table-driven tests: valid JSON, missing file, env overrides, invalid JSON

## 1.3: Implement StateStore with phase machine

### Subtasks
- [x] Define `Phase` enum, `BuildState` struct, `Store` struct in `state/store.go`
- [x] Implement methods: `SetConfigured()`, `StartBuild()` (returns error if unconfigured or in-progress), `FinishBuild(result, diagnostics)`, `Errors()`, `Warnings()`, `Health() string`
- [x] `StartBuild` uses two-phase lock: set `BuildInProgress=true` under write lock, return
- [x] `FinishBuild` acquires write lock, updates state, clears `BuildInProgress`
- [x] On exit code 0: update `LastSuccessfulBuildTime`; on timeout: set `Dirty=true`
- [x] Write tests: phase transitions, concurrent read/write with goroutines, race detector

## 1.4: Implement Builder interface and CMake backend

### Subtasks
- [x] Define `Builder` interface and `BuildResult` struct in `builder/builder.go`
- [x] Define `NewBuilder(cfg *config.Config) (Builder, error)` factory function
- [x] Implement `CMakeBuilder` in `builder/cmake.go`
- [x] `Configure`: runs `cmake -S <source> -B <build> -G Ninja` + injected args
- [x] `Build`: runs `cmake --build <build_dir>` with optional `--target` and `-j` flags; DiagnosticSerialBuild forces -j1
- [x] `Clean`: runs `cmake --build <build_dir> --target clean`
- [x] When `Dirty` flag is signaled, `Build` prepends `--clean-first`
- [x] Capture stdout and stderr into separate `bytes.Buffer`
- [x] Integration test with `testdata/` CMake project (skip via `os.LookPath`)

### Notes
- The factory function checks config.Generator: "ninja" -> CMakeBuilder (Phase 1), "make" -> MakeBuilder (Phase 4)
- For Phase 1, unknown/make generator returns an error pointing to Phase 4

## 1.5: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [x] Fix any issues

## Acceptance Criteria
- [x] `go build ./...` succeeds with no warnings from `go vet`
- [x] Config loads from file, applies defaults, respects env var overrides
- [x] StateStore enforces Unconfigured -> Configured -> Built transitions
- [x] StateStore passes race detector under concurrent access
- [x] CMakeBuilder executes configure/build/clean against a real CMake project (when cmake available)
- [x] All tests pass with `-race` flag
