---
schema: 1
kind: pr
pr: 986
title: First-party stages end to end in their Windows runners; the venv launcher was the share miss (story 54.23)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [977, 988, 990, 997, 1003]
---

## What happened
- A new required job, `runner-stages` (`_runner-stages.yml`, Windows): real first-party stages (`thumb_small`, and `convert_mp4`'s dry run) go through the worker's real plugin path into their runner groups, in their AppContainers, through the release's launcher, share broker, pipes, bundled Postgres and the plugin API's executor. Five cases: local and SMB-share libraries, plus the guarded share case with `MAKTABA_WINDOWS_SHARE_RUNNERS` off. Each job must have run in its runner (never the worker's fallback) and made the right output; the verdict's own tests run on every OS in the unit job.
- It found the root cause of v0.3.105's share miss (`thumb_small` done in 0 ms with no thumbnail, which #988 and #990 had only contained). On Windows a venv's `python.exe` is a launcher that starts the base interpreter as its child. The share broker serves only the pid the launcher started, so it refused the child, and the closed pipe read as files that were not there. The launcher now starts the base interpreter named in `pyvenv.cfg` directly, with `__PYVENV_LAUNCHER__` set to the venv's python so `sys.executable` and the venv's packages stay the same.
- The runner's file shim fails closed on what remained: a broken broker pipe is a refusal; plain `exists`/`isfile`/`isdir`/`lexists` raise a refusal instead of answering False; `islink`, `isjunction` and `os.access` go through the broker; any other file call on a share path (`os.open`, `glob`, mkdir, `shutil` copies) is refused by the audit hook, so the job runs in the worker. The broker logs each client it turns away, and the runner's job line counts what the broker answered and names the first missing paths.
- Proof on 8b9af16 (run 42474896846): the broker answered `stat:2 open:2` and `small.jpg` was 8308 bytes; the interpreter lines show `venv: true` with pids matching the broker's hello. Negative control e79025a skipped the runner's process setup: every runner case was refused (`WinError 5`), the worker ran it, and the verdict failed (run 42475229784). Reverted in ca6c1ca.
- The job's ffmpeg download first used a bare `Invoke-WebRequest` on the github.com release URL, the 504 path #1003 had just replaced; review moved it to the cached, API-first `fetch-pinned.sh`. The fingerprint retry loop the job found was split out and merged first as #997.
- The integration job then failed on the resume-invariant lint (`tests/lint/test_no_sidecar_checkpoint.py`), which reads `.partial` as a sidecar checkpoint and matched a test's `functools.partial`; a small opener factory replaced it.

## What we learned
- A venv on Windows is not one process: anything that trusts a pid (a broker, a Job Object check, a handle grant) must start the base interpreter, not the venv's `python.exe`.
- A guard that keeps work out of a path hides that path's bugs from production, so a CI end to end that turns the guard off (here `MAKTABA_WINDOWS_SHARE_RUNNERS=1`) is what proves it, together with a negative control that shows the verdict can fail.
- A file shim must be total: every lookup Windows answers without `os.stat` (and every call that swallows `OSError`) is another place a refusal reads as absence.

## Keep doing
- Running a negative control on a new end to end before trusting its green, and reverting it in a commit of its own.
- Splitting a fix found by a long-running PR into its own small PR (#997) so it merges on its own checks.

## Stop doing
- Adding a raw download to a new workflow: every pinned download goes through `tools/release/fetch-pinned.sh` and a cache.

## Open
- Drop #990's worker-only guard for the read-only runner groups now that `thumb_small/share` passes in its runner; keep it for the groups that write in full (no broker) until they have their own route.
- ffprobe on a share cannot run inside a runner yet.
- GPU stages on Windows.
