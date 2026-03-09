---
title: "Builder Infrastructure"
type: phase
plan: BuildProgressNotifications
phase: 1
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "CMakeBuilder supports ProgressFunc callback with io.MultiWriter tee, scanner goroutine, throttling, and sync.WaitGroup synchronization. Unit tests cover all progress paths."
tasks:
  - id: "1.1"
    title: "Add ProgressFunc type and SetProgressFunc to CMakeBuilder"
    status: complete
    verification: "CMakeBuilder has a progressFunc field and progressMinInterval field (defaulting to 250ms). SetProgressFunc(fn) sets the field, SetProgressFunc(nil) clears it. ProgressFunc type is exported from builder package. go vet passes."
  - id: "1.2"
    title: "Modify run() with io.MultiWriter tee, scanner goroutine, and throttle"
    status: complete
    depends_on: ["1.1"]
    verification: "When progressFunc is set: stderr is teed via io.MultiWriter to buffer + pipe; scanner goroutine matches [N/M] and calls progressFunc; throttle fires at progressMinInterval; final N==M line always sent; sync.WaitGroup ensures goroutine exits before run() returns; on scanner panic, goroutine recovers, drains pipe via io.Copy(io.Discard), and wg.Done() fires. When progressFunc is nil: run() uses existing cmd.Stderr=&stderr path with no pipe or goroutine. BuildResult.Stderr contains ALL stderr output in both modes."
  - id: "1.3"
    title: "Unit tests for progress path"
    status: complete
    depends_on: ["1.2"]
    verification: "Tests cover: callback receives correct (current, total) from [N/M] lines; callback not called when progressFunc is nil; rate limiting delivers fewer callbacks than total lines when progressMinInterval is small but non-zero; final [M/M] line always delivered; malformed lines (bare text, [abc/def]) produce no callback; BuildResult.Stderr contains full output including [N/M] lines when progress is enabled; panic in callback does not deadlock and stderr is still complete. go test -race passes on builder package."
---

# Phase 1: Builder Infrastructure

## Overview

Add the `ProgressFunc` callback infrastructure to `CMakeBuilder` and modify `run()` to support streaming stderr progress detection. This phase is entirely within the `builder/` package — no changes to `main.go` or the handler layer.

## 1.1: Add ProgressFunc type and SetProgressFunc to CMakeBuilder

### Subtasks
- [x] Define `ProgressFunc` type in `builder/cmake.go`: `type ProgressFunc func(current, total int, message string)`
- [x] Add `progressFunc ProgressFunc` field to `CMakeBuilder` struct
- [x] Add `progressMinInterval time.Duration` field to `CMakeBuilder` struct, defaulting to 250ms in `NewCMakeBuilder`
- [x] Implement `SetProgressFunc(fn ProgressFunc)` method on `CMakeBuilder`

### Notes
The `ProgressFunc` type is defined in the `builder` package because it's used by `CMakeBuilder.run()`. The `progressMinInterval` field (not a package-level constant) enables tests to override the throttle interval.

## 1.2: Modify run() with io.MultiWriter tee, scanner goroutine, and throttle

### Subtasks
- [x] Add `ninjaProgressRe` regex `^\[(\d+)/(\d+)\]` as a package-level compiled regexp in `cmake.go`
- [x] In `run()`, when `b.progressFunc != nil`: create `io.Pipe`, set `cmd.Stderr = io.MultiWriter(&stderr, pipeW)`, launch scanner goroutine with `wg.Add(1)`
- [x] Scanner goroutine: `defer wg.Done()`, `defer recover()` with `io.Copy(io.Discard, r)` drain-on-panic, `bufio.Scanner` loop matching `ninjaProgressRe`, throttle check (250ms default), final-line override (`N == M` always sent)
- [x] After `cmd.Run()` returns: `pipeW.Close()`, then `wg.Wait()` before returning `BuildResult`
- [x] When `b.progressFunc == nil`: existing `cmd.Stderr = &stderr` path unchanged

### Notes
The goroutine synchronization sequence is critical: `cmd.Run()` blocks until process exits AND all I/O copying completes → `pipeW.Close()` causes scanner to see EOF → `wg.Wait()` ensures scanner goroutine has exited → `run()` returns. This guarantees no data race with `handleBuild`'s deferred `SetProgressFunc(nil)`.

On scanner panic: recover, log, drain remaining pipe data via `io.Copy(io.Discard, r)` to avoid blocking `io.MultiWriter`. The outer `defer wg.Done()` fires regardless.

## 1.3: Unit tests for progress path

### Subtasks
- [x] Test: `run()` with progress callback — use a shell script that writes `[N/M]` lines to stderr, verify callback receives correct (current, total, message) values
- [x] Test: `run()` without progress callback — verify `BuildResult.Stderr` is populated, no panic
- [x] Test: rate limiting — set `progressMinInterval` to 1ms, feed rapid lines, verify callback count < total lines but final line is always delivered
- [x] Test: malformed lines — lines without `[N/M]` pattern produce no callback calls
- [x] Test: `BuildResult.Stderr` integrity — full stderr output preserved when progress is enabled (tee doesn't filter)
- [x] Test: panic in callback does not deadlock run(), stderr still complete
- [x] Test: stdout unaffected by progress tee
- [x] Test: non-zero exit code with progress callback
- [x] Run `go test -race ./builder/` to verify race-free goroutine synchronization

### Notes
Tests use real subprocess execution (shell scripts echoing formatted output to stderr) since `run()` creates a `cmd.Stderr` writer. The `progressMinInterval` field makes throttle tests deterministic without timing sensitivity.

## Acceptance Criteria

- [x] `ProgressFunc` type exported from `builder` package
- [x] `CMakeBuilder.SetProgressFunc` sets/clears the callback
- [x] `run()` with progress: tees stderr, scanner goroutine calls callback with correct values
- [x] `run()` without progress: identical to existing behavior
- [x] Throttle limits callback frequency; final line always sent
- [x] `sync.WaitGroup` prevents data race on goroutine exit
- [x] `BuildResult.Stderr` contains complete output in both modes
- [x] `go vet ./builder/` and `go test -race ./builder/` pass
