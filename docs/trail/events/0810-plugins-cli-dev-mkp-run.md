---
schema: 1
kind: pr
pr: 810
title: Plugins from the command line, developer mode and mkp run (Epic 54, 54.10 §4-5, 54.12)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- `maktaba-server plugins …` passes through to `maktaba-api plugins`, which calls the plugins lifecycle and the components service the admin routes call, with the components CLI's flags and exit codes; `plugins dev <folder>` installs a folder as Development and reinstalls it on every save; `mkp run` installs a folder on a home in developer mode over the admin API (the install form's new `development` field). Closes #719.

## What we learned
- `mkp keys` and `mkp pack --key` were on main already; the guide still said `mkp pack --sign` and "not built". A guide written from the spec ahead of the build needs a pass when the build lands.
- The lifecycle treats a package of the same version with a new digest as an update (a new `<version>_<sha12>` folder), so developer mode's reload is an ordinary update that the supervisor's runner drains and switches.
- `mkp` has no HTTP client and the host's workspace no TLS stack: `mkp run` speaks HTTP/1.1 over `std::net` to the home's LAN address and sends HTTPS homes to developer mode on the home.
- A merge-on-green watcher must fail closed: read `statusCheckRollup` with jq under `set -euo pipefail`, and merge only when every check completed SUCCESS, SKIPPED or NEUTRAL (#809: a `grep -P` that failed on macOS read as green).
