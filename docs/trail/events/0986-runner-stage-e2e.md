---
schema: 1
kind: pr
pr: 986
title: Real first-party stages run end to end in their Windows runners, and a venv's runner was never served by its broker
date: 2026-11-08
session: 1cd940fd-ef3a-41ac-a113-67362d51eb8c
---

## What happened
- #986 (merged at 591af235e) adds `runner-stages`, a required Windows job (`_runner-stages.yml`, gated by `runner_stages` in `ci-paths.yml`). It runs real first-party stages through the worker's own plugin path (`native.choose`, then `run_native` over the supervisor's pipes) into runners started by the launcher's `runner-sandbox-run`, with their AppContainer, grants, broker, `winproc` and `winfiles`. It has five cases: `thumb_small` and `convert_mp4` on a local folder and on an SMB share, plus `thumb_small/share-guarded` with `MAKTABA_WINDOWS_SHARE_RUNNERS` unset, which proves #990's guard keeps a library stage on a share in the worker. A negative control (e79025a, `winproc.install()` skipped) failed the job, and ca6c1ca reverted it.
- The first share run found the stage done in 0 ms with `no_thumbnail`, and the runner had made no file lookups at all. Root cause: a venv's `Scripts\python.exe` on Windows is a launcher that starts the base interpreter as its child. The file broker admits only the pid the launcher started, and duplicates its handles into that process. So it closed the real runner's pipe. The shim's write raised a broken pipe, which is an `OSError`; `isfile` answers False to that, so the share video read as missing. Production starts runners from `<release>\venv\Scripts\python.exe` the same way, behind the share-runner override.
- Fixed in #986:
  - `runner-sandbox-run` starts a venv's runner as its base interpreter, with `__PYVENV_LAUNCHER__` naming the venv's python, as the venv launcher does (`runner.go` `venvBase`).
  - `winfiles.Broker` makes any pipe failure a `PermissionError`, never an absence.
  - The broker logs `plugin_runner_broker_client_refused` with both pids.
  - Each Windows runner logs `plugin_runner_interpreter` (pid, `sys.prefix`, `venv`), and the e2e requires `"venv": true`.
- The same e2e found the fingerprint runner closing the connection, with no answer, when a stage's fingerprint looked at a share path its shim refuses. The worker read that as a failed job, so the job went back to pending again and again. #997 (split out of #986) makes the runner answer the refusal, and has the worker take the fingerprint itself when a runner closes without one. Homes were not hit: #990's guard keeps share library stages, and their fingerprints, in the worker.
- `convert_mp4` on a share is handed to the worker by design. The files group writes libraries in full, and a share written in full is never brokered, so ffprobe on a share cannot run in a runner today.
- #1003's 504 outage hit this job's bare ffmpeg `Invoke-WebRequest` twice. The job now fetches the pinned zip through `tools/release/fetch-pinned.sh` and caches it, as `_plugins.yml` does.

## What we learned
- On Windows, a pid check on a pipe client fails for any program started through a launcher stub (venv `python.exe`, uv trampolines, `py.exe`). Start the real interpreter, or the check refuses the process that does the work.
- A refusal that reaches a lookup as a plain `OSError` turns into a silent "missing". Every IPC failure on a sandbox path has to surface as `PermissionError`, so the job is handed off and counted rather than finishing with a wrong result.
- A job that ends `done` in 0 ms with zero lookups is the signature of a refusal being swallowed. Per-job broker counts (`stat`, `open`, `_missing`, `_refused`, `plain`) on the runner's job line made that visible in one CI run.
- A negative control that skips one install step (`winproc`) is cheap proof that the e2e can fail. Keep one for each new required sandbox job.

## Keep doing
- Prove a launcher change on every runner start, local cases too, with a log line the test asserts on (here `plugin_runner_interpreter`), not by inference from the share case.

## Open
- Share runners stay behind `MAKTABA_WINDOWS_SHARE_RUNNERS=1`. Turning them on by default needs the files group's share writes brokered, or the runner to refuse them up front, so that `convert_mp4` on a share can run in a runner.
