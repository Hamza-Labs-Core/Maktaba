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
2. **Stop the server**: the install hold, then the stop event (contract-agent), then wait until the
   agent's mutex is free (≤ 100 s: the launcher's 60 s grace, Postgres's fast shutdown, the agent
   ending the launcher). If it does not stop in time the update is not installed: the tray says so
   and the shell starts the server again (as Resume does, unless it was paused before the update).
3. **Install** (`Update::install`): Tauri starts the NSIS installer and the shell exits. If the
   installer cannot be started, the shell starts the server again (unless it was paused) and says
   why. The installer's `PREINSTALL` hook (`--server-stop`) sets the hold too and finds nothing
   running; its restart-manager check closes any other `Maktaba.exe` window of this user.
4. The installer relaunches `Maktaba.exe` (`/R`); its `POSTINSTALL` hook (`--server-start`) has
   already ended the hold and run the task (unless the server was paused before the update). The new
   launcher starts the new `maktaba-server serve`, whose first start does the steps below.

A failed install (cancelled, disk full) leaves the old files, maybe some replaced, and the hold: the
watchdog does not start a server from them for the rest of that sign-in. The window's next start,
Resume, or the next sign-in runs the server again (an update offered again finishes the files).

## The supervisor's next start (every platform)

Built in this story for the Mac, Windows and Linux; the full rules are Epic 36's contract
([What the supervisor does on its next start](../36-mac-app/contract-update.md)). One **database
step** runs before every process but Postgres and the model server, which wait on it and show its
progress as their `detail`. Not on a machine that joined a home (the api in node mode: it reads
the home's database and never migrates, and Epic 37 keeps its copy): there is no database step,
nothing is dumped, migrated or recorded, and every role starts at once. Nor does a `serve --role`
without the api write a record.

- **The record** `<state>\state\database.json` names the release whose migrations the database
  holds, written before any role runs (`state\payload.json`, the copy saved after three healthy
  minutes, stands in only where there is no record).
- **The same release:** the migrations run (nothing to apply). **An older release:** refused for
  every process, naming the release to install and the dump from before the update. **Another, or
  no record:** `pg_dump -Fc` into `backups\pre-<old>-to-<new>.dump` (`pre-unknown-to-<new>`
  without a record; the newest `[postgres] update_dumps`, default 3, are kept beside the nightly
  7), the record says `migrating`, `maktaba-api migrate --timeout 0 up`, the record says
  `migrated`.
- **A failing migration:** the dump goes back into the running cluster (`restoring`, then
  `restored`, naming the release from before) and every process stays stopped, saying why and
  what next: install the old release again, or an update that fixes it (each start tries the
  update again). A stop or a crash part way goes on from the record at the next start.
- **A database ahead of the code:** the API refuses it on any install: `migrate up` exits 3 when
  the database holds a migration newer than its folder's, and `serve` when it holds one newer
  than the code ships.
- **The venv:** the pipeline waits for the database step, then for the venv bootstrap. The
  constraints or Python's minor changed: the venv is rebuilt in place (not built beside and
  swapped: pip writes the venv's absolute path into every script, on Windows inside each `.exe`
  launcher, so a moved venv would point at the old place). Only the wheel: the wheel alone.
  Python's patch, or the install moved: `venv --upgrade`. A pipeline whose Python is named
  (`MAKTABA_PYTHON`, `[pipeline] python`) waits for the database alone.
- `postgres.version` major: refused ("database needs an upgrade"; none is planned). ffmpeg and the
  Go binaries: nothing, they run from the payload.

## Downgrades

There is no in-app rollback. Installing an older installer over a newer one is allowed by NSIS;
the database step then refuses to run any process on the newer database (above). The way back is
the `pre-<old>-to-<new>.dump` (a restore command is a follow-up, as on the Mac).
