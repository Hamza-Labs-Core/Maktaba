---
schema: 1
kind: migration
title: Local tooling gotchas on the owner's Mac
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
The first trail collected the local tooling gotchas sessions kept hitting on the owner's Mac.

## What we learned
- Go API: build with `CGO_ENABLED=0` (the cgo link fails on the macOS 27 SDK). Build inside each module dir; `go build ./cmd/maktaba-server/...` from the root fails.
- Android: set `JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home` and a `local.properties` with `sdk.dir`.
- Python pipeline: `uv run --extra dev ruff|mypy|pytest` from `pipeline/`. Run uv commands one at a time (the `.venv` races). mypy is strict over tests too.
- zsh: variables do not word-split (use arrays); `$path` is PATH; `log` is a builtin (use `/usr/bin/log`); `sleep` is blocked in this harness (use until-loops in the background).
- Prettier prints "All matched files…" even after an error: check the exit code.
- Full Disk Access and Local Network grants apply only to processes started after them.
- `start_session` is refused over Remote Control: use background agents instead.
- Scratch Postgres needs `-k ''` (socket paths too long) and `--encoding=UTF8` (Arabic full text).

## Stop doing
- `launchctl kickstart -k` on the live server: it SIGKILLs workers. TERM the launcher instead.
- Leaving a second `Maktaba.app` copy in /Applications.
