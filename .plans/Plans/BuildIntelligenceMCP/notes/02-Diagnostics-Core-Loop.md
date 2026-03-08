---
title: "Phase 2 Debrief: Diagnostics and Core Loop"
type: debrief
plan: "BuildIntelligenceMCP"
phase: 2
phase_title: "Diagnostics and Core Loop"
status: complete
created: 2026-03-07
---

# Phase 2 Debrief: Diagnostics and Core Loop

## Decisions Made

- **Mock builder in test package, not builder/mock.go**: The plan called for `builder/mock.go` as an exported mock. Instead, we placed `fakeBuilder` and `blockingFakeBuilder` in `main_test.go` (the `main` package test file) since the E2E tests need access to the `mcpServer` struct which is unexported. A separate `builder/mock.go` would have required exporting internal server types or creating a separate test binary. This keeps the mock close to the tests that use it and avoids exporting test-only code.

- **`diagnostics.Parse()` convenience function**: Added a top-level `Parse(toolchain, stdout, stderr)` function in `parser.go` that wraps `NewParser().Parse()`. This simplifies the call site in `main.go` — one function call instead of two. The `NewParser()` factory is still available when callers need to hold a parser reference.

- **Handler methods on `mcpServer` struct**: Chose to put tool/resource handlers as methods on an `mcpServer` struct that holds builder, store, and config references. This avoids closures and makes the handlers testable by constructing `mcpServer` instances with fake dependencies.

- **E2E tests use raw JSON-RPC**: Rather than depending on the mcp-go client library for E2E tests, we send raw JSON-RPC messages over `io.Pipe`. This tests the actual wire protocol and avoids coupling tests to a specific client implementation. The `e2eEnv` helper struct provides a clean API for sending requests and reading responses.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| Clang JSON parser correctly parses real compiler output samples | Met | 17 test cases including warnings, errors, template errors, concatenated arrays, malformed JSON |
| MCP server starts over stdio and responds to tool calls | Met | Verified via E2E tests with `io.Pipe`-based JSON-RPC |
| `build()` -> `get_errors()` loop works end-to-end with mock builder | Met | `TestE2EFailedBuildThenGetErrors` exercises the full loop |
| `build_health` resource returns correct state for all phases | Met | 5 state variants tested: UNCONFIGURED, READY, OK, FAIL, DIRTY |
| State guards prevent builds when unconfigured or already in progress | Met | Unit tests + E2E `TestE2EBuildInProgressGuard` with blocking builder |
| All tests pass with `-race` flag | Met | 38 tests across all packages, all pass under race detector |

## Deviations

- **Task 2.1 was mostly pre-done**: The `Diagnostic` struct and `Severity` constants were already in `diagnostics/types.go` from Phase 1 scaffolding. Only the `DiagnosticParser` interface needed to be added. This was expected — Phase 1 scaffolded the types, Phase 2 just needed the interface.

- **No separate `builder/mock.go`**: As noted in Decisions, the mock builder lives in `main_test.go` and `e2e_test.go` rather than in the builder package. The plan's intent (testable builder abstraction) is met, just with different file organization.

- **`RegexParser` is a no-op stub**: The plan called for a regex fallback in Phase 4. The current `RegexParser.Parse()` returns `nil, nil`. This is correct — gcc and msvc won't produce meaningful output until their parsers are implemented.

## Risks & Issues Encountered

- **mcp-go `stdioSessionInstance` is a package-level global**: The `StdioServer` in mcp-go uses a package-level `stdioSessionInstance` singleton. This means E2E tests that use `NewStdioServer` cannot run truly in parallel. We mitigated this by not using `t.Parallel()` on E2E tests. This is a framework limitation, not a project issue.

- **Tool call worker pool in mcp-go**: The `StdioServer` queues `tools/call` requests to a worker pool for concurrent processing, but handles other methods synchronously. This means the `TestE2EBuildInProgressGuard` test needed careful design — the first build blocks in a worker, the second build gets queued to a different worker. Response ordering is non-deterministic, so the test collects both responses and finds the expected one by ID.

## Lessons Learned

- **mcp-go API patterns**: `mcp.WithArray("name", mcp.WithStringItems())` for array params, `req.GetInt("name", default)` for extraction, `mcp.NewToolResultText()`/`mcp.NewToolResultError()` for responses. `server.NewStdioServer(s).Listen(ctx, reader, writer)` for piped I/O in tests. These patterns will be reused in Phase 3 when adding more tools.

- **E2E test harness is reusable**: The `e2eEnv` struct with `startE2E()`, `callTool()`, `readResource()`, and `toolResultText()` helpers can be reused for all future tool testing in Phase 3. New tools just need registration in `startE2E()`.

- **Interleaved JSON array splitting is important**: Clang's JSON diagnostic output can get concatenated when Ninja runs parallel TUs. The bracket-depth splitter in `splitJSONArrays()` handles this correctly, including strings containing brackets and escaped quotes. This will also apply to GCC JSON parsing in Phase 4.

## Impact on Subsequent Phases

- **Phase 3 (Full Toolset)**: The `main.go` structure with `mcpServer` struct and handler methods is ready for adding `get_warnings`, `get_build_graph`, `get_changed_files`, `clean`, and `configure` tools. Each tool is just another `s.AddTool()` call + handler method. The E2E test harness can be extended for new tools.

- **Phase 4 (Broad Compiler Compatibility)**: The `diagnostics/parser.go` dispatcher is ready for GCC and regex parser implementations. Just swap the `RegexParser{}` stubs for real implementations. The `DiagnosticParser` interface and `Parse()` convenience function mean no changes needed in `main.go` — the dispatcher handles routing.

- **No plan changes needed**: All downstream phases can proceed as designed. No scope expansion or structural changes required.
