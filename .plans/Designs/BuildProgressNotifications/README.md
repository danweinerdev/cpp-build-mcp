---
title: "Build Progress Notifications"
type: design
status: approved
created: 2026-03-08
updated: 2026-03-08
tags: [mcp, progress, ninja, streaming]
related: [Designs/BuildIntelligenceMCP, Designs/MultiConfigSupport]
---

# Build Progress Notifications

## Overview

Add real-time build progress reporting to the `build` tool using MCP's `notifications/progress` protocol. When a client includes a `progressToken` in the tool call's `_meta`, the server sends periodic `[N/M]` progress updates parsed from Ninja's stderr output during the build. Clients that don't send a `progressToken` see no behavior change.

This extends the existing two-tier response model (build summary → get_errors detail) with a third tier: live progress during execution. For a large project compiling 800 files, the AI agent can observe compilation progress instead of waiting in the dark for minutes.

## Architecture

### Components

```
handleBuild (main.go)
  │
  ├── extracts progressToken from req.Params.Meta.ProgressToken
  ├── calls StartBuild() — must succeed before setting progress
  ├── creates progressCallback (closure over mcpSrv, ctx, token, configName)
  ├── ps.SetProgressFunc(callback); defer ps.SetProgressFunc(nil)
  │
  └── inst.builder.Build(ctx, targets, jobs)
        │
        └── run(ctx, "cmake", args)  [modified]
              │
              ├── cmd.Stderr = io.MultiWriter(&stderrBuf, stderrPipeW)
              ├── cmd.Stdout = &stdoutBuf  [unchanged]
              │
              ├── wg.Add(1)
              ├── goroutine: scan stderrPipeR line-by-line
              │     ├── defer wg.Done()
              │     ├── match ^\[(\d+)/(\d+)\] → throttle check → call progressFunc(N, M, line)
              │     └── non-match → ignore (line already captured by stderrBuf)
              │
              ├── cmd.Run()  [blocks until process exits + I/O completes]
              ├── pipeW.Close()  [causes scanner to see EOF]
              └── wg.Wait()  [waits for scanner goroutine to finish]
```

**Goroutine synchronization:** `run()` uses a `sync.WaitGroup` to ensure the scanner goroutine has fully exited before returning `BuildResult`. The sequence is: `cmd.Run()` returns (process exited, all internal I/O goroutines done) → `pipeW.Close()` (scanner sees EOF) → `wg.Wait()` (scanner goroutine calls `wg.Done()` and exits) → `run()` returns. This guarantees no data race between the goroutine calling `progressFunc` and `handleBuild`'s deferred `SetProgressFunc(nil)`.

### Data Flow

1. MCP client sends `tools/call` for `build` with `_meta: { progressToken: "tok-123" }`
2. `handleBuild` extracts `progressToken` from `req.Params.Meta.ProgressToken` (type `any` — may be string or float64 depending on client)
3. `handleBuild` gets `*MCPServer` via `server.ServerFromContext(ctx)`. If nil, skips progress setup entirely.
4. `handleBuild` calls `inst.store.StartBuild()` — validates state, sets `BuildInProgress = true`
5. **Only after `StartBuild()` succeeds**, `handleBuild` constructs a `ProgressFunc` closure and calls `SetProgressFunc(callback)` with `defer SetProgressFunc(nil)`. This ordering is critical: if `StartBuild()` fails, `SetProgressFunc` is never called and the defer never runs.
6. `handleBuild` stores the `ProgressFunc` on the builder instance via `SetProgressFunc`
7. `builder.Build()` → `run()` starts the cmake process
8. `run()` tees stderr through `io.MultiWriter` to both a `bytes.Buffer` (for the final `BuildResult.Stderr`) and an `io.PipeWriter`
9. A goroutine reads the pipe with `bufio.Scanner`, matches Ninja `[N/M]` lines (regex `^\[(\d+)/(\d+)\]`), and calls the `ProgressFunc`
10. The `ProgressFunc` sends `notifications/progress` to the client (see closure construction below)
11. When the process exits, `cmd.Run()` returns. `run()` then calls `pipeW.Close()`, which causes the scanner goroutine to see EOF and exit. `run()` waits on a `sync.WaitGroup` for the goroutine to finish before returning `BuildResult`.
12. `handleBuild` clears the `ProgressFunc` (via deferred `SetProgressFunc(nil)`) and returns the final tool result

**ProgressFunc closure construction in `handleBuild`:**

```go
// Meta is a pointer — nil when client doesn't send _meta.
var progressToken any
if req.Params.Meta != nil {
    progressToken = req.Params.Meta.ProgressToken
}
mcpSrv := server.ServerFromContext(ctx)

// ... after StartBuild() succeeds ...

if progressToken != nil && mcpSrv != nil {
    configName := inst.name
    multiConfig := srv.registry.len() > 1
    callback := func(current, total int, line string) {
        msg := line
        if multiConfig {
            msg = "[" + configName + "] " + line
        }
        if err := mcpSrv.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
            "progressToken": progressToken, // re-embed verbatim, do NOT type-convert
            "progress":      float64(current),
            "total":         float64(total),
            "message":       msg,
        }); err != nil {
            slog.Debug("progress notification failed", "error", err)
        }
    }
    if ps, ok := inst.builder.(progressSetter); ok {
        ps.SetProgressFunc(callback)
        defer ps.SetProgressFunc(nil)
    }
}
```

Note: `progressToken` is `mcp.ProgressToken` (type alias for `any`). It must be re-embedded in the params map verbatim without type conversion — the client expects the exact value it sent (string or number) echoed back.

### Interfaces

**New type in `builder` package:**

```go
// ProgressFunc is called with build progress updates. current and total
// correspond to Ninja's [current/total] progress line. message is the
// full progress line text.
type ProgressFunc func(current, total int, message string)
```

**Extended Builder implementations (NOT the interface):**

```go
// SetProgressFunc sets an optional progress callback for the next Build call.
// Pass nil to disable. The callback is not cleared automatically — the caller
// is responsible for clearing it after Build returns.
func (b *CMakeBuilder) SetProgressFunc(fn ProgressFunc)
```

**The `Builder` interface itself is NOT changed.** `SetProgressFunc` is a concrete method on `CMakeBuilder` only, called via type assertion in `handleBuild`. This avoids breaking the interface contract and all existing test fakes. Builders that don't implement `progressSetter` (like `MakeBuilder` and test fakes) simply won't receive progress calls — no change needed. This is intentional: Make does not emit `[N/M]` progress lines (see Decision 4).

```go
// In handleBuild, conditionally set progress:
type progressSetter interface {
    SetProgressFunc(ProgressFunc)
}
if ps, ok := inst.builder.(progressSetter); ok {
    ps.SetProgressFunc(callback)
    defer ps.SetProgressFunc(nil)
}
```

## Design Decisions

### Decision 1: Setter method + type assertion vs. interface change

**Context:** The `Builder` interface is implemented by `CMakeBuilder`, `MakeBuilder`, and multiple test fakes (`fakeBuilder`, `blockingFakeBuilder`). Adding `ProgressFunc` to the interface forces all implementations to change.

**Options Considered:**
1. Add `ProgressFunc` parameter to `Builder.Build()` — breaks interface, all fakes must update
2. Add `SetProgressFunc` to the `Builder` interface — same breakage
3. Concrete `SetProgressFunc` method + `progressSetter` type assertion in handler — no interface change
4. Pass callback through `context.Context` value — implicit, hard to test, leaky

**Decision:** Option 3 — concrete setter with type assertion.

**Rationale:** Progress reporting is an optional enhancement, not a core builder contract. Test fakes shouldn't need to carry a `ProgressFunc` field they never use. The type assertion pattern is idiomatic Go for optional capabilities (similar to `io.WriterTo`, `http.Hijacker`). The `defer ps.SetProgressFunc(nil)` cleanup pattern is simple and explicit.

### Decision 2: io.MultiWriter tee vs. io.Pipe-only

**Context:** stderr must both be accumulated (for `BuildResult.Stderr`) and scanned line-by-line (for progress). Two approaches: (a) `io.MultiWriter(&buf, pipeW)` tees every byte to both, or (b) use only a pipe, accumulate in the scanner goroutine.

**Options Considered:**
1. `io.MultiWriter` — tees to buffer + pipe simultaneously, scanner goroutine reads pipe
2. Pipe-only — scanner goroutine reads all lines, appends to a `strings.Builder`, returns accumulated output
3. Custom `io.Writer` that buffers and scans inline — complex, mixes concerns

**Decision:** Option 1 — `io.MultiWriter`.

**Rationale:** The `bytes.Buffer` accumulation is the existing pattern. Keeping it means `BuildResult.Stderr` is populated identically whether progress is enabled or not. The scanner goroutine only needs to match progress lines and call the callback — it doesn't own the accumulated output. When `ProgressFunc` is nil, no pipe or goroutine is created — `run()` uses the existing `cmd.Stderr = &stderr` path unchanged.

### Decision 3: Rate limiting strategy

**Context:** A large Ninja build emits one `[N/M]` line per compiled file. With parallel compilation (`-j8`), multiple files finish near-simultaneously, producing bursts of `[N/M]` lines. For 800 files with `-j8`, up to 8 lines arrive within milliseconds of each other. Without throttling, every line becomes a notification.

**Options Considered:**
1. No throttling — send every `[N/M]` line
2. Time-based — at most one notification per 250ms
3. Count-based — every 5% of total (M/20 files)
4. Adaptive — whichever of time or count triggers first

**Decision:** Option 2 — time-based, at most one notification per 250ms.

**Rationale:** 250ms balances responsiveness with volume. With `-j8` parallelism and 800 files, compilation events cluster in bursts. At 250ms throttle, a 2-minute build produces ~480 notifications at most. For fast incremental builds (10 files in 2s), this sends ~8 notifications. Time-based is simpler than percentage-based and handles varying compilation speeds naturally.

The throttle interval is stored as a field on `CMakeBuilder` (`progressMinInterval`) defaulting to 250ms. This allows tests to override it to a small value (e.g., 1ms) to avoid timing-sensitive flakiness.

**Final-line override:** When `N == M` (build 100% complete), the notification is always sent regardless of the throttle timer. The `N == M` check is evaluated **before** the throttle check.

**Progress line regex:** `^\[(\d+)/(\d+)\]` — captures both N (current) and M (total). This matches Ninja's exact format. Lines that don't match this pattern (including malformed lines like `[abc/def]`) are silently ignored.

### Decision 4: Make builds — no progress total

**Context:** `make` does not emit `[N/M]` progress lines. It invokes the compiler directly, with each invocation visible in stderr.

**Decision:** No progress notifications for Make builds in this iteration.

**Rationale:** Make builds use `MakeBuilder`, which has a separate `runMake()` method. Detecting compiler invocations and counting them requires knowing the total upfront (which Make doesn't provide). Progress without a total is low-value — the client can't show a percentage. `MakeBuilder` intentionally does NOT implement the `progressSetter` interface — the type assertion in `handleBuild` fails silently, and no progress callback is set. This can be revisited later if there's demand.

### Decision 5: Configure progress — deferred

**Context:** `cmake` configure also takes time but doesn't emit `[N/M]` lines. It outputs irregular text about finding compilers, checking features, etc.

**Decision:** Defer configure progress to a future iteration.

**Rationale:** Configure is typically fast (seconds) compared to builds (minutes). Without a structured progress format, notifications would be heartbeat-style ("still configuring...") which adds complexity for low value. The architecture supports adding this later via the same `SetProgressFunc` + `run()` pattern.

### Decision 6: Notification message content

**Context:** The `message` field in progress notifications can carry human-readable text. Ninja progress lines contain useful info: `[42/803] Building CXX object src/foo.cpp.o`.

**Options Considered:**
1. Full Ninja line as message
2. Shortened: just `"Building src/foo.cpp.o"`
3. Structured: `"Compiling foo.cpp (42/803)"`
4. Config-prefixed: `"[debug] Building src/foo.cpp.o"`

**Decision:** Option 4 when multiple configs exist, option 1 for single config.

**Rationale:** The full Ninja line is already concise and informative. Adding the config prefix in multi-config mode prevents ambiguity if the client is tracking multiple concurrent builds. The `progress` and `total` numeric fields carry the machine-readable progress; `message` is for human display. The config name is embedded in the closure by `handleBuild` (which has `inst.name`) — `CMakeBuilder.run()` does not need to know the config name directly.

## Error Handling

| Scenario | Handling |
|----------|----------|
| `server.ServerFromContext(ctx)` returns nil | Don't set progress callback. Build proceeds normally without notifications. |
| `SendNotificationToClient` returns error | Log at `slog.Debug` level, continue building. Never fail a build because of a notification error. |
| Notification channel full (`ErrNotificationChannelBlocked`) | Same as above — logged and ignored. The build is more important than the notification. |
| `progressToken` is nil (client didn't request progress) | No callback set. `run()` takes the fast path with no pipe/goroutine. Zero overhead. |
| Scanner goroutine panics | The goroutine does NOT close `pipeW` on panic — it recovers, logs the panic, and drains remaining pipe data via `io.Copy(io.Discard, r)` until EOF. This ensures `io.MultiWriter` never sees `ErrClosedPipe`, and `cmd.Run()` proceeds normally. The outer `defer wg.Done()` fires as usual when the goroutine exits. |
| Process killed mid-build (timeout/cancel) | Pipe writer is closed when process exits, scanner drains remaining lines, goroutine exits. `BuildResult.Killed = true` is set as before. |

## Testing Strategy

### Unit Tests

1. **`run()` with progress callback** — Use a fake command (e.g., `echo` with formatted output) to verify the callback is called with correct `(current, total)` values from `[N/M]` lines in stderr.

2. **`run()` without progress callback** — Verify the existing behavior is unchanged: `BuildResult.Stderr` contains the full stderr output, no goroutine is created.

3. **Rate limiting** — Set `progressMinInterval` to 1ms on the builder, feed many rapid `[N/M]` lines, verify the callback is called fewer times than the total line count (throttle fires). Also verify the final `[M/M]` line is always delivered. Using a very small interval avoids timing-sensitive flakiness on loaded CI machines.

4. **Malformed progress lines** — Lines like `[abc/def]` or bare text are ignored (no callback). Lines matching `[N/M]` format are processed normally.

5. **`BuildResult.Stderr` integrity** — When progress is enabled, `BuildResult.Stderr` still contains ALL stderr output (including the `[N/M]` lines), not just the non-progress lines. The tee must not filter.

### Integration Tests

6. **`handleBuild` with progressToken** — Set up a `fakeBuilder` that emits `[N/M]` lines in stderr. Verify `notifications/progress` JSON-RPC messages appear in the transport output with the correct `progressToken`, `progress`, `total`, and `message` fields.

7. **`handleBuild` without progressToken** — Verify no `notifications/progress` messages appear in the transport output.

8. **Multi-config message prefix** — With two configs, verify the `message` field includes the config name prefix.

### Structural Verification

- `go vet ./...` — catches printf mismatches, unreachable code
- `go test -race ./...` — the scanner goroutine + `io.MultiWriter` pattern must be race-free
- `staticcheck ./...` — additional correctness checks

The `-race` flag is critical here: the scanner goroutine reads from a pipe while `cmd.Run()` writes to it via `io.MultiWriter`. The `io.PipeWriter.Close()` synchronization must be verified under the race detector.

## Migration / Rollout

### Backward Compatibility

This is a purely additive change:

- **No interface changes** — `Builder` interface is unchanged
- **No behavior change without progressToken** — clients that don't send `_meta.progressToken` see identical behavior. The `run()` method takes its existing fast path.
- **No new configuration** — progress notifications are protocol-level, controlled by the client per-request

### Rollout Steps

1. Add `ProgressFunc` type, `progressMinInterval` field, and `SetProgressFunc` to `CMakeBuilder`
2. Modify `CMakeBuilder.run()` to support tee + scanner goroutine + `sync.WaitGroup` when `progressFunc` is set
3. Add `progressSetter` interface and type assertion in `handleBuild` — **after** `StartBuild()` succeeds, with `defer SetProgressFunc(nil)` immediately following
4. Add unit tests for `run()` progress path (callback invocation, rate limiting, stderr integrity, malformed lines)
5. Add integration test for `handleBuild` progress notifications (with/without token, multi-config prefix)
6. Verify existing tests pass unchanged (`go vet`, `go test -race`, `staticcheck`)

Note: `MakeBuilder` intentionally does NOT implement `progressSetter`. No changes to `MakeBuilder` are needed.
