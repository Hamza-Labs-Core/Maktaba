---
schema: 1
kind: pr
pr: 694
title: The plugin host's log-flood test bounds by elapsed time
date: 2026-11-06
session: 14c6f63b-ff36-454d-9735-8989bcb48e6d
prs: [683]
---

## What happened
- `log_flood_is_throttled` (from #683) asserted a fixed `< 200` lines, but the limit is a burst plus a rate, so a slow runner let 207 through on #669 and #693. It now uses the unit test's bound: `BURST + elapsed × LINES_PER_SECOND` plus the host's own few lines.

## What we learned
- A rate limit's test needs the elapsed time in its bound; a fixed count only holds on a fast machine.
- `make plugind-guests` needs rustup's cargo: Homebrew's `/opt/homebrew/bin/cargo` comes first on PATH here and has no `wasm32-wasip2` target, even when `rustup target list --installed` shows it.
