---
title: "Partial Target Builds"
type: design
status: approved
created: 2026-03-09
updated: 2026-03-09
tags: [go, mcp, cmake, build-system, targets]
related: []
---

# Partial Target Builds

## Overview

Enable AI agents to build specific CMake targets instead of the full project. A partial build passes `--target <name>` to `cmake --build`, compiling only the requested target and its dependencies. This is critical for large projects (hundreds of targets) where a full build is wasteful when the agent is iterating on a single library or executable.

The core plumbing already exists: the `build` tool accepts `targets?: string[]` and `CMakeBuilder.buildBuildArgs` emits `--target <name>` for each entry. What is missing is **target discovery** (how the agent knows what targets exist) and **target-aware responses** (reflecting what was actually built back to the agent). This design covers closing those gaps.

## Current State

### What Already Works

The end-to-end path from MCP tool call to cmake invocation is fully wired:

1. **Tool definition** (`main.go:83`): `build` accepts `targets?: string[]`
2. **Handler** (`main.go:334-343`): extracts `targets` from the JSON-RPC request
3. **Builder interface** (`builder/builder.go`): `Build(ctx, targets []string, jobs int)`
4. **CMakeBuilder** (`cmake.go:200-222`): `buildBuildArgs` emits `--target <name>` per entry
5. **Unit tests** (`cmake_test.go`): verify `--target app` and `--target lib` appear in args

An agent can already call:
```json
{"tool": "build", "arguments": {"targets": ["mylib"], "jobs": 4}}
```
This produces: `cmake --build <dir> --target mylib -- -j4`

### What's Missing

| Gap | Impact |
|-----|--------|
| **No target discovery** | Agent must guess target names or read CMakeLists.txt |
| **No target validation** | Invalid target name causes cmake failure with an opaque error |
| **Build response doesn't report targets** | Agent can't confirm what was actually built |
| **`Clean` ignores targets parameter** | `handleClean` passes targets to `Clean()` but `CMakeBuilder.buildCleanArgs()` hardcodes `--target clean` |
| **Diagnostics not scoped to targets** | After a partial build, `get_errors` returns all errors from the last build; no way to ask "errors for target X" |

## Architecture

### Components

```
┌────────────────────────────────────────────────────────────────┐
│                        main.go                                  │
│                  MCP Tool Registration                          │
├─────────┬────────────┬────────────────────────────────────────┤
│ build   │ list_      │ clean           (existing tools,        │
│ (exists)│ targets    │ (simplified —   modified or new)        │
│         │ (NEW)      │  targets param                          │
│         │            │  removed)                                │
├─────────┴────────────┴────────────────────────────────────────┤
│                    builder/                                      │
│  CMakeBuilder                                                    │
│  ├─ Build()          — already emits --target (no change)       │
│  ├─ Clean()          — no change (already does full clean)      │
│  ├─ ListTargets()    — NEW: parse cmake --build --target help   │
│  └─ buildCleanArgs() — no change (hardcodes --target clean)     │
│                                                                  │
│  MakeBuilder                                                     │
│  ├─ Build()          — targets passed as make arguments          │
│  ├─ Clean()          — no change                                 │
│  └─ ListTargets()    — returns ErrTargetsNotSupported            │
└──────────────────────────────────────────────────────────────────┘
```

### Data Flow

**Target Discovery Flow (new):**
```
list_targets(config?) tool call
  │
  ▼
main.go handler
  │ resolves config instance
  │ checks store.GetPhase() >= PhaseConfigured
  │ checks store.IsBuilding() — rejects if build in progress
  ▼
builder.ListTargets(ctx)
  │ runs: cmake --build <buildDir> --target help
  │ captures stdout
  ▼
parseTargetList(stdout)
  │ detects generator from output format (see Parser Specification below)
  │ filters out internal CMake targets
  ▼
[]TargetInfo{ Name string }
  ▼
JSON response to MCP client
  { targets: [{name}], count }
```

**Partial Build Flow (existing, enhanced response):**
```
build(targets: ["mylib"]) tool call
  │
  ▼
main.go handler
  │ extracts targets from request (already works)
  ▼
CMakeBuilder.Build(ctx, ["mylib"], jobs)
  │ runs: cmake --build <dir> --target mylib -- -j4
  ▼
buildResponse
  │ NEW: includes "targets_requested": ["mylib"]
  │ existing: exit_code, error_count, warning_count, duration_ms
  ▼
JSON response
```

**Clean Flow (simplified — targets parameter removed):**
```
clean(config?) tool call
  │
  ▼
main.go handler
  │ resolves config instance
  │ checks store.GetPhase() >= PhaseConfigured (NEW precondition)
  ▼
CMakeBuilder.Clean(ctx)
  │ runs: cmake --build <dir> --target clean
  ▼
cleanResponse
  { config, success, message: "Clean complete" }
```

> **Pre-existing bug fix:** `handleClean` currently calls `inst.store.SetClean()` unconditionally on success, which advances `Phase` to `PhaseConfigured`. If called when `Phase == Unconfigured`, this incorrectly makes `build` callable before any configure step. This design adds a `PhaseConfigured` precondition check to `handleClean`, mirroring `handleBuild`'s `StartBuild()` guard.

### Parser Specification: `cmake --build --target help`

The output format depends on the underlying generator. `parseTargetList` must handle both.

**Ninja generator output:**
```
all
clean
help
myapp
mylib
edit_cache
rebuild_cache
CMakeFiles/myapp.dir/all
CMakeFiles/mylib.dir/all
```

Each line is a target name. Some Ninja versions emit `target_name: phony` with a colon suffix — the parser strips `: phony` or `: PHONY` if present.

**Unix Makefiles generator output:**
```
The following are some of the valid targets for this Makefile:
... all (the default if no target is provided)
... clean
... depend
... rebuild_cache
... edit_cache
... myapp
... mylib
... myapp.o
... mylib.o
```

Lines start with `... ` (three dots + space). The parser strips this prefix and any parenthetical suffix.

**Filtering rules — exclude these targets:**
- `all`, `clean`, `help`, `depend`
- `edit_cache`, `rebuild_cache`
- `install`, `install/local`, `install/strip`, `list_install_components`
- `package`, `package_source`
- `test`, `RUN_TESTS`, `NightlyMemoryCheck`
- Any target containing `/` (internal CMake directory targets like `CMakeFiles/myapp.dir/all`)
- Any target ending in `.o` or `.obj` (individual object file targets)

**What remains** after filtering is the set of user-defined targets (executables, libraries, custom targets).

### Interfaces

#### Builder Interface (extended)

```go
type Builder interface {
    Configure(ctx context.Context, args []string) (*BuildResult, error)
    Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error)
    Clean(ctx context.Context, targets []string) (*BuildResult, error)
    SetDirty(dirty bool)

    // NEW
    ListTargets(ctx context.Context) ([]TargetInfo, error)
}

type TargetInfo struct {
    Name string `json:"name"`
}
```

> **Note on type classification:** Ninja's `--target help` output does not reliably indicate whether a target is an executable, library, or custom target — all user targets appear as bare names or `name: phony`. Reliable classification would require parsing `build.ninja` rules or cross-referencing cmake internals, which is fragile and generator-specific. The `TargetInfo` struct contains only `Name`. Type classification may be added later if a reliable heuristic emerges.

#### New MCP Tool: `list_targets`

```
list_targets
  config?: string   — Configuration name (omit for default)

Response:
  {
    "config": "debug",
    "targets": [
      {"name": "myapp"},
      {"name": "mylib"},
      {"name": "tests"}
    ],
    "count": 3
  }
```

#### Enhanced `build` Response

```json
{
  "config": "debug",
  "exit_code": 0,
  "error_count": 0,
  "warning_count": 2,
  "duration_ms": 1234,
  "files_compiled": 3,
  "targets_requested": ["mylib"]
}
```

The `targets_requested` field uses `json:"targets_requested,omitempty"`. In Go, `omitempty` on a `[]string` omits the field when the slice is nil (not just empty). The handler must leave this field as its nil zero-value when no targets are provided — do not initialize to `[]string{}` or `make([]string, 0)`, as these are non-nil and would serialize as `"targets_requested": []` rather than being omitted. The existing `handleBuild` code already uses `var targets []string` (nil) and only appends when targets are provided, so this works naturally.

## Design Decisions

### Decision 1: Target Discovery via `cmake --build --target help`

**Context:** The AI agent needs to know what targets are available before it can request a partial build. Without discovery, it must guess names from CMakeLists.txt or trial-and-error.

**Options Considered:**
1. Parse `cmake --build <dir> --target help` output
2. Parse `build.ninja` file directly for `build ... : phony` rules
3. Add a CMake script that exports targets via `cmake -P`
4. Parse `CMakeLists.txt` for `add_executable`/`add_library` calls

**Decision:** Option 1 — `cmake --build <dir> --target help`

**Rationale:** This is the official cmake mechanism for listing targets. It works regardless of the underlying generator (Ninja, Make, etc.). The output format is stable and well-documented. Parsing `build.ninja` (option 2) ties us to Ninja specifically. Parsing CMakeLists.txt (option 4) would miss targets added by subdirectories, `FetchContent`, or generator expressions. The cmake command approach requires a configured build directory (which we already require for `build`), and executes in milliseconds.

### Decision 2: Per-Target Clean Strategy

**Context:** `CMakeBuilder.Clean` currently ignores the `targets` parameter and always runs `--target clean`. The `clean` tool's schema already accepts `targets?: string[]` and `handleClean` already extracts them, but they're silently dropped.

**Options Considered:**
1. Rebuild-then-clean: `cmake --build <dir> --target <name> --clean-first` (cleans, then builds)
2. Remove per-target object files by parsing `build.ninja`
3. Keep full-clean-only behavior, remove `targets` from the `clean` tool schema

**Decision:** Option 3 — Remove `targets` from the `clean` tool schema.

**Rationale:** CMake does not natively support per-target clean. `--clean-first` (option 1) is a build flag, not a clean flag — it cleans *everything* then builds the target, which is the opposite of what "clean just this target" means. Parsing build.ninja to find object files (option 2) is fragile and generator-specific. The honest answer is that CMake's `clean` target removes all build artifacts; there's no standard way to clean a single target. Removing the misleading parameter is better than silently ignoring it. The `clean` tool should do what it says: clean everything for the given configuration.

### Decision 3: Target Validation Before Build

**Context:** If the agent passes an invalid target name, cmake fails with an error like `ninja: unknown target 'foo'`. Should we validate before invoking cmake?

**Options Considered:**
1. Pre-validate against `ListTargets()` output before building
2. Let cmake fail and return the error naturally
3. Validate but only warn, still attempt the build

**Decision:** Option 2 — Let cmake fail naturally.

**Rationale:** Pre-validation (option 1) adds a round-trip to cmake for every targeted build. Since the agent already has access to `list_targets`, it can validate client-side if desired. The cmake error message for an unknown target is clear enough: Ninja says `ninja: unknown target 'foo'`, which our regex parser captures. Adding validation would also create a TOCTOU gap — targets can appear or disappear between `list_targets` and `build`. Letting cmake be the source of truth is simpler and more correct.

### Decision 4: Target Type Classification

**Context:** `list_targets` returns target names. Should we also classify them (executable, library, custom)?

**Options Considered:**
1. Return names only — simplest
2. Classify by parsing `cmake --build --target help` output
3. Cross-reference with `compile_commands.json` to infer type
4. Parse `build.ninja` rules to find linker commands (`CXX_EXECUTABLE_LINKER` vs `CXX_STATIC_LIBRARY_LINKER`)

**Decision:** Option 1 — Return names only.

**Rationale:** Ninja's `--target help` output lists target names without indicating whether they are executables, libraries, or custom targets — all user targets appear as bare names or `name: phony`. The Makefile generator similarly provides no type information. Reliable classification would require parsing `build.ninja` internal rules (option 4), which is fragile, generator-specific, and breaks the design principle of using cmake's public CLI. Cross-referencing with `compile_commands.json` (option 3) doesn't work because that file lists source files, not targets. The target name alone (e.g., `mylib`, `myapp`, `test_utils`) is usually sufficient for an AI agent to make reasonable choices. Type classification may be added later if a reliable, generator-agnostic heuristic emerges.

### Decision 5: MakeBuilder Target Support

**Context:** The `Builder` interface requires `ListTargets()`. Make projects don't have a reliable target enumeration mechanism.

**Options Considered:**
1. Run `make -qp` to extract targets from the Makefile database
2. Return an error indicating targets are not supported for Make
3. Return an empty list

**Decision:** Option 2 — Return an `ErrTargetsNotSupported` error.

**Rationale:** Make's `make -qp` output is complex, non-standard across implementations, and includes many internal targets. Since MakeBuilder is already a degraded path (no configure step, no compile_commands.json), it's honest to say target listing isn't supported. The MCP handler translates this to a clear tool error: `"Target listing is not supported for Make projects. Use CMake for target-aware builds."` The `build` tool with targets continues to work for Make (targets are passed as make arguments), but without discovery.

## Error Handling

**`list_targets` before configure:** Returns MCP tool error `"Project not configured. Call configure() first."` — checks `store.GetPhase() >= PhaseConfigured`, same pattern as `build`.

**`list_targets` during a build:** Returns MCP tool error `"Build in progress. Wait for the current build to complete."` — checks `store.IsBuilding()`. This prevents `list_targets` from running `cmake --build --target help` concurrently with an active `cmake --build`, which would cause Ninja to fail with a lock error on `.ninja_deps`/`.ninja_log`. Both commands invoke Ninja, and Ninja locks its state files exclusively.

**`clean` before configure (pre-existing bug fix):** `handleClean` now checks `store.GetPhase() >= PhaseConfigured` before invoking `builder.Clean()`. Previously, `handleClean` had no precondition check — calling `clean` on an unconfigured project would fail in cmake but then call `store.SetClean()`, which incorrectly advanced `Phase` to `PhaseConfigured`, making `build` callable before any configure step. The new guard returns MCP tool error `"Project not configured. Call configure() first."`.

**`cmake --build --target help` fails:** If the cmake subprocess returns non-zero (e.g., corrupt build dir), return MCP tool error with the stderr content. This is a configuration issue, not a target issue.

**Target parsing produces empty list:** Return `{"targets": [], "count": 0}`. This can happen with degenerate CMakeLists.txt files that define no targets. The response is valid — the agent simply knows there's nothing to build.

**Invalid target name in `build`:** CMake fails with a non-zero exit code. The error is captured in `BuildResult.Stderr` and the regex parser extracts it as a diagnostic. The `build` response returns `exit_code: 1` with `error_count: 1`. The agent calls `get_errors()` and sees a clear message about the unknown target.

**Multiple targets, one invalid:** CMake fails on the first invalid target. All valid targets that were already built remain built (Ninja is incremental). The error response shows which target failed.

## Testing Strategy

**Unit tests:**
- `builder/cmake_test.go`: Test `parseTargetList` with inline fixture strings for both Ninja and Makefile generator output formats (see Parser Specification above for the exact strings to use as test fixtures)
- `builder/cmake_test.go`: Test that internal targets (`edit_cache`, `rebuild_cache`, `clean`, `all`, `help`, targets with `/`, targets ending in `.o`) are filtered out
- `builder/cmake_test.go`: Test `parseTargetList` with empty output returns empty list
- `main_test.go`: Test `handleListTargets` handler with mock builder returning canned `[]TargetInfo`
- `main_test.go`: Test `handleListTargets` returns error when phase is unconfigured
- `main_test.go`: Test `handleListTargets` returns error when build is in progress
- `main_test.go`: Test `handleBuild` response includes `targets_requested` when targets are provided, omits it when nil
- `main_test.go`: Test `handleClean` returns error when phase is unconfigured (pre-existing bug fix)
- `main_test.go`: Verify `handleClean` does not accept targets parameter

**Integration tests (requires configured cmake project):**
- Build with `targets: ["<known_target>"]` and verify only that target is compiled
- Call `ListTargets` on the `testdata/cmake` fixture after configure and verify user-defined targets appear
- Build with an invalid target and verify the error is captured as a diagnostic

### Structural Verification

Per Go conventions:
- `go vet ./...` on every change
- `-race` flag on all tests — `ListTargets` runs a subprocess and updates no shared state, but the handler accesses `configInstance` under the registry
- `staticcheck ./...` if available

## Migration / Rollout

This is an enhancement to an existing system. Changes are additive and backward-compatible.

**Phase 1 — Target Discovery + Internal Target Filtering:**
- Add `ListTargets(ctx)` to the `Builder` interface
- Implement `parseTargetList(stdout string) []TargetInfo` with both Ninja and Makefile format parsers
- Implement internal target filter list: `all`, `clean`, `help`, `depend`, `edit_cache`, `rebuild_cache`, `install`, `install/local`, `install/strip`, `list_install_components`, `package`, `package_source`, `test`, `RUN_TESTS`, `NightlyMemoryCheck`, plus targets containing `/` or ending in `.o`/`.obj`
- Implement `CMakeBuilder.ListTargets` running `cmake --build --target help`
- Implement `MakeBuilder.ListTargets` returning `ErrTargetsNotSupported`
- Add `list_targets` MCP tool with `PhaseConfigured` and `IsBuilding` precondition checks
- Add unit tests with inline fixture strings for both generator formats
- **Done when:** `list_targets` on `testdata/cmake` returns exactly the user-defined targets with none of the internal CMake targets

**Phase 2 — Response Enhancements + Clean Hardening:**
- Add `TargetsRequested []string \`json:"targets_requested,omitempty"\`` field to `buildResponse` (nil zero-value when no targets provided)
- Remove `targets` parameter from `clean` tool schema
- Simplify `handleClean` to not extract targets
- Add `PhaseConfigured` precondition check to `handleClean` (pre-existing bug fix)
- **Done when:** `build(targets: ["app"])` response includes `"targets_requested": ["app"]`; full build response omits the field entirely; `clean` on unconfigured project returns tool error
