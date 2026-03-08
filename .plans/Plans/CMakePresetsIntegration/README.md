---
title: "CMake Presets Integration"
type: plan
status: complete
created: 2026-03-08
updated: 2026-03-08
tags: [go, cmake, presets, zero-config, auto-discovery]
related: [Designs/CMakePresetsIntegration, Research/CMakePresetsIntegration, Plans/MultiConfigSupport]
phases:
  - id: 1
    title: "Preset Pass-Through"
    status: complete
    doc: "01-Preset-Pass-Through.md"
  - id: 2
    title: "Auto-Discovery"
    status: complete
    doc: "02-Auto-Discovery.md"
    depends_on: [1]
  - id: 3
    title: "Polish and Edge Cases"
    status: complete
    doc: "03-Polish-And-Edge-Cases.md"
    depends_on: [2]
---

# CMake Presets Integration

## Overview

Eliminate mandatory configuration for CMake projects that already define build configurations via `CMakePresets.json`. After this plan, cpp-build-mcp auto-discovers build configurations from presets files and uses `cmake --preset` at configure time, making `.cpp-build-mcp.json` optional.

The guiding principle: the user should not have to tell cpp-build-mcp what cmake already knows.

**Config source chain (long-term architecture):**

```
1. CMakePresets.json + CMakeUserPresets.json  (auto-discovered)
2. .cpp-build-mcp.json                        (optional overrides)
3. Environment variables                       (single-config only)
4. Built-in defaults                           (always)
```

## Architecture

The implementation has two key mechanisms:

1. **Minimal preset parsing** — `loadPresetsMetadata` extracts only preset names, `binaryDir`, and `generator` from `CMakePresets.json`. Inherits chains are resolved only for these two fields. Macros are expanded only for `${sourceDir}` and `${presetName}`. Everything else is delegated to cmake.

2. **`cmake --preset` at configure time** — When `Config.Preset` is set, `buildConfigureArgs` emits `cmake --preset <name>` instead of constructing `-S`/`-B`/`-G` flags manually. cmake handles inherits resolution, cacheVariables, conditions, and macro expansion natively. cpp-build-mcp appends `CMAKE_EXPORT_COMPILE_COMMANDS=ON` and diagnostic flags.

The existing `configRegistry` from MultiConfigSupport accepts `*config.Config` values however they are sourced — no registry changes needed.

See `Designs/CMakePresetsIntegration/README.md` for the full design.

## Key Decisions

1. **Parse presets minimally, delegate to cmake --preset** — cpp-build-mcp only needs names and binaryDir. cmake handles the rest.
2. **`CMakeArgs` passed through in preset mode** — user-specified `-D` flags from `.cpp-build-mcp.json` are appended after `--preset`. Research confirmed `-D` flags override preset cacheVariables.
3. **Generator normalization is two-way** — presets use cmake names (`"Ninja"`), Config uses lowercase (`"ninja"`). Maps in both directions are needed: `config/` normalizes preset→config, `builder/` maps config→cmake for the `-G` flag.
4. **`configs` map in .cpp-build-mcp.json suppresses preset discovery** — explicit config declarations take full precedence. Presets are only auto-discovered when no `configs` map exists.
5. **CMakeUserPresets.json read by default** — matches cmake behavior. User presets shadow project presets by name.
6. **Condition evaluation deferred** — platform-incompatible presets surface in `list_configs`; cmake error at configure time is the feedback mechanism.
7. **`include` field (v4+) detected but not followed** — cpp-build-mcp does not recursively read included preset files. When `include` is present, a warning is emitted so users know some presets may not be discovered. Full `include` support is a future enhancement.

## Dependencies

- Completed MultiConfigSupport Phase 1 (Registry Foundation) and Phase 2 (Multi-Config File Format)
- Sequenced before MultiConfigSupport Phase 3 (Integration and Polish) — both modify `main_test.go` and response structs, so sequential execution avoids merge conflicts
- No new external Go dependencies
- CMake 3.19+ required for `--preset` support (3.21+ for presets v3 features)
