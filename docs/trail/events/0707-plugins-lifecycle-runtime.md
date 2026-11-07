---
schema: 1
kind: pr
pr: 707
title: Plugins part 3, install, run and manage a plugin (Epic 54, 54.03, 54.07-54.09, 54.37, 54.38)
date: 2026-11-06
session: 08ec1b23-2735-4eb1-952d-c4e10425aeba
prs: [683, 693]
---

## What happened
- Third PR of four for Epic 54 Phase 1: plugin entries in every catalog consumer (Go and Python agree through a golden and one gates table), the supervisor's `plugins` role running `maktaba-plugind` on demand, plugin files on disk through the supervisor's loopback install, the lifecycle on the components service's hooks (keep-off, grants, rollback, purge), the circuit breaker, plugin log segments, and `maktaba-plugind` in the release payloads. Migration 0195 (`plugin_keep_off`, `plugin_health`). An end-to-end test (`tests/components/test_plugins_live.py`, in components-live) installs the hello guest into a whole server, runs its job in 0.2 s, disables and removes it.

## What we learned
- The whole-server e2e found three things no unit test did: the pipeline had no `MAKTABA_SUPERVISOR_TOKEN_FILE` (the mint answered 401 and the job deferred every minute); a deep data folder puts `<data>/run/*.sock` past `sun_path` (104 bytes on macOS; the API's log showed `bind: invalid argument`); a plugin installed before the supervisor runner's first successful read (the tables appear only after the API migrates) was in the runner's first set, so the pipeline worker was never restarted. Run the live test before pushing a change to the plugin wiring.
- The log redactor writes the home folder as `~`: a path in the log that looks like `.../clogs~/data` is `.../clogs/home/<x>/data`.
- Plugin stages are loaded at the pipeline worker's start, so any change of the installed set needs a restart of the main worker; the supervisor's `Restart` is the clean way (no backoff, not a crash).
- Wasmtime under the macOS hardened runtime needs `allow-jit` and `allow-unsigned-executable-memory`; the payload signs `maktaba-plugind` with its own entitlements file.
- The affected-tests gate maps from main's coverage: tests in a new file do not count for an old source; put them in a test file the map already names, or add a `No-Test-Change` trailer for a constant bump.
- `make compat-prev` writes `api/internal/compatprobe/main.go` while it runs and deletes it after: a `git add -A` during the run committed it, and CI's lint failed on its `fmt.Printf`. Commit only named paths while it runs.
- A new catalog group needs its place in the web's `GROUP_ORDER` (componentWords.ts): the Components page's test compares the groups with the catalog dump.
- On the Mac worktree, `go test ./...` of cmd/maktaba-server fails `TestSetupCLIDrivesTheSteps`, the pgbundle backup tests and `TestRestoreAskedPutsTheBackupBackBeforeAnyRole` with under 20 GB free (they fail on main too); `make compat-prev` needs `psql` on PATH (the app's `server/postgres/bin` has it).

## Open
- PR 4: 54.10 (re-auth, typed name, CLI), 54.11, 54.12, 54.13.
- #712 (the migrate job), #708 (rehearsal scenario 15, chaos), #709 (the updater's pre-update list), #710 (Windows), #711 (job metrics, problem-report opt-in).
