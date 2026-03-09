---
title: "Large Project Friction Points"
type: research
status: draft
created: 2026-03-08
tags: [presets, scale, fusion, real-world]
related: [Plans/CMakePresetsIntegration, Designs/CMakePresetsIntegration]
---

# Large Project Friction Points

## Context

Analysis of how cpp-build-mcp would handle a production-scale C++ project (Perpetual Fusion — 646 .cpp files, 3,420 headers, 68 CMakeLists.txt files, 19 configure presets, 21 build presets, 10 test presets, 21 git submodules, multi-platform with sanitizer variants).

## Immediate Issues

### 1. Multi-config generator noise
The project has Visual Studio presets (multi-config generator). `isMultiConfigGenerator` correctly filters them, but emits `slog.Info` for each skipped preset. With several VS presets, startup logs get noisy.

### 2. Build presets and test presets ignored
cpp-build-mcp only reads `configurePresets`. The project has 21 build presets and 10 test presets. Build presets specify targets, jobs, and config — currently the `build` tool accepts targets/jobs directly so build presets aren't critical, but test presets could be valuable for a future `test` tool.

### 3. Too many configs loaded simultaneously
Sanitizer build types (Asan, Tsan, Ubsan) are configure presets with custom `CMAKE_BUILD_TYPE` values. With Ninja (single-config generator), they pass through discovery. That's potentially 12+ configs loaded at once. A developer typically only works with 2-3 at a time.

### 4. `$env{}` / `$penv{}` macros in binaryDir
If any presets use environment variable macros in `binaryDir`, `expandBinaryDir` skips them with a warning. Common in CI-oriented presets where build output goes to an env-specified directory.

### 5. `include` field (v4+) not followed
If the presets file uses `include` to pull in platform-specific preset fragments, cpp-build-mcp detects but doesn't follow them. Some presets would be silently missing from discovery.

### 6. Git submodule churn in change detection
21 git submodules in External/. `get_changed_files` uses mtime-based detection, which could be noisy if submodules are updated (touching many files under External/).

## Potential Improvements

### Preset filtering (Small effort)
Let users exclude presets by pattern in `.cpp-build-mcp.json`. Example:

```json
{
  "exclude_presets": ["*-asan", "*-tsan", "*-ubsan", "windows-*"],
  "default_config": "linux-clang-debug"
}
```

Or the inverse — `include_presets` for an allowlist approach. This would let users scope down to 2-3 active configs without needing a full `configs` map (which suppresses preset discovery entirely).

### `include` support (Medium effort)
Follow `include` fields to discover presets in separate files. Requires recursive file reading with cycle detection. Enables projects that split presets across platform-specific files.

### Build presets pass-through (Medium effort)
Pass `--build --preset <name>` instead of manual target/jobs construction. New field on Config, new branch in `buildBuildArgs`. Low priority since the `build` tool's direct target/jobs parameters work fine.

### Source directory filtering for change detection (Small effort)
Allow `exclude_paths` in config to skip External/, third_party/, etc. from `get_changed_files`. Reduces noise from dependency updates.

### Default config heuristic (Small effort)
Instead of alphabetical-first, detect the current platform and prefer a matching preset (e.g., on Linux, prefer `linux-*-debug` over `darwin-*`). Already solvable via `default_config` in `.cpp-build-mcp.json`, so this is convenience only.

## Priority Assessment

| Improvement | Impact | Effort | Priority |
|------------|--------|--------|----------|
| Preset filtering | High — makes large preset files usable | Small | **Do first** |
| Source dir filtering for changes | Medium — reduces noise | Small | Second |
| `include` support | Medium — enables split preset files | Medium | Third |
| Build presets | Low — manual targets work fine | Medium | Defer |
| Default config heuristic | Low — `default_config` exists | Small | Defer |

## Open Questions

- How common are `include`-based preset files in real-world projects? Need more data points beyond Fusion.
- Should preset filtering use glob patterns, regex, or simple prefix/suffix matching?
- Should excluded presets still appear in `list_configs` with a "filtered" status, or be completely hidden?
- Is there demand for a `test` tool that uses testPresets?
