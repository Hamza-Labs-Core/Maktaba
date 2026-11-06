---
schema: 1
kind: pr
pr: 683
title: Plugins part 1, the manifest, the package and the sandboxed plugin host (Epic 54, 54.01-54.03)
date: 2026-11-06
session: e67d03b4-bb59-41b3-8c8f-ee3aad4592fc
---

## What happened
- First PR of four for Epic 54 Phase 1. One manifest schema in `shared/plugins` read by four validators (Go, Rust, Python, TypeScript) that agree through committed goldens on a 52-case corpus; `componentpack.UnpackPlugin`; the Rust host `maktaba-plugind` (Wasmtime 50.0.1) with fuel, epoch deadline, memory caps, import audit, compile cache and API forwarders; its OS sandbox on macOS, Linux and Windows; a `plugins` CI job with escape probes against a canary on four runners.

## What we learned
- Rust std on `wasm32-wasip2` imports `wasi:cli/stdin` and five terminal interfaces even for a guest that never reads input; a `no_std` guest with wit-bindgen (`std_feature`, `extern crate alloc as std`, its own `cabi_realloc` and `memcmp`) imports only `maktaba:plugin/*` and is 14 KB. componentize-py and jco components import `wasi:filesystem`, so the Python and JS templates (54.12) need a way around the host's filesystem refusal.
- Homebrew's `cargo` on the Mac is not rustup's and has no wasm target: build guests with `~/.rustup/toolchains/stable-*/bin/cargo` (or put it first on PATH).
- Under Seatbelt a sandboxed process cannot `realpath` its own folders (the ancestors are denied): resolve paths before the sandbox and check package paths by their words.
- Wasmtime 50's `ResourceLimiter` returns `wasmtime::Error`, not `anyhow`, and `Config::trap_on_grow_failure` is gone: a limiter that returns an error traps.
- Debug builds of the host overshoot an epoch deadline by up to ~0.6 s when tests share the machine; N7 (deadline + 200 ms) holds in release.
- On Windows, bind and UDP send can succeed inside an AppContainer while WFP drops the traffic: the probes count ALLOWED only on two-way traffic (an echo, a reachable port).

- The repo's root `.gitignore` has `MANIFEST`; on the Mac's case-insensitive checkout it silently ignored a folder named `manifest/` (the Rust crate never reached CI). Check `git check-ignore -v` when a new folder's files do not show in `git status`.
- Windows AppContainer: `CreateProcess` fails with ERROR_ENVVAR_NOT_FOUND (203) unless the given environment holds `TEMP`, `TMP`, `LOCALAPPDATA` and the profile folders (Windows rewrites them for the container). Port 47001 is WinRM's on Windows runners (bind gives 10013). Inside the container a process can bind loopback and reach its own listener; nothing outside reaches it.
- `cargo test -p maktaba-plugind` rebuilds the binary without `--features escape-probes` into the same path: build the probe binary after the tests.
- CodeQL's zip-slip query does not see a path check in another function or an equality check as a sanitizer; check `..` where the name meets the disk.
- The pipeline's unit tier forbids sockets, and an asyncio event loop opens a socket pair: async tests without a service go in the integration tier.

## Open
- PR 2 (54.04-54.06), PR 3 (54.07-54.09, 54.37, 54.38, the supervisor role and payload), PR 4 (54.10-54.13).
