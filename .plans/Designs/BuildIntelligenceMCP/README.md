---
title: "Build Intelligence MCP Server"
type: design
status: review
created: 2026-03-07
updated: 2026-03-07
tags: [go, mcp, cpp, build-system, diagnostics]
related: []
---

# Build Intelligence MCP Server

## Overview

An MCP (Model Context Protocol) server written in Go that wraps C++ build systems (CMake/Ninja/Make) and exposes structured, token-efficient tools for AI coding agents. The server eliminates raw build log noise from AI context windows by pre-processing compiler output into structured, actionable diagnostics.

The core loop it enables: `build()` -> `get_errors()` -> fix -> `build()` — with each tool response fitting comfortably in context (~500 tokens in the happy path). Raw compiler output never appears in the Claude context window directly.

## Architecture

### Components

```
┌─────────────────────────────────────────────────────────┐
│                    main.go                               │
│              MCP Server + Tool Registration               │
│         (github.com/mark3labs/mcp-go, stdio)             │
├──────────┬──────────┬──────────┬──────────┬─────────────┤
│ builder/ │ diag/    │ graph/   │ state/   │ config/     │
│          │          │          │          │             │
│ Builder  │ Parser   │ compile_ │ Store    │ Config      │
│ iface    │ dispatch │ commands │ (RWMutex)│ (.json+env) │
│          │          │ .json    │          │             │
│ cmake.go │ clang.go │ reader   │ last     │ build_dir   │
│ make.go  │ gcc.go   │ summary  │ build    │ source_dir  │
│          │ regex.go │          │ errors   │ toolchain   │
│          │          │          │ dirty    │             │
└──────────┴──────────┴──────────┴──────────┴─────────────┘
```

**main.go** — MCP server entrypoint. Creates a `server.NewMCPServer`, registers all tools and the `build_health` resource, and calls `server.ServeStdio(s)`. Each tool handler is a thin adapter that calls into the domain packages and formats JSON responses. This file owns no business logic.

**builder/** — Abstraction over build systems.
- `builder.go` — `Builder` interface (`Configure`, `Build`, `Clean`) + factory function that selects the implementation based on config/detection.
- `cmake.go` — CMake + Ninja implementation. Runs `cmake` for configure, `cmake --build` for builds. At configure time, automatically injects: `-DCMAKE_EXPORT_COMPILE_COMMANDS=ON` (for `get_build_graph`) and `-DCMAKE_C_FLAGS=-fdiagnostics-format=json`/`-DCMAKE_CXX_FLAGS=-fdiagnostics-format=json` (appended to existing flags) when the toolchain is Clang or GCC 10+. These are CMake cache variables, so they persist across rebuilds without re-injection.
- `make.go` — GNU Make fallback. Runs `make` for builds. Injects `-fdiagnostics-format=json` via `CFLAGS`/`CXXFLAGS` environment variables at build time (Make respects env vars, unlike CMake+Ninja which bakes flags at configure time). For `Configure`, this is a no-op (Make has no configure step) but sets `Phase = Configured` to allow builds.

**diagnostics/** — Compiler output parsing.
- `parser.go` — Dispatcher that selects the parsing strategy based on toolchain (Clang JSON, GCC structured, regex fallback). Accepts both stdout and stderr from the build and routes the correct stream to the appropriate parser.
- `clang.go` — Parses Clang's `-fdiagnostics-format=json` output into `[]Diagnostic`. **Important:** Clang writes JSON diagnostics to **stdout** (not stderr). The human-readable summary still goes to stderr. When invoked through Ninja with parallelism, JSON output from multiple TUs can interleave on stdout, producing malformed JSON. Mitigation: the parser splits stdout on top-level JSON array boundaries (`[...]`) and parses each array independently. For robustness, the build step can also use `ninja -j1` when diagnostic capture is active (configurable via `diagnostic_serial_build` in config).
- `gcc.go` — Parses GCC's structured diagnostic output (GCC 10+ `-fdiagnostics-format=json`). **GCC's JSON schema differs from Clang's:** GCC emits a JSON array where each element can have nested `children` arrays (notes, fix-it hints, template expansion traces). Mapping to `Diagnostic`: each top-level element becomes a `Diagnostic`; `children` with `kind: "note"` are flattened into separate `Diagnostic` entries with `severity: "note"` and a `related_to` field pointing to the parent's file:line. Template expansion `children` are capped at depth 3 to avoid hundreds of entries from deep template errors. Falls back to regex for GCC < 10.
- `regex.go` — Regex-based fallback parser for MSVC and legacy toolchains. Handles `file(line,col): error C1234: message` patterns. Also serves as the fallback for GCC < 10 (`file:line:col: error: message` pattern).

**graph/** — Build graph introspection.
- `compile_commands.go` — Reads and summarizes `compile_commands.json`. Extracts translation units, include directories, and compiler flags. Returns a compact summary (file count, unique flags, include paths) rather than the full file. **CMake enablement:** The `configure()` step automatically injects `-DCMAKE_EXPORT_COMPILE_COMMANDS=ON` to ensure the file is generated. **Make fallback:** For Make projects, `compile_commands.json` does not exist. In this case, `get_build_graph` returns a degraded response: source file count from a directory walk of `source_dir` (filtered by `.c`, `.cc`, `.cpp`, `.cxx` extensions), no flags, no include dirs, and `"available": false` in the response to signal incomplete data. **File not found:** If the file doesn't exist (project not yet configured, or Make project), returns `{ available: false, reason: "compile_commands.json not found — run configure() first or use CMake", file_count: N }` with the source file count from directory walk.

**state/** — In-memory build state tracking.
- `store.go` — `Store` struct protected by `sync.RWMutex`. Tracks: phase (unconfigured/configured/built), last build time, last successful build time, last build result (exit code, duration, error/warning counts), last error set (`[]Diagnostic`), last warning set (`[]Diagnostic`), dirty flag (set when a build is killed), build-in-progress flag. Write-locked during builds, read-locked during tool queries.

**config/** — Project configuration.
- `config.go` — Loads from `.cpp-build-mcp.json` in the project root, with environment variable overrides (`CPP_BUILD_MCP_BUILD_DIR`, etc.). Fields: `build_dir`, `source_dir`, `toolchain` (auto/clang/gcc/msvc), `generator` (ninja/make), `cmake_args`, `build_timeout`, `inject_diagnostic_flags` (bool, default true), `diagnostic_serial_build` (bool, default false — when true, uses `-j1` during builds to prevent JSON output interleaving).

### Data Flow

**Build Flow:**
```
build() tool call
  │
  ▼
main.go handler
  │ reads config, checks state (configured? build in progress?)
  │ acquires state write lock
  ▼
builder.Build(ctx, targets, jobs)
  │ spawns subprocess via os/exec
  │ captures stdout + stderr separately
  ▼
diagnostics.Parse(toolchain, stdout, stderr)
  │ dispatches to clang/gcc/regex parser
  │ Clang JSON: parses stdout (JSON diagnostics)
  │ GCC JSON:   parses stdout (JSON diagnostics)
  │ Regex:      parses stderr (human-readable output)
  ▼
[]Diagnostic
  │
  ▼
state.Store.Update(result, diagnostics)
  │ stores errors, warnings, timestamps
  │ on success: updates LastSuccessfulBuildTime
  ▼
JSON response to MCP client
  { exit_code, error_count, warning_count, duration_ms, files_compiled }
```

**Query Flow (get_errors, get_warnings, get_build_graph):**
```
tool call
  │
  ▼
main.go handler
  │ acquires state read lock
  ▼
state.Store.Errors() / state.Store.Warnings()
  │
  ▼
JSON response ([]Diagnostic or summary)
```

**Configure Flow:**
```
configure() tool call
  │
  ▼
builder.Configure(ctx, cmake_args)
  │ spawns cmake subprocess
  │ parses output for errors
  ▼
state.Store.SetConfigured(result)
  │
  ▼
JSON response { success, error_count, messages }
```

### Interfaces

#### Builder Interface

```go
type BuildResult struct {
    ExitCode     int
    Stdout       string
    Stderr       string
    Duration     time.Duration
}

type Builder interface {
    // Configure runs the build system configuration step (e.g., cmake).
    Configure(ctx context.Context, args []string) (*BuildResult, error)

    // Build runs an incremental build.
    // targets: specific targets to build (nil = default target).
    // jobs: parallelism level (0 = auto).
    Build(ctx context.Context, targets []string, jobs int) (*BuildResult, error)

    // Clean removes build artifacts.
    // targets: specific targets to clean (nil = full clean).
    Clean(ctx context.Context, targets []string) (*BuildResult, error)
}
```

#### Diagnostic Types

```go
type Severity string

const (
    SeverityError   Severity = "error"
    SeverityWarning Severity = "warning"
    SeverityNote    Severity = "note"
)

type Diagnostic struct {
    File      string   `json:"file"`
    Line      int      `json:"line"`
    Column    int      `json:"column"`
    Severity  Severity `json:"severity"`
    Message   string   `json:"message"`
    Code      string   `json:"code,omitempty"`       // e.g., "-Wunused-variable" or "C2065"
    Source    string   `json:"source,omitempty"`      // "clang", "gcc", "msvc"
    RelatedTo string   `json:"related_to,omitempty"` // "file:line" of parent diagnostic (for GCC flattened children)
}

type DiagnosticParser interface {
    // Parse accepts both stdout and stderr from the build subprocess.
    // Clang JSON mode reads from stdout; GCC JSON and regex parsers read from stderr.
    Parse(stdout, stderr string) ([]Diagnostic, error)
}
```

#### StateStore

**State Machine:**
```
  ┌──────────────┐  configure()  ┌──────────────┐  build()   ┌───────────┐
  │ Unconfigured ├──────────────►│  Configured   ├───────────►│   Built   │
  └──────┬───────┘               └──────┬────────┘            └─────┬─────┘
         │                              │                           │
         │◄─── timeout/kill ────────────┼───── sets Dirty=true ─────┘
         │     (lock files left)        │                           │
         │                              │◄──── clean() ─────────────┘
         │                              │      (resets to Configured)
         │◄──── configure() ────────────┘
```

- `build()` checks `Phase >= Configured`; returns MCP tool error `"Project not configured. Call configure() first."` if not.
- `build()` checks `BuildInProgress`; returns MCP tool error `"Build already in progress"` if true.
- If `Dirty` is true (previous build was killed), the next `build()` runs with `--clean-first` (CMake) or `make clean && make` (Make) automatically, then clears `Dirty`.

```go
type Phase int

const (
    PhaseUnconfigured Phase = iota
    PhaseConfigured
    PhaseBuilt
)

type BuildState struct {
    Phase                 Phase
    LastBuildTime         time.Time
    LastSuccessfulBuildTime time.Time
    LastExitCode          int
    LastDuration          time.Duration
    Errors                []Diagnostic
    Warnings              []Diagnostic
    ErrorCount            int
    WarningCount          int
    DirtyFiles            []string
    Dirty                 bool  // true if previous build was killed (lock files may exist)
    BuildInProgress       bool
}

type Store struct {
    mu    sync.RWMutex
    state BuildState
}
```

#### MCP Tools (registered in main.go)

| Tool | Parameters | Response Shape |
|------|-----------|---------------|
| `build` | `targets?: string[]`, `jobs?: int` | `{ exit_code, error_count, warning_count, duration_ms, files_compiled }` |
| `get_errors` | — | `{ errors: []{file, line, col, severity, message, code} }` |
| `get_warnings` | `filter?: string` | `{ warnings: []{file, line, col, severity, message, code} }` |
| `get_build_graph` | — | `{ available, file_count, translation_units?: [{file, flags}], include_dirs?, reason? }` |
| `get_changed_files` | — | `{ files: string[], count, method: "git"\|"mtime" }` |
| `clean` | `targets?: string[]` | `{ success, message }` |
| `configure` | `cmake_args?: string[]` | `{ success, error_count, messages }` |

**`build` response notes:** `files_compiled` is parsed from Ninja's progress output (`[N/M]` lines) or Make's command echo. Returns 0 for cache-hit builds where nothing was recompiled. `duration_ms` is an integer (milliseconds).

**`get_warnings` filter semantics:** The `filter` parameter is a warning code prefix match. Examples: `filter: "-Wunused"` returns all `-Wunused-*` warnings; `filter: "src/core"` matches diagnostics whose `file` field contains that substring. The filter is applied as a case-insensitive substring match against both the `code` and `file` fields (OR logic).

#### MCP Resource

| Resource URI | Description |
|-------------|-------------|
| `build://health` | One-line terse summary based on state phase |

**`build_health` states:**
- `PhaseUnconfigured`: `"UNCONFIGURED: no build has run — call configure() then build()"`
- `PhaseConfigured` (no build yet): `"READY: configured, no build run yet — call build()"`
- `PhaseBuilt` (success): `"OK: 0 errors, 2 warnings, last build 30s ago"`
- `PhaseBuilt` (failure): `"FAIL: 5 errors, last build 10s ago"`
- `Dirty == true`: `"DIRTY: previous build was killed, next build will clean first"`

## Design Decisions

### Decision 1: mcp-go SDK (mark3labs/mcp-go)

**Context:** Need an MCP SDK for Go to handle protocol framing, tool/resource registration, and stdio transport.

**Options Considered:**
1. `github.com/mark3labs/mcp-go` — community SDK, widely used, simple API
2. `github.com/modelcontextprotocol/go-sdk` — official SDK, newer, more code snippets in docs
3. Hand-roll MCP JSON-RPC over stdio

**Decision:** Use `github.com/mark3labs/mcp-go` as specified in requirements.

**Rationale:** The project prompt explicitly requires this SDK. Its API is clean — `server.NewMCPServer()`, `s.AddTool()`, `s.AddResource()`, `server.ServeStdio(s)`. Tool handlers are `func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)` which maps naturally to our architecture. Supports `server.WithToolCapabilities(true)` and `server.WithResourceCapabilities(false, true)` for the capabilities we need.

### Decision 2: Builder as Interface with Factory

**Context:** Need to support CMake+Ninja as primary and Make as fallback, with testability.

**Options Considered:**
1. Single struct with conditional logic
2. Interface with separate implementations + factory
3. Strategy pattern with runtime switching

**Decision:** Interface with separate implementations + factory function.

**Rationale:** Clean separation of concerns. Each implementation encapsulates its own subprocess invocation and flag handling. Factory function selects based on config `generator` field or auto-detection (probe for `ninja --version`, fall back to `make --version`). Testable via a mock implementation. No over-engineering — just an interface, two implementations, one factory.

### Decision 3: Diagnostic Parsing Strategy Dispatch

**Context:** Different compilers produce diagnostics in different formats. Need to parse all of them into a common `[]Diagnostic`.

**Options Considered:**
1. Always use regex (universal but fragile)
2. Always require `-fdiagnostics-format=json` (not portable)
3. Dispatch based on toolchain: JSON parsing for Clang/GCC 10+, regex fallback for MSVC/legacy

**Decision:** Dispatch based on toolchain with JSON as the preferred path.

**Rationale:** Clang's `-fdiagnostics-format=json` produces structured output that is reliable and complete. GCC 10+ supports the same flag. For these toolchains, we inject the flag via `CMAKE_C_FLAGS`/`CMAKE_CXX_FLAGS` and parse the JSON directly. For MSVC or older GCC, we fall back to regex. The dispatcher in `parser.go` selects the strategy based on the configured/detected toolchain. This gives us the best accuracy where possible while maintaining broad compatibility.

### Decision 4: In-Memory State (No Persistence)

**Context:** Need to track build results between tool calls for `get_errors()`, `get_warnings()`, etc.

**Options Considered:**
1. In-memory `StateStore` with `sync.RWMutex`
2. SQLite for persistence across server restarts
3. File-based JSON state

**Decision:** In-memory `StateStore` with `sync.RWMutex`.

**Rationale:** The MCP server lifecycle matches the AI session lifecycle. State does not need to survive server restarts — when Claude starts a new session, a fresh build is appropriate anyway. In-memory keeps the implementation simple and dependency-free. `sync.RWMutex` allows concurrent reads from multiple tool queries while a build holds the write lock.

### Decision 5: Subprocess Management with context.Context

**Context:** Builds can take a long time. Need cancellation support and timeouts.

**Options Considered:**
1. `os/exec.Command` with manual timeout via goroutine + timer
2. `exec.CommandContext` with `context.WithTimeout`
3. External process management library

**Decision:** `exec.CommandContext` with `context.WithTimeout`.

**Rationale:** Standard library approach. The MCP handler receives a `context.Context` from the SDK, which we wrap with a timeout from config (`build_timeout`, default 5 minutes). If the AI session ends or a timeout fires, the build process is killed. No external dependencies needed.

**Kill cleanup:** Go's `exec.CommandContext` sends `SIGKILL` by default, which can leave behind lock files (`CMakeCache.txt.lock`, `.ninja_lock`) and partially-written object files. To mitigate: (1) On timeout/cancellation, set `BuildState.Dirty = true`. (2) The next `build()` call detects `Dirty == true` and runs with `--clean-first` (CMake) or `make clean && make` (Make) before proceeding, then clears `Dirty`. (3) Optionally, use `cmd.Cancel` (Go 1.20+) to send `SIGTERM` first with a grace period before `SIGKILL`, giving Ninja/Make time to clean up.

### Decision 6: Diagnostic Flag Injection

**Context:** Need to ensure compilers produce parseable output. Can't assume the user's CMakeLists.txt already includes diagnostic format flags.

**Options Considered:**
1. Require users to add flags to their CMakeLists.txt manually
2. Inject flags via `CMAKE_C_FLAGS`/`CMAKE_CXX_FLAGS` at configure time (CMake cache variables)
3. Inject flags via `CFLAGS`/`CXXFLAGS` environment variables at build time

**Decision:** Configure-time injection for CMake, build-time env vars for Make.

**Rationale:** The two build systems require different approaches:
- **CMake+Ninja:** Flags must be injected at configure time via `-DCMAKE_C_FLAGS` / `-DCMAKE_CXX_FLAGS` because CMake-generated Ninja files bake in compiler flags at configure time. Environment variables like `CFLAGS`/`CXXFLAGS` at build time have **no effect** on Ninja builds — Ninja does not re-read env vars. The injected flags are appended to existing cache values so user flags are preserved. These are sticky across rebuilds.
- **Make:** Environment variables `CFLAGS`/`CXXFLAGS` at build time work correctly because Make evaluates variables at invocation time. We append `-fdiagnostics-format=json` to the existing env var value.
- Both approaches are non-invasive — no user build files are modified. The config file allows users to disable injection (`"inject_diagnostic_flags": false`) if it conflicts with their setup.

### Decision 7: Token-Efficient Response Design

**Context:** All tool responses must be JSON and kept under ~500 tokens in the happy path.

**Options Considered:**
1. Return full compiler output and let the client truncate
2. Return pre-summarized results with counts, then detailed diagnostics on demand
3. Return everything structured but paginated

**Decision:** Two-tier approach: `build()` returns a summary (counts + exit code + duration), `get_errors()`/`get_warnings()` return the full diagnostic list.

**Rationale:** This matches how an AI agent works. First, it calls `build()` to see if the build passed. If it failed, it calls `get_errors()` to see what went wrong. The `build_health` resource provides an even cheaper check — a single line that Claude can read before deciding whether to call any tools at all. For `get_errors()`, we cap at 20 diagnostics by default (most builds have far fewer distinct errors) and deduplicate template instantiation noise.

### Decision 8: Changed Files Detection

**Context:** `get_changed_files()` needs to identify what changed since the last successful build.

**Options Considered:**
1. Git-only (`git diff --name-only` against last build commit)
2. Mtime-only (compare file mtimes against last build timestamp)
3. Git primary, mtime fallback

**Decision:** Git primary, mtime fallback.

**Rationale:** Most C++ projects are in git. `git diff --name-only` is fast and accurate. For non-git projects or when git is unavailable, fall back to walking the source directory (scoped to `source_dir`, excluding `build_dir`) and comparing mtimes against `BuildState.LastSuccessfulBuildTime`. The response includes a `method` field so the caller knows which detection was used.

**Mtime caveats:** The mtime method has known false-positive edge cases (editors that update mtime on open-without-save, generated headers touched by the build). The `method: "mtime"` field signals to the AI that the file list may include false positives and should be treated as approximate. After a failed build, `get_changed_files` uses `LastSuccessfulBuildTime` (not `LastBuildTime`) to report files changed since the last known-good state.

## Error Handling

**Build subprocess errors:** Non-zero exit codes from the compiler are expected (that's why we exist). These are captured as `BuildResult.ExitCode` and diagnostics are parsed from stderr. The tool returns structured data — not an MCP error. Only true failures (process spawn failure, timeout, I/O errors) return MCP errors.

**Parse failures:** If diagnostic parsing fails (unexpected format, corrupt output), we log via `slog.Warn`, store raw stderr as a single `Diagnostic` with `severity: "error"` and `message: "Failed to parse compiler output: <truncated raw stderr>"`, and still return a valid tool response. The AI can then fall back to reading raw output if needed.

**Configuration errors:** Missing build directory, missing cmake, missing ninja — these return MCP tool errors with actionable messages (e.g., `"cmake not found in PATH. Install cmake or set build_dir to an existing build directory"`).

**CMake configure failures:** CMake output is human-readable, not structured. Rather than building a full CMake output parser, `configure()` returns raw CMake output as a single `messages` string array (split on `CMake Error` / `CMake Warning` boundaries using simple prefix matching). CMake errors follow predictable patterns (`CMake Error at CMakeLists.txt:42 (find_package):`), so this lightweight splitting provides adequate structure for AI consumption without a dedicated parser. The `error_count` is derived from counting `CMake Error` prefixes. This is acceptable because configure failures are infrequent and the raw output is directly actionable.

**Timeout:** Build timeout produces a `Diagnostic` with `message: "Build timed out after Xs"` and `exit_code: -1`. The process is killed via context cancellation. Sets `BuildState.Dirty = true` so the next build runs a clean step first (see Decision 5).

**Concurrent access:** `StateStore` RWMutex ensures that a `build()` call (write lock) blocks `get_errors()` reads until the build completes. Multiple concurrent reads are allowed. If a `build()` is already running and another `build()` is called, the second call returns an MCP tool error `"Build already in progress"`. The `BuildInProgress` flag is checked under the write lock and set atomically before releasing the lock for the actual build (using a two-phase lock: set `BuildInProgress = true` under write lock, release lock, run build, re-acquire write lock to update results and clear `BuildInProgress`).

## Testing Strategy

**Unit tests:**
- `diagnostics/` — Table-driven tests for each parser with real compiler output samples. Test Clang JSON, GCC JSON, GCC regex, MSVC regex. Test malformed input gracefully.
- `state/` — Concurrent read/write tests using goroutines to verify mutex correctness.
- `config/` — Test JSON loading, env var overrides, defaults.
- `graph/` — Test `compile_commands.json` parsing with sample files.

**Integration tests:**
- `builder/` — Test with a real CMake project (small test fixture in `testdata/`). Verify that `Configure` + `Build` produces expected results. Skip via `os.LookPath("cmake")` + `t.Skip("cmake not found")` when tools are unavailable.
- End-to-end: Use `io.Pipe` pairs to create in-process stdin/stdout for the MCP server (no subprocess needed). Call `server.ServeStdio` in a goroutine with the pipe, then write JSON-RPC requests to one end and read responses from the other. This tests the full tool handler chain without needing a real build environment.

**Mock builder for tool handler tests:**
- Implement `Builder` interface as a mock that returns canned `BuildResult` values. Test that MCP handlers correctly format responses, handle edge cases (empty errors, zero warnings, timeouts, unconfigured state, build-in-progress guard).

### Structural Verification

Per Go conventions:
- `go vet ./...` on every change — catches printf format mismatches, unreachable code, suspicious constructs
- `-race` flag on all tests — the `StateStore` uses goroutines and shared state, making race detection critical
- `staticcheck ./...` if available — additional correctness checks beyond go vet

## Migration / Rollout

This is a greenfield project — no migration needed. Rollout plan:

1. **Phase 1 — Core loop:** `main.go` + `builder/cmake.go` + `diagnostics/clang.go` + `state/store.go` + `config/config.go`. This enables the `build()` -> `get_errors()` loop with Clang.
2. **Phase 2 — Full toolset:** `get_warnings()`, `get_build_graph()`, `get_changed_files()`, `clean()`, `configure()`, `build_health` resource.
3. **Phase 3 — Broad compatibility:** `diagnostics/gcc.go`, `diagnostics/regex.go`, `builder/make.go`.
4. **Phase 4 — Stretch:** `suggest_fix()`, watch mode, ninja stats, ccache detection.

### Claude Code Integration

Users add to their `claude_desktop_config.json` or `.mcp.json`:

```json
{
  "mcpServers": {
    "cpp-build": {
      "command": "go",
      "args": ["run", "."],
      "cwd": "/path/to/cpp-build-mcp"
    }
  }
}
```

Or after `go install`:

```json
{
  "mcpServers": {
    "cpp-build": {
      "command": "cpp-build-mcp"
    }
  }
}
```

The server reads `.cpp-build-mcp.json` from the current working directory of the C++ project being worked on.
