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

> **Built in Epic 38 story 38.12** for the Mac, Windows and Linux alike
> (`cmd/maktaba-server/serve_appbundle.go`, `internal/apppayload`, `internal/appvenv`,
> `internal/pgbundle/upgrade.go`, the supervisor's `Process.Prepare`; the API's own guard in
> `api/schema_ahead.go`). Each process waits on its `Prepare` before its first start, and
> `/processes` shows what it waits on as its `detail`: a failure shows as `failed` with the
> reason, retried from 30 s up to the policy's `failed_retry`, or, when no retry in this run can
> mend it, `failed` with no `next_restart_at` until the server starts again.

### The database step, before any role

Every process but postgres (and the model server, which keeps nothing of the library) waits for
one run of the database step. It reads **the database's record**,
`<state>/state/database.json`: the release whose migrations the database holds, written before
any role runs, so it is right even when that release never ran three healthy minutes. An
install from before the record has none (an earlier build of this story has only
`state/payload.json`, which then stands in for it).

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

There is no in-app rollback: Tauri does not keep the previous app. To go back, the owner
installs the previous `.dmg`:

- after an update whose migrations **failed**, the database is already back as the previous
  release left it, and the record names that release: it starts as before;
- after an update that **migrated** the database, the previous release (this story's or later)
  finds the record naming a newer release and refuses to start any role, and says so (its API
  refuses the newer schema on its own too); the way back is restoring the
  `pre-<old>-to-<new>.dump` taken before the update (a `maktaba-server` restore command, a
  follow-up), then starting.

## Open for the owner

- The Tauri updater key pair: `plugins.updater.pubkey` in `tauri.conf.json` is still a
  placeholder; the private key goes into `TAURI_SIGNING_PRIVATE_KEY(_PASSWORD)`.
- The Developer ID (README of `tools/release/mac-app`): without it an update cannot keep the
  login-item approval across versions reliably, and Gatekeeper blocks the downloaded app.
