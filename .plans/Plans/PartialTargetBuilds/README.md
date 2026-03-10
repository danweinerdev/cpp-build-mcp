---
title: "Partial Target Builds"
type: plan
status: complete
created: 2026-03-09
updated: 2026-03-09
tags: [mcp, cmake, targets, build-system]
related: [Designs/PartialTargetBuilds]
phases:
  - id: 1
    title: "Target Discovery"
    status: complete
    doc: "01-Target-Discovery.md"
  - id: 2
    title: "Response Enhancements and Clean Hardening"
    status: complete
    doc: "02-Response-and-Clean.md"
    depends_on: [1]
---

# Partial Target Builds

## Overview

Add target discovery to the MCP server so AI agents can enumerate available CMake build targets before requesting partial builds. The `build` tool already supports `targets: ["name"]` end-to-end — this plan adds the missing `list_targets` tool, enhances the build response to echo back requested targets, and fixes a pre-existing bug in `handleClean`.

## Architecture

The implementation touches three layers:

1. **Builder interface** (`builder/builder.go`) — add `ListTargets(ctx) ([]TargetInfo, error)`
2. **CMakeBuilder** (`builder/cmake.go`) — implement via `cmake --build <dir> --target help`, parse output with `parseTargetList`
3. **MCP handlers** (`main.go`) — new `list_targets` tool, enhanced `buildResponse`, simplified `clean` tool

See `Designs/PartialTargetBuilds/README.md` for full architecture, parser specification, and design decisions.

## Key Decisions

1. **`cmake --build --target help`** for discovery — official cmake mechanism, generator-agnostic
2. **Names only, no type classification** — Ninja/Make help output doesn't reliably indicate executable vs library
3. **No pre-validation** — let cmake fail naturally on invalid targets (avoids TOCTOU, saves a round-trip)
4. **Remove `targets` from `clean`** — CMake has no per-target clean; the param was silently ignored
5. **Reject `list_targets` during builds** — both commands invoke Ninja, which locks its state files

## Dependencies

- No new Go dependencies
- No new external tools — uses existing cmake CLI
- Requires the `Builder` interface to gain one method (breaking change for `MakeBuilder` — must add stub)
