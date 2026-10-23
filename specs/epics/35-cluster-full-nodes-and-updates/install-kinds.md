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
| The app's updater is the only updater inside an app; the server's own is off (Epic 36 contract-update). | The server decides **what** and the order of the machines (policy, home first, one node at a time); a desktop app that runs the server stages what the home orders and installs it **when someone clicks there** (owner decision D1, "Ask first" below); the app's updater still does the swap for Mac and Windows. Headless and plain server installs update by themselves in the window. |
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
| **Maktaba.app** (Mac, Epic 36) | `MAKTABA_APP_BUNDLE` inside `*.app/Contents/Resources/server` | verify `release.json`; check the app feed (`latest.json`) of that release lists this platform; keep a copy of the running payload (`<state>/updates/payloads/<v>`, an APFS clone); rehearse (home) | **order** the release from the app (`<state>/updates/app-install.json`; start it with `maktaba://install-update` when it does not answer): it downloads and checks its update and shows it; **after a click there and the home's go**, the app's updater swaps the bundle (from what it staged, checked again) and restarts the server | `<state>/updates/payload-override` names the kept payload; the launcher runs it |
| **Windows app** (Epic 38) | `MAKTABA_APP_BUNDLE` in `<install>\server` beside `Maktaba.exe` | same (a plain copy) | same; the NSIS installer's hooks stop and start the server | same |
| **Linux app** (Epic 39; `MAKTABA_RUNTIME_OWNER=app`) | the launcher runs from a runtime copy | download the release's `maktaba-linux-payload-<v>-<arch>.tar.gz`, verified against the signed `checksums.txt`, installed as a new runtime directory; rehearse (home) | order it from the app (nothing for it to fetch: it shows it); after a click there and the home's go, `runtime/current` → the new directory, `previous` → the old; exit 75 (the launcher starts `runtime/current` again) | `current` → `previous`; exit 75 |
| **Headless** (`install.sh --app`; `MAKTABA_RUNTIME_OWNER=headless`) | the same runtime copy | the same | by itself, in the window: the switch above, no order | the same |
| **Plain server** (`setup --self-managed`, `.deb`/`.rpm` in the layout) | `MAKTABA_LAUNCHER=1`, `bin/releases/<v>` | `releaseverify/install.Stage` into `bin/releases/<v>` with the release's migrations; rehearse (home) | `current` = `<v>` under the launcher's guard (`pending`); exit 75 | `current` = `previous`; exit 75 |

A **flat** install (binaries copied by hand, no launcher) has nothing to fall back with: its run
fails its preflight with `not_self_managed` and the one command that converts it
(`maktaba-server setup --service`, which writes the layout and points the service at the
launcher); from then on it updates itself. Docker keeps Epic 28.3's answer.

### Where each piece lives

| Piece | Code |
|---|---|
| The run: home prepare, gates (window, TV), drain, apply, commit, rollout, catch-up, admin actions | `api/internal/updates/driver.go`, `run_handlers.go`, `runs.go` (slot 0149: `update_runs.install_kind`, `detail`; `update_targets.name`, `kind`, `detail`, `updated_at`) |
| A machine's part: journal, prepare, apply, watch, commit, rollback, resume | `cmd/maktaba-server/internal/updater` (`machine.go`, `journal.go`), served on the supervisor's loopback (`/update/*`) and as `update.*` commands to a node's machine agent (`serve_update.go`) |
| Installers | `updater/layout.go`, `updater/runtime.go`, `updater/appbundle.go` |
| Health of the release that came up | `serve_update.go` (`machineHealth`: every role running, none crash-looping or halted by the database step, the api's `/readyz` and its version, on a node the schema guard and the machine agent) |
| The database | `cmd/maktaba-server/internal/dbsafe` (dump checked with `pg_restore --list`, rehearsal, restore in place), the database step in `serve_appbundle.go` (now also `layoutHomeGates` for a plain server home), the copies' slots dropped before a dump is put back (`pgbundle.DropSlotsSQL`) |
| The launcher's guard for the app and the runtime copy | `cmd/maktaba-launcher/run.go` (`bundleGuard`, `bundleRollback`), `shared/go/layout/guard.go` (`pending.json`, `payload-override`, `MarkRecordRestoring`) |
| The order, its staging and its click (ask first) | `cmd/maktaba-server/internal/updater/request.go`, `apps/desktop/src-tauri/src/autoinstall.rs`, `shell.rs` (`install_requested`, `stage_order`), `tray.rs` (`entries_with_order`) |
| The release's migrations | `release.yml` (`maktaba-migrations-<v>.tar.gz`), `release.json` `migrations`, `releaseverify/install.Stage` (beside the binaries), `api/migrate.go` (found beside `maktaba-api`) |
| The page | `web/src/pages/Admin/Updates.tsx` (`/admin/updates`) |

### Ask first: a desktop app installs on a click (owner decision D1, 2026-10-23)

A desktop app that runs the server — the home's, or a full node's: Maktaba.app, the Windows
app, the Linux app — keeps its "restart to install". What the home orders is downloaded,
verified and staged by itself; it is installed only when someone clicks on that machine.
Headless installs and plain server installs update fully by themselves within the policy's
window.

**The order** (`<launcher root>/updates/app-install.json`, the app's own state folder;
`updater/request.go`, `autoinstall.rs`): the supervisor writes `ordered` with the release's
own feed (`releases/download/v<v>/latest.json`: the channel, pin and skip rules hold) and its
`kind` (`app`: the app's updater installs it; `runtime`: the server installs its own runtime,
D3) and `home`. The app stages it (kind app: downloads its update from that feed, checks it
with its key, keeps it in `updates/app-update-<v>.bin`) and answers `staged`; the tray shows
it — on the home "Install Maktaba <v> (this home's update) and restart", on a node "Install
Maktaba <v>: the cluster's update waits for this machine". A click answers `clicked`; the
supervisor waits for the home's `go` and then switches (kind app: `go`, and the app installs
what it staged, checked again; kind runtime: the runtime switch). The supervisor's journal
says `awaiting_click`, then `clicked`; a restart takes it up where it waited; a newer run
replaces an order still waiting; Abort withdraws it (the app forgets what it staged).

**The run** (`driver.go`):

- **The home** asks: its release is ordered as soon as it is prepared, without the window's or
  the TV's gate (the click is the person's moment) and with nothing drained while it waits.
  Its click drains the cluster's workers (as before an automatic apply), then the home goes.
  The machines are ordered only once the home committed.
- **A machine** asks: it is ordered as soon as it is prepared and waits for its click while the
  rollout goes on with the others (target `waiting_click`); its click takes the next turn
  (`clicked`, then the go: still one machine at a time).
- Nothing else waits for ever: a run left waiting only for clicks stays open (step
  `waiting_click`, the Updates page names the machines with the release each waits for, and a
  notice `update.waiting_click` says where), and a newer release takes its place (`superseded`:
  the orders withdrawn, the newer one ordered instead). Cancel withdraws the home's order
  before its go, and stops a rollout; Skip withdraws one machine's.
- **A machine behind the home stays safe**: the schema guard pauses a node only when the home's
  schema is one its code cannot run against (a breaking release, `maktaba_min_node_schema`);
  additive releases leave it working behind the home. Its copy of the home's database (Epic 37)
  pauses while the home's schema is ahead (`incompatible`) and its reads go to the home's
  database meanwhile; after its update it copies the home anew.

An app that runs the server no longer offers its own feed's updates (the home decides what);
a client-only app keeps them. The Linux app older than the server it runs (D3: the server
updated its runtime) offers its own update for its window and tray.

### The database (home only)

1. **Prepare: rehearsal.** A dump of the home's database (bundled Postgres: its own `pg_dump`;
   another Postgres: `pg_dump` of the server's major on `PATH` or in `MAKTABA_PG_BIN`) is
   restored into `maktaba_rehearse_<run8>` and the new release's `maktaba-api migrate up` runs on
   it (the release's `migrations` archive, verified like everything else). A failure blocks the
   version; the real database was never touched. Skipped above `rehearse_max_db_gb`, without
   `CREATEDB`, or for a release without a migrations archive (logged).
2. **Apply: the 38.12 database step**, now for every home (it ran only inside the apps): at the
   new release's start, before any role, the dump (`backups/pre-<old>-to-<new>.dump`, verified
   with `pg_restore --list`), the migrations, the record `state/database.json`; a failed
   migration puts the dump back and the run is `rolled_back` with nothing changed. The watch
   neither commits nor counts the release unhealthy while the step works (or the app's Python
   environment is built), however long it takes; the launcher's guard bounds it.
3. **Rolling back after the migrations ran** (a check failed in the watch, or the launcher's guard
   fired): the record is set to `restoring` (failed = the new release, dump = the one above)
   before the previous release starts, and that release's database step puts the dump back
   before any role runs (code every release since 38.12 has). What is lost: the writes between
   the dump and the rollback — minutes, inside the window, with every pipeline worker of the
   cluster drained from the home's apply until it commits. The failed state is dumped aside
   first (`backups/failed-<new>-<utc>.dump`) for anyone who wants it back.
4. **The machines' copies (Epic 37) across it.** A machine's copy follows only a home whose
   schema is not ahead of its own: from the home's migration until that machine's update, it
   pauses (`incompatible`; its reads go to the home), and the machine's new release migrates
   its own database and copies the home anew. A dump put back takes the copies' logical slots
   with it (`pgbundle.DropSlotsSQL`, before the bundled `DROP DATABASE`, which refuses a
   database with slots, and before the in-place restore, which skips the dump's
   publications): what they streamed is gone, and each machine copies the restored database
   anew when its grant says its slot is. A machine rolled back after its new release migrated
   its copy: the previous release refuses that database (reported as the copy's error, reads
   go to the home), while the copy's subscription keeps applying the home's changes; the copy
   is taken up again when the machine takes the release.

### Nodes

Nodes never migrate the home's database (their own copy of it, Epic 37, is migrated by their
new release; "The database" step 4). Apply = switch, restart, watch (schema guard fits, api
`/readyz` with the new version, the machine agent back on the channel), commit; a failed check
switches back (binaries only). A node is never ordered to a version the home has not committed, and a node
that comes back behind the watermark gets a `catch_up` run of its own.

## What still needs a person

- **A click on each desktop app that runs the server** (owner decision D1): the home's first,
  then each machine's; the release is staged and waiting. The rollout goes on with the others
  meanwhile, and a newer release replaces an order nobody clicked.
- **Installs from before this release** have automatic installs compiled off (and, in the apps,
  discovery off): they take this release once by the way they update today (the app's "Install
  update", `install.sh`, `maktaba-server update`); every release after it comes by itself (in
  a desktop app, staged and waiting for its click).
- A Mac app in `/Applications` the user cannot write without an admin password: the app reports
  the install failed (the run fails, nothing changed) — move the app or update it once by hand.
- A flat install (binaries copied by hand, no launcher): one `maktaba-server setup --service`.
- A Linux app installed as a `.deb`/`.rpm`: its server's runtime switches on the click (no root
  needed, D3); the app's own window and tray update with the package manager or the app's
  "Install update".
- A release whose migrations break the previous release (`breaks_previous_home`): held for an
  admin's approval (unchanged).
- A failed run blocks that version; the next release installs by itself, or an admin retries.
