---
schema: 1
kind: pr
pr: 1003
title: Pinned downloads by the GitHub API first, retried and cached
date: 2026-11-08
session: f5223aa0-10de-45e5-8846-eefe883a6516
prs: [997, 986]
---

## What happened
- On 2026-11-08 github.com's `/releases/download/` front door answered 504 to the first request for long stretches, while `api.github.com`'s asset endpoint served the same files at once. Every CI job that fetched a GitHub release asset went red: Mac bundle-size and Maktaba.app (Postgres, CPython), the Windows app (CPython, Postgres, ffmpeg, tauri's NSIS tools), the plugin sandbox on Windows (ffmpeg), the Linux app (tauri's AppImage tools) and the Windows venv job (CPython). Main's red held release tags and blocked #997 and #986.
- `tools/release/fetch-pinned.sh` is the one pinned download: a file already in place with the pin is kept (no download); a github.com release asset goes by the API first (release by tag, asset by name, `Accept: application/octet-stream`, the job's `GITHUB_TOKEN`) and the github.com URL second, in each of 5 tries 10-80 s apart; any other 4xx stops at once; the SHA-256 decides whichever route gave the file. The Mac, Linux and Windows payload scripts' `fetch` delegate to it, so the release legs get it too.
- The Mac and Windows app jobs keep their whole download folder in one cache keyed on runner OS, arch and `hashFiles(pins.env)`, filled by a retried prefetch on a miss and saved on any ref. Tauri's NSIS toolset (Windows) and AppImage tools (Linux) are pinned by SHA-256, cached and laid out where tauri looks, so it downloads none of them.

## What we learned
- curl's `--retry 3` waits 1, 2 then 4 s: an outage of minutes is past it in 11 s. A wider backoff alone did not help either: github.com 504'd for the whole 2.5 minutes of five tries, and the API route is what got the files.
- A cache saved only by main's push leaves every pull request downloading while main has no entry (the Mac downloads cache had none on main); a cache saved on any miss at least serves that pull request's reruns.
- Tauri's bundlers download their own tools from GitHub releases with no retry: NSIS (checked by SHA-1 in `nsis/mod.rs`) and, on Linux, AppRun, linuxdeploy and linuxdeploy-plugin-appimage (a `continuous` build, checked by nothing; linuxdeploy failed without it). Each file present under tauri's name in its tools folder is not downloaded.
- curl reports HTTP code `000` for `file://`; a helper that treats only 2xx as success fails pins-check.sh's offline test.
- A composite action under `.github/actions/` or an edit to `ci.yml` runs every job: a shared CI script listed in the filters that use it keeps the run to the related jobs.

## Keep doing
- Fetch every pinned GitHub release asset through `tools/release/fetch-pinned.sh`, with the job's `GITHUB_TOKEN` passed to the step.
- When a download fails, try the same URL from a workstation and by `gh api`: it showed a front-door outage, not a bad pin, and pointed at the route that worked.

## Open
- `cmd/maktaba-server/internal/pgbin/pgbin.go` (a node's runtime Postgres) still fetches the github.com URL with no API route.
- CodeQL's default setup and `golangci-lint-action` download their tools from GitHub releases; neither can use this route, and a rerun is the remedy.
- linuxdeploy-plugin-appimage is pinned to its `continuous` build of 2026-10-01: when that build moves, the Linux app job warns and tauri downloads it unpinned.
