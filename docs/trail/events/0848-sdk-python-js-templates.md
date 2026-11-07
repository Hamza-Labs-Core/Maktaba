---
schema: 1
kind: pr
pr: 848
title: Python and JavaScript plugin templates (mkp new, build, world)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- `mkp new python|js` makes a project.
- `mkp build` runs componentize-py (`--stub-wasi`) or jco (`--disable all`) into `package/plugin.wasm`, then runs the home's validation and audit.
- `mkp world` writes the project's WIT with the `plugin` world narrowed to the ungated interfaces and those its manifest's capabilities cover.
- plugind's per-store core instance cap went from 16 to 64; tables (16) and memories (4) are unchanged.
- CI installs both toolchains, pinned, and runs new, build, test and pack for each.

## What we learned
- jco with `--disable all` imports no WASI at all. componentize-py imports all of WASI (filesystem, sockets, stdin, terminals) unless `--stub-wasi`, which makes every call a trapping stub and bakes in the PRNG seed.
- componentize-py and jco import every interface of the world they are built against, unlike Rust's bindings. Against the full world, the audit refuses them with `plugin_undeclared_import`. Narrowing the world from the audit's own tables makes the build and the home agree.
- A componentize-py component needs 38 core instances: one per module of its runtime, measured by building plugind with the cap read from the environment. Tables and memories fit the old caps.
- plugind's memory cap is per memory, and memories are counted (4). So the instance cap does not loosen the memory bound; a test holds both.
- TinyGo's wasip2 runtime imports `wasi:cli/stdin` and `wasi:filesystem` even without `fmt` or `os` (the Go template, #851, builds for `wasm-unknown` instead).
- A negative test can pass for the wrong reason: the first undeclared-import test called `setTags`, which does not exist. It now calls `addTags`, and a positive control declares `tags.write` and builds.
- CI found that a job's time limit started before the host compiled a package it had not loaded and before the job waited for a slot. The Python hello (compile 15 s in a debug build, over 30 s on a 2-vCPU runner) timed out at 89 fuel.
  - The fix: the job's clock starts at its store; a compile has its own cap (180 s, the package refused after) on 2 workers; the cache is named by Wasmtime version and engine settings, and warms at start.
- Wasmtime checks a store's deadline only at epoch ticks (100 ms here). A guest that ends inside its first tick passes even with its deadline gone, so a test of the deadline needs guest work spanning ticks. The tests time the machine first.

## Keep doing
- Pair every "does not build" test with the same code building once the thing it lacks is added.
- Before raising a limit a test hits, measure which phase uses the time.

## Open
- The Go template (#851).
