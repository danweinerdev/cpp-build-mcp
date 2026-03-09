---
title: "v1 Launch and Real-World Validation"
type: retro
status: draft
created: 2026-03-09
updated: 2026-03-09
tags: [milestone, validation, bugs, process]
related:
  - Plans/BuildIntelligenceMCP
  - Plans/MultiConfigSupport
  - Plans/CMakePresetsIntegration
  - Plans/BuildProgressNotifications
  - Plans/ClangSARIFSupport
---

# v1 Launch and Real-World Validation

Covers the full arc from initial scaffold through five completed plans to real-world validation against the Fusion project (Clang 21, GCC 15, Fedora 43, CMake presets).

## What Went Well

- **Evidence-first bug discovery.** Writing `clang_sarif_evidence_test.go` with real compiler output before designing the SARIF fix meant every design decision was grounded in empirical data, not documentation assumptions. The evidence tests caught both the Clang flag rejection and GCC stream mismatch before implementation started.

- **Plan/design/implement pipeline.** The structured planning workflow (research → design → plan → implement → debrief) consistently produced clean implementations. Phases 1-4 of ClangSARIFSupport and the Handler Integration phase of BuildProgressNotifications had zero post-completion issues.

- **Parallel parser implementations over shared abstractions.** Keeping ClangParser and GCCParser stream selection separate (Decision 4 in the SARIF design) avoided premature abstraction. The parsers have different stripping/detection needs, and parallel 4-line implementations are clearer than a parameterized helper.

- **progressSetter type assertion pattern.** Not changing the Builder interface kept every test fake and MakeBuilder untouched. The optional-capability pattern (`if ps, ok := builder.(progressSetter)`) is clean and idiomatic.

- **Test coverage is strong.** 506 tests across 7 packages, 3.2:1 test-to-production line ratio. The test suite reliably catches regressions within its scope.

- **DiagnosticFormat.cmake injection.** The CMAKE_PROJECT_INCLUDE approach for probing compiler diagnostic support at configure time is elegant — it lets downstream projects (like Fusion) adopt the results without needing to know about cpp-build-mcp's internals.

## What Could Be Improved

- **Stream assumptions weren't validated empirically before design.** The progress scanner assumed Ninja writes `[N/M]` to stderr. The design doc stated it, the plan repeated it, the tests encoded it, the implementation followed it — and it was wrong. A 10-second `cmake --build ... 1>/dev/null` test during design would have caught it. **This is the single biggest process failure in the project so far.**

- **Unit tests with synthetic input can encode wrong assumptions.** The progress tests used `echo "..." >&2` shell scripts, perfectly matching the (wrong) implementation. The tests validated scanner mechanics but not the stream assumption. When tests are written against the same mental model as the code, they can't catch errors in that model.

- **No integration test against a real CMake project.** The e2e tests use `fakeBuilder` / `progressFakeBuilder` which call callbacks directly, never running cmake or Ninja. The CMAKE_PROJECT_INCLUDE path resolution bug and the progress stream bug both would have been caught by a single test building a trivial CMake project.

- **Path resolution edge cases with presets.** `CMAKE_PROJECT_INCLUDE` with a relative path works when source dir equals working dir, which is the common case for non-preset builds. Preset builds with deeply nested `binaryDir` values (e.g., `tmp/Linux/X64/Clang/Debug`) break this. The fix (absolute paths via `filepath.Abs`) was simple, but the bug wasn't anticipated.

- **Design docs become stale after bug fixes.** The BuildProgressNotifications design still references "tee stderr" in its architecture diagram even though the plan README was updated. Design docs should be treated as living documents when the implementation diverges.

## Action Items

- [ ] **Add a real-cmake smoke test.** Create a minimal CMake project (one .cpp file) in `testdata/` and add a test that runs `Configure` + `Build` end-to-end. This catches path resolution, stream, and flag injection issues that fakes miss.
- [ ] **Add a real-ninja progress test.** Extend the smoke test to set a `ProgressFunc` and verify at least one `[N/M]` callback fires during a real build. This directly validates the stream assumption.
- [ ] **Update design docs after post-completion fixes.** BuildProgressNotifications design still says "tee stderr" in the architecture diagram. Add a "Post-Completion Updates" section or correct inline.
- [ ] **Document stream behavior in code comments.** Add a comment in `run()` explaining *why* the scanner is on stdout (Ninja writes progress there), so future maintainers don't "fix" it back to stderr.
- [ ] **Consider a "validate assumptions" checklist for designs.** Before approving a design, explicitly list empirical assumptions and how they were verified. "Ninja writes progress to stderr" would have been flagged as unverified.

## Key Metrics

| Metric | Value | Notes |
|--------|-------|-------|
| Plans completed | 5 | BuildIntelligenceMCP, MultiConfigSupport, CMakePresetsIntegration, BuildProgressNotifications, ClangSARIFSupport |
| Phases completed | 17 | Across all 5 plans |
| Total commits | 66 | From initial scaffold to current |
| Go source files | 35 | 18 test files, 17 production |
| Lines of Go | 16,171 | 12,335 test, 3,836 production |
| Test count | 506 | Across 7 packages |
| Test:production ratio | 3.2:1 | By line count |
| Post-completion bugs | 3 | Progress stream, CMAKE_PROJECT_INCLUDE path, build dir creation |
| Bugs caught by tests | 0/3 | All three required real-world integration to surface |
| Bugs caught by real usage | 3/3 | Fusion project validation |

## Takeaways

**The test suite is excellent at catching regressions but cannot validate assumptions.** All three post-completion bugs shared a pattern: the tests encoded the same mental model as the code. When the model was wrong (Ninja stream, path resolution semantics), the tests passed happily. The only thing that caught these bugs was running against a real project with real tools.

**Evidence tests are the strongest tool in the workflow.** The SARIF work had zero post-completion issues in the parser layer because `clang_sarif_evidence_test.go` was written first with real compiler output. The progress and path bugs had no equivalent empirical validation before implementation.

**The planning pipeline works, but needs a "verify assumptions" gate.** Research → design → plan → implement is a strong workflow. The gap is between research and design: assumptions made during research need to be flagged and empirically validated before they become design decisions. A simple checklist ("how do we know Ninja writes to stderr?") would close this gap.

**Absolute paths are always safer for cross-process file references.** CMake resolves paths relative to its own source directory, not the caller's working directory. Any time one process passes a file path to another (especially via `-D` flags or environment variables), use absolute paths.
