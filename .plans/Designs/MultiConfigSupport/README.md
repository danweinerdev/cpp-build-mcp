---
title: "Multi-Configuration Build Support"
type: design
status: review
created: 2026-03-07
updated: 2026-03-07
tags: [go, mcp, cpp, build-system, multi-config]
related: [Designs/BuildIntelligenceMCP]
---

# Multi-Configuration Build Support

## Overview

Extend cpp-build-mcp to support multiple named build configurations simultaneously (e.g., Debug + Release, or different toolchains). Currently the server is a hard singleton — one config, one builder, one state store. This design introduces a configuration registry that maps named configurations to independent (Config, Builder, Store) tuples, letting an AI agent work across multiple build types in one session.

The primary use case: an AI agent fixes an error in Debug mode, then verifies the fix also compiles in Release (where different optimizations may trigger different warnings or errors).

## Current State

The server is structured as a singleton:

```
mcpServer {
    builder: Builder       // one CMakeBuilder or MakeBuilder
    store:   *state.Store  // one state machine (Unconfigured → Configured → Built)
    cfg:     *config.Config // one build_dir, one toolchain, one generator
}
```

Every tool handler reads from or writes to this single tuple. There is no mechanism to address a specific configuration — `build()` always builds in `cfg.BuildDir`, `get_errors()` always reads from the one store.

### Single-Config Assumptions Found

| Location | Assumption |
|----------|-----------|
| `main.go:26-30` | `mcpServer` holds one builder, one store, one config |
| `config/config.go:17-29` | `Config` has one `BuildDir`, one `Toolchain`, one `CMakeArgs` |
| `state/store.go` | One `BuildState` with one `Phase`, one `BuildInProgress`, one `Errors` list |
| `builder/cmake.go` | `buildBuildArgs` has no `--config` flag (required for multi-config generators) |
| `main.go:199-209` | `resolveToolchain()` mutates `srv.cfg.InjectDiagnosticFlags` — a shared field |
| `build://health` resource | Fixed URI, returns one status string for one configuration |
| All 8 tool handlers | No `config` parameter on any tool |

## Architecture

### Components

```
┌─────────────────────────────────────────────────────────────┐
│                         main.go                              │
│                    MCP Server + Router                        │
│                                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              ConfigRegistry                          │    │
│  │                                                      │    │
│  │  "debug"   → { Config, CMakeBuilder, Store }         │    │
│  │  "release" → { Config, CMakeBuilder, Store }         │    │
│  │  "asan"    → { Config, CMakeBuilder, Store }         │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
│  Tool handlers route by config name:                         │
│    build(config: "debug")  → registry["debug"].builder       │
│    get_errors(config: "release") → registry["release"].store │
├──────────┬──────────┬──────────┬──────────┬─────────────────┤
│ builder/ │ diag/    │ state/   │ graph/   │ config/         │
│          │          │          │          │                 │
│ (unchanged interfaces — one instance per config)            │
└──────────┴──────────┴──────────┴──────────┴─────────────────┘
```

The key insight: **the Builder interface, State Store, and Diagnostic parsers do not need to change**. They are already designed for one configuration. Multi-config support means having multiple instances, not modifying the instances themselves.

### ConfigRegistry

A new type in `main.go` (the registry stays in the main package to avoid exporting internal types):

```go
type configInstance struct {
    name    string
    cfg     *config.Config  // MUST be a distinct value copy per instance, not a shared pointer
    builder builder.Builder
    store   *state.Store
}

type configRegistry struct {
    mu        sync.RWMutex
    instances map[string]*configInstance
    dflt      string // name of the default config
}
```

**Config copy semantics:** Each `configInstance` must hold a distinct `config.Config` value. The config loader copies top-level defaults into each per-config instance by value, not by pointer. This is critical because `resolveToolchain()` mutates `cfg.InjectDiagnosticFlags` — if instances shared a pointer, this would be a data race.

The `mcpServer` struct changes from holding one tuple to holding a registry:

```go
type mcpServer struct {
    registry *configRegistry
}
```

All config access goes through the registry — there is no `baseCfg` on `mcpServer`. Handlers call `srv.registry.Get(configName)` and read fields from `instance.cfg`. This prevents accidental bypass of per-config routing.

### Config File Format

The `.cpp-build-mcp.json` file gains an optional `configs` map. When absent, the server behaves exactly as today (backward compatible).

**Single config (current, unchanged):**
```json
{
  "build_dir": "build",
  "generator": "ninja",
  "cmake_args": ["-DCMAKE_BUILD_TYPE=Debug"]
}
```

**Multi-config:**
```json
{
  "source_dir": ".",
  "generator": "ninja",
  "toolchain": "auto",
  "configs": {
    "debug": {
      "build_dir": "build/debug",
      "cmake_args": ["-DCMAKE_BUILD_TYPE=Debug"]
    },
    "release": {
      "build_dir": "build/release",
      "cmake_args": ["-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_INTERPROCEDURAL_OPTIMIZATION=ON"]
    },
    "asan": {
      "build_dir": "build/asan",
      "cmake_args": ["-DCMAKE_BUILD_TYPE=Debug", "-DCMAKE_CXX_FLAGS=-fsanitize=address"]
    }
  },
  "default_config": "debug"
}
```

Fields at the top level serve as defaults. Each entry in `configs` inherits all top-level fields and overrides only the fields it specifies. `default_config` selects which configuration is used when no `config` parameter is provided in tool calls.

### Data Flow

```
build(config: "release", targets: ["app"])
  │
  ▼
mcpServer.handleBuild()
  │
  ├─ resolve config name ("release", or default if omitted)
  ├─ registry.Get("release") → configInstance
  │
  ├─ instance.store.StartBuild()
  ├─ instance.builder.Build(ctx, targets, jobs)
  ├─ diagnostics.Parse(instance.cfg.Toolchain, stdout, stderr)
  ├─ instance.store.FinishBuild(...)
  │
  ▼
{exit_code, error_count, ..., config: "release"}
```

The response includes the `config` field so the AI agent always knows which configuration produced the result.

### Tool API Changes

Every tool gains an optional `config` string parameter. When omitted, the default configuration is used.

| Tool | New Parameter | Notes |
|------|--------------|-------|
| `configure` | `config?: string` | Configures the named config's build directory |
| `build` | `config?: string` | Builds the named configuration |
| `get_errors` | `config?: string` | Returns errors from the named config's last build |
| `get_warnings` | `config?: string, filter?: string` | Returns warnings from the named config |
| `suggest_fix` | `config?: string, error_index: number` | Context around an error from the named config |
| `clean` | `config?: string` | Cleans the named config's build directory |
| `get_changed_files` | `config?: string` | Changed since named config's last successful build |
| `get_build_graph` | `config?: string` | Graph from named config's compile_commands.json |

New tools:

| Tool | Parameters | Response |
|------|-----------|----------|
| `list_configs` | _(none)_ | `{configs: [{name, build_dir, status}], default_config}` |

The `build://health` resource returns an aggregate summary across all configurations, or becomes a tool (`get_health(config?: string)`) for per-config queries. MCP resources don't support query parameters, so the resource URI remains a project-wide summary.

### Interfaces

The `Store` interface is **unchanged**. The `Builder` interface gains one method:

```go
type Builder interface {
    Configure(ctx context.Context, args []string) (*BuildResult, error)
    Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error)
    Clean(ctx context.Context, targets []string) (*BuildResult, error)
    SetDirty(dirty bool) // promoted from concrete types to interface
}
```

`SetDirty` is currently only on the concrete `CMakeBuilder` and `MakeBuilder` types, accessed via a type switch in `handleBuild`. Promoting it to the interface eliminates the type switch and makes dirty-flag propagation work naturally with per-config builder instances. This is a prerequisite change that should happen before multi-config work begins.

The registry is an internal type in `main.go` (not an exported interface, since it is only used within the main package):

```go
func (r *configRegistry) get(name string) (*configInstance, error)
func (r *configRegistry) defaultInstance() *configInstance
func (r *configRegistry) list() []ConfigSummary

type ConfigSummary struct {
    Name     string `json:"name"`
    BuildDir string `json:"build_dir"`
    Status   string `json:"status"` // "unconfigured", "configured", "built"
}
```

## Design Decisions

### Decision 1: Multiple Instances vs. Parameterized Single Instance

**Context:** Two approaches to multi-config: (a) one Builder/Store pair per configuration, or (b) one Builder/Store that accepts a config name parameter on each method.

**Options Considered:**
1. **Multiple instances** — registry of `(Config, Builder, Store)` tuples keyed by name
2. **Parameterized instance** — single Builder/Store with `config` parameter on Build/Errors/etc.
3. **Multi-config CMake generator** — use `Ninja Multi-Config` with `--config` at build time

**Decision:** Multiple instances (option 1).

**Rationale:** The existing Builder and Store are well-tested and correctly scoped to a single configuration. Creating multiple instances reuses all existing code without modification. Option 2 would require invasive changes to Store (keyed maps instead of flat fields) and Builder (passing config through every method). Option 3 is CMake-specific and has known limitations (no `compile_commands.json` generation, no Make equivalent). Multiple instances also enable different toolchains per config (e.g., Debug with Clang + ASAN, Release with GCC).

### Decision 2: Optional Config Parameter with Default

**Context:** Adding a required `config` parameter to every tool call would break backward compatibility and add verbosity for single-config users.

**Options Considered:**
1. Required `config` parameter on all tools
2. Optional `config` parameter, defaults to `default_config`
3. Separate tool sets (e.g., `build_debug`, `build_release`)

**Decision:** Optional parameter with default (option 2).

**Rationale:** Backward compatible — existing single-config setups work without any tool call changes. The `default_config` field in the config file controls which configuration is used when `config` is omitted. Option 3 would create combinatorial explosion of tool registrations and is not practical. For single-config users, the behavior is identical to today.

### Decision 3: Concurrent Build Policy

**Context:** With multiple configurations, should Debug and Release be able to build simultaneously?

**Options Considered:**
1. **Global lock** — only one build at a time across all configs (simple, safe)
2. **Per-config lock** — each config can build independently (more flexible)
3. **Per-config lock with global job limit** — independent builds but capped total parallelism

**Decision:** Per-config lock with advisory warning (option 2, simplified).

**Rationale:** Each config already has its own `Store` with its own `BuildInProgress` flag, so per-config locking is the natural outcome of multiple instances. Two simultaneous Ninja builds can thrash CPU and I/O, but this is the user's choice — the same problem exists if you run `ninja` in two terminals today. A global job limit (option 3) adds complexity with marginal benefit since the AI agent typically works sequentially. If CPU thrashing is a problem, the agent can serialize builds itself.

### Decision 4: Config File Inheritance

**Context:** Multiple configurations share most settings (source_dir, generator, toolchain). Each config only varies build_dir and cmake_args.

**Options Considered:**
1. Each config entry is a complete Config (no inheritance)
2. Top-level fields serve as defaults, config entries override selectively
3. Explicit `extends: "base"` field with named base configs

**Decision:** Top-level defaults with selective override (option 2).

**Rationale:** Simplest model. Most multi-config setups only vary `build_dir` and `cmake_args`. Top-level fields like `source_dir`, `generator`, `toolchain`, `build_timeout` are shared naturally. Option 3 adds indirection without clear benefit. Option 1 forces repetition.

### Decision 5: Health Resource Handling

**Context:** `build://health` is a fixed-URI MCP resource that currently returns one line. Multi-config needs per-config health.

**Options Considered:**
1. Keep resource as aggregate ("2/3 configs OK, 1 FAIL")
2. Replace with a `get_health` tool that accepts `config` parameter
3. Multiple resource URIs (`build://health/debug`, `build://health/release`)

**Decision:** Aggregate resource + per-config detail in `list_configs` response (option 1).

**Rationale:** MCP resources are designed for lightweight status checks. The detailed per-config state is available via `list_configs` or `get_errors(config: "release")`. Option 3 requires dynamic resource registration which is more complex. Option 2 loses the "free" resource read that doesn't cost a tool call.

**Aggregate format specification:**
- Single config: uses the existing format verbatim (e.g., `"OK: 0 errors, 2 warnings, last build 30s ago"`) for backward compatibility
- Multiple configs: `"debug: OK | release: FAIL(3 errors) | asan: UNCONFIGURED"` — pipe-separated, each entry is `name: STATUS` with optional detail in parentheses. Dirty state: `"debug: DIRTY"`

## Error Handling

**Unknown config name:** Tool calls with an unrecognized `config` value return an MCP tool error: `"unknown configuration 'foo' (available: debug, release, asan)"`. The error message includes the list of valid config names to help the AI agent self-correct.

**Conflicting build directories:** If two configs specify the same `build_dir`, return an error at startup: `"configurations 'debug' and 'release' share build_dir 'build' — each configuration must have a unique build_dir"`.

**Partial configuration failures:** If `configure(config: "release")` fails, only the "release" instance is affected. Other configurations remain in their current state. The aggregate health resource reflects the failure.

**`resolveToolchain()` side effect:** The current `resolveToolchain()` mutates `srv.cfg.InjectDiagnosticFlags` for `gcc-legacy`. With per-config configs, each `configInstance` has its own `Config` value copy (not a shared pointer — see ConfigRegistry section), so this mutation is isolated and safe.

**Environment variable overrides in multi-config mode:** When a `configs` map is present in the config file, environment variables (`CPP_BUILD_MCP_BUILD_DIR`, etc.) are **ignored** and a `slog.Warn` message is logged. Environment variables are ambiguous in multi-config mode — `CPP_BUILD_MCP_BUILD_DIR` applied to all instances would break the build_dir uniqueness invariant. In single-config mode (no `configs` key), env vars behave exactly as today.

**`suggest_fix` cross-config consistency:** The `error_index` parameter refers to an index in the named config's error list. The agent must use a consistent `config` value across `get_errors` and `suggest_fix` calls — calling `get_errors(config: "debug")` and then `suggest_fix(config: "release", error_index: 0)` would return the wrong diagnostic context. The `suggest_fix` response includes the `config` field to make the provenance visible.

## Testing Strategy

**Unit tests:**
- `configRegistry`: Get/Default/List/Add, unknown config error, duplicate build_dir detection
- Config loading: `configs` map parsing, top-level inheritance, `default_config` resolution, backward compatibility (no `configs` key)
- Tool handlers: verify `config` parameter routing, verify default config fallback, verify error on unknown config
- Health resource: aggregate formatting across multiple config states

**Integration tests:**
- E2E with two named configs, each with a `fakeBuilder`: configure both, build one, get_errors from the other, verify state isolation
- `list_configs` reflects correct phases for each config independently

### Structural Verification

- `go vet ./...` — all packages
- `go test -race ./...` — critical for concurrent per-config builds with shared registry
- `staticcheck ./...` — if available

## Migration / Rollout

### Backward Compatibility

The design is fully backward compatible:

1. **No `configs` key in config file** → server creates a single instance named `"default"`, identical to current behavior
2. **No `config` parameter in tool calls** → routes to the default configuration
3. **`build://health` resource** → returns the single config's status, same format as today

### Rollout Phases

**Phase 1: Config Registry + Routing**
- Promote `SetDirty(bool)` to the `Builder` interface, remove the type switch in `handleBuild`
- Introduce `configRegistry` in `main.go`
- In `main()`, call `config.Load()` as before, wrap the result in a `configRegistry` with one entry named `"default"`, set `registry.dflt = "default"`
- Refactor all tool handlers to resolve config via `srv.registry.get(configName)` instead of accessing `srv.builder`/`srv.store`/`srv.cfg` directly
- Add optional `config` string parameter to all tool registrations
- Add `list_configs` tool — in single-config mode returns `[{name: "default", build_dir: "build", status: "..."}]`
- No config file format changes — the registry is an internal abstraction only
- All existing tests must pass unchanged (handlers with no `config` param route to the default instance)

**Phase 2: Multi-Config File Format**
- Add `configs` map parsing to config loader
- Add top-level inheritance logic
- Add `default_config` field
- Add `build_dir` uniqueness validation
- Tests with multi-config `.cpp-build-mcp.json` files

**Phase 3: Aggregate Health + Polish**
- Update `build://health` to aggregate across configs
- Ensure `get_changed_files` works correctly with per-config timestamps
- `get_build_graph` reads the correct config's `compile_commands.json` (trivial routing fix — same `graph.ReadSummary` function called with per-config `BuildDir`)
- Documentation updates

### What This Design Does NOT Cover

- **Multi-config CMake generators** (Ninja Multi-Config, Visual Studio): These require `--config` at build time and do not generate `compile_commands.json`. Supporting them is a separate design concern that would require changes to `CMakeBuilder.buildBuildArgs()` and a fallback for `get_build_graph`. The multiple-instances approach in this design is compatible with single-config generators only.

- **Dynamic config creation at runtime**: All configurations are defined in the config file at startup. There is no tool for creating new configurations mid-session. This could be added later but is out of scope.

- **Cross-config dependency tracking**: No mechanism for "build Release only if Debug passes." The AI agent manages sequencing.
