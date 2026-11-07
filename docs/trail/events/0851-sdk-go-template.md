---
schema: 1
kind: pr
pr: 851
title: The Go plugin template (TinyGo wasm-unknown)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [848]
---

## What happened
- `mkp new go` makes a TinyGo project. `mkp build` narrows the world, generates bindings (`wit-bindgen-go`), builds TinyGo's `wasm-unknown` target, makes the component with `wasm-tools`, then runs the home's checks.
- The hello is 14.8 KB and imports only `maktaba:plugin`.
- A non-blocking CI step (Go is tier 2) installs TinyGo, `wasm-tools` and `wit-bindgen-go` pinned, and runs the Go tests.

## What we learned
- The agreed plan was a stub component exporting `wasi:cli/stdin` and `wasi:filesystem`. It cannot work for TinyGo: its `syscall` init calls `stdin.GetStdin()` at every start, and a stub cannot return an `input-stream` (a resource of the host's `wasi:io`).
- TinyGo's `wasm-unknown` target links no WASI, so the problem disappears with the host unchanged. It has no `cabi_realloc`, so the template exports one that pins its blocks until the host frees them.
- In `wasm-unknown` (a reactor) `main` is never called. An import used only from `main` is dropped as dead code, and the first refused-import test passed for that reason. The import now sits in `run`.

## Keep doing
- Read the runtime's init before choosing a stub: what it calls at start decides what a stub must answer.

## Open
- None.
