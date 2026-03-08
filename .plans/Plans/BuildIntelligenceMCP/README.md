---
title: "Build Intelligence MCP Server"
type: plan
status: active
created: 2026-03-07
updated: 2026-03-07

tags: [go, mcp, cpp, build-system, diagnostics]
related: [Designs/BuildIntelligenceMCP]
phases:
  - id: 1
    title: "Foundation"
    status: complete
    doc: "01-Foundation.md"
  - id: 2
    title: "Diagnostics and Core Loop"
    status: complete
    doc: "02-Diagnostics-Core-Loop.md"
    depends_on: [1]
  - id: 3
    title: "Full Toolset"
    status: complete
    doc: "03-Full-Toolset.md"
    depends_on: [2]
  - id: 4
    title: "Broad Compiler Compatibility"
    status: complete
    doc: "04-Broad-Compatibility.md"
    depends_on: [2]
  - id: 5
    title: "Polish"
    status: planned
    doc: "05-Polish.md"
    depends_on: [3, 4]
---

# Build Intelligence MCP Server

## Overview

Implement an MCP server in Go that wraps C++ build systems (CMake/Ninja/Make) and exposes structured, token-efficient tools for AI coding agents. The server pre-processes compiler output into structured diagnostics so raw build logs never enter the AI context window.

The primary success metric: a Claude Code session can run `build()` -> `get_errors()` -> fix -> `build()` in a tight loop, with each tool response under ~500 tokens.

## Architecture

See `Designs/BuildIntelligenceMCP/README.md` for the full architecture. Key components:

- **main.go** — MCP server entrypoint (mcp-go SDK, stdio transport)
- **builder/** — `Builder` interface with CMake and Make implementations
- **diagnostics/** — Parser dispatch: Clang JSON (stdout), GCC JSON, regex fallback
- **graph/** — `compile_commands.json` reader/summarizer
- **state/** — Thread-safe `Store` with phase machine (Unconfigured -> Configured -> Built)
- **config/** — `.cpp-build-mcp.json` + env var overrides

## Key Decisions

1. **mcp-go SDK** — `github.com/mark3labs/mcp-go` as required by project spec
2. **Builder interface** — Swappable backends, testable via mock
3. **Diagnostic dispatch** — JSON for Clang/GCC 10+, regex for MSVC/legacy
4. **In-memory state** — No persistence; server lifecycle = session lifecycle
5. **Flag injection** — Configure-time for CMake, env vars for Make
6. **Two-tier responses** — `build()` returns summary, `get_errors()` returns details

## Dependencies

- Go 1.22+
- `github.com/mark3labs/mcp-go` — MCP SDK
- CMake + Ninja (primary build target) or GNU Make (fallback)
- Clang or GCC 10+ for JSON diagnostic output
- No other external Go dependencies (stdlib only beyond mcp-go)
