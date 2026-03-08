# cpp-build-mcp

An MCP server in Go that wraps C++ build systems (CMake/Ninja/Make) and exposes structured, token-efficient tools for AI coding agents. Pre-processes compiler output into structured diagnostics so raw build logs never enter the AI context window.

## Installation

```
go install github.com/danweinerdev/cpp-build-mcp@latest
```

## Configuration

Create a `.cpp-build-mcp.json` file in your project root. All fields are optional with sensible defaults:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `build_dir` | string | `"build"` | Path to the build output directory |
| `source_dir` | string | `"."` | Path to the source directory |
| `toolchain` | string | `"auto"` | Compiler toolchain: `"auto"`, `"clang"`, `"gcc"`, `"msvc"` |
| `generator` | string | `"ninja"` | Build system generator: `"ninja"`, `"make"` |
| `cmake_args` | string[] | `[]` | Additional arguments passed to CMake configure |
| `build_timeout` | string | `"5m"` | Maximum build duration (Go duration format) |
| `inject_diagnostic_flags` | bool | `true` | Inject `-fdiagnostics-format=json` for structured output |
| `diagnostic_serial_build` | bool | `false` | Force single-threaded builds for cleaner diagnostic output |

Example:

```json
{
  "build_dir": "build",
  "generator": "ninja",
  "cmake_args": ["-DCMAKE_BUILD_TYPE=Debug"]
}
```

### Environment variable overrides

Environment variables take precedence over the config file:

- `CPP_BUILD_MCP_BUILD_DIR`
- `CPP_BUILD_MCP_SOURCE_DIR`
- `CPP_BUILD_MCP_TOOLCHAIN`
- `CPP_BUILD_MCP_GENERATOR`
- `CPP_BUILD_MCP_BUILD_TIMEOUT`

## Integration

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "cpp-build": {
      "command": "cpp-build-mcp",
      "args": [],
      "cwd": "/path/to/your/project"
    }
  }
}
```

### Claude Code

Add a `.mcp.json` file to your project root:

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

## Tool Reference

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
| `build://health` _(resource)_ | _(read)_ | One-line status: `OK`, `FAIL`, `READY`, `UNCONFIGURED`, or `DIRTY` |

## Worked Example

A typical AI agent workflow using these tools:

```
1. configure()
   -> {success: true, error_count: 0, messages: []}

2. build()
   -> {exit_code: 1, error_count: 2, warning_count: 1, duration_ms: 1420, files_compiled: 5}

3. get_errors()
   -> {errors: [
        {file: "src/main.cpp", line: 42, column: 10, severity: "error",
         message: "no member named 'push' in 'std::vector<int>'", code: ""},
        {file: "src/util.cpp", line: 17, column: 5, severity: "error",
         message: "use of undeclared identifier 'result'", code: ""}
      ]}

4. Fix the reported errors in source files.

5. build()
   -> {exit_code: 0, error_count: 0, warning_count: 0, duration_ms: 380, files_compiled: 2}
```

The two-step design (`build` then `get_errors`) is intentional. The `build` tool returns a compact summary with counts and timing. Only when errors exist does the agent call `get_errors` to retrieve the full diagnostics. This keeps token usage low on successful builds -- the common case.

## Supported Toolchains

| Toolchain | Diagnostic Parsing |
|-----------|--------------------|
| Clang | JSON diagnostics (`-fdiagnostics-format=json`) |
| GCC 10+ | JSON diagnostics (`-fdiagnostics-format=json`) |
| GCC < 10 | Regex fallback (auto-detected, diagnostic flag injection disabled) |
| MSVC | Regex fallback |

When `toolchain` is set to `"auto"` (the default), the server inspects `compile_commands.json` and the system compiler to determine which parser to use.
