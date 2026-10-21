# Epic 38 — contract: updating the Windows app (story 38.12)

> **Owners:** track C (the shell's updater), track A (the supervisor's next start, built here for
> every platform). **Builds on:** [Epic 36's update contract](../36-mac-app/contract-update.md):
> one updater, the launcher's self-update off inside the app, the API's update discovery off
> (`MAKTABA_UPDATE_DISABLE=1`), `maktaba-server update` refused under `MAKTABA_APP_BUNDLE`.

## One updater

The Tauri updater, feed `latest.json` on the GitHub release (platform `windows-x86_64`: the NSIS
`-setup.exe`, signed with the Tauri updater key). It runs the new installer in passive mode (`/P`),
which replaces `Maktaba.exe` and the whole `server\` payload in place.

## The Windows difference: files in use

On a Mac the bundle is swapped by a rename while the old server keeps running from the old inodes.
On Windows a running program's files cannot be replaced: while the server runs, `server\bin\*.exe`,
`server\postgres\bin\*`, `server\python\*` and `server\ffmpeg\*` are all in use. So the shell stops
the server **before** the installer starts, and starts it again from the new payload afterwards:

1. **Download and verify** the update (`Update::download`), with the server still running.
2. **Stop the server**: the stop event (contract-agent), then wait until the agent's mutex is free
   (≤ 90 s: the launcher's 60 s grace, Postgres's fast shutdown). If it does not stop in time, or
   the install then fails, the update is not installed: the tray says so and the shell starts the
   server again (its task, unless it was paused before the update). Once the stop event is set the
   server is stopping; nothing but the shell's start brings it back.
3. **Install** (`Update::install`): Tauri starts the NSIS installer and the shell exits. The
   installer's `PREINSTALL` hook (`--server-stop`) finds nothing running; its restart-manager check
   closes any other `Maktaba.exe` window of this user.
4. The installer relaunches `Maktaba.exe` (`/R`); its `POSTINSTALL` hook (`--server-start`) has
   already run the task (unless the server was paused before the update). The new launcher starts the
   new `maktaba-server serve`, whose first start does the steps below.

A failed install (cancelled, disk full) leaves the old files: the next start of `Maktaba.exe` (the
window or the sign-in task) runs the old server again.

## The supervisor's next start (every platform)

As specified in Epic 36's contract, built in this story for the Mac, Windows and Linux:
`<payload>\payload.json` is compared with `<state>\state\payload.json` (the copy saved after the
last start that ran healthy for 3 minutes):

| Changed | Action, in order |
|---|---|
| nothing | start the roles |
| `version` (higher) | `pg_dump -Fc` into `backups\pre-<old>-to-<new>.dump` (kept beside the nightly 7), then the API's migrations; a failing migration stops here with the dump intact |
| `version` (lower) and the database's schema is newer than this build knows | do not start the API; `/processes` and the tray say "the database is newer than this Maktaba: install the newer version" |
| `postgres.version` major | refuse with "database needs an upgrade" (no major bump is planned) |
| `pipeline.constraints_sha256` or `python.version` | rebuild the venv (`venv_bootstrap` builds `venv` afresh; the pipeline workers are stopped while it runs; progress in `/processes`) |
| only `pipeline.sha256` | reinstall the wheel alone (seconds) |
| ffmpeg, Go binaries | nothing: they run from the payload |

Each step is idempotent, and `state\payload.json` is written only after the healthy 3 minutes, so a
crash part way repeats the steps at the next start.

## Downgrades

There is no in-app rollback. Installing an older installer over a newer one is allowed by NSIS; the
supervisor then refuses to run the API on a schema it does not know (above). The way back is the
`pre-<old>-to-<new>.dump` (a restore command is a follow-up, as on the Mac).
