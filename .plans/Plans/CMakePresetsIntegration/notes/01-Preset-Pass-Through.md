---
title: "Phase 1 Debrief: Preset Pass-Through"
type: debrief
plan: "CMakePresetsIntegration"
phase: 1
phase_title: "Preset Pass-Through"
status: complete
created: 2026-03-08
---

# Phase 1 Debrief: Preset Pass-Through

## Decisions Made

- **Kept `"make"` case in `generatorCMakeName` as dead code with comment.** The `NewBuilder` factory routes `Generator == "make"` to `MakeBuilder`, so `CMakeBuilder.buildConfigureArgs` never sees `"make"` in production. Considered removing it but kept it for completeness/documentation of the full mapping. Added a comment explaining the unreachability. The corresponding test exercises the function directly (valid unit test) even though the path is unreachable through the factory.

- **Shared diagnostic flags / CMakeArgs / extraArgs code after if/else.** Rather than duplicating the append logic in both the preset and non-preset branches, the if/else only sets the initial `args` slice, and the trailing appends (diagnostic flags, CMakeArgs, extraArgs) are shared. This keeps the two code paths minimal and reduces drift risk.

- **`slog.Warn` for default BuildDir with preset accepted as-is (no constant extraction).** The reviewer suggested extracting `DefaultBuildDir` as a named constant to avoid the hardcoded `"build"` comparison. Decided against it — Phase 2 auto-discovery eliminates this scenario entirely by setting `BuildDir` from the preset's `binaryDir`. The false-positive risk (user explicitly sets `build_dir: "build"` matching their preset) is low and temporary.

- **E2E tests verify routing, not `--preset` arg construction.** The poke-holes analysis identified that `fakeBuilder.lastConfigureArgs` captures only `extraArgs` (the `cmake_args` tool parameter), not the full `buildConfigureArgs` output. This was a critical finding that changed the E2E test strategy: integration tests verify config parsing and dispatch routing; `--preset` arg construction is verified exclusively by unit tests in `cmake_test.go`. The plan was updated before implementation to reflect this constraint.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| `buildConfigureArgs` uses `cfg.Generator` (not hardcoded `"Ninja"`) | Met | `generatorCMakeName(b.cfg.Generator)` at `cmake.go:100` |
| `Config.Preset` field exists and parses from `.cpp-build-mcp.json` | Met | `Config.Preset string` at `config.go:23`, tested in both single and multi-config |
| `buildConfigureArgs` with `Preset != ""` produces `--preset` without `-S`/`-B`/`-G` | Met | 5 unit tests covering preset mode, non-preset regression, diag flags, CMakeArgs, extraArgs |
| Existing tests pass unchanged (backward compatible) | Met | All pre-existing tests pass; only the "basic configure args" test was updated to explicitly set `Generator: "ninja"` |
| `go vet`, `go test -race`, and `staticcheck` all pass | Met | All 7 packages pass clean |

## Deviations

- **Task 1.4 verification criteria revised before implementation.** The original plan said E2E tests would "verify the builder receives `--preset` args." The poke-holes analysis proved this was impossible with `fakeBuilder`. The plan was corrected before any code was written — E2E tests now verify config routing and `Config.Preset` population instead.

- **Tasks 1.1 and 1.2 landed in a single commit.** Both agents ran in parallel (Wave 1) and their changes were committed together as `4786148`. No conflict occurred because they touch disjoint files (`builder/` vs `config/`).

- **`Preset + Generator == "make"` interaction documented but not guarded.** The code review surfaced that setting both `preset` and `generator: "make"` would route to `MakeBuilder` (whose Configure is a no-op), silently ignoring the preset. Rather than adding validation, we documented the interaction in the phase notes — preset support is cmake-specific by design, and combining it with the make builder is user error.

## Risks & Issues Encountered

- **`fakeBuilder` limitation (Critical, from poke-holes).** The `fakeBuilder.lastConfigureArgs` field captures only handler-level `extraArgs`, not the full `buildConfigureArgs` output. This was identified during the adversarial analysis before implementation began. Resolution: revised the plan to scope E2E tests to routing verification, with `--preset` coverage delegated to unit tests. No code workaround needed — the test strategy was adjusted.

- **Reviewer found `"make"` case in `generatorCMakeName` is dead code (Major).** `NewBuilder` routes `Generator == "make"` to `MakeBuilder`, so `CMakeBuilder` never sees it. Resolution: added a comment explaining the unreachability and a note to the phase doc about the `Preset + "make"` interaction.

- **Missing ordering assertion in preset extraArgs test (Minor).** The `"preset mode with extraArgs"` test verified `--extra` was present but didn't assert it appeared after `--preset`. Resolution: added the ordering check.

- **Missing negative dispatch assertion (Minor).** `TestMultiConfigPresetFieldIntegration` didn't check that the release builder was NOT called when routing to debug. Resolution: added `releaseFB.lastConfigureArgs != nil` guard.

## Lessons Learned

- **Adversarial analysis before implementation pays off.** The poke-holes review caught a critical flaw in the test strategy (the `fakeBuilder` limitation) that would have been discovered much later during implementation, wasting time writing tests that couldn't verify what they claimed. Running `/poke-holes` on the plan before `/implement` should be standard practice.

- **Wave 1 parallelism works well for disjoint packages.** Tasks 1.1 (builder/) and 1.2 (config/) had zero file overlap, making parallel execution clean. The single-commit outcome was a minor artifact but caused no issues.

- **Code review catches architectural gaps, not just bugs.** The `"make"` dead-code finding is not a bug — the code is correct. But it reveals an architectural gap (factory routing vs. builder internals) that would confuse future readers. Comments are the cheapest fix.

## Impact on Subsequent Phases

- **Phase 2 can proceed as planned.** The `Config.Preset` field, `buildConfigureArgs` preset branch, and `generatorCMakeName` helper are all in place. Phase 2's `loadPresetsMetadata` will set `Config.Preset` programmatically (same field, different source).

- **Phase 2 Task 2.4 `applyJSON` guard is critical.** The `slog.Warn` for default BuildDir in `buildConfigureArgs` is a Phase 1 workaround. Phase 2 sets `BuildDir` from preset metadata, but `applyJSON` could overwrite it with `.cpp-build-mcp.json`'s top-level `build_dir`. The post-override guard (restoring `BuildDir`/`Preset`/`Generator` from metadata after `applyJSON`) was moved from Phase 3 to Phase 2 Task 2.4 during plan review — this decision was correct.

- **The `fakeBuilder.lastConfigureArgs` constraint carries forward.** Phase 3 Task 3.3 E2E tests have the same limitation. The plan was already updated to reflect this — E2E tests verify routing and config state, not argument construction.
