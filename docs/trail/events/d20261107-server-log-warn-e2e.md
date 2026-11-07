---
schema: 1
kind: incident
title: server.log at warn+ hid the info lines the e2e harnesses read
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [891, 896]
---

## What happened
- #891 makes `server.log` (the supervisor's echo) carry only warnings and errors by default. Its `replica` job then failed: `TestNodeAsksForItsAccess` found no `cluster_access_requested`, and `TestNodeFollowsTheHomeDatabase` found no `why=refused_moved`. The replica, switch, update and rehearsal harnesses read each machine's whole output for info lines.
- Fixed in #891: each harness's `server.toml` sets `[logs] server_log = "all"`. Older releases keep an unknown key, so the rehearsal's old binaries read the same file.
- #896's `affected-tests` job blocked on the native series titles (Android `DiagnosticsScreens.kt`, Apple `DashboardsView.swift`). Tests now check those titles in English and Arabic. The local gate had passed before the native files changed.

## What we learned
- Before cutting what a log carries by default, search the tests that read it: `git grep -l "log.String()" -- 'api/*e2e*_test.go'` and the harnesses that write a `server.toml`. Unit tests don't catch it. Only the e2e jobs do, and only when the path filter runs them.
- A check for an absence (`toml_key_superseded` not in the log) passes for nothing once the line it would see is hidden. A harness should log every line, whatever the default is.
- Run the affected-tests gate again after the last file is added to a commit, not before it.

## Open
- None.
