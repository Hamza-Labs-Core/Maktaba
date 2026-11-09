---
schema: 1
kind: pr
pr: 1049
title: Each test session builds and runs its own plugin-API dev server
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- `plugin_api_dev()` in `pipeline/tests/plugins/firstparty/harness.py` built the dev API into one file per checkout (`$TMPDIR/maktaba-plugin-api-dev-<hash of the checkout path>`). Every session in that checkout shared it: xdist workers, two pytest runs in one worktree, and CI jobs on one runner. Two sessions could link over the same file at once, or run a binary while another was rewriting it.
- A run killed before its clean-up left its dev server running and holding its database connection.
- The mint port (`127.0.0.1:0`), the per-test socket folders and the uuid database names were already separate, and stay as they were.
- Now each session builds once into its own folder. It starts from a shared seed binary that is swapped in by one atomic rename, so `go build` finds the binary up to date: 0.8 s instead of 3.2 s, which keeps #996's speed-up.
- `start_dev_api` and `stop_dev_api` track only the session's own processes and kill one that hasn't stopped after 5 s.
- The dev server's new `-watch-stdin` flag makes it exit when the test process's end of stdin closes. A session removes leftover folders whose owner process is gone (Linux and macOS only: on Windows `os.kill(pid, 0)` kills).
- `test_dev_api_sessions.py` starts two sessions in separate processes that build at the same time. It checks that their addresses, sockets and binaries differ, that one ending leaves the other serving, and that a session killed with SIGKILL takes its server with it.
- Two full first-party suites ran at once from one checkout against two scratch clusters: 280 passed each, with nothing left behind.
- The first CI run failed `affected-tests`: the new flag in `api/tools/plugin-api-dev/main.go` had no Go test. The stdin watcher became a function of any reader (`stopWhenClosed`), with a Go test over a pipe. A re-run also hit the N16 `detect` timing check, the separate item fixed under the N16 measure work. Merged as f14fd0ab9 on 2fb1d754e with 67 checks green.

## What we learned
- A content-free path keyed only by the checkout is a shared resource between every session in that checkout. Per-session paths with an atomic seed keep both isolation and the build cache.
- `affected-tests` maps a Go file to Go tests. A Python end-to-end test does not count for it, so a Go flag needs its own Go test.
- A watcher armed right after a push can read the old head and stop with "head moved". Wait until GitHub reports the new head before arming.

## Open
- Core-media's parity test takes 5.2–7.2 s on the owner's Mac, over the 5 s soft cap, on main as well. It is likely the read checks #1025 added to its statements.
