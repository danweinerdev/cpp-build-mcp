---
title: "Configure Tool Refactor"
type: plan
status: complete
created: 2026-03-10
updated: 2026-03-10
tags: [api, naming, ux, configure, presets, build]
related: [Research/ConfigureToolRename]
phases:
  - id: 1
    title: "load_presets Tool"
    status: complete
    doc: "01-Load-Presets-Tool.md"
  - id: 2
    title: "build --reconfigure Parameter"
    status: complete
    doc: "02-Build-Reconfigure.md"
    depends_on: [1]
  - id: 3
    title: "Integration Tests & Polish"
    status: complete
    doc: "03-Integration-Polish.md"
    depends_on: [2]
---

# Configure Tool Refactor

## Overview

Implements Option D from the ConfigureToolRename research: add a `load_presets` tool for mid-session config reload, add a `reconfigure` parameter to `build` for cmake-then-build in one step, and keep the existing `configure` tool as-is (it already runs cmake correctly).

The result is a three-tool workflow:
- **`load_presets`** — reload configs/presets from disk without restarting the server
- **`configure`** — run cmake configure standalone (for diagnosing cmake errors)
- **`build --reconfigure`** — re-run cmake configure before building (convenience for the common "something changed" case)

## Architecture

### Current State
- `configure` tool runs `builder.Configure()` which invokes cmake. Sets `PhaseConfigured`.
- `build` tool requires `PhaseConfigured`. Runs `cmake --build`. Does NOT auto-configure.
- Config/preset loading happens once at startup in `main()` via `config.LoadMulti(".")`. No mid-session reload.

### Changes
1. **`load_presets`**: New MCP tool + handler. Calls `config.LoadMulti(".")`, diffs against the current registry, and updates it. Existing configs with unchanged settings preserve their state store. New configs get fresh stores. Removed configs are dropped.
2. **`build --reconfigure`**: New boolean parameter. When true, runs `builder.Configure()` + `store.SetConfigured()` before the existing build flow. Handles the unconfigured case (no prior `configure` call needed).
3. **`configure`**: No changes. Already does the right thing.

### Registry Reload Strategy
`load_presets` rebuilds the registry by comparing fresh `config.LoadMulti` output against existing instances:
- **Unchanged config** (same BuildDir, Generator, Preset, Toolchain): preserve existing `configInstance` (keeps state store, builder)
- **Changed config** (settings differ): create new builder, reset state store
- **New config**: create fresh `configInstance`
- **Removed config**: drop from registry (warn if it was the default)

## Key Decisions

1. **Keep `configure` as-is** — The research note described an outdated state where `configure` only loaded config. The current implementation already runs cmake, so no rename is needed.
2. **`load_presets` reloads the full registry** — Rather than a read-only discovery tool, it actively updates the registry so new presets are immediately usable.
3. **`reconfigure` is a boolean, not a separate tool** — Keeps the tool count manageable. The standalone `configure` tool already exists for cmake-only runs.
4. **Preserve state for unchanged configs** — Avoids losing build history when reloading presets that didn't change.

## Dependencies

- No external dependencies. All changes are internal to the MCP server.
- The `config.LoadMulti` function already handles all config/preset loading logic.
