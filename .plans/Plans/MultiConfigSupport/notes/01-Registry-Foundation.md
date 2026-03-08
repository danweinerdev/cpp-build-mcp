---
title: "Phase 1 Debrief: Registry Foundation"
type: debrief
plan: "MultiConfigSupport"
phase: 1
phase_title: "Registry Foundation"
status: complete
created: 2026-03-07
---

# Phase 1 Debrief: Registry Foundation

## Decisions Made

- **`registry.go` as a separate file** — The plan allowed either `main.go` or a dedicated file. We chose `registry.go` in the main package, keeping `main.go` focused on MCP wiring and handlers. Cleaner separation without adding a subpackage.

- **`newTestServer` returns `(*mcpServer, *state.Store)` tuple** — The plan reviewer flagged that removing `srv.store` would break test setup. Instead of adding a convenience accessor back to `mcpServer` (which would violate the "no builder/store/cfg fields" requirement), we changed `newTestServer` to return both values. Tests use `store` directly for setup, `srv` for handler calls. This preserved all assertion code unchanged.

- **`fakeBuilder.SetDirty` captures state** — Code review identified that a no-op `SetDirty` stub would make dirty-propagation untestable. We added `lastDirtySet bool` to `fakeBuilder` proactively, before it became a gap in Task 1.3.

- **`resolveToolchain` made a free function** — Changed from a method on `mcpServer` to `resolveToolchain(inst *configInstance)`. Runs eagerly at registry construction time in `main()`, not lazily at first build. Eliminates race risk when concurrent builds would both try to detect and mutate their config instances.

- **`storeStatusToken` priority: building > dirty > phase** — Added `TestStoreStatusTokenBuildingTakesPrecedenceOverDirty` after code review flagged the missing edge case. The priority order matches `Store.Health()` semantics.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| `mcpServer` has only `registry` field | Met | No `builder`, `store`, or `cfg` fields remain |
| `SetDirty` on `Builder` interface | Met | Type switch in `handleBuild` eliminated |
| All existing test assertions pass unchanged | Met | 37 unit tests + 6 E2E tests — only setup code changed |
| `list_configs` tool works | Met | Returns `[{name:"default", build_dir:"build", status:"unconfigured"}]` |
| `go vet`, `go test -race`, `staticcheck` pass | Met | All 7 packages clean |

## Deviations

- **Plan reviewer caught critical test infrastructure issue** — The original plan said "all existing tests pass with zero changes." The reviewer correctly identified that `newTestServer` and `startE2E` construct `mcpServer` directly and would fail to compile. We revised the plan to say "test assertions pass unchanged" and explicitly included updating test helpers as subtasks. No code deviation — this was a plan correction before implementation.

- **`TestE2EBuildInProgressGuard` required special handling** — This test constructs `mcpServer` inline (not via `startE2E`), using a `blockingFakeBuilder`. The refactor had to update this test's direct construction too. Not called out in the plan's subtask list but was an obvious corollary.

## Risks & Issues Encountered

- **Wave 1 file overlap risk** — Tasks 1.1 and 1.2 both touched the main package, but 1.1 modified `main.go`/`builder.go` while 1.2 created new files (`registry.go`, `registry_test.go`). No actual conflicts occurred. The advisory overlap analysis correctly judged this as safe to parallelize.

- **`add()` silently overwrites** — Code review noted that `configRegistry.add()` overwrites if the name already exists. Not a problem in Phase 1 (single "default" entry) but flagged as a Phase 2 prerequisite where build_dir uniqueness validation must be added. No action taken now — by design.

## Lessons Learned

- **Returning tuples from test helpers is cleaner than accessor methods** — When a struct field is removed, returning the now-inaccessible value as a second return from the helper keeps test code simple without re-adding fields.

- **Code review before merging waves catches real bugs** — The `fakeBuilder.SetDirty` no-op and the missing building-over-dirty test were both caught between Wave 1 and Wave 2, preventing testability gaps from compounding.

- **Eager toolchain detection is safer** — Moving `resolveToolchain` to startup eliminates a class of race conditions that would have been hard to debug in multi-config mode. This was called out in the plan review and is now baked in.

## Impact on Subsequent Phases

- **Phase 2 can proceed as planned** — The registry is in place, `config.Load()` still works for single-config, and `LoadMulti(dir)` can be added alongside it. The `registry.add()` method needs no changes — Phase 2's uniqueness validation happens in the config loader, not the registry.

- **`resolveToolchain` per-instance is ready** — Phase 2 task 2.3 calls `resolveToolchain()` per config instance at registry construction. The function signature already accepts `*configInstance`, so this will work without changes.

- **`storeStatusToken` is ready for Phase 3** — The aggregate health resource (Phase 3, task 3.1) can reuse `storeStatusToken()` directly for the compact per-config status tokens in the pipe-separated format.

- **No changes needed to Phase 2 or Phase 3 plans** — Everything aligns with the original plan structure.
