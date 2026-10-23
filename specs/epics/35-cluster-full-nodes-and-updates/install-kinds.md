# Epic 35 phase B + D, reconciled with the one-app installs (Epics 36, 38, 39)

> **Status:** built with stories 35.4–35.7, 35.12 and the parts of 35.13 that show, pause and
> retry a run. **Why this page:** the Epic 35 design (2026-10-19) predates the desktop apps that
> carry the server (Epic 36 Mac, 38 Windows, 39 Linux) and the database step they run at every
> start (Epic 38 story 38.12). This page says what changed, and what each kind of install does
> now. Where it and [design.md](design.md) disagree, this page wins.

## What the older design assumed, and what is true now

| design.md | Now |
|---|---|
| Every machine runs a launcher over `bin/releases/<v>` (§2). | Four kinds of install (below). Only plain server installs use `bin/releases`. The apps run a payload inside the app (Mac, Windows) or a runtime copy of it (Linux). |
| The app's updater is the only updater inside an app; the server's own is off (Epic 36 contract-update). | The server decides **when** and **what** (policy, window, home first, one node at a time); the app's updater still does the swap for Mac and Windows, ordered by the server, without a click. |
| Rollback replaces binaries; the schema stays (migrations are additive). | Every installed release (38.12) refuses a database a newer release migrated (`migrate up` exits 3, `serve` refuses). So a rollback after a migration also **puts back the dump taken before it**. |
| The orchestrator backs up and migrates before restarting the roles (§5.3 steps 6–9). | Every home migrates at the new release's start, through the 38.12 database step (dump, migrate, put back on failure), whatever the install kind. The orchestrator rehearses the migrations on a scratch copy before anything is installed. |
| TV: Epic 33 hot swap, or a restart when nobody watches (§7.2). | The fallback: the machine restarts in the window only while `tv_staging.quiet_max_viewers` (0) are watching; at the window's end it is `tv_busy` and tried again next window. The Epic 33 binary swap and the TV handover across a supervisor restart (§8.1) are not used yet. |

## One run, four installers

The **home's API** drives the run (`api/internal/updates`, `Driver`): it picks the release (story
35.3), applies the window, the TV gate and the drain, and orders each machine's **supervisor** to
*prepare* and then *apply* — the home's own supervisor over its loopback admin, each node's over
the cluster channel (`update.prepare`, `update.apply`, `update.status`, `update.abort`,
`update.rollback` to its machine agent). The run lives in `update_runs`, `update_run_steps` and
`update_targets`; an API that restarts mid-run (it is one of the things an update restarts)
takes it up again from them.

Each **supervisor** runs the machine's own part (`cmd/maktaba-server/internal/updater`) with a
journal (`<state>/updates/journal.json`, written atomically before and after every step) that
outlives its own restart: *prepare* (fetch and verify the release, stage it, smoke-test it, and on
the home rehearse its migrations), *apply* (switch to it and restart), then, in the release that
comes up, *watch* and *commit* or *roll back*. **The journal is the contract between releases:
whichever release starts next — the new one, or the previous one after a rollback — finishes the
run.**

| Install kind | How the supervisor tells | Prepare (nothing changes) | Apply | Roll back |
|---|---|---|---|---|
| **Maktaba.app** (Mac, Epic 36) | `MAKTABA_APP_BUNDLE` inside `*.app/Contents/Resources/server` | verify `release.json`; check the app feed (`latest.json`) of that release lists this platform; keep a copy of the running payload (`<state>/updates/payloads/<v>`, an APFS clone); rehearse (home) | ask the app (`<state>/updates/app-install.json`; start it with `--install-update` when it is not running); the app's updater downloads the app, checks its signature, swaps the bundle and restarts the server | `<state>/updates/payload-override` names the kept payload; the launcher runs it |
| **Windows app** (Epic 38) | `MAKTABA_APP_BUNDLE` in `<install>\server` beside `Maktaba.exe` | same (a plain copy) | same; the NSIS installer's hooks stop and start the server | same |
| **Linux app and headless** (`install.sh --app`, Epic 39) | the launcher runs from a runtime copy (`MAKTABA_RUNTIME_OWNER`) | download the release's `maktaba-linux-payload-<v>-<arch>.tar.gz`, verified against the signed `checksums.txt`, installed as a new runtime directory; rehearse (home) | `runtime/current` → the new directory, `previous` → the old; exit 75 (the launcher starts `runtime/current` again) | `current` → `previous`; exit 75 |
| **Plain server** (`setup --self-managed`, `.deb`/`.rpm` in the layout) | `MAKTABA_LAUNCHER=1`, `bin/releases/<v>` | `releaseverify/install.Stage` into `bin/releases/<v>` with the release's migrations; rehearse (home) | `current` = `<v>` under the launcher's guard (`pending`); exit 75 | `current` = `previous`; exit 75 |

A **flat** install (binaries copied by hand, no launcher) is converted to the plain server layout
on its first run when the supervisor can write its own folder (`selfmanaged.Convert`, the launcher
put beside it) and the service starts the launcher; one it cannot convert is reported
(`not_self_managed`) with the one command that converts it. Docker keeps Epic 28.3's answer.

### The app shell installs without a click (owner decision)

A desktop app that runs the server (the home, or a full node) installs the update the server
orders, in the window, without a click; its window, if open, relaunches. The server names the
exact release (`releases/download/v<v>/latest.json`), so the channel, pin and skip rules hold and
the app installs nothing the server did not verify. A client-only app keeps the "restart to
install" badge. The Linux app's server updates its own runtime (a `.deb` update needs root at
03:00); the app catches up with its own updater later and no longer says it is older than the
server.

### The database (home only)

1. **Prepare: rehearsal.** A dump of the home's database (bundled Postgres: its own `pg_dump`;
   another Postgres: `pg_dump` of the server's major on `PATH` or `[postgres] bin_dir`) is
   restored into `maktaba_rehearse_<run8>` and the new release's `maktaba-api migrate up` runs on
   it (the release's `migrations` archive, verified like everything else). A failure blocks the
   version; the real database was never touched. Skipped above `rehearse_max_db_gb`, without
   `CREATEDB`, or for a release without a migrations archive (logged).
2. **Apply: the 38.12 database step**, now for every home (it ran only inside the apps): at the
   new release's start, before any role, the dump (`backups/pre-<old>-to-<new>.dump`, verified
   with `pg_restore --list`), the migrations, the record `state/database.json`; a failed
   migration puts the dump back and the run is `rolled_back` with nothing changed.
3. **Rolling back after the migrations ran** (a check failed in the watch, or the launcher's guard
   fired): the record is set to `restoring` (failed = the new release, dump = the one above)
   before the previous release starts, and that release's database step puts the dump back
   before any role runs (code every release since 38.12 has). What is lost: the writes between
   the dump and the rollback — minutes, inside the window, with every pipeline worker of the
   cluster drained from the home's apply until it commits. The failed state is dumped aside
   first (`backups/failed-<new>-<utc>.dump`) for anyone who wants it back.

### Nodes

Nodes never migrate. Apply = switch, restart, watch (schema guard fits, api `/readyz` with the
new version, the machine agent back on the channel), commit; a failed check switches back
(binaries only). A node is never ordered to a version the home has not committed, and a node
that comes back behind the watermark gets a `catch_up` run of its own.

## What still needs a person

- **Installs from before this release** have automatic installs compiled off (and, in the apps,
  discovery off): they take this release once by the way they update today (the app's "Install
  update", `install.sh`, `maktaba-server update`); every release after it installs itself.
- A Mac app in `/Applications` the user cannot write without an admin password: the app reports
  the install failed (the run fails, nothing changed) — move the app or update it once by hand.
- A flat install that cannot convert itself (its folder is not writable by the service's user, or
  its service runs the binary itself): one `maktaba-server setup --service`.
- A release whose migrations break the previous release (`breaks_previous_home`): held for an
  admin's approval (unchanged).
- A failed run blocks that version; the next release installs by itself, or an admin retries.
