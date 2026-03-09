---
title: "Diagnostic Stream & Format Fix"
type: design
status: approved
created: 2026-03-08
updated: 2026-03-08
tags: [diagnostics, clang, sarif, gcc, stderr, builder]
related: [Designs/BuildIntelligenceMCP]
---

# Diagnostic Stream & Format Fix

## Overview

Empirical testing (Clang 21.1.8, GCC 15.2.1, Fedora 43) revealed two bugs in the diagnostic parsing pipeline:

**Bug 1 — Clang: wrong flag, wrong stream.** The builder injects `-fdiagnostics-format=json` for Clang. Clang **rejects this flag entirely** with `error: invalid value 'json' in '-fdiagnostics-format json'`, causing every Clang compilation unit to fail. The correct flag is `-fdiagnostics-format=sarif`, which produces SARIF 2.1.0 JSON on **stderr** (not stdout). The `ClangParser` reads only stdout.

**Bug 2 — GCC: wrong stream.** GCC's `-fdiagnostics-format=json` works correctly, but GCC 15 writes the JSON output to **stderr** (not stdout). The `GCCParser` reads only stdout, silently losing all structured diagnostics.

Both bugs are documented with passing tests in `diagnostics/clang_sarif_evidence_test.go`.

This design fixes both:
1. **Builder layer:** Inject `-fdiagnostics-format=sarif -Wno-sarif-format-unstable` for Clang (cmake.go, make.go)
2. **ClangParser:** Add SARIF format detection, stderr fallback, and SARIF parsing
3. **GCCParser:** Add stderr fallback (same stdout-first pattern)

## Empirical Evidence

Tested on the actual system. Results in `diagnostics/clang_sarif_evidence_test.go`.

| Compiler | Flag | Stream | Format | Status |
|----------|------|--------|--------|--------|
| Clang 21 | `-fdiagnostics-format=json` | — | — | **Hard error** — flag rejected |
| Clang 21 | `-fdiagnostics-format=sarif` | stderr | SARIF 2.1.0 `{...}` | Works |
| GCC 15 | `-fdiagnostics-format=json` | stderr | JSON array `[...]` | Works |
| GCC 15 | `-fdiagnostics-format=sarif` | — | — | **Rejected** — use `sarif-stderr` |

**Clang stderr details:** Output is `\n` + SARIF JSON (single line) + `\n\n` + `"N warning(s) generated.\n"`. The trailing summary text is outside the JSON object. Leading blank line requires `TrimSpace` before format detection.

**Clang ruleId:** Numeric string (e.g., `"7538"`), not a `-W` flag name like GCC uses. Maps to `Diagnostic.Code`.

## Architecture

### Components

```
builder/
├── cmake.go          # Flag injection — sarif for clang (line 132-136)
├── cmake_test.go     # Updated assertions for sarif flags
├── make.go           # Env var injection — toolchain-aware flag selection (line 92-122)
└── make_test.go      # Updated + new tests for toolchain-conditioned injection

diagnostics/
├── clang.go          # ClangParser — SARIF detection + stderr fallback
├── sarif.go          # SARIF types and parsing logic (new file)
├── sarif_test.go     # SARIF-specific tests (new file)
├── clang_test.go     # SARIF integration tests; "stderr is ignored" renamed
├── gcc.go            # GCCParser — stderr fallback (Bug 2 fix)
├── gcc_test.go       # Updated: "stderr is ignored" renamed + new stderr tests
├── parser.go         # NewParser — unchanged
└── types.go          # Diagnostic, DiagnosticParser — unchanged
```

### Data Flow

```
CMake configure (cmake.go)
        │
        ├── toolchain == "clang" && InjectDiagnosticFlags
        │     └── -DCMAKE_C_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable
        │         -DCMAKE_CXX_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable
        │
        └── toolchain == "gcc" && InjectDiagnosticFlags
              └── (unchanged — -fdiagnostics-format=json via GCC's own path)

Make build (make.go)
        │
        ├── toolchain == "clang" && InjectDiagnosticFlags
        │     └── CFLAGS/CXXFLAGS += -fdiagnostics-format=sarif -Wno-sarif-format-unstable
        │
        └── other toolchain && InjectDiagnosticFlags
              └── CFLAGS/CXXFLAGS += -fdiagnostics-format=json (unchanged)

Build subprocess → stdout + stderr
        │
        ├──► ClangParser.Parse(stdout, stderr)
        │     ├── 1. Strip Ninja progress lines from BOTH streams
        │     ├── 2. Select stream: stdout if has structured content, else stderr, else nil
        │     ├── 3. Detect format: '{' → SARIF, '[' → native JSON
        │     ├── [sarif]      → parseSARIF → []Diagnostic
        │     └── [clang-json] → existing splitJSONArrays path (unchanged)
        │
        └──► GCCParser.Parse(stdout, stderr)
              ├── 1. Select stream: stdout if non-empty after trim, else stderr
              └── 2. Existing splitJSONArrays + unmarshal path (unchanged logic)
```

**Stream selection predicate (both parsers):** "Has structured content" means the first non-whitespace character is `{` or `[`. For GCC, the simpler "non-empty after TrimSpace" suffices since GCC JSON is always `[...]` — no format detection needed.

### Interfaces

**No changes to public interfaces.** `DiagnosticParser`, `NewParser`, `Diagnostic`, config types are unchanged.

New unexported function signatures:

```go
// sarif.go
func parseSARIF(data string) ([]Diagnostic, error)
func splitJSONObjects(s string) []string
func stripFileURI(uri string) string
func mapSARIFLevel(level string) Severity

// clang.go (modification)
func detectOutputFormat(s string) string  // returns "sarif", "clang-json", or ""
func hasStructuredContent(s string) bool  // first non-ws char is '{' or '['
```

No new functions needed for `gcc.go` — it gains a stderr fallback inside `Parse`, but no new helpers.

## Design Decisions

### Decision 1: Fix the injected flag (sarif instead of json for Clang)

**Context:** Clang **rejects** `-fdiagnostics-format=json` with a hard error. Confirmed empirically: `error: invalid value 'json' in '-fdiagnostics-format json'`. This breaks every Clang compilation unit.

**Decision:** Change to `-fdiagnostics-format=sarif -Wno-sarif-format-unstable` for Clang.

**Rationale:** `-fdiagnostics-format=json` is not valid for Clang. The `-Wno-sarif-format-unstable` flag suppresses the unstable-format warning and is silently ignored by Clang versions that don't recognize it (unknown `-Wno-*` flags are not errors).

### Decision 2: Auto-detect format inside ClangParser

**Context:** After changing the injected flag, the parser primarily receives SARIF. We want backward compatibility for users who may have manually configured other flags.

**Decision:** Auto-detect inside `ClangParser.Parse` based on first non-whitespace character.

**Rationale:** SARIF is always a JSON object (`{...}`), native Clang JSON is always an array (`[...]`). Structural invariant.

### Decision 3: Stream selection — stdout priority with stderr fallback (both parsers)

**Context:** Both Clang SARIF and GCC JSON go to stderr on the tested platform. Both parsers currently read only stdout.

**Decision:** Both `ClangParser` and `GCCParser` check stdout first; fall back to stderr if stdout has no content after stripping.

**Rationale:** Stdout-first preserves backward compatibility for any environment where output goes to stdout. The stderr fallback catches the confirmed real-world behavior. The existing `"stderr is ignored"` tests in both parsers validate the priority: when both streams have content, stdout wins.

### Decision 4: Parallel implementations, not shared helper

**Context:** Both parsers need stdout-first/stderr-fallback stream selection. Should we extract a shared helper?

**Options Considered:**
1. Shared `selectStream(stdout, stderr string) string` helper in a new `stream.go`
2. Parallel implementations in each parser

**Decision:** Option 2 — parallel implementations.

**Rationale:** The details differ: `ClangParser` strips Ninja lines from both streams and needs format detection (`{` vs `[`). `GCCParser` does a simpler non-empty check. A shared helper would need to be parameterized for these differences, making it more complex than two simple inline checks. If a third parser needs the same pattern in the future, extract then.

### Decision 5: SARIF types — minimal

**Decision:** Minimal types covering only the fields we map to `Diagnostic`.

**Fields needed:**
- `runs[].results[].message.text` → `Diagnostic.Message`
- `runs[].results[].locations[].physicalLocation.artifactLocation.uri` → `Diagnostic.File` (after URI stripping)
- `runs[].results[].locations[].physicalLocation.region.startLine` → `Diagnostic.Line`
- `runs[].results[].locations[].physicalLocation.region.startColumn` → `Diagnostic.Column`
- `runs[].results[].level` → `Diagnostic.Severity`
- `runs[].results[].ruleId` → `Diagnostic.Code`

### Decision 6: Concatenated SARIF splitting

**Context:** With Ninja `-j > 1`, multiple TUs may emit SARIF objects to the same stream (`{...}{...}`).

**Decision:** `splitJSONObjects` using brace-depth tracking, consistent with existing `splitJSONArrays` pattern. Trailing text (e.g., `"1 warning generated."`) is naturally skipped since it doesn't start with `{`.

### Decision 7: make.go toolchain-aware flag selection

**Context:** `make.go:buildEnv()` injects `-fdiagnostics-format=json` unconditionally.

**Decision:** `diagnosticFlag()` method with switch on `b.cfg.Toolchain`:
```go
func (b *MakeBuilder) diagnosticFlag() string {
    switch strings.ToLower(b.cfg.Toolchain) {
    case "clang":
        return "-fdiagnostics-format=sarif -Wno-sarif-format-unstable"
    default:
        return "-fdiagnostics-format=json"
    }
}
```

### Decision 8: CMake flag string format

**Decision:** Space-separated string in a single `-D` argument. `exec.CommandContext` passes each slice element as a single OS-level argument; CMake splits `CMAKE_C_FLAGS` on whitespace.

### Decision 9: mapSARIFLevel default for unknown levels

**Decision:** Unknown levels map to `SeverityWarning`. Conservative but not alarmist.

### Decision 10: URI stripping for file:// URIs

**Decision:** `stripFileURI` handles:
- `file:///absolute/path` → `/absolute/path` (triple slash = empty authority + absolute path)
- `file://host/path` → left as-is (authority form)
- `file:relative/path` → `relative/path`
- Bare path (no scheme) → unchanged

### Decision 11: GCC stderr fix — minimal change

**Context:** `GCCParser.Parse` reads only stdout. GCC 15 writes JSON to stderr.

**Options Considered:**
1. Only change `GCCParser` to read stderr instead of stdout
2. Stdout-first with stderr fallback (same pattern as Clang)
3. Read both and concatenate

**Decision:** Option 2 — stdout-first with stderr fallback.

**Rationale:** Option 1 might break environments where GCC writes to stdout (older GCC or different platforms). Option 2 handles both cases. Option 3 risks double-counting. The implementation is 4 lines: if stdout is empty after trim, use stderr instead.

```go
func (p *GCCParser) Parse(stdout, stderr string) ([]Diagnostic, error) {
    input := strings.TrimSpace(stdout)
    if input == "" {
        input = strings.TrimSpace(stderr)
    }
    if input == "" {
        return nil, nil
    }
    // ... existing splitJSONArrays + unmarshal logic using 'input' ...
}
```

## Error Handling

**Malformed SARIF:** Returns single fallback `Diagnostic` with `Severity: error` and truncated output — same pattern as existing Clang/GCC JSON fallbacks.

**Missing SARIF fields:** Results with no `locations` → empty File, zero Line/Column. Message is still returned.

**Empty runs/results:** Returns `nil, nil`.

**Neither stream has structured content (Clang):** Returns `nil, nil`.

**Both streams empty (GCC):** Returns `nil, nil` — same as current behavior with empty stdout.

**Clang stderr trailing text:** `"N warning(s) generated."` after SARIF JSON is outside the `{...}` boundary and is naturally ignored by `splitJSONObjects` brace-depth tracking.

**Clang stderr leading blank line:** `TrimSpace` before stream selection handles this.

## Testing Strategy

### Unit Tests (sarif_test.go)

| Test | What it validates |
|------|-------------------|
| Single SARIF error | File, line, column, severity, message, ruleId → Code, source |
| Single SARIF warning | Warning level maps to SeverityWarning |
| SARIF note level | Note level maps to SeverityNote |
| Unknown SARIF level | Maps to SeverityWarning (Decision 9) |
| Multiple results in one run | All results converted to diagnostics |
| Multiple runs | Results from all runs merged into single slice |
| Empty results array | Returns nil slice |
| Missing locations | Diagnostic has empty File, zero Line/Column |
| `file:///absolute/path` URI | Stripped to `/absolute/path` |
| `file://host/path` URI | Left as-is |
| `file:relative/path` URI | Stripped to `relative/path` |
| Bare path (no scheme) | Left unchanged |
| Malformed SARIF | Returns fallback error diagnostic |
| Concatenated SARIF objects | `splitJSONObjects` splits `{...}{...}` correctly |
| `splitJSONObjects` with trailing text | `"1 warning generated."` after `}` is ignored |

### ClangParser Integration Tests (clang_test.go updates)

| Test | What it validates |
|------|-------------------|
| SARIF on stdout | ClangParser.Parse detects and parses SARIF from stdout |
| SARIF on stderr (stdout empty) | ClangParser.Parse falls back to stderr |
| Real Clang stderr format | Leading `\n` + SARIF + `\n\n` + summary text → parsed correctly |
| Native JSON on stdout (existing tests) | No regression |
| `"stderr not used when stdout has content"` (renamed) | Stdout wins |
| Ninja progress + SARIF | Progress lines stripped from both streams |
| Neither stream has structured content | Returns nil, nil |

### GCCParser Integration Tests (gcc_test.go updates)

| Test | What it validates |
|------|-------------------|
| `"stderr not used when stdout has content"` (renamed) | Stdout wins |
| GCC JSON on stderr (stdout empty) | GCCParser falls back to stderr |
| Both streams empty | Returns nil, nil (no regression) |
| Existing tests | All pass unchanged |

### Builder Tests

| Test | What it validates |
|------|-------------------|
| `cmake.go`: Clang injects sarif flags | Full `-DCMAKE_C_FLAGS=-fdiagnostics-format=sarif -Wno-sarif-format-unstable` |
| `cmake.go`: GCC does not inject flags | No regression |
| `make.go`: Clang toolchain injects sarif | `CFLAGS` contains sarif flag |
| `make.go`: GCC toolchain injects json | Updated with `Toolchain: "gcc"` |
| `make.go`: Auto/empty toolchain injects json | Default case |
| `make.go`: Inject disabled → no flags | No regression |

### Evidence Tests (clang_sarif_evidence_test.go — already exists)

| Test | What it validates |
|------|-------------------|
| Current parser phantom diagnostic | Documents `splitJSONArrays` extracting inner SARIF arrays |
| Current parser stderr miss | Documents that SARIF on stderr is lost |
| Clang json flag rejection | Documents that `-fdiagnostics-format=json` is invalid for Clang |
| GCC stderr mismatch | Documents that GCC JSON on stderr is missed |
| SARIF format detection | Validates TrimSpace needed, trailing text presence, numeric ruleId |

### Structural Verification

| Tool | When | What it catches |
|------|------|-----------------|
| `go vet ./...` | Every phase | Printf format mismatches, unreachable code |
| `go test -race ./...` | Every phase | Data races |
| `staticcheck ./...` | If available | Additional correctness checks |

## Migration / Rollout

**Parser changes are backward compatible. Builder flag change requires CMake reconfigure.**

1. **ClangParser changes** — backward compatible. SARIF and native JSON both handled. Stderr fallback is additive.
2. **GCCParser changes** — backward compatible. Stderr fallback is additive. Existing stdout path unchanged.
3. **CMake builder flag change** — takes effect on next `configure`. Existing build dirs keep their flags until reconfigured. **Note:** Since Clang rejects `-fdiagnostics-format=json`, existing Clang builds are already broken; the reconfigure fixes them.
4. **Make builder flag change** — takes effect on next `Build` call (no configure step).
5. Users with `inject_diagnostic_flags: false` are unaffected.

**Rollout sequence (tests green at every step):**
1. Add `sarif.go` with types + `parseSARIF` + `splitJSONObjects` + `stripFileURI` + `mapSARIFLevel`
2. Add `sarif_test.go` with unit tests
3. Modify `clang.go`: add format detection, stream selection, Ninja stripping on both streams, SARIF dispatch
4. Update `clang_test.go`: rename stderr test, add SARIF integration tests
5. Modify `gcc.go`: add stderr fallback in `Parse`
6. Update `gcc_test.go`: rename stderr test, add stderr fallback tests
7. Update `builder/cmake.go` to inject SARIF flags for Clang
8. Update `builder/cmake_test.go` to assert SARIF flags
9. Update `builder/make.go` to be toolchain-aware
10. Update `builder/make_test.go` with toolchain-specific tests
11. Run full test suite + `go vet` + `staticcheck`
