---
title: "Phase 1 Debrief: Builder Infrastructure"
type: debrief
plan: "BuildProgressNotifications"
phase: 1
phase_title: "Builder Infrastructure"
status: complete
created: 2026-03-09
---

# Phase 1 Debrief: Builder Infrastructure

## Decisions Made

- **io.MultiWriter tee on stderr** — followed the design exactly, attaching the scanner goroutine to stderr. This was the root cause of the post-completion bug (see Risks & Issues below).
- **progressMinInterval as builder field** — made test overriding clean and deterministic. Good call.
- **sync.WaitGroup + panic recovery** — implemented as designed. No issues observed.

## Requirements Assessment

| Criterion | Status | Notes |
|-----------|--------|-------|
| ProgressFunc type exported | Met | |
| SetProgressFunc sets/clears callback | Met | |
| run() tees stream, scanner matches [N/M] | Partially Met | Tee was on wrong stream (stderr instead of stdout) |
| run() without progress identical to existing | Met | |
| Throttle limits frequency; final line always sent | Met | |
| sync.WaitGroup prevents data race | Met | |
| BuildResult.Stderr contains complete output | Met | |
| go vet, go test -race pass | Met | Tests passed but exercised the wrong stream |

## Deviations

- **No deviations from plan during implementation.** The plan and design both specified stderr as the scanner source, and the implementation followed faithfully. The deviation was in the design's assumptions — see Risks & Issues.

## Risks & Issues Encountered

- **Critical bug: scanner attached to stderr, but Ninja writes progress to stdout.** The design document, plan, and all phase docs stated "tee stderr" because the assumption was that Ninja writes `[N/M]` lines to stderr. This turned out to be wrong — Ninja writes progress to **stdout**. Confirmed empirically by running `cmake --build` on Fusion with separated streams: stdout had 2613 lines of `[N/M]` progress, stderr had 0 lines. Fix: `d71fe2d` — moved `io.MultiWriter` from `cmd.Stderr` to `cmd.Stdout`.

- **Unit tests passed despite the bug** because the shell scripts used `>&2` to write test progress to stderr, matching the (incorrect) implementation. The tests validated the scanner mechanics correctly, but the stream assumption was baked into both the code and the tests. This is a case where unit tests can't catch an integration-level assumption error.

## Lessons Learned

- **Verify stream assumptions empirically before designing.** The Ninja-writes-to-stderr assumption was plausible and went unquestioned through design review, plan review, implementation, and testing. A single `cmake --build ... 1>/dev/null` vs `2>/dev/null` test during design would have caught it.
- **Integration tests with real tools catch what unit tests with shell scripts miss.** The e2e tests used `progressFakeBuilder` which called the callback directly — they never exercised the actual stream tee. A test that ran a real Ninja build (even a trivial one) would have caught this immediately.
- **When tests use synthetic input, make sure the synthetic input matches real-world behavior.** The `>&2` in test scripts encoded the same wrong assumption as the production code.

## Impact on Subsequent Phases

- Phase 2 (Handler Integration) was unaffected — it wires `progressSetter` and `SendNotificationToClient`, which are stream-agnostic. The fix was entirely within Phase 1's `run()` method.
- The design document and plan README still reference "tee stderr" — these should be updated for accuracy but are not blocking.
