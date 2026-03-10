---
title: "Configure Tool Rename"
type: research
status: draft
created: 2026-03-09
tags: [api, naming, ux, configure, presets]
---

# Configure Tool Rename

## Problem

The `configure` MCP tool is confusingly named. Users expect "configure" to mean "run `cmake` to generate the build environment" (the standard CMake meaning), but the current tool actually loads/validates the config (presets, source dir, build dir, etc.) and sets internal state to "configured".

## Current Behavior

- `configure` — loads config, validates paths, sets state. Does NOT invoke cmake.
- `build` — invokes cmake configure + build internally (cmake is run as part of the build step).

## Proposed Changes

**Option A: Rename only**
- Rename `configure` → `load_presets` (reflects what it actually does)
- Keep `build` as-is

**Option B: Rename + new configure**
- Rename current `configure` → `load_presets`
- Add a new `configure` tool that actually runs `cmake --preset <preset>` to generate Ninja/Makefile build files
- `build` continues to also trigger cmake configure if needed

**Option C: Remove configure entirely**
- Remove `configure` since `build` already handles cmake configure internally
- Presets are loaded automatically from `planning-config.json` / CMakePresets.json on server startup

## Considerations

- Removing `configure` may break existing workflows where clients call configure → build sequentially
- A real `configure` tool would let users run cmake configure without building, which is useful for diagnosing cmake-level errors (like the "Unknown CMake command" issue)
- `load_presets` is more accurate but less discoverable — consider `reload_config` as an alternative name
