---
title: "Configure Tool Rename"
type: research
status: archived
created: 2026-03-09
updated: 2026-03-10
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

**Option D: Rename + new configure + build reconfigure flag** ⭐
- Rename current `configure` → `load_presets`
- Add a new `configure` tool that actually runs `cmake --preset <preset>` to generate build files
- Add a `reconfigure` parameter to `build` that re-runs cmake configure before building
- Gives full control: load presets, configure standalone, or reconfigure-and-build in one step

## Considerations

- Removing `configure` may break existing workflows where clients call configure → build sequentially
- A real `configure` tool would let users run cmake configure without building, which is useful for diagnosing cmake-level errors (like the "Unknown CMake command" issue)
- `load_presets` is more accurate but less discoverable — consider `reload_config` as an alternative name
- Option D provides the most flexibility: `load_presets` for config management, `configure` for cmake-only runs, and `build --reconfigure` for the common "something changed, rebuild from scratch" workflow

## Resolution

**Option D implemented** (Plan: ConfigureToolRefactor) with an important adjustment:

The research note's description of the current `configure` tool was outdated — by the time implementation began, `configure` already ran cmake (via `builder.Configure()`). So no rename was needed.

What was actually built:
- **`load_presets`** — New tool that reloads configs/presets from disk mid-session (re-reads `.cpp-build-mcp.json` and `CMakePresets.json`, diffs against registry, preserves state for unchanged configs)
- **`configure`** — Kept as-is (already runs cmake correctly)
- **`build --reconfigure`** — New boolean parameter that runs cmake configure before building, making a prior `configure` call optional
