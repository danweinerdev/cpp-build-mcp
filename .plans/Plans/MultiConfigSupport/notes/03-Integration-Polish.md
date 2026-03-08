---
title: "Phase 3 Debrief: Integration and Polish"
type: debrief
plan: "MultiConfigSupport"
phase: 3
phase_title: "Integration and Polish"
status: complete
created: 2026-03-08
---

# Phase 3 Debrief: Integration and Polish

## Decisions Made

- **Tasks 3.1 and 3.2 serialized despite no explicit dependencies.** Both tasks modify `main.go` (response structs, handler functions) and `main_test.go`. The overlap analysis identified moderate conflict risk — 3.1 modifies `handleBuildHealth` and adds health tests, 3.2 modifies response structs and all handlers. Serialization avoided merge conflicts. Tasks ran as 3.1 → 3.2 sequentially, then 3.3 in Wave 2.

- **`aggregateHealthToken()` created instead of reusing `storeStatusToken()`.** The plan specified reusing `storeStatusToken()` from Phase 1, but that function returns lowercase tokens (`"ok"`, `"built"`, `"unconfigured"`) and doesn't produce `"FAIL(N errors)"` — it maps `PhaseBuilt` to `"built"` unconditionally. The design spec requires uppercase tokens with error counts (`"OK"`, `"FAIL(3 errors)"`). A new `aggregateHealthToken()` was the correct approach. The plan overestimated what the existing helper could do.

- **`buildGraphResponse` wraps `*graph.GraphSummary` via struct embedding.** Adding a `Config` field to `get_build_graph` responses required a wrapper since `graph.GraphSummary` is in a separate package. Pointer embedding (`*graph.GraphSummary`) promotes all fields to the top-level JSON alongside `config`. This avoids modifying the graph package for a concern that belongs in the main package.

- **E2E tests placed in `e2e_test.go`, separate from `main_test.go`.** The new multi-config E2E tests use a different helper (`startMultiE2E` / `multiE2EEnv`) than the existing single-config E2E tests (`startE2E` / `e2eEnv`). Placing them in a separate file provides clear separation while remaining in the same package.

- **`registry.all()` method added for sorted instance iteration.** `handleBuildHealth` needs to iterate all instances in alphabetical order. Rather than exposing the internal `instances` map, a new `all()` method returns a sorted `[]*configInstance` slice under the read lock. This matches the pattern of `list()` which returns sorted `[]ConfigSummary`.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| `build://health` shows aggregate status across all configs | Met | Pipe-separated format: `"debug: OK \| release: FAIL(3 errors)"`. Single config returns existing verbose format unchanged. |
| All tool responses include `config` field | Met | All 8 response structs have `Config string` field, set to `inst.name` in every handler |
| E2E tests prove complete state isolation between configs | Met | 6 E2E tests: configure isolation, error isolation, independent build counts, list_configs status, health aggregation, comprehensive state isolation |
| README documents multi-config feature with examples | Met | Config file example, session walkthrough, updated config/tool reference tables, CMake Presets section |
| `go vet`, `go test -race`, and `staticcheck` all pass | Met | All 7 packages pass clean |
| Single-config mode behavior is completely unchanged | Met | `handleBuildHealth` branches on `registry.len() == 1`, returns existing `Health()` format. All existing tests pass. |

## Deviations

- **`aggregateHealthToken()` instead of `storeStatusToken()`.** The plan's subtask 3.1.2 said "Use the `storeStatusToken()` helper from Phase 1." The implementation created a new function because `storeStatusToken` returns the wrong format (lowercase, no error counts, maps built→"built" not "OK"/"FAIL"). This is a planning blind spot — the plan should have checked the existing function's output format.

- **`LastExitCode()` accessor added to `state/store.go`.** The plan didn't anticipate needing a new public method on Store. `aggregateHealthToken` needs the last exit code to distinguish `OK` (exit 0) from `FAIL` (exit non-zero) in the `PhaseBuilt` state. The existing `Health()` method already had this logic internally but didn't expose the raw value.

- **Code reviewer false-positive on CMake Presets README section.** The Task 3.3 reviewer flagged the CMake Presets documentation in README.md as documenting a feature that "does not exist in the codebase." This was incorrect — the CMakePresetsIntegration plan (3 phases, 11 tasks) was completed immediately before this phase. The reviewer only searched `main.go` and didn't find the presets code in `config/presets.go` and `config/config.go`. No action taken on this finding.

## Risks & Issues Encountered

- **`len()`/`all()` TOCTOU in `handleBuildHealth` (Minor, from code review).** The handler checks `registry.len() == 1` and then calls either `defaultInstance()` or `all()`, each acquiring the lock separately. Between the two calls, a goroutine could theoretically modify the registry. Resolution: added a comment documenting that the registry is append-only after startup, making the two-call pattern safe. If dynamic config registration is ever added, this would need consolidation to a single lock acquisition.

- **Vacuous test assertion in `TestBuildHealthSingleConfigReturnsVerboseFormat` (Minor, from code review).** The test used `strings.Contains(text, "OK:")` which would pass even if `Health()` emitted an unexpected format like `"OK: something wrong"`. Resolution: changed to `strings.HasPrefix(text, "OK:")` and added `"warnings"` check for tighter format verification.

- **Test name mismatch in `TestMultiConfigResponseRoutesConfigField` (Minor, from code review).** The test name implied it verified all tools' routing, but only tested `handleConfigure`. Resolution: renamed to `TestMultiConfigConfigureResponseRoutesConfigField` for accuracy. The broader multi-tool coverage is provided by Task 3.3's E2E tests.

- **Toolchain coupling in E2E test fixtures (Minor, from code review).** `startMultiE2E` sets `Toolchain: "clang"` without explaining why. The fakeBuilder stderr/stdout fixtures use Clang JSON diagnostic format. If someone changed the toolchain to "gcc", `diagnostics.Parse` would switch to a different parser and silently produce zero diagnostics, making error-count assertions pass vacuously. Resolution: added a comment documenting the coupling.

## Lessons Learned

- **Plan subtasks that reference existing helpers should verify the helper's output format.** The `storeStatusToken()` reuse assumption was wrong because nobody checked what the function actually returns. A one-line comment in the plan like "storeStatusToken returns lowercase 'ok'/'built'/etc" would have caught this mismatch with the uppercase `"OK"`/`"FAIL(N errors)"` spec.

- **Code reviewers need full codebase context.** The false-positive on CMake Presets documentation happened because the reviewer only searched `main.go`. When reviewing documentation that references features, the reviewer should search the full codebase, not just the files changed in the commit. This is an inherent limitation of scoped code review — the review scope was the commit diff, but the documentation's correctness depends on code outside the diff.

- **Serializing tasks that modify the same file remains the safe default.** Tasks 3.1 and 3.2 both modified `main.go` (response structs, handlers) and `main_test.go`. Serialization added no overhead since the tasks were small, and avoided any merge risk. The Phase 2 debrief lesson from CMakePresetsIntegration applies here too.

- **E2E tests in a separate file improve maintainability.** Putting multi-config E2E tests in `e2e_test.go` (alongside existing single-config E2E tests) keeps `main_test.go` focused on unit-level handler tests. The `multiE2EEnv` / `startMultiE2E` pattern parallels `e2eEnv` / `startE2E` cleanly.

- **Struct embedding for response wrappers is idiomatic.** Using `*graph.GraphSummary` embedding in `buildGraphResponse` avoids cross-package modification while keeping JSON output flat. This pattern can be reused if other packages' types need config field injection.

## Impact on Subsequent Phases

- **MultiConfigSupport plan is fully complete.** All 3 phases delivered. No further phases remain.

- **Both active plans (MultiConfigSupport and CMakePresetsIntegration) are now complete.** The codebase is in a stable state with full multi-config and preset auto-discovery support.

- **The `aggregateHealthToken()` / `storeStatusToken()` split is technical debt.** Two functions in `registry.go` map store state to string tokens with overlapping but incompatible semantics. If a new health format or status display is needed in the future, consider unifying them. For now, each serves a distinct purpose (list_configs vs build://health).

- **`fakeBuilder.configureCalled` pattern (from CMakePresetsIntegration Phase 3) is available.** The `configureCalled bool` field added to `fakeBuilder` during CMakePresetsIntegration review fixes is used by the multi-config E2E tests implicitly (via the existing `Configure` method setting it). Future tests can use this for positive dispatch assertions.
