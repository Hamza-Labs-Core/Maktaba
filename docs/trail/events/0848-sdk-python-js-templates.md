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
- TinyGo's wasip2 runtime imports `wasi:cli/stdin` and `wasi:filesystem` even without `fmt` or `os`. The Go template needs a stub component composed in (the next PR).
- A negative test can pass for the wrong reason: the first undeclared-import test called `setTags`, which does not exist. It now calls `addTags`, and a positive control declares `tags.write` and builds.

## Keep doing
- Pair every "does not build" test with the same code building once the thing it lacks is added.

## Open
- The Go template, with a composed stub for stdin and the filesystem, and `mkp build go` failing if anything refused is left (#718).
