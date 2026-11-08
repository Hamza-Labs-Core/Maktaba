---
schema: 1
kind: incident
title: After #1014, a pinned tool's cache was evicted within an hour, and the aarch64 AppImage bundle failed once in linuxdeploy
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [1003, 1014, 1015]
---

## What happened
- #1014's proof was run 42480410454 on its head ec6339f00, run three times.
- **The golangci-lint cache was evicted within the hour.**
  - Attempt 1's lint restored `pinned-linux-golangci-lint-2.12.2-linux-amd64.tar.gz-8df580d2670fed8f` and downloaded nothing.
  - On attempt 2, about an hour later, the same key was gone ("Cache not found"). The job fetched the archive again through the GitHub API, checked its SHA-256 and saved it again.
- **Measured afterwards:** the repo's Actions caches reported 9.16 GB in use against GitHub's 10 GB limit, so entries are evicted least-recently-used first. The listing by family:
  - setup-go: 7.9 GB (47%), with two Go versions (1.25.8 and 1.26.0), each on Ubuntu 22.04, Ubuntu 24.04, Windows and macOS;
  - Rust build caches: 5.2 GB (31%);
  - setup-gradle: 1.7 GB (10%);
  - pinned tools: 0.55 GB (3%);
  - the app payload download caches: 0.52 GB (3%).
- **The aarch64 Linux app failed once in Tauri's bundler.**
  - On attempt 2, `linux-app / Linux app (aarch64)` failed with `failed to bundle project: failed to run linuxdeploy` (job 128737257329).
  - The pinned Tauri tools were not the cause: AppRun, linuxdeploy and linuxdeploy-plugin-appimage had all downloaded through the API, with their SHA-256 checked and mode 755.
  - The same job passed on attempt 1, on the attempt-3 rerun, and on main in the same window.
  - Tauri hides linuxdeploy's own output, so the cause is not in the log. The likely one is a download inside the bundler: the appimage plugin's appimagetool fetches its AppImage runtime from github.com while bundling, which no pin or retry covers.

## What we learned
- A cache near GitHub's 10 GB limit churns. A small pinned entry can be evicted between two runs an hour apart, and a cache that always misses only adds a download. The space goes to setup-go's duplicates (one module and build cache per Go version per runner image) and Rust's per-job caches, not to the payload downloads, so trimming the payload caches would not help.
- A tool that downloads inside another tool's step (appimagetool's runtime, inside Tauri's linuxdeploy) is invisible to the job log and to `fetch-pinned.sh` until its output is shown.

## Keep doing
- Measuring cache usage by family (`gh api repos/O/R/actions/caches`) before trimming any cache.

## Open
- Show linuxdeploy's output in the Linux app jobs, and pin or cache, with a checksum, the AppImage runtime that appimagetool fetches.
- Bring the cache use under the limit: cut setup-go's duplicates and Rust's per-job caches, so that small pinned tools stay cached.
