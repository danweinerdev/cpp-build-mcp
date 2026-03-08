---
title: "CMakePresets.json Integration"
type: research
status: active
created: 2026-03-07
updated: 2026-03-07
tags: [cmake, presets, config, multi-config, discovery]
related: [Designs/MultiConfigSupport, Plans/MultiConfigSupport]
---

# CMakePresets.json Integration

## Context

cpp-build-mcp currently uses a custom `.cpp-build-mcp.json` config file to
describe build configurations (build_dir, generator, cmake_args, toolchain,
etc.). The multi-config design adds a `configs` map to this file for named
configurations like Debug/Release/ASAN.

Many CMake projects already define named build configurations via
`CMakePresets.json` — the standard introduced in CMake 3.19. If cpp-build-mcp
could read these presets files, users could skip writing `.cpp-build-mcp.json`
entirely for the common case. This research evaluates whether and how to
integrate CMakePresets.json with cpp-build-mcp's configuration system.

---

## Findings

### Key Insights

#### CMakePresets.json Format

`CMakePresets.json` sits in the project root (checked in) alongside an optional
`CMakeUserPresets.json` (local developer overrides, not checked in). The schema
is versioned; the version determines the minimum CMake required:

| Version | CMake Required | Key Additions |
|---------|---------------|---------------|
| 1 | 3.19 | configurePresets, macro expansion |
| 2 | 3.20 | buildPresets, testPresets |
| 3 | 3.21 | conditions, generator/binaryDir optional |
| 4 | 3.23 | includes, resolvePackageReferences |
| 5 | 3.24 | testOutputTruncation |
| 6 | 3.25 | packagePresets, workflowPresets |
| 7 | 3.27 | trace field |
| 8 | 3.28 | $schema field |
| 9 | 3.30 | macro expansion in includes |

Version 3 (CMake 3.21+) is the practical baseline: it makes `generator` and
`binaryDir` optional and adds `condition` for platform-specific gating. Most
real-world project presets files use version 3 or 4.

**configurePresets fields relevant to cpp-build-mcp:**

| CMakePresets field | Type | Maps to Config field | Notes |
|---|---|---|---|
| `name` | string | config name (key) | Machine-friendly identifier |
| `displayName` | string | — | Human label only |
| `binaryDir` | string | `build_dir` | Supports `${sourceDir}`, `${presetName}` macros |
| `generator` | string | `generator` | Full CMake names: `"Ninja"`, `"Unix Makefiles"` |
| `toolchainFile` | string | `toolchain` (partial) | Path, not a keyword like `"clang"` |
| `cacheVariables` | map | `cmake_args` (partial) | Structured `{VAR: {type, value}}` vs `-DVAR=val` strings |
| `inherits` | string/array | — | Preset-level inheritance chain |
| `hidden` | boolean | — | Hides from CLI; marks base presets |
| `environment` | map | — | Environment variables for the configure step |
| `condition` | object | — | Conditional enablement (v3+) |

**No CMakePresets equivalent exists for:**
- `build_timeout` — cpp-build-mcp-specific
- `inject_diagnostic_flags` — cpp-build-mcp-specific
- `diagnostic_serial_build` — cpp-build-mcp-specific
- `source_dir` — implied by `${sourceDir}` macro in `binaryDir`; no explicit field

**buildPresets fields:**

| Field | Type | Meaning |
|---|---|---|
| `name` | string | Identifier |
| `configurePreset` | string | Links to a configurePreset by name |
| `targets` | string/array | Default targets |
| `jobs` | integer | Parallel job count |
| `cleanFirst` | boolean | Equivalent to `--clean-first` |
| `configuration` | string | For multi-config generators only (Debug/Release) |

Build presets reference configure presets by name. They are a secondary concern
for cpp-build-mcp since the `build` tool accepts `targets` and `jobs`
parameters directly from the LLM agent.

#### cmake --preset CLI Behavior

```
cmake --preset <configure-preset-name>     # configure phase
cmake --build --preset <build-preset-name> # build phase
```

- The `--preset` flag reads the preset and synthesizes the equivalent `-S`, `-B`,
  `-G`, `-D` arguments from the preset's fields.
- Command-line `-D` flags appended after `--preset` **override** preset
  `cacheVariables` — the flag with the last-wins rule applies.
- `compile_commands.json` is generated the same way as without presets — via
  `CMAKE_EXPORT_COMPILE_COMMANDS=ON`. This can be set in the preset's
  `cacheVariables` or appended on the command line after `--preset`.
- **Minimum CMake for `--preset` support: 3.19.**
- Hidden presets (`"hidden": true`) cannot be referenced from `--preset` and
  only serve as base presets for `inherits`.

#### Macro Expansion in binaryDir

`binaryDir` supports macro expressions:
- `${sourceDir}` — absolute path to the project source
- `${presetName}` — the preset's `name` field
- `${generator}` — the generator name
- `$env{VAR}` — environment variable

Example: `"binaryDir": "${sourceDir}/build/${presetName}"` expands to
`/path/to/project/build/debug` for a preset named `debug`. This means
`binaryDir` values from presets often need macro expansion before being used
as a `build_dir` string by cpp-build-mcp.

#### cacheVariables Schema

`cacheVariables` is a JSON object where each value is either a string or a
typed object:

```json
"cacheVariables": {
  "CMAKE_BUILD_TYPE": "Debug",
  "MY_FLAG": { "type": "BOOL", "value": "ON" },
  "CMAKE_EXPORT_COMPILE_COMMANDS": "ON"
}
```

To convert to `-D` flags for use by cpp-build-mcp's `cmake_args`:
```
-DCMAKE_BUILD_TYPE=Debug
-DMY_FLAG:BOOL=ON
-DCMAKE_EXPORT_COMPILE_COMMANDS=ON
```

This conversion is straightforward but requires type-aware serialization.

#### generator Name Normalization

CMakePresets.json uses the full CMake generator name:
- `"Ninja"` → cpp-build-mcp `"ninja"`
- `"Unix Makefiles"` → cpp-build-mcp `"make"`
- `"Ninja Multi-Config"` → no equivalent in cpp-build-mcp

The mapping is case-sensitive and requires normalization. Multi-config
generators (`Ninja Multi-Config`, `Visual Studio *`) do not produce
`compile_commands.json` and are not supported by cpp-build-mcp's
`get_build_graph` tool, which is a hard constraint.

#### CMakeUserPresets.json Layering

`CMakeUserPresets.json` implicitly includes `CMakePresets.json` and can
override or add presets. For auto-discovery, cpp-build-mcp would need to
merge both files — user presets shadow project presets with the same name.
This layering is well-defined in the schema but adds parsing complexity.

#### Prevalence

CMakePresets.json is in widespread use:
- Supported by VS Code CMake Tools, CLion, Qt Creator, Visual Studio 2022.
- Generated automatically by `cmake --fresh -G Ninja -S . --preset debug` or
  by IDEs like VS Code when you initialize a project.
- Common in embedded/cross-compilation projects (via `toolchainFile`).
- The majority of greenfield CMake projects targeting modern tooling include one.

---

### Real-World Adoption Patterns

Analysis of CMakePresets.json files from representative open-source C++ projects
reveals consistent patterns that are directly relevant to scope decisions for the
scoped Option A implementation.

#### binaryDir macro usage

Across five projects examined (microsoft/STL, lvgl/lvgl, microsoft/vcpkg-tool,
cpp-best-practices/cmake_template, and a cross-platform embedded template):

- **`${sourceDir}/out/build/${presetName}`** and **`${sourceDir}/build/${presetName}`**
  are by far the most common patterns. Every project examined used one of these
  two forms.
- **`$env{}` in binaryDir is effectively absent.** None of the projects examined
  used `$env{}` macros in the `binaryDir` field itself. Environment variables appear
  elsewhere (e.g., `toolchainFile: "$env{VCPKG_ROOT}/..."`, vendor settings for
  Visual Studio remote builds), but not in `binaryDir`.
- **Relative paths** (e.g., `"out/build/${presetName}"` without `${sourceDir}`)
  do appear in some projects (vcpkg-tool). These resolve relative to the source
  directory when processed by cmake but require cpp-build-mcp to prepend the
  source dir during macro expansion.

**Implication for scoped Option A:** Supporting only `${sourceDir}` and
`${presetName}` in `binaryDir` expansion covers virtually all real-world cases.
`$env{}` in `binaryDir` can be documented as unsupported without material user
impact.

#### inherits chain depth

Projects use 1–3 levels of inheritance:
- **1 level** is most common for simple projects (a hidden `_base` preset plus
  concrete presets that inherit from it directly).
- **2–3 levels** appear in more complex projects (e.g., vcpkg-tool uses 3 levels:
  `_base` → `Win-x64-Debug` → `Win-x64-Debug-WithArtifacts`).
- Multi-inheritance from parallel base presets (e.g., `"inherits": ["_base",
  "_windows", "_debug"]`) is the recommended best practice and commonly observed.
- Chains deeper than 3 levels are not present in any project examined.

**Implication:** A recursive inherits resolver must handle multi-inheritance
(array form of `inherits`) and transitive chains up to ~3 levels. This is
tractable; the resolver can detect cycles via a visited set.

#### condition field usage

`condition` fields are used by **larger, multi-platform projects** to gate
presets by operating system (checking `${hostSystemName}` or `$env{VSCMD_ARG_TGT_ARCH}`).
Smaller or single-platform projects omit `condition` entirely. The `equals`
and `inList` condition types are the most common; complex nested conditions are
rare.

**Implication:** Skipping `condition` evaluation (as scoped Option A proposes)
will surface Windows-only presets on Linux for projects like microsoft/STL and
cpp-best-practices/cmake_template. For cross-platform projects this is a visible
papercut — the LLM agent will attempt to use a preset that cmake itself would
refuse. Adding basic `equals`/`inList` condition evaluation on `${hostSystemName}`
(a static string, not a runtime variable) would eliminate the most common case.

#### schema version

Versions 3 and 5 dominate the projects examined. Version 3 (CMake 3.21) is the
most common baseline, reflecting that it made `generator` and `binaryDir`
optional and added `condition`. Version 6 is the highest version observed in
practice (CLion's maximum supported version). Version 8+ (which adds the
`$schema` field) is rarely used outside Microsoft's own tooling.

---

### Existing Parsing Tools

#### Go libraries

**No dedicated Go library for CMakePresets.json parsing exists.** The search
found no open-source Go package targeting the CMakePresets JSON format. The
closest relevant Go packages are CMake language parsers (for CMakeLists.txt
syntax), not JSON preset parsers. Any Go implementation would be custom-built
using `encoding/json` with hand-defined structs.

**A CMakePresets JSON Schema is published** by Kitware at:
```
https://cmake.org/cmake/help/latest/_downloads/3e2d73bff478d88a7de0de736ba5e361/schema.json
```
(Requires schema version 8+ in the file via the `$schema` field to activate;
the schema itself follows JSON Schema draft 2020-12.) Go libraries such as
`github.com/santhosh-tekuri/jsonschema` support this draft and could be used
for structural validation, but the schema URL is not stable across CMake versions
and should not be embedded as a hard dependency.

#### How IDEs parse presets

VS Code CMake Tools and CLion both parse `CMakePresets.json` directly in their
own implementations rather than delegating to cmake. They resolve `inherits`
chains and evaluate `condition` fields themselves in order to populate the
preset dropdowns without invoking cmake. This confirms that client-side parsing
is the expected pattern for tools that need to list presets before a configure
step is run.

VS Code CMake Tools added presets support in v1.7+ (Visual Studio 2019 16.10+),
and Microsoft contributed the build/test presets spec to CMake 3.20. This means
VS Code's implementation predates much of the schema evolution and has accumulated
substantial handling for edge cases (inherits resolution, condition evaluation,
CMakeUserPresets.json merging). Its TypeScript source is a practical reference
for what a full implementation must handle.

---

### cmake --preset Interaction Details

Research clarified several behavioral details relevant to Option B and scoped
Option A:

#### Mixing preset-configure with non-preset build

**`cmake --build <binaryDir>` works correctly after `cmake --preset <name>`.**
After a preset configure step, the build directory contains a standard
CMakeCache.txt. `cmake --build <dir>` reads the cache and builds normally —
no `--preset` flag is needed for the build phase. This is the pattern
cpp-build-mcp uses in `buildBuildArgs` (which passes `--build <cfg.BuildDir>`
directly). So Option B requires no change to the build step — only the configure
step uses `--preset`.

#### Appending -D flags after --preset

**Confirmed: `-D` flags appended after `--preset` override preset cacheVariables
with last-wins semantics.** Example: `cmake --preset debug -DCMAKE_EXPORT_COMPILE_COMMANDS=ON`
injects `CMAKE_EXPORT_COMPILE_COMMANDS=ON` even if the preset's `cacheVariables`
does not include it. This is the documented behavior ("command-line flags have
precedence over values found in a preset"). This confirms that Option B's
approach of appending `-DCMAKE_EXPORT_COMPILE_COMMANDS=ON` after `--preset` is
correct and safe.

#### cmake --build --preset without configure preset

`cmake --build --preset <build-preset>` infers the build directory from the
build preset's linked `configurePreset`. This is a separate feature from
what cpp-build-mcp uses; the server passes `--build <binaryDir>` directly
and does not need build presets.

---

### Multi-Config Generator Details

#### Ninja Multi-Config and compile_commands.json

The cmake documentation for `CMAKE_EXPORT_COMPILE_COMMANDS` states the variable
is implemented for "Makefile Generators and Ninja Generators." The precise
behavior with Ninja Multi-Config has been reported as problematic: the single
`compile_commands.json` it produces merges entries from all configurations
together, making it **effectively unusable for tools like clangd that expect
per-configuration flags** (debug vs. release flags would be mixed, producing
incorrect include paths or define sets).

This confirms the existing document's claim that Ninja Multi-Config is not
compatible with cpp-build-mcp's `get_build_graph` tool. The constraint is
not merely theoretical.

**Visual Studio generators** (`Visual Studio 16 2019`, etc.) explicitly do not
support `CMAKE_EXPORT_COMPILE_COMMANDS` — the variable is ignored for those
generators. This is a harder exclusion than Ninja Multi-Config, where the
file is generated but unreliable.

**Recommended handling:** When cpp-build-mcp encounters a configure preset
with `"generator": "Ninja Multi-Config"` or any `"Visual Studio *"` generator,
it should log a warning and either skip the preset (in auto-discovery) or
return an error on configure (in Option B with an explicit `preset` field).

---

### Sources

- [cmake-presets(7) — CMake 4.3.0-rc2 Documentation](https://cmake.org/cmake/help/latest/manual/cmake-presets.7.html)
- [Configure and build with CMake Presets — Microsoft Learn](https://learn.microsoft.com/en-us/cpp/build/cmake-presets-vs?view=msvc-170)
- [CMake Presets — Feabhas Blog](https://blog.feabhas.com/2023/08/cmake-presets/)
- [Introduction to CMake Presets — studyplan.dev](https://www.studyplan.dev/cmake/cmake-presets)
- [cmake(1) — CMake 4.3.0-rc2 Documentation (cmake --build section)](https://cmake.org/cmake/help/latest/manual/cmake.1.html)
- [Ninja Multi-Config — CMake Documentation](https://cmake.org/cmake/help/latest/generator/Ninja%20Multi-Config.html)
- [CMAKE_EXPORT_COMPILE_COMMANDS — CMake Documentation](https://cmake.org/cmake/help/latest/variable/CMAKE_EXPORT_COMPILE_COMMANDS.html)
- [cmake-presets(7) Macro Expansion — cmake-presets(7) Arch manual pages](https://man.archlinux.org/man/extra/cmake/cmake-presets.7.en)
- [CMake Presets integration in Visual Studio and VS Code — C++ Team Blog](https://devblogs.microsoft.com/cppblog/cmake-presets-integration-in-visual-studio-and-visual-studio-code/)
- [CMake Presets Tutorial: Inheritance and User Customization — studyplan.dev](https://www.studyplan.dev/cmake/cmake-user-presets-and-inheritance)
- [microsoft/STL CMakePresets.json (example: schema v5, binaryDir pattern)](https://github.com/microsoft/STL/blob/main/CMakePresets.json)
- [microsoft/vcpkg-tool CMakePresets.json (example: schema v3, 3-level inherits)](https://github.com/microsoft/vcpkg-tool/blob/main/CMakePresets.json)
- [cpp-best-practices/cmake_template CMakePresets.json (example: schema v3, condition usage)](https://github.com/cpp-best-practices/cmake_template/blob/main/CMakePresets.json)
- [lvgl/lvgl CMakePresets.json (example: schema v3, platform conditions)](https://github.com/lvgl/lvgl/blob/master/CMakePresets.json)
- [Kitware CMakePresets JSON Schema](https://cmake.org/cmake/help/latest/_downloads/3e2d73bff478d88a7de0de736ba5e361/schema.json)
- [Visual Studio Code CMake Tools 1.17 — Overriding Cache Variables with --preset](https://devblogs.microsoft.com/cppblog/visual-studio-code-cmake-tools-extension-1-17-update-cmake-presets-v6-overriding-cache-variables-and-side-bar-updates/)

---

## Analysis

### Field Mapping Summary

```
CMakePresets configurePreset  →  cpp-build-mcp config.Config
─────────────────────────────────────────────────────────────
name            (string)        → map key (config name)
binaryDir       (string+macros) → BuildDir  (after macro expansion)
generator       (string)        → Generator (after normalization)
cacheVariables  (map)           → CMakeArgs (serialized as -D flags)
toolchainFile   (string)        → n/a; CMakeArgs as -DCMAKE_TOOLCHAIN_FILE=
inherits        (string/array)  → resolved before conversion (transitive)
hidden          (boolean)       → skip (not user-visible presets)

No mapping exists for:
  BuildTimeout, InjectDiagnosticFlags, DiagnosticSerialBuild, SourceDir
```

The three cpp-build-mcp-specific fields (`BuildTimeout`,
`InjectDiagnosticFlags`, `DiagnosticSerialBuild`) have no home in
`CMakePresets.json`. Any integration approach must provide a fallback for
these, either via the existing `.cpp-build-mcp.json` or hardcoded defaults.

### Evaluation of Integration Options

---

#### Option A: Auto-Discover

When no `configs` map exists in `.cpp-build-mcp.json` **and** no
`.cpp-build-mcp.json` exists at all, look for `CMakePresets.json` in the
project root, parse all non-hidden `configurePresets`, and populate the
config registry from them.

**Implementation sketch:**
1. `config.LoadMulti(dir)` falls back to reading `CMakePresets.json` when
   `.cpp-build-mcp.json` is absent.
2. For each non-hidden configure preset, expand macros in `binaryDir`,
   normalize `generator`, convert `cacheVariables` to `cmake_args` strings.
3. cpp-build-mcp-specific fields (`BuildTimeout` etc.) use defaults.
4. `CMakeUserPresets.json` is also read if present and merged.

**Pros:**
- Zero configuration for users of CMake projects with presets files.
- Auto-discovers the preset names that other tools (IDEs, CI) already use.
- No duplication between CMakePresets.json and .cpp-build-mcp.json.

**Cons:**
- Macro expansion logic (`${sourceDir}`, `${presetName}`, `$env{VAR}`) must
  be implemented from scratch in Go. This is non-trivial — some macros depend
  on runtime state (environment variables) and others on parse-time context.
- `inherits` chains must be resolved transitively before any field can be
  read. A preset that inherits from three levels of hidden base presets is
  common in real projects.
- `condition` (v3+) requires evaluating boolean expressions about the host
  platform to decide which presets are active. Ignoring conditions may surface
  presets that CMake itself would skip (e.g., Windows-only presets on Linux).
- Generators like `Ninja Multi-Config` must be filtered out, but without
  `--preset` being used to configure, the generator field controls which
  backend runs — cpp-build-mcp would still construct `-S`/`-B`/`-G` flags
  itself, so the builder code stays unchanged.
- CMakeUserPresets.json layering means two files must be read and merged.
- Significant new parsing surface area with edge cases (malformed presets,
  circular inherits, v1 files without binaryDir).

**Effort:** High. Macro expansion + inherits resolution + condition evaluation
is a substantial parser. Risk of subtle bugs (e.g., wrong sourceDir when MCP
server working directory differs from project root).

---

#### Option B: Reference (--preset Pass-Through)

`.cpp-build-mcp.json` gains a `preset` field per config entry. When set, the
server passes `cmake --preset <name>` instead of constructing `-S`/`-B`/`-G`/`-D`
args manually. The cmake process handles macro expansion and inherits resolution
natively.

**Example config:**
```json
{
  "configs": {
    "debug": {
      "preset": "debug",
      "build_dir": "build/debug"
    },
    "release": {
      "preset": "release",
      "build_dir": "build/release"
    }
  }
}
```

`build_dir` is still required (cpp-build-mcp uses it to find
`compile_commands.json` and track state). If `preset` is set, `buildConfigureArgs`
switches from `-S . -B build/debug -G Ninja -DCMAKE_BUILD_TYPE=Debug` to
`cmake --preset debug`, and then appends
`-DCMAKE_EXPORT_COMPILE_COMMANDS=ON` and any diagnostic flags since the preset
may not include those.

**Note on compile_commands.json:** `--preset` does not prevent adding
additional `-D` flags. `cmake --preset debug -DCMAKE_EXPORT_COMPILE_COMMANDS=ON`
works and the flag wins over a preset's cacheVariable for the same key (since
CLI `-D` overrides preset cacheVariables).

**Implementation sketch:**
1. Add `Preset string` to `configJSON` (not `Config`; it is config-file-only
   metadata, not a runtime field on `Config`).
2. Store the resolved preset name in `configInstance` (not in `Config`).
3. `CMakeBuilder.buildConfigureArgs()` checks if `inst.preset != ""`:
   - If yes: `["--preset", name, "-DCMAKE_EXPORT_COMPILE_COMMANDS=ON", ...diag flags..., ...extraArgs...]`
   - If no: existing logic unchanged.
4. `build_dir` must still be specified in `.cpp-build-mcp.json` (or derivable
   from the preset's binaryDir — see Option C hybrid below).

**Pros:**
- No macro expansion logic in Go — cmake handles it.
- No inherits resolution — cmake handles it.
- No condition evaluation — cmake handles it.
- Minimal code change: one branch in `buildConfigureArgs`.
- Users who have CMakePresets.json still need a `.cpp-build-mcp.json`, but
  it is much shorter (just `preset` + `build_dir` per config).
- The `--preset` flag works with CMake 3.19+, which is already required for
  presets to exist.

**Cons:**
- Still requires a `.cpp-build-mcp.json`. Not zero-config.
- `build_dir` duplication: users must know what `binaryDir` the preset expands
  to and repeat it in `.cpp-build-mcp.json` so cpp-build-mcp can find
  `compile_commands.json`.
- Cannot use the preset name as the config name automatically (user specifies
  both `"debug"` as the map key and `"preset": "debug"`).
- Cannot list available presets via `list_configs` without reading
  `CMakePresets.json` — so the UX improvement is minimal.
- If the user forgets to set `CMAKE_EXPORT_COMPILE_COMMANDS=ON` in the preset
  AND the server appends it, they get the right behavior, but it's implicit.

**Effort:** Low-Medium. Primarily a change to `CMakeBuilder.buildConfigureArgs`
and a new field in the config struct. Requires deciding where `preset` lives
(`Config` vs `configInstance` vs `configJSON`).

---

#### Option C: Hybrid (Auto-Discover + Merge)

Read `CMakePresets.json` for auto-discovery of configure preset names and
`binaryDir`, but merge with `.cpp-build-mcp.json` for server-specific fields.
`.cpp-build-mcp.json` becomes optional and additive.

**Priority order:**
1. `CMakePresets.json` provides base config (name, binaryDir, generator, cacheVariables).
2. `.cpp-build-mcp.json` overrides or supplements (build_timeout, inject_diagnostic_flags,
   diagnostic_serial_build, additional cmake_args).
3. Environment variables apply in single-config mode as today.

**Implementation sketch:**
1. `LoadMulti(dir)` first checks for `CMakePresets.json`. If found, parse
   non-hidden configurePresets into a base map.
2. Resolve `inherits` chains transitively (required to get correct binaryDir
   and cacheVariables).
3. If `.cpp-build-mcp.json` exists, overlay its fields.
4. If `.cpp-build-mcp.json` has a `configs` map, merge named entries with same
   names from the presets (cpp-build-mcp config wins on conflict).
5. `build_dir` derived from preset's `binaryDir` after simple macro expansion
   (`${sourceDir}` → dir, `${presetName}` → name).

**Pros:**
- Closest to zero-config for preset-using projects.
- `.cpp-build-mcp.json` only needs server-specific overrides — can be very
  small or absent.
- Config names match preset names automatically.
- Full preset discovery: LLM agent sees the same configs as VS Code/CLion.

**Cons:**
- Still requires macro expansion (at minimum `${sourceDir}` and `${presetName}`).
- Still requires inherits resolution.
- Merge semantics between two config sources introduce ambiguity and edge cases
  (what if CMakePresets has a preset named "debug" and .cpp-build-mcp.json has
  a configs entry named "debug" with a different build_dir?).
- The `condition` evaluation problem remains — do we skip platform-incompatible
  presets or include them all?
- Significantly more complex loading code.
- Two sources of truth for the same conceptual config is harder to explain
  in docs and troubleshoot.

**Effort:** High. Inherits resolution + partial macro expansion + merge logic +
conflict resolution rules.

---

#### Option D: Pass-Through (Tool-Level --preset Parameter)

Add an optional `preset` string parameter to the `configure` MCP tool. When
provided, the server calls `cmake --preset <name>` instead of the normal
argument construction. No changes to `.cpp-build-mcp.json`.

**Example agent interaction:**
```
configure(config: "debug", preset: "debug")
```

**Pros:**
- Simplest implementation of all options.
- No config format changes.
- LLM agent can choose which preset to use at configure time.
- Works with any preset without any server-side configuration.

**Cons:**
- The LLM agent must know preset names (it would need to read the presets file
  or have the user tell it).
- `build_dir` mismatch: the server doesn't know what directory cmake configured
  into, so `compile_commands.json` lookup and `get_build_graph` break unless
  the preset's `binaryDir` matches the configured `build_dir`.
- No auto-discovery: `list_configs` still shows only what's in
  `.cpp-build-mcp.json`.
- Does not address the original motivation (auto-populating the registry from
  presets).

**Effort:** Very Low. One parameter on the `configure` tool, one branch in
`buildConfigureArgs`. But solves a different (and lesser) problem.

---

### Implications

Options A–D are not alternatives to choose between — they are **implementation phases of a single config source chain architecture**.

1. **Option B (`--preset` pass-through) is the mechanism for layer 1 of the chain.** When a config entry has a preset name, `CMakeBuilder.buildConfigureArgs` delegates to `cmake --preset <name>`, letting cmake handle all inherits resolution, macro expansion, and cacheVariable application natively. cpp-build-mcp appends `CMAKE_EXPORT_COMPILE_COMMANDS=ON` and any diagnostic flags after `--preset`.

2. **Option A (auto-discover) provides the zero-config discovery layer.** By reading `CMakePresets.json` (and `CMakeUserPresets.json`) at startup when no `.cpp-build-mcp.json` exists, cpp-build-mcp can populate the config registry from preset names and binaryDir values without requiring any user configuration file. Combined with Option B's `--preset` pass-through at configure time, cmake still handles all preset complexity — cpp-build-mcp only needs names and binaryDir for registry metadata.

3. **Combined, Options A and B deliver the full config source chain.** Options C and D are subsumed: Option C's merge behavior falls out naturally when `.cpp-build-mcp.json` overlays preset-derived configs, and Option D (tool-level `--preset`) is unnecessary because the config layer already handles preset selection.

4. **The hard constraint is `compile_commands.json`.** cpp-build-mcp's `get_build_graph`, toolchain auto-detection, and suggest_fix all depend on finding `compile_commands.json` at `build_dir`. Any integration approach must reliably determine `binaryDir` at startup — this is why auto-discovery (parsing `binaryDir` from presets) and not Option D (tool-level preset) is the right zero-config path.

5. **CMakeUserPresets.json must be read in CMake's merge order.** On a developer machine, local overrides in `CMakeUserPresets.json` may change preset names or binaryDir values. Auto-discovery should read both files with user presets shadowing project presets on name conflict.

---

### Architecture Direction

The right long-term architecture is a **config source chain with fallback**:

```
1. CMakePresets.json + CMakeUserPresets.json  (auto-discovered)
2. .cpp-build-mcp.json                        (optional overrides)
3. Environment variables                       (single-config only)
4. Built-in defaults                           (always)
```

Each layer provides what it knows:

- **CMakePresets** provides build configurations: names, binaryDir, generator, cacheVariables. cpp-build-mcp uses `cmake --preset <name>` at configure time so cmake resolves inherits, conditions, and macros natively. cpp-build-mcp only needs names and binaryDir from the presets file for registry metadata.
- **`.cpp-build-mcp.json`** provides server-specific tuning (`build_timeout`, `inject_diagnostic_flags`, `diagnostic_serial_build`) and can override preset-derived values. It is optional — only needed for the three server-specific fields CMakePresets cannot express, or for users who want full manual control.
- **Environment variables** apply for single-config backward compatibility, same as today.
- **Built-in defaults** fill any remaining gaps.

**Key design principles:**

1. **Zero config for any CMake project with presets** — no `.cpp-build-mcp.json` required. The LLM agent sees the same configs as VS Code and CLion.
2. **Use `cmake --preset <name>` at configure time** — let cmake handle inherits, conditions, macros, and cacheVariables natively. cpp-build-mcp does not need to re-implement preset resolution.
3. **`.cpp-build-mcp.json` is optional overrides** — only needed for the three server-specific fields CMakePresets cannot express, or when the user wants explicit config control.
4. **Without `CMakePresets.json`** — single "default" config using cmake defaults, same as today. No regression.
5. **The `configs` map in `.cpp-build-mcp.json` still works** — for users who don't have presets or want full manual control. This is unchanged from the multi-config design.

**Implementation phases (sequenced, not alternatives):**

- **Phase 1: Option B** — Add `preset` field to per-config entry in `.cpp-build-mcp.json`. When set, `buildConfigureArgs` switches to `cmake --preset <name>` plus `CMAKE_EXPORT_COMPILE_COMMANDS=ON` and diagnostic flags. Minimal code change; unblocks users who already have `CMakePresets.json` and are willing to write a small `.cpp-build-mcp.json`.
- **Phase 2: Option A (scoped)** — Auto-discover configure presets when `.cpp-build-mcp.json` is absent. Scope: expand only `${sourceDir}` and `${presetName}` in `binaryDir` (covers all real-world cases per adoption analysis); resolve `inherits` recursively; skip hidden presets; skip multi-config generator presets with a warning; skip `condition` evaluation (document limitation). Use `--preset` at configure time — no cacheVariable conversion needed. Merge with `.cpp-build-mcp.json` if present for server-specific overrides.
- **Option D is subsumed** — tool-level `--preset` is unnecessary when the config layer handles preset selection. It also breaks the `build_dir` invariant and does not address auto-discovery.

---

## Open Questions

- **binaryDir macro coverage:** ~~What percentage of real-world CMakePresets.json
  files use macros beyond `${sourceDir}` and `${presetName}`?~~ **Answered** (see
  "Real-World Adoption Patterns" above): `$env{}` in `binaryDir` is absent from all
  projects examined. Only `${sourceDir}` and `${presetName}` are used in binaryDir.
  Relative paths without `${sourceDir}` also appear and must be handled by prepending
  the source directory.

- **Multi-config generator handling:** ~~Should cpp-build-mcp warn (or refuse)
  when a configure preset specifies `"generator": "Ninja Multi-Config"`?~~ **Answered**
  (see "Multi-Config Generator Details" above): Yes, it should warn or refuse.
  Ninja Multi-Config produces a merged compile_commands.json that is unusable for
  clangd. Visual Studio generators produce no compile_commands.json at all. The
  recommended action is to skip such presets during auto-discovery and return an
  error on explicit configure.

- **Which option to implement first:** ~~Should Option B or A come first?~~ **Answered**
  (see Architecture Direction above): Option B (`--preset` pass-through) is the first
  implementation phase because it requires no presets file parsing, is a minimal code
  change, and immediately unblocks users who have presets and are willing to write a
  small `.cpp-build-mcp.json`. Option A (auto-discover) is the second phase.

- **CMakeUserPresets.json priority:** ~~Should user presets be read by default
  (matching CMake behavior) or opt-in?~~ **Answered** (see Design doc, Decision 5):
  Read both files by default, matching cmake behavior. cpp-build-mcp runs on the
  developer's machine — their local presets are what they want to use.

- **Preset name conflicts:** If both `CMakePresets.json` and `.cpp-build-mcp.json`
  define a config named `"debug"`, which wins? The config source chain implies
  `.cpp-build-mcp.json` wins (layer 2 overrides layer 1). This needs to be explicit
  in implementation and documented.

- **`build_dir` absence:** Preset `binaryDir` is optional in v3+. What does
  cpp-build-mcp do when a non-hidden preset omits `binaryDir`? Recommended: skip
  the preset with a warning. Falling back to a default `"build"` directory would
  silently misroute `compile_commands.json` lookup if the preset configures elsewhere.

- **Condition evaluation scope:** The decision to skip `condition` evaluation in
  Phase 2 (auto-discover) means platform-incompatible presets will appear in
  `list_configs` and the LLM agent may attempt to use them. Implementing basic
  `equals`/`inList` evaluation on `${hostSystemName}` (a static value) would
  eliminate the most common cross-platform papercut without significant complexity.
  Deferred to a follow-up; document the limitation at launch.
