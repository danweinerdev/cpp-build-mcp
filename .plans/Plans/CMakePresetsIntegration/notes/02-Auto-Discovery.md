---
title: "Phase 2 Debrief: Auto-Discovery"
type: debrief
plan: "CMakePresetsIntegration"
phase: 2
phase_title: "Auto-Discovery"
status: complete
created: 2026-03-08
---

# Phase 2 Debrief: Auto-Discovery

## Decisions Made

- **`buildPresetConfigs` helper centralizes preset discovery.** `LoadMulti` has two entry points where presets can be discovered: (1) no `.cpp-build-mcp.json` exists, (2) config file exists without `configs` map. Rather than duplicating preset logic in both branches, a `buildPresetConfigs(dir, data, defaultConfig, path)` helper was extracted. It returns `(configs, defaultName, ok, error)` — the `ok` boolean lets the caller fall through to single-config mode when no presets are found.

- **Nil vs non-nil empty slice for `loadPresetsMetadata` return.** Originally `var result []presetMetadata` (nil when all presets filtered). Changed to `make([]presetMetadata, 0)` so callers can distinguish "no CMakePresets.json file" (`nil` return) from "file exists but all presets filtered" (non-nil empty slice). This distinction is required for the "CMakePresets.json found but no usable configure presets" warning to fire only when the file actually exists.

- **Tasks 2.2 and 2.3 serialized despite Wave 2 parallelism opportunity.** Both tasks write to `config/presets.go`, and Task 2.3's `loadPresetsMetadata` calls Task 2.2's `resolveInherits` and `expandBinaryDir`. The overlap analysis before Wave 2 flagged this, and serialization was chosen over parallel execution with merge risk. Tasks ran as 2.2 → 2.3 sequentially.

- **Single-preset mode (1 preset) applies env vars; multi-preset (2+) suppresses them.** The plan specified "suppress env vars in multi-preset mode (2+ presets)." With exactly 1 preset, the behavior matches single-config semantics — env vars apply. This avoids a behavioral surprise where a project with one preset in `CMakePresets.json` would behave differently from one with the equivalent single-config `.cpp-build-mcp.json`.

- **`isMultiConfigGenerator` and `normalizeGenerator` extracted as named helpers.** The plan implied inline filtering logic within `loadPresetsMetadata`. The implementation extracted these as standalone functions with their own unit tests (`TestIsMultiConfigGenerator`, `TestNormalizeGenerator`). This improves testability and readability without adding complexity.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| `loadPresetsMetadata` parses CMakePresets.json with inherits resolution, macro expansion, and filtering | Met | Full pipeline: read → merge → warn include → resolve inherits → filter → expand → normalize → validate uniqueness |
| `LoadMulti` with CMakePresets.json only (no .cpp-build-mcp.json) returns preset-derived configs | Met | Test: `CMakePresets.json only returns preset-derived configs` in `TestLoadMulti_PresetDerived` |
| CMakeUserPresets.json is read and merged when present | Met | `readUserPresets` + `mergePresets`; tested in `TestLoadPresetsMetadata/user_presets_shadow_project_presets` |
| `configs` map in .cpp-build-mcp.json suppresses preset discovery | Met | The `file.Configs != nil` branch runs before `buildPresetConfigs`; tested |
| All presets skipped falls back to single "default" config | Met | Non-nil empty slice → `slog.Warn` → `ok=false` → caller falls through; tested with and without config file |
| Env vars suppressed in multi-preset mode | Met | `len(presets) >= 2` → `warnEnvVarsIgnored()`; single preset applies `applyEnv` |
| `go vet`, `go test -race`, and `staticcheck` all pass | Met | All 7 packages pass clean |

## Deviations

- **Tasks 2.2 and 2.3 serialized (not parallel).** The dependency graph allowed parallel execution, but the overlap analysis showed both tasks write to `config/presets.go` and 2.3 calls 2.2's functions. Serialization avoided merge conflicts at the cost of wall-clock time.

- **`loadPresetsMetadata` nil/empty-slice semantics added during Task 2.4.** The original implementation used `var result []presetMetadata`, which was nil when all presets were filtered. Task 2.4 needed to distinguish "no file" from "all filtered" for the warning message. The change from `var` to `make([]presetMetadata, 0)` was a small but semantically important fix made during Task 2.4 implementation.

- **Wrong file name in user presets include warning.** The `CMakeUserPresets.json` include warning at `presets.go:250` used the same message string as the project file warning ("CMakePresets.json uses 'include'..."). Caught by the Task 2.2/2.3 code review. Fixed in the review-findings commit by changing to "CMakeUserPresets.json uses 'include'...".

- **Three additional tests added beyond plan specification.** Code review identified gaps: 3-node circular inherits (structurally distinct DFS case from 2-node), partial-overlap `mergePresets` (mixed collision/append), and `CMakeUserPresets.json` include field warning (would have caught the wrong-file-name bug). All added in the review-findings commit.

## Risks & Issues Encountered

- **Wrong file name in include warning (Minor, from code review).** Copy-paste error: user presets include warning said "CMakePresets.json" instead of "CMakeUserPresets.json". The existing test only covered the project file include path, so the bug went undetected. Resolution: fixed the message string and added a test for the user presets include path. Root cause: the plan's wording ("either presets file") was ambiguous about whether the warning should identify which file — a planning blind spot that led to the copy-paste.

- **File overlap between Tasks 2.2 and 2.3 (Managed).** Both tasks write to `config/presets.go`. The Wave 2 overlap analysis before launch flagged the risk. Resolution: serialized execution. No conflicts occurred. The Phase 1 lesson about parallel execution of disjoint packages (builder/ vs config/) did not apply here since both tasks target the same file.

- **Duplicate-name presets silently last-one-wins in `resolveInherits` index (Noted, not fixed).** The `byName` index in `resolveInherits` maps preset names to slice indices. If two presets share a name, the last one wins and the first becomes unreachable in the inherits graph. CMake itself rejects duplicate names, so this is acceptable — but noted by the code review as a potential future validation point.

## Lessons Learned

- **Nil vs non-nil empty slice semantics matter in Go API design.** The distinction between "no file found" (nil) and "file found but all entries filtered" (empty slice) is a common Go pattern. Getting it wrong produced a subtle bug: the "no usable presets" warning would fire even when no CMakePresets.json existed. The fix was a one-line change (`var` → `make`), but the design implication is significant — return value semantics should be specified in the plan when the distinction matters.

- **Serializing overlapping tasks is the safe default.** The Phase 1 lesson that parallel execution works well for disjoint packages holds. The corollary from Phase 2: when two tasks target the same file AND one calls the other's functions, serialization avoids both merge conflicts and API coordination issues. The overlap analysis before each wave is the right checkpoint.

- **Code review catches log message bugs that tests miss.** The wrong file name in the include warning is functionally irrelevant to test assertions (tests check for "include" substring, not the file name). The code reviewer read the actual string and noticed the mismatch. This reinforces the Phase 1 lesson that code review finds different things than tests — here it was a UX bug invisible to automated validation.

- **Named helper functions improve code review quality.** Extracting `isMultiConfigGenerator` and `normalizeGenerator` as standalone functions (beyond plan spec) made the code reviewer's job easier and produced targeted, independently verifiable tests. Inline logic in a 90-line orchestrator function is harder to reason about.

## Impact on Subsequent Phases

- **Phase 3 can proceed as planned.** The `buildPresetConfigs` helper, `loadPresetsMetadata` pipeline, and `Config.Preset` field are all in place. Phase 3's edge-case handling should modify `buildPresetConfigs` for any new logic (e.g., warning when preset binaryDir is outside source tree).

- **The Phase 1 `slog.Warn` for default BuildDir + preset is now redundant in auto-discovery mode.** Phase 1 added a warning when `Preset` is set but `BuildDir == "build"` (the default). In auto-discovery mode, `buildPresetConfigs` always sets `BuildDir` from preset metadata, so the default is never reached. The warning remains relevant only for manual preset references (`.cpp-build-mcp.json` with `"preset": "..."` but no `CMakePresets.json`). Phase 3 should consider whether this warning is still useful or just noise.

- **The `fakeBuilder.lastConfigureArgs` constraint still applies.** Phase 3 Task 3.3 E2E tests have the same limitation identified in Phase 1. The plan was already updated — E2E tests verify routing and config state, not argument construction. No additional changes needed.

- **`loadPresetsMetadata` nil/empty-slice contract must be maintained.** Any future changes to `loadPresetsMetadata` must preserve the nil (no file) vs empty (all filtered) distinction. `buildPresetConfigs` relies on this for correct warning behavior.
