---
title: "CMake Presets Integration"
type: design
status: approved
created: 2026-03-07
updated: 2026-03-08
tags: [go, cmake, presets, zero-config, auto-discovery]
related: [Designs/MultiConfigSupport, Research/CMakePresetsIntegration]
---

# CMake Presets Integration

## Overview

Eliminate mandatory configuration for CMake projects that already define build configurations via `CMakePresets.json`. Today, users must create a `.cpp-build-mcp.json` file and duplicate information their project already declares. This design makes `.cpp-build-mcp.json` optional — cpp-build-mcp auto-discovers build configurations from `CMakePresets.json` and uses `cmake --preset` at configure time, letting cmake handle all preset complexity natively.

The guiding principle: **the user should not have to tell cpp-build-mcp what cmake already knows.**

## Current State

Config loading follows a single path:

```
.cpp-build-mcp.json  →  config.LoadMulti(dir)  →  map[string]*Config  →  registry
```

If the file is absent, a single "default" config with built-in defaults is used. If present, configs are parsed from either a flat format (single config) or a `configs` map (multi-config with inheritance). Either way, the user writes the file.

`CMakeBuilder.buildConfigureArgs` constructs arguments manually:

```
cmake -S . -B build/debug -G Ninja -DCMAKE_EXPORT_COMPILE_COMMANDS=ON ...
```

There is no awareness of `CMakePresets.json` or `cmake --preset`.

### Known Issues

- `buildConfigureArgs` hardcodes `"-G", "Ninja"` regardless of `cfg.Generator` (`cmake.go:64`). This must be fixed independently — it affects non-preset builds today.

## Architecture

### Config Source Chain

Configuration is resolved through a layered chain where each source provides what it knows:

```
┌─────────────────────────────────────────────────┐
│                 Config Source Chain               │
│                                                   │
│  1. CMakePresets.json + CMakeUserPresets.json      │
│     → preset names, binaryDir, generator          │
│     → auto-discovered; provides build configs     │
│                                                   │
│  2. .cpp-build-mcp.json                           │
│     → build_timeout, inject_diagnostic_flags,     │
│       diagnostic_serial_build, preset overrides   │
│     → optional; provides server-specific tuning   │
│                                                   │
│  3. Environment variables                         │
│     → single-config mode only (existing behavior) │
│                                                   │
│  4. Built-in defaults                             │
│     → always; fills gaps                          │
│                                                   │
│  Priority: 2 overrides 1, 3 overrides 2 (single) │
└─────────────────────────────────────────────────┘
```

### Resolution Logic in LoadMulti

```
LoadMulti(dir string) (map[string]*Config, string, error)

1. Check for .cpp-build-mcp.json
   ├─ Has "configs" map?  → multi-config from JSON (existing path, no presets)
   └─ No "configs" map?   → continue to step 2

2. Check for CMakePresets.json (and CMakeUserPresets.json)
   ├─ Found and has usable presets?
   │     → create per-preset Config entries (multi-config, env vars suppressed)
   │     → merge with .cpp-build-mcp.json top-level fields if present
   │     → store preset name on each Config for --preset invocation
   ├─ Found but zero usable presets (all hidden/filtered/skipped)?
   │     → slog.Warn("CMakePresets.json found but no usable configure presets")
   │     → fall through to step 3 (single "default" config)
   └─ Not found?
        → continue to step 3

3. Single "default" config path
   ├─ .cpp-build-mcp.json exists?  → apply JSON fields
   ├─ Apply env vars (single-config only)
   └─ return {"default": cfg}, "default"

4. Fill remaining fields from defaults()
```

**Env var rule:** Environment variables apply only in single-config mode (step 3). When presets produce multiple configs (step 2, first branch), env vars are suppressed with `slog.Warn` — same as the existing `configs` map behavior. When presets are found but all are skipped, the fallback to single-config mode re-enables env vars because the effective result is a single config.

The key insight: **when presets are discovered, cpp-build-mcp only needs two things from the presets file: the preset name (for `cmake --preset`) and the resolved `binaryDir` (for finding `compile_commands.json`).** Everything else — inherits chains, cacheVariables, conditions, macro expansion — is handled by cmake itself when `--preset` is invoked.

### Components

```
┌──────────────────────────────────────────────────────────┐
│                      config/config.go                      │
│                                                            │
│  LoadMulti(dir) ─┬─ loadPresetsMetadata(dir)               │
│                  │   ├─ parse CMakePresets.json             │
│                  │   ├─ parse CMakeUserPresets.json         │
│                  │   ├─ resolve inherits (for binaryDir)   │
│                  │   ├─ expand ${sourceDir}, ${presetName} │
│                  │   ├─ filter: skip hidden, skip multi-   │
│                  │   │         config generators            │
│                  │   └─ return []PresetMetadata             │
│                  │                                         │
│                  ├─ merge with .cpp-build-mcp.json         │
│                  │   (server-specific overrides)            │
│                  │                                         │
│                  └─ return map[string]*Config + default     │
│                                                            │
│  Config struct gains: Preset string                        │
│  (empty = no preset, non-empty = use --preset)             │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│                    builder/cmake.go                        │
│                                                            │
│  buildConfigureArgs(extraArgs) ──┐                         │
│     if cfg.Preset != "":         │                         │
│       ["--preset", name,         │ cmake handles inherits, │
│        "-DCMAKE_EXPORT_..=ON",   │ cacheVars, macros,      │
│        ...diagFlags,             │ generator, binaryDir    │
│        ...extraArgs]             │                         │
│     else:                        │                         │
│       ["-S", "-B", "-G",         │ existing manual path    │
│        "-D...", ...extraArgs]    │                         │
│                                  │                         │
│  Fix: -G uses cfg.Generator,    │                         │
│       not hardcoded "Ninja"      │                         │
└──────────────────────────────────────────────────────────┘
```

### PresetMetadata

A minimal struct for the data cpp-build-mcp needs from presets — deliberately not a full preset representation:

```go
// PresetMetadata holds the minimal information cpp-build-mcp needs from
// a CMake configure preset. Full preset semantics (cacheVariables, inherits
// resolution, condition evaluation) are delegated to cmake --preset.
type PresetMetadata struct {
    Name      string // preset name (used in cmake --preset <name>)
    BinaryDir string // resolved binaryDir (used as build_dir)
    Generator string // generator name, if specified (for filtering)
}
```

### Data Flow

**Startup (auto-discovery):**

```
CMakePresets.json
     │
     ▼
loadPresetsMetadata(dir)
     │  parse → resolve inherits → expand macros → filter
     ▼
[]PresetMetadata{
    {Name:"debug",   BinaryDir:"build/debug",   Generator:"Ninja"},
    {Name:"release", BinaryDir:"build/release",  Generator:"Ninja"},
}
     │
     ▼
merge with .cpp-build-mcp.json (if exists)
     │  apply build_timeout, inject_diagnostic_flags, etc.
     ▼
map[string]*Config{
    "debug":   {BuildDir:"build/debug",   Preset:"debug",   ...defaults...},
    "release": {BuildDir:"build/release", Preset:"release", ...defaults...},
}
     │
     ▼
registry  →  per-config Builder + Store + resolveToolchain
```

**Configure invocation (with preset):**

```
configure(config: "release")
     │
     ▼
CMakeBuilder.buildConfigureArgs()
     │  cfg.Preset == "release"
     ▼
cmake --preset release -DCMAKE_EXPORT_COMPILE_COMMANDS=ON [-fdiag flags]
     │
     ▼
cmake reads CMakePresets.json natively, resolves everything, configures
     │
     ▼
build/release/compile_commands.json  ←  cpp-build-mcp finds this via cfg.BuildDir
```

### Config Struct Change

```go
type Config struct {
    BuildDir              string        `json:"build_dir"`
    SourceDir             string        `json:"source_dir"`
    Toolchain             string        `json:"toolchain"`
    Generator             string        `json:"generator"`
    CMakeArgs             []string      `json:"cmake_args"`
    BuildTimeout          time.Duration `json:"build_timeout"`
    InjectDiagnosticFlags bool          `json:"inject_diagnostic_flags"`
    DiagnosticSerialBuild bool          `json:"diagnostic_serial_build"`
    Preset                string        `json:"preset"` // NEW: cmake preset name, empty = manual configure
}
```

The `Preset` field is set:
- From auto-discovery: preset name from `CMakePresets.json`
- From `.cpp-build-mcp.json`: explicit `"preset": "debug"` in a config entry
- Empty: existing manual configure path (backward compatible)

The `configJSON` struct also gains `Preset *string` so `applyJSON` can read the field from `.cpp-build-mcp.json`:

```go
type configJSON struct {
    // ...existing fields...
    Preset *string `json:"preset"`
}
```

A top-level `preset` field in `.cpp-build-mcp.json` (outside a `configs` map) is supported: it applies to the single default config and invokes `cmake --preset` at configure time.

### Generator Normalization

CMakePresets.json uses cmake's full generator names (`"Ninja"`, `"Unix Makefiles"`). `Config.Generator` uses normalized lowercase names (`"ninja"`, `"make"`). The `loadPresetsMetadata` function normalizes generator names before returning:

```go
var generatorMap = map[string]string{
    "Ninja":          "ninja",
    "Unix Makefiles": "make",
}
```

Unknown generator strings (including multi-config generators like `"Ninja Multi-Config"` and `"Visual Studio"` variants) are preserved as-is for filtering but are not assigned to `Config.Generator`. Presets with multi-config generators are filtered out (see Error Handling). Presets with no generator field default to `"ninja"` — matching cmake's own default when no generator is specified.

### Interfaces

No changes to the `Builder` interface. The preset awareness is internal to `CMakeBuilder.buildConfigureArgs`.

`loadPresetsMetadata` is a package-internal function in `config/`:

```go
// loadPresetsMetadata reads CMakePresets.json (and CMakeUserPresets.json if
// present), resolves inherits chains for binaryDir, expands macros, and
// returns metadata for non-hidden, single-config-generator presets.
func loadPresetsMetadata(dir string) ([]PresetMetadata, error)
```

## Design Decisions

### Decision 1: Parse presets minimally, delegate to cmake --preset

**Context:** CMakePresets.json is a complex format with inherits chains, macro expansion, condition evaluation, cacheVariables with typed values, and 9 schema versions. A full parser is a large surface area.

**Options Considered:**
1. Full parser — resolve everything in Go, convert cacheVariables to `-D` flags, evaluate conditions, never use `--preset`
2. Minimal parser — extract only names and binaryDir, use `cmake --preset` at configure time so cmake handles everything else
3. No parser — require users to specify preset names in `.cpp-build-mcp.json`, let cmake resolve binaryDir

**Decision:** Minimal parser (option 2).

**Rationale:** cpp-build-mcp needs exactly two things from a preset: the name (to pass to `cmake --preset`) and the binaryDir (to find `compile_commands.json` and track build state). Everything else — cacheVariables, inherits resolution of non-binaryDir fields, condition evaluation, environment variables, toolchainFile — is cmake's job. Delegating to `cmake --preset` means cpp-build-mcp automatically supports all preset features past and future without tracking cmake releases. Option 1 is high-effort and would always lag behind cmake's own preset handling. Option 3 loses the zero-config property.

### Decision 2: Inherits resolution scoped to binaryDir only

**Context:** Presets use `inherits` for field inheritance. A preset may inherit binaryDir from a hidden base preset. To resolve binaryDir, the inherits chain must be walked — but only for binaryDir and generator (the two fields cpp-build-mcp reads).

**Options Considered:**
1. Full inherits resolution for all fields
2. Inherits resolution only for binaryDir and generator
3. No inherits resolution — skip presets that don't declare binaryDir directly

**Decision:** Scoped resolution (option 2).

**Rationale:** Walking the inherits chain for two string fields is straightforward and handles the common pattern where a hidden base preset defines `binaryDir: "${sourceDir}/build/${presetName}"` and visible presets inherit it. Option 3 would miss a significant fraction of real-world presets. Option 1 is unnecessary since cmake handles the full resolution.

### Decision 3: Macro expansion limited to ${sourceDir} and ${presetName}

**Context:** `binaryDir` supports macros: `${sourceDir}`, `${presetName}`, `${generator}`, `$env{VAR}`, `$penv{VAR}`, `${dollar}`, `${hostSystemName}`. Research shows that real-world `binaryDir` values overwhelmingly use only `${sourceDir}` and `${presetName}`.

**Options Considered:**
1. Support all macros
2. Support `${sourceDir}` and `${presetName}` only; skip presets with unresolvable macros
3. Support `${sourceDir}`, `${presetName}`, and `${hostSystemName}`; skip the rest

**Decision:** Option 2, with a logged warning for skipped presets.

**Rationale:** Research across popular open-source projects found zero instances of `$env{}` in `binaryDir`. `${sourceDir}` and `${presetName}` cover effectively all real-world cases. Presets using other macros in `binaryDir` get a warning and are omitted from the registry — the user can still reference them explicitly via `.cpp-build-mcp.json` with a manual `build_dir`. This keeps the parser simple while covering the practical surface.

**Relative path handling:** After macro expansion, if `binaryDir` is not an absolute path (e.g., `"out/build/${presetName}"` without `${sourceDir}` prefix), it is joined with the source directory: `filepath.Join(dir, expandedBinaryDir)`. This matches cmake's own behavior where relative `binaryDir` is resolved relative to the source tree root. Some real-world projects (e.g., vcpkg-tool) use this pattern.

### Decision 4: Skip condition evaluation

**Context:** Presets v3+ support `condition` fields that gate presets by platform, compiler, or custom expressions.

**Options Considered:**
1. Implement condition evaluation (at least `equals`/`inList` on `${hostSystemName}`)
2. Skip condition evaluation entirely — include all non-hidden presets
3. Skip condition evaluation but filter by `${hostSystemName}` as a post-step

**Decision:** Option 2 for the initial implementation, with option 1 as a follow-up.

**Rationale:** Including platform-incompatible presets is safe — `cmake --preset windows-debug` on Linux will fail at configure time with a clear cmake error, not a crash. The user sees the failure and knows to use a different config. Implementing condition evaluation adds complexity for a case that only affects cross-platform projects, and those users likely already know which presets work on their platform. If user feedback shows this is a friction point, condition evaluation can be added incrementally.

### Decision 5: CMakeUserPresets.json is read by default

**Context:** `CMakeUserPresets.json` is the local developer override file. It implicitly includes `CMakePresets.json` and can add or override presets.

**Options Considered:**
1. Read both files (matching cmake behavior)
2. Read only `CMakePresets.json` (safer, more predictable)
3. Read both, with a flag to disable user presets

**Decision:** Read both (option 1).

**Rationale:** cpp-build-mcp runs on the developer's machine, not in CI. The developer's local presets are exactly what they want to use. Ignoring `CMakeUserPresets.json` would create a confusing difference between what the developer's IDE sees and what cpp-build-mcp sees. User presets shadow project presets with the same name, matching cmake's merge order.

### Decision 6: .cpp-build-mcp.json configs map takes precedence over presets

**Context:** A user might have both `CMakePresets.json` and a `configs` map in `.cpp-build-mcp.json`. Which source wins?

**Options Considered:**
1. `configs` map in `.cpp-build-mcp.json` — presets are ignored entirely
2. Merge both — preset-derived configs with JSON overrides
3. Error — ambiguous, force the user to choose

**Decision:** Option 1 — `configs` map takes precedence, presets are not read.

**Rationale:** The `configs` map is an explicit, complete declaration of build configurations. If a user writes it, they want full control. Merging would create ambiguity about which source contributed which field. This also preserves full backward compatibility — existing `.cpp-build-mcp.json` files with `configs` maps continue to work identically. Presets discovery only activates when the `configs` map is absent.

## Error Handling

**No CMakePresets.json and no .cpp-build-mcp.json:** Single "default" config with built-in defaults. No error — this is the zero-config path for projects without presets.

**CMakePresets.json parse error:** Return error at startup. Invalid JSON or unrecognized schema version is a fatal startup error — the user must fix their presets file.

**Preset with unresolvable binaryDir:** Log `slog.Warn` with the preset name and the unresolved macro, skip the preset. Other presets in the file are still loaded. If all presets are skipped, fall back to single "default" config with a warning.

**Preset with multi-config generator:** Skip with `slog.Info` — `Ninja Multi-Config` and Visual Studio generators cannot produce usable `compile_commands.json`. If the user explicitly references such a preset via `.cpp-build-mcp.json`, return an error at startup explaining why.

**Preset binaryDir not specified (v3+ allows this):** Skip with `slog.Warn` — cpp-build-mcp requires a known build directory. The user can reference this preset explicitly in `.cpp-build-mcp.json` with a manual `build_dir`.

**Duplicate binaryDir across presets:** Same validation as existing multi-config: error naming both conflicting configs.

**Circular inherits:** Detect during inherits resolution. Return error naming the cycle.

**cmake --preset fails at configure time:** Normal build failure path — the error is captured in `BuildResult` and surfaced via `get_errors`. No special handling needed. This includes platform-incompatible presets (no condition filtering): `cmake --preset windows-debug` on Linux produces a cmake configure error, surfaced as `success: false` in the `configure` response with cmake's error message. The LLM agent sees the failure and can try a different config.

**All presets skipped or zero configure presets:** When `CMakePresets.json` exists but yields zero usable configure presets (all hidden, all multi-config generators, all unresolvable binaryDir, or simply no configure presets defined), fall back to single "default" config with `slog.Warn`. This is the same outcome as "no presets file" — env vars apply, defaults fill gaps. The warning message distinguishes "file found, zero usable presets" from "file not found."

## Testing Strategy

**Unit tests (config/):**
- `loadPresetsMetadata`: valid presets file, inherits resolution, macro expansion, hidden filtering, multi-config generator filtering, unresolvable macro warning, circular inherits error, user presets merging
- `LoadMulti` with presets: presets-only (no `.cpp-build-mcp.json`), presets with `.cpp-build-mcp.json` overrides, `configs` map suppresses presets, `Preset` field populated correctly
- Backward compatibility: all existing `LoadMulti` and `Load` tests pass unchanged

**Unit tests (builder/):**
- `buildConfigureArgs` with `cfg.Preset != ""`: produces `--preset <name>` instead of `-S`/`-B`/`-G`
- `buildConfigureArgs` with `cfg.Preset == ""`: unchanged behavior (regression)
- `buildConfigureArgs` generator fix: uses `cfg.Generator` not hardcoded `"Ninja"`

**Integration tests (main/):**
- Server starts with `CMakePresets.json` only (no `.cpp-build-mcp.json`), `list_configs` returns preset-derived configs
- `configure(config:"debug")` invokes cmake with `--preset debug`
- Default config is alphabetically first preset (or `default_config` from `.cpp-build-mcp.json` if present)
- Hybrid: `CMakePresets.json` with `.cpp-build-mcp.json` top-level overrides (no `configs` map) — verify preset-derived configs inherit `build_timeout` from JSON, preset names and `binaryDir` values are not overridden

### Structural Verification

- `go vet ./...` — all packages
- `go test -race ./...` — preset parsing has no goroutines, but registry population does
- `staticcheck ./...` — if available

## Migration / Rollout

### Backward Compatibility

Fully backward compatible:

1. **No `CMakePresets.json`, no `.cpp-build-mcp.json`** — single "default" config, same as today
2. **`.cpp-build-mcp.json` without `configs` map** — single config from JSON + env vars, same as today
3. **`.cpp-build-mcp.json` with `configs` map** — multi-config from JSON, presets not read, same as today
4. **`CMakePresets.json` exists, no `.cpp-build-mcp.json`** — NEW: auto-discover presets, zero config

The only new behavior is case 4, which only activates for projects that have `CMakePresets.json` but no `.cpp-build-mcp.json`.

### Rollout Phases

**Phase 1: --preset support and generator fix (prerequisite)**
- Fix `buildConfigureArgs` generator hardcoding (use `cfg.Generator`)
- Add `Preset` field to `Config`
- Add `--preset` branch to `buildConfigureArgs`
- Add `preset` field support to `.cpp-build-mcp.json` config entries
- Test: manual preset reference works end-to-end

**Phase 2: Auto-discovery**
- Implement `loadPresetsMetadata` — parse, inherits resolution, macro expansion, filtering
- Wire into `LoadMulti` — presets path between "no configs map" and "no file" cases
- Handle `CMakeUserPresets.json` merging
- Test: zero-config with `CMakePresets.json` only

**Phase 3: Polish**
- `default_config` resolution from `.cpp-build-mcp.json` when presets are the source
- `.cpp-build-mcp.json` top-level overrides (build_timeout etc.) applied to preset-derived configs
- Documentation for all three modes (manual, presets, hybrid)
- Edge case handling: all presets skipped fallback, condition evaluation (if needed based on feedback)

### What This Design Does NOT Cover

- **Full condition evaluation** — deferred unless user feedback shows platform-incompatible preset surfacing is a real problem
- **buildPresets** — cpp-build-mcp's `build` tool accepts targets/jobs directly; build presets add no value
- **testPresets / packagePresets / workflowPresets** — out of scope for a build intelligence server
- **Runtime preset creation** — presets are read at startup, not modified mid-session
- **Non-CMake build systems** — `MakeBuilder` is unaffected; presets are CMake-specific
