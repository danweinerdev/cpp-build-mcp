---
title: "Handler Integration"
type: phase
plan: BuildProgressNotifications
phase: 2
status: complete
created: 2026-03-08
updated: 2026-03-08
deliverable: "handleBuild wires progress notifications to MCP protocol. E2E tests verify notifications flow through the JSON-RPC transport. Full structural verification passes."
tasks:
  - id: "2.1"
    title: "Add progressSetter interface and notification wiring in handleBuild"
    status: complete
    verification: "progressSetter interface defined with SetProgressFunc method. handleBuild extracts progressToken from req.Params.Meta.ProgressToken. When token is non-nil and server.ServerFromContext(ctx) is non-nil, closure is constructed per design spec (verbatim progressToken re-embedding, float64 progress/total, config-prefixed message in multi-config mode). SetProgressFunc called only after StartBuild() succeeds. defer SetProgressFunc(nil) immediately follows. When token is nil or server is nil, no progress callback is set."
  - id: "2.2"
    title: "Integration and E2E tests for progress notifications"
    status: complete
    depends_on: ["2.1"]
    verification: "E2E test with fakeBuilder emitting [N/M] stderr lines: JSON-RPC notifications/progress messages appear in transport output with correct progressToken, progress (float64), total (float64), and message fields. Without progressToken: no notifications/progress messages in output. Multi-config test: message field contains [configName] prefix. go test -race passes."
  - id: "2.3"
    title: "Structural verification"
    status: complete
    depends_on: ["2.2"]
    verification: "go vet ./... reports no issues. go test -race ./... passes all packages (7 packages). staticcheck ./... reports no issues."
---

# Phase 2: Handler Integration

## Overview

Wire the builder-level progress infrastructure (from Phase 1) to the MCP protocol layer. Add the `progressSetter` type assertion in `handleBuild`, construct the notification closure, and verify the full flow with E2E tests.

## 2.1: Add progressSetter interface and notification wiring in handleBuild

### Subtasks
- [x] Define `progressSetter` interface in `main.go`: `type progressSetter interface { SetProgressFunc(builder.ProgressFunc) }`
- [x] In `handleBuild`, after `StartBuild()` succeeds: extract `progressToken` and `mcpSrv`
- [x] Construct `ProgressFunc` closure per design spec (verbatim token re-embedding, config-prefixed message)
- [x] Type-assert `inst.builder.(progressSetter)`, call `SetProgressFunc(callback)` with `defer SetProgressFunc(nil)`
- [x] Import `server` package from mcp-go for `ServerFromContext`

### Notes
The closure captures `ctx` (handler context with session), `mcpSrv` (`*server.MCPServer`), `progressToken` (type `any`, re-embedded verbatim), `inst.name`, and `srv.registry.len() > 1` for multi-config detection. `SendNotificationToClient` errors are logged at `slog.Debug` and ignored — never fail a build for a notification error.

## 2.2: Integration and E2E tests for progress notifications

### Subtasks
- [x] Create a `progressFakeBuilder` (or extend `fakeBuilder`) that implements `progressSetter` and emits `[N/M]` lines in `Build()` stderr output
- [x] E2E test: build with `progressToken` in `_meta` — verify `notifications/progress` JSON-RPC messages appear with correct fields
- [x] E2E test: build without `progressToken` — verify no `notifications/progress` messages
- [x] E2E test: multi-config with `progressToken` — verify message contains `[configName]` prefix
- [x] Add helper to capture JSON-RPC notifications from the transport pipe (read lines, filter for `notifications/progress` method)

### Notes
The E2E tests use the same JSON-RPC pipe infrastructure as existing E2E tests (`startE2E`/`startMultiE2E`). The key challenge is capturing notifications that arrive interleaved with the tool response. The notification capture helper reads all JSON-RPC messages from the pipe and filters by method.

Since `fakeBuilder` doesn't actually run a subprocess, progress notifications need a different approach: either extend `fakeBuilder` to implement `progressSetter` (calling the callback directly in `Build()`), or use a real subprocess test. The former is simpler and sufficient for E2E validation.

## 2.3: Structural verification

### Subtasks
- [x] Run `go vet ./...`
- [x] Run `go test -race ./...`
- [x] Run `staticcheck ./...`

### Notes
The `-race` flag is especially critical for this feature: the scanner goroutine in `run()` reads from a pipe while `cmd.Run()` writes via `io.MultiWriter`. The `sync.WaitGroup` synchronization must be verified under the race detector.

## Acceptance Criteria

- [x] `handleBuild` sends `notifications/progress` when client provides `progressToken`
- [x] No notifications sent when `progressToken` is absent
- [x] Multi-config builds include config name in message
- [x] Notification errors do not fail the build
- [x] All existing tests continue to pass
- [x] `go vet`, `go test -race`, `staticcheck` all clean
