# cpp-build-mcp

An MCP server that wraps C++ build systems (CMake/Ninja/Make) and exposes structured, token-efficient tools for AI coding agents. Compiler output is pre-processed into structured diagnostics so raw build logs never enter the AI context window.

The core loop: **configure** -> **build** -> **get_errors** -> fix -> **build** — with each response under ~500 tokens.

## Architecture

```mermaid
graph TD
    AI[AI Agent / Claude] -->|MCP stdio| MAIN[main.go<br/>MCP Server]

    MAIN --> BUILD[builder/]
    MAIN --> DIAG[diagnostics/]
    MAIN --> STATE[state/]
    MAIN --> GRAPH[graph/]
    MAIN --> CHANGES[changes/]
    MAIN --> CONFIG[config/]

    BUILD --> CMAKE[cmake.go<br/>CMake + Ninja]
    BUILD --> MAKE[make.go<br/>GNU Make]
    BUILD --> DETECT[detect.go<br/>Auto-detection]

    DIAG --> CLANG[clang.go<br/>JSON parser]
    DIAG --> GCC[gcc.go<br/>JSON parser]
    DIAG --> REGEX[regex.go<br/>Fallback parser]

    STATE --> STORE[store.go<br/>Thread-safe state<br/>sync.RWMutex]

    CONFIG --> JSON_CFG[.cpp-build-mcp.json<br/>+ env var overrides]

    GRAPH --> CC[compile_commands.json<br/>reader / summarizer]

    style AI fill:#e1f0ff,stroke:#4a90d9
    style MAIN fill:#f0f0f0,stroke:#666
```

### Build Flow

```mermaid
sequenceDiagram
    participant Agent as AI Agent
    participant MCP as MCP Server
    participant Builder as Builder
    participant Parser as Diagnostic Parser
    participant Store as State Store

    Agent->>MCP: build(targets, jobs)
    MCP->>Store: StartBuild()
    Store-->>MCP: ok / error

    MCP->>Builder: Build(ctx, targets, jobs)
    Builder-->>MCP: BuildResult{stdout, stderr, exit_code}

    MCP->>Parser: Parse(toolchain, stdout, stderr)
    Parser-->>MCP: []Diagnostic

    MCP->>Store: FinishBuild(exit_code, errors, warnings)

    MCP-->>Agent: {exit_code, error_count, warning_count, duration_ms, files_compiled}

    opt exit_code != 0
        Agent->>MCP: get_errors()
        MCP->>Store: Errors()
        Store-->>MCP: []Diagnostic
        MCP-->>Agent: {errors: [{file, line, severity, message, ...}]}
    end
```

### State Machine

```mermaid
stateDiagram-v2
    [*] --> Unconfigured
    Unconfigured --> Configured : configure()
    Configured --> Built : build()
    Built --> Configured : clean()
    Built --> Built : build()

    Built --> Dirty : timeout / kill
    Dirty --> Configured : next build auto-cleans

    note right of Dirty
        SIGTERM sent first,
        SIGKILL after 3s grace.
        Next build runs --clean-first.
    end note
```

## Installation

```
go install github.com/danweinerdev/cpp-build-mcp@latest
```

Requires Go 1.22+ and a C++ build toolchain (CMake + Ninja, or GNU Make).

## Using with Claude Code

Add a `.mcp.json` file to your C++ project root:

```json
{
  "mcpServers": {
    "cpp-build": {
      "command": "cpp-build-mcp",
      "args": []
    }
  }
}
```

Then create a `.cpp-build-mcp.json` config (optional — defaults work for standard CMake projects):

```json
{
  "build_dir": "build",
  "generator": "ninja",
  "cmake_args": ["-DCMAKE_BUILD_TYPE=Debug"]
}
```

Once configured, Claude Code can use the build tools directly. A typical session looks like:

```
You: "Build the project and fix any errors"

Claude calls: configure()
  -> {success: true, error_count: 0, messages: []}

Claude calls: build()
  -> {exit_code: 1, error_count: 2, warning_count: 1, duration_ms: 1420, files_compiled: 5}

Claude calls: get_errors()
  -> {errors: [
       {file: "src/main.cpp", line: 42, column: 10, severity: "error",
        message: "no member named 'push' in 'std::vector<int>'"},
       {file: "src/util.cpp", line: 17, column: 5, severity: "error",
        message: "use of undeclared identifier 'result'"}
     ]}

Claude calls: suggest_fix(error_index: 0)
  -> {file: "src/main.cpp", start_line: 32, end_line: 52, source: "...", diagnostic: {...}}

Claude fixes the code, then:

Claude calls: build()
  -> {exit_code: 0, error_count: 0, warning_count: 0, duration_ms: 380, files_compiled: 2}
```

The two-step design (`build` then `get_errors`) is intentional — `build` returns a compact summary so successful builds (the common case) cost minimal tokens. Full diagnostics are fetched only when needed.

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "cpp-build": {
      "command": "cpp-build-mcp",
      "args": [],
      "cwd": "/path/to/your/cpp/project"
    }
  }
}
```

## Tools

| Tool | Parameters | Response |
|------|-----------|----------|
| `configure` | `cmake_args?: string[]` | `{success, error_count, messages}` |
| `build` | `targets?: string[], jobs?: number` | `{exit_code, error_count, warning_count, duration_ms, files_compiled}` |
| `get_errors` | _(none)_ | `{errors: [{file, line, column, severity, message, code}]}` |
| `get_warnings` | `filter?: string` | `{warnings: [...], count}` |
| `suggest_fix` | `error_index: number` | `{file, start_line, end_line, source, diagnostic}` |
| `clean` | `targets?: string[]` | `{success, message}` |
| `get_changed_files` | _(none)_ | `{files, count, method}` |
| `get_build_graph` | _(none)_ | `{available, file_count, translation_units, include_dirs}` |

**Resource:** `build://health` — one-line status string: `OK`, `FAIL`, `READY`, `UNCONFIGURED`, or `DIRTY`.

### Tool Details

**`build`** — Runs an incremental build. Parses Ninja `[N/M]` progress lines to report `files_compiled`. If the previous build was killed (dirty state), automatically cleans first. Build timeout is configurable (default 5 minutes); on timeout, sends SIGTERM with a 3-second grace period before SIGKILL.

**`get_errors`** — Returns up to 20 structured diagnostics from the last build. Each entry includes file path, line, column, severity, message, and diagnostic code. Errors are parsed from JSON (Clang/GCC 10+) or regex (MSVC/legacy).

**`suggest_fix`** — Given an error index, reads the source file and returns +/-10 lines of context around the error location. Useful for understanding the code around a diagnostic without reading the entire file.

**`get_changed_files`** — Detects files changed since the last successful build using `git diff` (preferred) or mtime comparison (fallback). The `method` field indicates which detection was used.

**`get_build_graph`** — Summarizes `compile_commands.json`: file count, translation units, include directories. Returns `available: false` for Make projects or unconfigured CMake projects.

## Configuration

`.cpp-build-mcp.json` in the project root. All fields optional:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `build_dir` | string | `"build"` | Build output directory |
| `source_dir` | string | `"."` | Source directory |
| `toolchain` | string | `"auto"` | `"auto"`, `"clang"`, `"gcc"`, `"msvc"` |
| `generator` | string | `"ninja"` | `"ninja"` or `"make"` |
| `cmake_args` | string[] | `[]` | Extra CMake configure arguments |
| `build_timeout` | string | `"5m"` | Max build duration (Go duration format) |
| `inject_diagnostic_flags` | bool | `true` | Inject `-fdiagnostics-format=json` |
| `diagnostic_serial_build` | bool | `false` | Force `-j1` for cleaner diagnostic output |

### Environment variable overrides

These take precedence over the config file:

- `CPP_BUILD_MCP_BUILD_DIR`
- `CPP_BUILD_MCP_SOURCE_DIR`
- `CPP_BUILD_MCP_TOOLCHAIN`
- `CPP_BUILD_MCP_GENERATOR`
- `CPP_BUILD_MCP_BUILD_TIMEOUT`

## Supported Toolchains

```mermaid
graph LR
    AUTO[toolchain: auto] --> CC[compile_commands.json]
    CC -->|contains clang| CLANG[Clang JSON Parser]
    CC -->|contains gcc/g++| VER{GCC version?}
    VER -->|>= 10| GCC_JSON[GCC JSON Parser]
    VER -->|< 10| REGEX[Regex Fallback]
    CC -->|contains cl.exe| REGEX
    CC -->|not found| SYS[System compiler probe]
    SYS --> CLANG
    SYS --> GCC_JSON
    SYS --> REGEX

    style CLANG fill:#d4edda,stroke:#28a745
    style GCC_JSON fill:#d4edda,stroke:#28a745
    style REGEX fill:#fff3cd,stroke:#ffc107
```

| Toolchain | Parser | Diagnostic Source |
|-----------|--------|-------------------|
| Clang | JSON (`-fdiagnostics-format=json`) | stdout |
| GCC 10+ | JSON (`-fdiagnostics-format=json`) | stdout |
| GCC < 10 | Regex fallback (auto-detected) | stderr |
| MSVC | Regex fallback | stderr |

When `toolchain` is `"auto"` (default), the server inspects `compile_commands.json` and probes the system compiler to select the best parser. GCC < 10 is automatically detected via version probing, and diagnostic flag injection is disabled to avoid passing unsupported flags.

## How It Works

The server sits between the AI agent and the build system. Instead of the agent seeing raw compiler output like:

```
/home/user/project/src/main.cpp:42:10: error: no member named 'push' in 'std::vector<int>'
    vec.push(42);
        ^~~~
/home/user/project/src/main.cpp:42:10: note: did you mean 'push_back'?
```

It receives structured JSON:

```json
{"exit_code": 1, "error_count": 1, "warning_count": 0, "duration_ms": 820, "files_compiled": 3}
```

And only fetches the detail it needs:

```json
{"errors": [{"file": "src/main.cpp", "line": 42, "column": 10, "severity": "error",
  "message": "no member named 'push' in 'std::vector<int>'"}]}
```

This keeps the AI context window clean — no multi-page build logs, no ANSI color codes, no duplicated template instantiation noise (GCC children are capped at depth 3).
