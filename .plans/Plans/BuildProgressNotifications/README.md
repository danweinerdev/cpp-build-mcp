---
title: "Build Progress Notifications"
type: plan
status: complete
created: 2026-03-08
updated: 2026-03-08
tags: [mcp, progress, ninja, streaming]
related: [Designs/BuildProgressNotifications]
phases:
  - id: 1
    title: "Builder Infrastructure"
    status: complete
    doc: "01-Builder-Infrastructure.md"
  - id: 2
    title: "Handler Integration"
    status: complete
    doc: "02-Handler-Integration.md"
    depends_on: [1]
---

# Build Progress Notifications

## Overview

Add real-time build progress reporting to the `build` tool using MCP's `notifications/progress` protocol. When a client includes a `progressToken` in `_meta`, the server parses Ninja `[N/M]` progress lines from stderr during the build and sends periodic progress notifications. Clients that don't send a `progressToken` see no behavior change.

## Architecture

The implementation follows the approved design in `Designs/BuildProgressNotifications/README.md`. Key points:

- `ProgressFunc` type and `SetProgressFunc` method added to `CMakeBuilder` (NOT the `Builder` interface)
- `run()` modified to tee stderr via `io.MultiWriter` to both a buffer and a pipe feeding a scanner goroutine
- Scanner goroutine matches `^\[(\d+)/(\d+)\]` lines and calls `ProgressFunc` with throttling (250ms default, final line always sent)
- `handleBuild` uses a `progressSetter` type assertion to conditionally wire up notifications
- `sync.WaitGroup` ensures goroutine completes before `run()` returns

## Key Decisions

1. **No `Builder` interface change** — `progressSetter` type assertion keeps fakes and `MakeBuilder` unchanged
2. **`io.MultiWriter` tee** — preserves existing `BuildResult.Stderr` accumulation pattern
3. **250ms time-based throttle** — stored as `progressMinInterval` field on builder for test overriding
4. **Make builds excluded** — `MakeBuilder` doesn't implement `progressSetter`
5. **Configure progress deferred** — no structured progress format for cmake configure

## Dependencies

- Completed BuildIntelligenceMCP plan (all 5 phases)
- Completed MultiConfigSupport plan (all 3 phases)
- Approved BuildProgressNotifications design document
- mcp-go v0.45.0 (already in use) — provides `ProgressToken`, `SendNotificationToClient`
