# Epic 36 — how an app update replaces the server (story 36.8 contract)

> **Status:** contract for 36.8, written with 36.7 (packaging). **Owners:** track C (the shell's
> updater), track A (the supervisor's next start). Payload layout: `tools/release/mac-app/README.md`.

## One updater

Inside `Maktaba.app` there is exactly one updater: the app's (the Tauri updater, feed
`latest.json` on the GitHub release, each `.app.tar.gz` signed with the Tauri updater key).
It replaces the whole bundle — shell **and** `Contents/Resources/server` — in one swap.

Everything else that can update is off inside the app (done in 36.7):

| What | Off how |
|---|---|
| `maktaba-launcher` self-replacement (`--replace-self`, story 35.7) | the launcher detects it runs from `<X>.app/Contents/Resources/server/bin` (app-bundle mode) and refuses |
| the launcher's release pointers / rollback (`bin/releases/<v>`, `current`, `pending`) | not used in app-bundle mode: it starts `Resources/server/bin/maktaba-server` directly |
| the API's update discovery + install (Epic 28/35) | the launcher sets `MAKTABA_UPDATE_DISABLE=1` for the server and every role |
| `maktaba-server update` (CLI) | **track A:** refuse when `MAKTABA_APP_BUNDLE` is set ("updates come with the app") |
| the web Settings › Updates page | **track A/C:** show "Updated with the app" when the API reports updates disabled |

The launcher gives the server `MAKTABA_APP_BUNDLE=<…/Contents/Resources/server>` and **not**
`MAKTABA_LAUNCHER` / `MAKTABA_RELEASE_DIR` / `MAKTABA_INSTALL_ROOT`, so nothing treats the
read-only payload as a release layout to stage into, confirm or roll back. Its lock and
`launcher.log` live in `~/Library/Application Support/Maktaba/updates/`.

## What happens on an update

1. **Shell (track C):** the updater downloads and verifies the new `.app.tar.gz`, then swaps
   the bundle. Tauri extracts beside the app and renames over it, so the path
   `/Applications/Maktaba.app` is always either the old or the new app, never half of each.
2. The running server is still the **old** one (its files are open; the old inodes live on).
   The shell restarts it from the new bundle: `launchctl kickstart -k gui/<uid>/com.hamzalabs.maktaba.server`
   (if the LaunchAgent plist changed, `SMAppService` unregister + register instead).
   `-k` sends SIGTERM: the launcher asks `maktaba-server` to stop, which drains the roles and
   stops Postgres cleanly (36.1). The plist's `ExitTimeOut` must cover the launcher's
   `--stop-grace` (60 s): **90 s**. Only then launchd kills.
3. The new launcher starts the new `maktaba-server serve`. The shell relaunches its window.

The new app must be signed by the **same Team ID**: the login item approval
(`SMAppService`) and anything the app keeps in the Keychain are tied to it.

## What the supervisor does on its next start (track A)

The supervisor compares the bundle's `Resources/server/payload.json` with the copy it saved
after the last good start, `~/Library/Application Support/Maktaba/state/payload.json`:

| Changed | Action, in order |
|---|---|
| nothing | start the roles as usual |
| `version` | 1. **backup first:** `pg_dump -Fc` into `backups/pre-<old>-to-<new>.dump` (kept beside the nightly 7) — as the Epic 35 update path does. 2. apply the DB migrations (the API's migrate step), stopping if one fails: the old schema + the dump are intact. 3. go on below |
| `postgres.version` major (16 → 17) | do **not** start Postgres on the old cluster: `pg_upgrade` from the previous app's binaries is impossible (they are gone), so the major bump ships `pg_dumpall` before it (the release before a major bump writes `backups/pre-major.sql`) and the new start does `initdb` + restore. Until a major bump is planned: refuse with a clear status ("database needs an upgrade") rather than corrupt anything. Minor (16.15 → 16.16): nothing to do |
| `pipeline.constraints_sha256` | rebuild the venv (36.3): build `venv.new/` from the bundled Python + wheel + constraints, then swap it for `venv/` (the old one stays usable until the swap); report progress in `/processes` |
| only `pipeline.sha256` (the wheel) | `pip install --no-deps --force-reinstall` the new wheel into the venv (seconds) |
| `python.version` | rebuild the venv (its interpreter is the bundled one) |
| `ffmpeg`, Go binaries | nothing: they run from the bundle |

Then start the roles (postgres → api → streaming → pipeline workers). Write
`state/payload.json` only once every role has run healthy for a while (the 3-minute rule of
`confirmAfter`), so a crash before that repeats the same steps next start (each is idempotent:
the dump is re-taken, migrations are recorded, `venv.new` is rebuilt from scratch).

## Going back

There is no in-app rollback: Tauri does not keep the previous app. To go back, the owner
installs the previous `.dmg`. The supervisor then sees a `version` **lower** than
`state/payload.json` and a DB schema newer than the binaries know: it does not start the API
(forward-only migrations) and says so; the way back is restoring the `pre-<old>-to-<new>.dump`
taken in step 1 (a `maktaba-server` restore command, a follow-up), then starting.

## Open for the owner

- The Developer ID (README of `tools/release/mac-app`): without it an update cannot keep the
  login-item approval across versions reliably, and Gatekeeper blocks the downloaded app.
