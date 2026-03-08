---
title: "Multi-Configuration Build Support"
type: plan
status: complete
created: 2026-03-07
updated: 2026-03-08
tags: [go, mcp, multi-config, registry]
related: [Designs/MultiConfigSupport]
phases:
  - id: 1
    title: "Registry Foundation"
    status: complete
    doc: "01-Registry-Foundation.md"
  - id: 2
    title: "Multi-Config File Format"
    status: complete
    doc: "02-Multi-Config-Format.md"
    depends_on: [1]
  - id: 3
    title: "Integration and Polish"
    status: complete
    doc: "03-Integration-Polish.md"
    depends_on: [2]
---

# Multi-Configuration Build Support

## Overview

Extend cpp-build-mcp to support multiple named build configurations simultaneously (e.g., Debug + Release, or different toolchains). Introduces a `configRegistry` that maps named configurations to independent `(Config, Builder, Store)` tuples, letting an AI agent work across multiple build types in one session.

The primary use case: an AI agent fixes an error in Debug mode, then verifies the fix also compiles in Release — where different optimizations may trigger different warnings or errors.

Full backward compatibility: single-config users see no behavior change.

## Architecture

See `Designs/MultiConfigSupport/README.md` for the full design. Key insight: the existing Builder, Store, and Diagnostic parsers are already scoped to one configuration. Multi-config means having **multiple instances**, not modifying the instances themselves.

```
mcpServer {
    registry: *configRegistry {
        "debug"   -> { Config, CMakeBuilder, Store }
        "release" -> { Config, CMakeBuilder, Store }
    }
}
```

All tool handlers gain an optional `config` parameter that routes to the correct instance. When omitted, the default configuration is used.

## Key Decisions

1. **Multiple instances over parameterized single instance** — reuses all existing code without modification
2. **Optional `config` parameter with default** — backward compatible, no tool call changes needed for single-config users
3. **Per-config locking** — each config has its own Store with its own `BuildInProgress` flag; natural outcome of multiple instances
4. **Top-level config inheritance** — shared fields (source_dir, generator, toolchain) defined once at top level, per-config entries override selectively
5. **Aggregate health resource** — `build://health` shows all configs at a glance; per-config detail via `list_configs`

## Dependencies

- Completed BuildIntelligenceMCP plan (all 5 phases)
- Reviewed MultiConfigSupport design document (currently in `review` status)
- No new external Go dependencies
