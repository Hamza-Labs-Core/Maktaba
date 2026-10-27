# Epic 36 — how an app update replaces the server (story 36.8 contract)

> **Status:** contract for 36.8, written with 36.7 (packaging). **Owners:** track C (the shell's
> updater), track A (the supervisor's next start). Payload layout: `tools/release/mac-app/README.md`.

## One updater

Inside `Maktaba.app` there is exactly one updater: the app's (the Tauri updater, feed
`latest.json` on the GitHub release, each `.app.tar.gz` signed with the Tauri updater key).
It replaces the whole bundle — shell **and** `Contents/Resources/server` — in one swap.

> **Epic 35 (install-kinds.md "No click"):** the server now decides *what*: the home's
> update (its channel, home first, then each machine) orders the release it verified
> (`<state>/updates/app-install.json`, the release's own feed
> `releases/download/v<version>/latest.json`); the app downloads and checks it, says when it
> installs ("Maktaba will update to <v> and restart by <time>", then "is updating now"), and
> installs it by itself when the home says go (owner decision D1 of 2026-10-27: no click),
> saying how it went in the same file. The app's updater still does the swap. Before it, the
> server keeps a copy of the payload it ran (`<state>/updates/payloads/<version>`); the
> launcher runs that copy (`<state>/updates/payload-override`) when the new server cannot stay
> up, and the API's update discovery is no longer turned off inside the app.

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
   (if the LaunchAgent plist changed, `SMAppService` unregister + register instead). Built:
   the login item is bound to the build that registered it (an app signed ad hoc is its code
   hash), so each new build registers it again (unregister, then register) at its first start,
   and on Resume and Restart (a stamp of the executable in `state/login-item-build`); after an
   app update the settling of its attempt does the same, and once more if the server stays
   silent for 30 s while the app waits for it on the new release. One registration at a time.
   A refusal fails the update's attempt (`login_item_unsigned`, else `relaunch_failed`, with
   the login item's state).
   `-k` sends SIGTERM: the launcher asks `maktaba-server` to stop, which drains the roles and
   stops Postgres cleanly (36.1). The plist's `ExitTimeOut` must cover the launcher's
   `--stop-grace` (60 s): **90 s**. Only then launchd kills.
3. The new launcher starts the new `maktaba-server serve`. The shell relaunches its window.

The new app must be signed by the **same Team ID**: the login item approval
(`SMAppService`) and anything the app keeps in the Keychain are tied to it.

## What the supervisor does on its next start (track A)

> **Built in Epic 38 story 38.12** for the Mac, Windows and Linux alike
> (`cmd/maktaba-server/serve_appbundle.go`, `internal/apppayload`, `internal/appvenv`,
> `internal/pgbundle/upgrade.go`, the supervisor's `Process.Prepare`; the API's own guard in
> `api/schema_ahead.go`). Each process waits on its `Prepare` before its first start, and
> `/processes` shows what it waits on as its `detail`: a failure shows as `failed` with the
> reason, retried from 30 s up to the policy's `failed_retry`, or, when no retry in this run can
> mend it, `failed` with no `next_restart_at` until the server starts again. Beside the English
> `detail` (for the logs and older shells) each such process has `detail_code`, a snake_case code
> from the fixed list below, and `detail_params`, its values, for a screen to say it in its own
> language (both left out when there is no detail).

### The database step, before any role

Every process but postgres (and the model server, which keeps nothing of the library) waits for
one run of the database step. It reads **the database's record**,
`<state>/state/database.json`: the release whose migrations the database holds, written before
any role runs, so it is right even when that release never ran three healthy minutes. An
install from before the record has none (an earlier build of this story has only
`state/payload.json`, which then stands in for it).

**A joined full node has no database step** (Epic 35 story 35.8: a joined node config, unless
`[server] role = "home"`, the same test that puts its api in node mode). Its api reads the
home's database and never migrates; the home's own start dumps, migrates and records. So on a
node no process waits on the step, nothing is dumped or migrated, and no `state/database.json`
is written (one there would later refuse an older app on a machine that has no database of its
own). The pipeline still waits for the venv bootstrap, and `state/payload.json` is still saved
after three healthy minutes. A run without the api (`serve --role streaming`, ...) migrates
nothing either: it dumps nothing and leaves the record alone, and only refuses an older release
than the record, or a database whose putting back has not finished.

| The payload's `version`, against the record | What the step does |
|---|---|
| the same | runs the migrations (`maktaba-api migrate up`: nothing to apply, done at once); nothing is dumped |
| an older *release* | refuses, for every process: its code does not know the newer one's migrations. The detail names the release to install and the dump from before that update. (A development build's order is not known: it goes on, and its migrations refuse a database ahead of them, below) |
| another, or no record | 1. **backup first:** `pg_dump -Fc` into `backups/pre-<old>-to-<new>.dump` (`pre-unknown-to-<new>` without a record: an Epic 36 Mac on its first update to this build; a fresh install's first start dumps the database setup made). A dump of that name already there is replaced: the dump is always the database as it is now. The newest `[postgres] update_dumps` (default 3) pre-update dumps stay, the nightly 7 counted apart. 2. the record says `migrating` (to the new version, from the old, with the dump). 3. the migrations, with no time limit of their own (`migrate --timeout 0 up`: nothing else uses the database meanwhile, and a stop of the server ends them). 4. the record says `migrated` |

When the migrations fail, the update stops there and **the database goes back** as it was:
the record says `restoring`, the dump is restored into the running cluster (the database made
afresh, what is connected to it cut off), then the record names the release from before
(`restored`, with the release that failed). Every process stays stopped with a detail saying
that the update to `<new>` could not change the database (with the migration's last lines), that
the database is back as it was before the update (with the dump's path), and what happens next:
install `<old>` again to go on as before, or an update that fixes it; starting Maktaba again
tries the update again (one try per start: a fresh dump, the migrations, the restore if they fail
again).

Why the server stays stopped rather than running the previous payload: the updater replaced the
whole payload (Tauri keeps no previous app; on Windows and Linux the installer replaced the
files), so the previous release's binaries are gone, and the new release's code cannot run on
the old schema it was not written for. The restored database is exactly what the previous
release left, and the record names that release, so installing it again starts at once, with
no downgrade refusal. Each case the step can meet again is safe to repeat:

- a stop or a crash **while migrating** (the record still says `migrating`): the next start
  goes on with the same dump (none is taken of a half-migrated database), and restores it if
  the migrations fail;
- a stop or a crash **while restoring** (the record says `restoring`): the next start finishes
  the restore first; with the release that failed it then stops again (that start was the way
  back), with another release it goes on from the restored database;
- the restore itself fails: every process stays stopped, the detail gives both errors and the
  dump's path; the next start tries the restore again;
- the migrations refuse a database **ahead of them** (`migrate up` exits 3: a newer Maktaba
  migrated it): every process stays stopped, nothing is restored, the record stays as it was.

The API refuses such a database on every install, not only in the app: `migrate up` exits 3 when
the database holds a migration newer than the newest in its migrations folder, and `serve`
refuses to start when it holds one newer than the newest the code ships (`schemaguard.SchemaRev`).

With a database of the user's own (`[database] url` set: no bundled Postgres) nothing is dumped
or restored; a failed migration is tried again like any start step.

### The detail codes

`detail_params` values are strings unless said; releases are versions (`""` when not known),
paths are whole, `error` values are the programs' own English (pg_dump's, the migration's last
lines). The list is fixed (`detailCodes` in `serve_appbundle.go`, checked by a test); a new detail
takes a new code.

| `detail_code` | `detail_params` | The English `detail` |
|---|---|---|
| `db_waiting` | — | Waiting for the library database |
| `db_checking` | `release` | Checking the library database |
| `db_dumping` | `from`, `to`, `mb`, `expected_mb` | Saving the library database before the update to `to`: `mb` written so far, of about `expected_mb` (the newest finished dump's size; 0 when there is none) |
| `db_migrating` | `to` | Updating the library database for `to` |
| `db_restoring` | `failed`, `dump` | The update to `failed` could not change the library database: putting it back as it was |
| `db_restored` | `failed`, `error`, `dump`, `release` | The update to `failed` could not change the library database (`error`). The database is back as it was before the update (restored from `dump`), and Maktaba stays stopped: install `release` (`""`: the version you had before) again to go on as before, or an update that fixes this; starting Maktaba again tries this update again |
| `db_refused_older` | `release`, `needed`, `dump` | This Maktaba (`release`) is older than the one whose changes the library database holds (`needed`): it would not know them. Install `needed` or later (when `dump` is not `""`: the database as it was before the update to `needed` is in `dump`) |
| `db_ahead` | `release`, `error` | The library database was changed by a newer Maktaba than this one (`release`), whose code would not know those changes: install that newer version again (`error`) |
| `db_dump_failed` | `to`, `error` | The library database could not be saved before the update to `to`, so the update waits (nothing was changed): `error` |
| `db_migrate_failed` | `release`, `error` | The library database could not be brought up to date for `release`: `error` (no update: tried again) |
| `db_restore_failed` | `failed`, `error`, `dump`, `restore_error` | The update to `failed` could not change the library database (`error`), and putting the database back from `dump` failed too: `restore_error`. Maktaba stays stopped; its next start tries to put it back again |
| `db_restore_unfinished` | `failed`, `dump` | The library database is being put back as it was before the update to `failed` (from `dump`): start the whole server (`maktaba-server serve`) to finish it |
| `db_restoring_backup` | `dump`, `by` | Putting the library database back from the backup `dump`, as `by` asked (a restore by hand, Epic 35 story 35.5 AC8) |
| `db_restore_backup_failed` | `dump`, `error` | Putting the library database back from the backup `dump` failed: nothing was changed; Maktaba stays stopped and its next start tries again |
| `db_no_cluster` | `failed`, `error`, `dump` | The update to `failed` could not change the library database (`error`), and this Maktaba runs no database of its own to put `dump` back into |
| `db_record_failed` | `path`, `error` | The library database's record (`path`) cannot be read or saved: `error` |
| `pg_unreadable` | `path`, `error` | The library database (`path`) cannot be read: `error` |
| `pg_major` | `cluster`, `payload` | The library database was made by PostgreSQL `cluster` and this Maktaba carries PostgreSQL `payload`: the database needs an upgrade between the two, which no update does yet |
| `venv_waiting` | — | Waiting for the transcription tools |
| `venv_building` | `step`, `done` (number), `total` (number), `percent` (number), `message` | Installing the transcription tools: `message` (`percent`%). `step` is the bootstrap's (`plan`, `venv`, `deps`, `wheel`, `verify`, `done`); on `deps`, `done` of `total` packages (else 0 of 0); `message` is pip's line |
| `venv_interrupted` | `log` | The transcription tools' install was stopped; it goes on from where it was next time (log: `log`) |
| `venv_failed` | `error`, `log` | The transcription tools could not be installed: `error` (log: `log`) |
| `storage_waiting` | `path` | Waiting for the disk that holds Maktaba's data (`path`): the data folder the owner chose (story 36.15) is not there; nothing starts and no database is made in its place until it is |
| `storage_moving` | `to`, `done` (number), `total` (number), `percent` (number) | Moving Maktaba's data to `to` (`percent`%): `done` of `total` bytes copied (story 36.15) |
| `storage_move_failed` | `to`, `error` | Maktaba's data could not be moved to `to`: `error`. Nothing was changed: Maktaba starts from where it was |

### The rest

| Changed | Action |
|---|---|
| `postgres.version` major (16 → 17) | do **not** start Postgres on the old cluster: `pg_upgrade` from the previous app's binaries is impossible (they are gone), so the major bump ships `pg_dumpall` before it (the release before a major bump writes `backups/pre-major.sql`) and the new start does `initdb` + restore. Until a major bump is planned: refuse with a clear status ("database needs an upgrade") rather than corrupt anything. Minor (16.15 → 16.16): nothing to do |
| `pipeline.constraints_sha256`, `python.version` minor | the venv bootstrap rebuilds the venv **in place**: it removes it (its stamp first) and builds it at `venv/` again; a build stopped or failed half-way resumes at the next start, and pip's cache keeps what it downloaded |
| only `pipeline.sha256` (the wheel) | the bootstrap reinstalls the wheel alone (`pip install --no-deps --force-reinstall`, seconds) |
| `python.version` patch, or the app moved | the bootstrap repoints the venv at the bundled Python (`venv --upgrade`) |
| `ffmpeg`, Go binaries | nothing: they run from the bundle |

The pipeline's processes (the main worker and every transcription worker) wait for the database
step, then for one run of the venv bootstrap: it compares the constraints, the wheel and the
Python with its own stamp and does nothing when nothing changed (well under a second). A pipeline
whose Python is named (`MAKTABA_PYTHON`, `[pipeline] python`) does not use the app's venv and
waits for the database alone. The bootstrap runs with an environment of its own and the
payload's Python as `--python` (contract-venv.md §1).

**Why in place, not a `venv.new/` swapped in.** A venv cannot be moved once built: pip writes
each console script with the venv's absolute interpreter path (a shebang; on Windows inside each
`.exe` launcher), so a venv built at `venv.new/` and renamed to `venv/` would leave every script
pointing at `venv.new/`. And the old venv is of no use to the new payload: a rebuild happens only
when the new wheel needs other dependencies or another Python, and the pipeline's processes wait
for the bootstrap, so nothing would run on the old venv during the build ("usable until the
swap" would protect nothing). Keeping it would cost a second venv's space (a few GB with torch)
during the build, enough to fail it on a nearly full disk where an in-place build succeeds. What
it would buy, going back to the previous app with its venv intact, would not work on its own
either: that app refuses the database the new one migrated until the pre-update dump is
restored, and after a successful build the old venv is gone in both designs.

Then the roles start (postgres → api → streaming → pipeline workers). Once every role has run
healthy for a while (the 3-minute rule of `confirmAfter`), `state/payload.json` is saved as the
last payload that ran (what ran, for the owner; the database's record is what the next update
compares with).

## Going back

There is no in-app rollback of the app itself: Tauri does not keep the previous app (the server's
payload keeps its own copies). To go back, the owner
installs the previous `.dmg`:

- after an update whose migrations **failed**, the database is already back as the previous
  release left it, and the record names that release: it starts as before;
- after an update that **migrated** the database, the previous release (this story's or later)
  finds the record naming a newer release and refuses to start any role, and says so (its API
  refuses the newer schema on its own too); the way back is restoring the
  `pre-<old>-to-<new>.dump` taken before the update (a `maktaba-server` restore command, a
  follow-up), then starting.

## Open for the owner

- The Developer ID (README of `tools/release/mac-app`): without it an update cannot keep the
  login-item approval across versions reliably, and Gatekeeper blocks the downloaded app.
