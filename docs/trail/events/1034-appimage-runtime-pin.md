---
schema: 1
kind: pr
pr: 1034
title: The AppImage runtime pinned and handed to appimagetool; linuxdeploy's and appimagetool's output in the log
date: 2026-11-08
session: 189fe2c3-f792-4847-a749-bfb7385c2a71
prs: [1014, 1036]
---

## What happened
- `linux-app / Linux app (aarch64)` failed once with `failed to bundle project: failed to run linuxdeploy` (run 42480410454, job 128737257329), and the log gave no reason. tauri-cli 2.12.3 discards linuxdeploy's output unless it runs verbose (`appimage/linuxdeploy.rs`: `cmd.output()` at log level Error).
- With `bundle.sh --verbose` (tauri's own log level only, not cargo's) and `LDAI_VERBOSE=1` (linuxdeploy-plugin-appimage passes `-v` to appimagetool), run 42491113741 showed the cause: while bundling, appimagetool downloaded the type2 runtime from `https://github.com/AppImage/type2-runtime/releases/download/continuous/runtime-<arch>`. It used no retry and no checksum, and it was the only download in the bundle step. A failed or slow fetch there fails linuxdeploy, and tauri hid the reason.
- #1034 (merged as 5aa377d50, from head 4aa5b1232):
  - `tools/release/linux-app/tauri-tools.sh` with `tauri-tools.pins` (`KIND ARCH SHA256 URL`) fetches AppRun, linuxdeploy, linuxdeploy-plugin-appimage and the runtime through `fetch-pinned.sh` (the API first, retried, SHA-256 checked) and lays them out in tauri's tools folder.
  - The runtime is pinned to type2-runtime's tagged release `20251108`, not `continuous`. Its path goes out as `LDAI_RUNTIME_FILE`, which the plugin passes on to appimagetool as `--runtime-file`, so appimagetool downloads nothing. A runtime that is not the pin fails the step. The plugin has no tagged build and stays pinned to its `continuous` build, warning when that moves (as in #1014).
  - `_linux-app.yml` caches the folder on a key over every pin (`tauri-tools.sh --key ARCH`). The Linux legs of `desktop-release.yml`, which fetched every tauri tool unpinned at release time, run the same script, restore main's cache, and bundle with `--verbose` and `LDAI_VERBOSE=1` too.
  - `tools/ci/tests/test_tauri_tools.py` (11 tests, against a local server) covers the layout and the handover, a second run that downloads nothing, a bad runtime, a moved plugin, the key, the pins file and both workflows' wiring.
- **Proof**, all in run 42493348141:
  - **Cache miss** (attempt 1, jobs 128770304083 aarch64 and 128770304158 x86_64): the runtime was fetched through the API with its SHA-256 checked, appimagetool ran with `--runtime-file`, there was no "Downloading runtime" line, and the entry was saved.
  - **Cache hit** (attempt 4, job 128783799269 x86_64; attempt 6, job 128791044484 aarch64): the cache was restored from the key, each file was "cached copy has the pinned SHA-256, not downloaded", appimagetool ran with `--runtime-file`, the embedded runtimes were 944632 and 936456 bytes, and the save step was skipped.

## What we learned
- A tool that runs another tool can hide its own downloads. appimagetool fetches the runtime itself unless it is given `--runtime-file`, so pinning the AppImage tools left one unpinned fetch that only verbose output showed. Read a bundle step's verbose log once for downloads before calling it pinned.
- tauri-cli keeps linuxdeploy's output only at `--verbose`. A `failed to run linuxdeploy` with nothing after it means the output was dropped, not that there was none.
- With the repository over its cache limit, an entry can be evicted minutes after it is saved. A cache-hit proof needs the rerun started right after the save step: cancel the run if other jobs are still going, then rerun the job at once. Both arches' entries saved in attempts 1 and 3 were gone before a later rerun, and only an immediate rerun hit.
- `gh run rerun --job` refuses while the run is still in progress, so two jobs of one run are rerun one after the other.

## Keep doing
- Pin by tagged release with SHA-256 whenever the upstream has one. Keep `continuous` with warn-on-replace only where there is no tag.
- Hand a tool its inputs as files (`LDAI_RUNTIME_FILE`) instead of letting it fetch them.

## Open
- linuxdeploy-plugin-appimage stays on a `continuous` build: no tagged release exists. The step warns when it moves.
- The next release's Linux legs are the first run of `tauri-tools.sh` in `desktop-release.yml`: check that they restore main's entry and bundle with no runtime download.
