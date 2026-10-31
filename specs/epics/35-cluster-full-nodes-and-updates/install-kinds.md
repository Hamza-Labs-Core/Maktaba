# Epic 35 phase B + D, reconciled with the one-app installs (Epics 36, 38, 39)

> **Status:** built with stories 35.4, 35.5, 35.6 (the restart fallback and the binary TV
> change, "The TV step" below), 35.7 (the TV handover
> across a supervisor restart, resume from the journal, the launcher's guard, launcher
> replacement, `--status`), 35.12 and the parts of 35.13 that show, pause and retry a run. A
> plain server install's TV processes (the front door and the slots) run on across its
> restarts; every other install restarts whole, under the TV gate (story 35.6's fallback).
> **Why this page:** the Epic 35 design (2026-10-19) predates the desktop apps that
> carry the server (Epic 36 Mac, 38 Windows, 39 Linux) and the database step they run at every
> start (Epic 38 story 38.12). This page says what changed, and what each kind of install does
> now. Where it and [design.md](design.md) disagree, this page wins.

## What the older design assumed, and what is true now

| design.md | Now |
|---|---|
| Every machine runs a launcher over `bin/releases/<v>` (§2). | Four kinds of install (below). Only plain server installs use `bin/releases`. The apps run a payload inside the app (Mac, Windows) or a runtime copy of it (Linux). |
| The app's updater is the only updater inside an app; the server's own is off (Epic 36 contract-update). | The server decides **what** and the order of the machines (policy, home first, one node at a time); a desktop app that runs the server stages what the home orders and installs it **at the home's go, by itself** (owner decision D1, "No click" below); the app's updater still does the swap for Mac and Windows. Every install updates by itself: an admin's run at once, an automatic one in the window; the machines follow once the home commits (2026-10-28). |
| Rollback replaces binaries; the schema stays (migrations are additive). | Every installed release (38.12) refuses a database a newer release migrated (`migrate up` exits 3, `serve` refuses). So a rollback after a migration also **puts back the dump taken before it**. |
| The orchestrator backs up and migrates before restarting the roles (§5.3 steps 6–9). | Every home migrates at the new release's start, through the 38.12 database step (dump, migrate, put back on failure), whatever the install kind. The orchestrator rehearses the migrations on a scratch copy before anything is installed. |
| TV: Epic 33 hot swap, or a restart when nobody watches (§7.2). | A plain server install (the launcher over `bin/releases`) hands its TV processes over across the restart (§8.1, story 35.7, "TV handover" below): the front door and the slots keep serving while every other process restarts on the new release, and the new supervisor adopts them, so the run waits for no quiet TV (the machine's prepared report says `hands_over`). Where they cannot run on (another install kind, a front door the new release's `edge_protocol` does not match, a Linux launcher from before the handover, a Windows server whose job lets no child leave it under a launcher from before it), the fallback: the home restarts in the window only while `tv_staging.quiet_max_viewers` (0) are watching (checked before the drain and again right before its apply); a machine only while its own streams have no more watchers (`channel_runtime.host_id`), and the machines keeping streams on air go last; at the window's end the home is `tv_busy` and tried again next window. Then the Epic 33 binary swap (story 35.6): once the new release runs, its supervisor asks the TV change controller for a change of the streaming binary alone (`source.kind = auto_update`), promoted without an approval at programme boundaries, and commits only once it is live (below, "The TV step"). |

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
| **Maktaba.app** (Mac, Epic 36) | `MAKTABA_APP_BUNDLE` inside `*.app/Contents/Resources/server` | verify `release.json`; check the app feed (`latest.json`) of that release lists this platform; keep a copy of the running payload (`<state>/updates/payloads/<v>`, an APFS clone); rehearse (home) | **order** the release from the app (`<state>/updates/app-install.json`; start it with `maktaba://install-update` when it does not answer): it downloads and checks its update and says when it installs; **at the home's go** (no click), the app's updater swaps the bundle (from what it staged, checked again) and restarts the server | `<state>/updates/payload-override` names the kept payload; the launcher runs it |
| **Windows app** (Epic 38) | `MAKTABA_APP_BUNDLE` in `<install>\server` beside `Maktaba.exe` | same (a plain copy) | same, except that the server agent (`Maktaba.exe --server-agent`) answers the order, not the window app, and no `maktaba://` link is sent (see "On Windows the server agent installs it"); the NSIS installer's hooks stop and start the server | same |
| **Linux app** (Epic 39; `MAKTABA_RUNTIME_OWNER=app`) | the launcher runs from a runtime copy | download the release's `maktaba-linux-payload-<v>-<arch>.tar.gz`, verified against the signed `checksums.txt`, installed as a new runtime directory; rehearse (home) | order it from the app (nothing for it to fetch: it says when it installs); at the home's go (no click), `runtime/current` → the new directory, `previous` → the old; exit 75 (the launcher starts `runtime/current` again) | `current` → `previous`; exit 75 |
| **Headless** (`install.sh --app`; `MAKTABA_RUNTIME_OWNER=headless`) | the same runtime copy | the same | by itself, in the window: the switch above, no order | the same |
| **Docker** (`ghcr.io/hamza-labs-core/maktaba-server`; `MAKTABA_CONTAINER=docker`, owner headless) | the same runtime copy, on the image's volume; kind `docker` | the same | the same as headless: the server switches the volume's runtime by itself and exits 75; the launcher (run by the image's entrypoint, `maktaba-server container`, behind tini) starts it | the same |
| **Plain server** (`setup --self-managed`, `.deb`/`.rpm` and the Homebrew formula in the layout) | `MAKTABA_LAUNCHER=1`, `bin/releases/<v>` | `releaseverify/install.Stage` into `bin/releases/<v>` with the release's migrations; **the pipeline's environment** `bin/releases/<v>/venv` built from the release's wheel and hash-checked constraints (verified like every file) with the machine's Python (§3.4); smoke (`python -m maktaba_pipeline --version` says the wheel's version, PEP 440's spelling of the release's); rehearse (home) | `current` = `<v>` under the launcher's guard (`pending`); exit 75; the pipeline runs the release's venv | `current` = `previous`; exit 75 |

A **flat** install (binaries copied by hand, no launcher) has nothing to fall back with: its run
fails its preflight with `not_self_managed` and the one command that converts it
(`maktaba-server setup --service`, which writes the layout and points the service at the
launcher); from then on it updates itself.

**Docker** is the headless install in a container (`deploy/docker/server`, built by `release.yml` from
the release's own Linux payloads, signed at its digest, `server` in `release-manifest.json`). The
image is only a bootstrap: its entrypoint puts its payload on the volume's runtime when the runtime
has none or an older one (never an older payload over a newer runtime), owned by the machine, waits
for the setup (`docker compose run --rm maktaba setup`: the terminal wizard, with no systemd checks
and no service step), then runs the runtime's launcher and copies its log to `docker logs`. Updates
are the headless install's, verified in process; there is no Docker socket and nothing to pull. A
newer image pulled later is switched to as an update is (`cmd_container.go`): under the launcher's
guard (`pending.json`, run `docker-image`, the runtime before to go back to, the database record),
with `appruntime.Point`, so the watermark stays; the entrypoint keeps it once the server has
answered with the new version for 3 minutes (guard cleared, watermark raised, older runtimes
pruned), and when the launcher goes back instead it records the version in
`updates/image-refused.json` and never switches to that image again (a newer one, yes); the image
that ran before still runs, as the watermark never rose. A `docker compose run` from a newer image
switches nothing and prunes nothing (a container may be running). An image managed elsewhere
(Watchtower) runs with `MAKTABA_UPDATE_DISABLE=1`, so only one thing updates it. A
container of the stack built from source (`deploy/compose`, the development and CI stack) keeps Epic
28.3's answer (`409`, `docker compose pull && docker compose up -d`).

### Where each piece lives

| Piece | Code |
|---|---|
| The run: home prepare, gates (window, TV), drain, apply, commit, rollout, catch-up, admin actions | `api/internal/updates/driver.go`, `run_handlers.go`, `runs.go` (slot 0149: `update_runs.install_kind`, `detail`; `update_targets.name`, `kind`, `detail`, `updated_at`) |
| A machine's part: journal, prepare, apply, watch, commit, rollback, resume | `cmd/maktaba-server/internal/updater` (`machine.go`, `journal.go`), served on the supervisor's loopback (`/update/*`) and as `update.*` commands to a node's machine agent (`serve_update.go`) |
| Installers | `updater/layout.go`, `updater/runtime.go`, `updater/appbundle.go` |
| Health of the release that came up | `serve_update.go` (`machineHealth`: every role running, none crash-looping or halted by the database step, each pipeline process's `/healthz` saying the version of the release's wheel where it runs the release's own environment (story 35.4 AC7), the api's `/readyz` and its version, on a node the schema guard and the machine agent) |
| The database | `cmd/maktaba-server/internal/dbsafe` (dump checked with `pg_restore --list`, rehearsal, restore in place), the database step in `serve_appbundle.go` (now also `layoutHomeGates` for a plain server home), the copies' slots dropped before a dump is put back (`pgbundle.DropSlotsSQL`) |
| The launcher's guard for the app and the runtime copy | `cmd/maktaba-launcher/run.go` (`bundleGuard`, `bundleRollback`), `shared/go/layout/guard.go` (`pending.json`, `payload-override`, `MarkRecordRestoring`) |
| The order, its staging, its notice and its go (no click) | `cmd/maktaba-server/internal/updater/request.go`, `apps/desktop/src-tauri/src/autoinstall.rs`, `shell.rs` (`install_requested`, `stage_order`), `tray.rs` (`entries_with_order`) |
| The release's migrations | `release.yml` (`maktaba-migrations-<v>.tar.gz`), `release.json` `migrations`, `releaseverify/install.Stage` (beside the binaries), `api/migrate.go` (found beside `maktaba-api`) |
| The page | `web/src/pages/Admin/Updates.tsx` (`/admin/updates`) |

### No click: a desktop app installs by itself (owner decision D1, 2026-10-27)

**Decision.** A desktop app that runs the server — the home's, or a full node's: Maktaba.app,
the Windows app, the Linux app — installs what the home orders by itself, as headless and
plain server installs do. An update an admin starts from the Updates page runs at once, and
**the machines follow once the home commits**: the home goes first (stage, migrations,
restart, watch, commit), and only its commit sends the machines their orders, by themselves,
one machine at a time; an automatic one does the same inside the policy's window, with only a
notice on the machine beforehand. Nobody clicks anything on the machine.

**Machines follow once the home commits (the owner's decision, 2026-10-28).** No machine is
ordered a release before the home has committed it (its watch passed): while the home
prepares, restarts and watches, the run lists the machines as waiting for the home to finish
(`update_runs.detail.waiting_home`); its commit orders them with no click. A home that rolls
back or fails orders nothing: the machines stay on their release, with no order to withdraw.
The rule holds for an admin's **Update now**, an automatic window's run, and every
machines-only run (a catch-up, a Retry): each needs the watermark — the home's commit — at its
release, and a home that runs a release it has not committed (its watch going on after its
run gave up on it, `home_timeout`) raises no watermark until its supervisor reports the
commit. **Why:** on 2026-10-28 windows-pc took v0.3.42 while the home still watched it; the
home rolled back to v0.3.41 and put its database back to schema 167, which v0.3.42 cannot run
on, and updates only go up: the machine stayed paused until the home's next release. Machines
are not rolled back with the home (out of scope, by the owner's choice): they are simply never
ordered before its commit.

**A joined machine never goes ahead of its home.** Its only source of a newer server release is
the home's order, after the home commits. The home's `/api/cluster/self` says the release it
committed last (`committed`, the watermark) and a node passes it on (`home.committed`). The
desktop app installs nothing newer on a machine whose server is a node, whatever the source
(its own feed, a catch-up, an order): `autoinstall::past_home` refuses it as the attempt's
`past_home` failure before anything stops; when the home's committed release is not known,
only the home's order goes. `maktaba-server update` (and the Linux runtime's) refuses the same
on a joined machine (`update_homecap.go`). An app or server joined to no home updates as before.

**What it replaced.** The first D1 (2026-10-23, "Ask first") kept the apps' "restart to
install": the release was downloaded, verified and staged by itself, then installed only when
someone clicked "Install … and restart" in that machine's tray or app (the supervisor's
journal said `awaiting_click`, then `clicked`; the run's step `waiting_click`; the notice
`update.waiting_click`). That applied even when an admin had started the update from the
Updates page, and the page gave no sign of it beyond a muted line: on 2026-10-27 the owner's
v0.3.33 run (`37146c9d-…`) sat at "Wait for a click in Maktaba on this machine" for over an
hour with nothing telling him to go to the Mac app. **Why it changed:** a manual run is
already the admin's deliberate decision, made on the Updates page; a second click on the
machine was a hidden, redundant step. An automatic run is the policy's decision, and its
window is when the owner said restarts are fine; a notice says when it happens.

**What stays.** Every safety rule of the run: the release verified (its signed version, the
exact ordered version only, from its own feed in the pinned repository); the workers drained
before the home goes; the dump before the update's migrations; the schema guard; one machine
at a time; the rollback when the new server does not stay up; Abort and Cancel (and Skip)
withdraw an order before its go.

**The order** (`<launcher root>/updates/app-install.json`, the app's own state folder;
`updater/request.go`, `autoinstall.rs`): the supervisor writes `ordered` with the release's
own feed (`releases/download/v<v>/latest.json`: the channel, pin and skip rules hold), its
`kind` (`app`: the app's updater installs it; `runtime`: the server installs its own runtime,
D3), `home`, and for an automatic run `install_by` (the latest the go comes: the window's end
and overrun) with `install_by_text` (the same time on this machine's clock, for the app's
words). The app stages it (kind app: downloads its update from that feed, checks it with its
key, keeps it in `updates/app-update-<v>.bin`) and answers `staged`; it tells the person, in
the tray (an item that is no button) and once in an OS notification: "Maktaba will update to
<v> and restart by <time>" (an automatic run) or "… and restart shortly" (an admin's run).
The supervisor's journal says `ordered`, then `app_staged`, and waits for the home's `go`:
then it switches (kind app: `go`, and the app installs what it staged, checked again, saying
"Maktaba is updating to <v> now" once more in the tray and an OS notification; kind runtime:
the runtime switch). A restart takes the order up where it waited; a newer run replaces an
order before its go; Abort withdraws it (the app forgets what it staged); an order left
waiting while a newer release was installed by hand is withdrawn (`replaced_by_newer`), never
installed over it. A retry of a release the app already holds (its own update went through,
the new server did not stay up, the launcher runs the copy kept before it) is a show-only
order (`kind: server`): at the go the server starts the app's own payload again, under the
guard. The app stages and installs only the exact ordered version (its updater's version
comparator), only from that release's feed in the pinned repository, signed with the version
(`requireSignedVersion`), and re-reads the order right before it installs.

**On Windows the server agent installs it (owner, 2026-10-31).** An update must not wait for a
window: the Windows app's always-running `Maktaba.exe --server-agent` (the sign-in task's)
answers the order in the window app's place (`agent_orders.rs`, with the window app's own
decisions, `autoinstall::decide`). It stages the release from its own feed (a setup .exe on
that release's page, checked with the app's key; its signature must carry the ordered version,
as the updater's `requireSignedVersion`, and name that very file, since the feed itself is
unsigned), keeps it with its name and signature in `updates/`, and answers `staged`; at the
home's go it checks it again, answers `installing` and starts the installer (`/P /UPDATE`) out
of its own kill-on-close job. Its answers `staged` and `installing` go in only while the order
is still at the state it read (`autoinstall::advance`): a late answer never takes back the
server's go. On Windows the window app neither stages nor settles the orders' attempts. The installer stops the agent
(`--server-stop`), replaces the app and its payload and runs the sign-in task again
(`--server-start`); the new agent settles the attempt. The agent tells the server so
(`MAKTABA_AGENT_ORDERS=1`), and the server then sends no `maktaba://install-update` link: on
2026-10-31 windows-pc's window app was not running, the link reached a Windows where it was not
registered, and the order sat `ordered` for eight hours while Windows showed "Get an app to
open this link" every 90 seconds. The window app only shows the order. What an installer
started this way rests on (a sign-in task's child outlives the task's process, and the task
runs again meanwhile) is checked on CI's Windows runner (`check-task-children.ps1`).

**The run** (`driver.go`):

- **The home** is ordered as soon as it is prepared (the step `waiting_app` while its app
  stages it: nothing drained). Once it is staged (`app_staged`), the home goes as any home
  does: an admin's run at once; an automatic one keeps its window (past the window's end and
  overrun the order is withdrawn and the next window tries again, `outside_window`) and its TV
  gate (`waiting_tv`). Then the cluster's workers drain and the home's supervisor is told to
  go. The machines are ordered only once the home committed. The notice `update.app_installs`
  tells the admins (it replaced `update.waiting_click`).
- **A machine** is ordered as soon as it is prepared, and its app stages the release while the
  rollout goes on with the others (target `ordered`); staged (`app_staged`), it goes at its
  turn like any machine: one at a time, inside an automatic run's window, under the TV gate.
- Nothing waits for ever: a run left waiting only for apps to stage their orders stays open
  (step `waiting_app`; the Updates page names the machines), and a newer release takes its
  place (`superseded`: the orders withdrawn, the newer one ordered instead). An automatic
  run's orders not gone by the window's end are withdrawn and held (`window_over`). Cancel
  withdraws the home's order before its go, and stops a rollout; Skip withdraws one machine's.
- **What older releases wrote** goes on: a journal or an order file that says
  `awaiting_click` or `clicked` (and a step `ask`) is read as `ordered` and `app_staged` (and
  the step `order`), so a run left waiting (the owner's v0.3.33 run) proceeds without a click,
  or a newer release supersedes it; the run's step `waiting_click` and the targets
  `waiting_click` and `clicked` are read as `waiting_app`, `ordered` and `app_staged`. A
  machine whose supervisor is still from before reports `awaiting_click` until someone clicks
  there once: that machine's own update removes it.
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
2. **Apply: the backup on the side, then the 38.12 database step** (owner rule, 2026-10-27: an
   update never blocks the library for a backup). Right before its switch, the home's apply
   (`internal/updater/backup.go`, step `backup`) compares the staged release's migrations (the
   verified migrations archive of its `release.json`) with the newest migration the database
   holds (goose's table):
   - **none to run**: no backup (the step is `skipped`); the switch is a restart;
   - **some, or that cannot be told**: the running release dumps the database while it goes on
     serving (`pg_dump` reads one snapshot; `backups/pre-<old>-to-<new>.dump`, verified with
     `pg_restore --list`), the step's detail saying how many MB it wrote; only the switch waits
     for it. A dump that fails switches nothing (`apply_failed`, `db_dump_failed`). The dump is
     noted in the record (`next`, `next_dump`, `next_at`).

   Then, at the new release's start, before any role, the database step (now for every home; it
   ran only inside the apps): a release with no migration to run takes no dump, checks the
   database (`migrate up`, which applies nothing) and records it with `version` left at the
   release whose migrations the database holds and `release` the one that runs, so the release
   before it knows the database and may run on it again; a release with migrations takes the
   dump noted for it (fresh: within an hour), else (a release installed another way) dumps as
   before, then the migrations and the record `state/database.json`; a failed migration puts the
   dump back and the run is `rolled_back` with nothing changed. Before an
   update's migrations the other client sessions on the database are ended (the machines' api
   sessions; replication goes on), and no migration waits longer than 2 minutes for a lock
   another session holds (`maktaba-api migrate --lock-timeout`, lib/pq's `lock_timeout`): it
   is tried again, the sessions ended again, at most 3 times. The watch neither commits nor
   counts the release unhealthy while the step works (or the app's Python environment is
   built) and makes progress: the dump says its size every minute, the environment's install
   its share; a step that shows no progress for an hour (or three times the rehearsed
   migrations, when longer) rolls back (`start_timeout`). A migration's statement and a dump
   put back show none while they work (an index built for an hour on a large home), so only
   the cap bounds them (their lock waits are bounded by the lock timeout): 2 hours plus a
   quarter of an hour per GiB of the database plus three times the rehearsed migrations, past
   which any start step rolls back (`start_timeout`).
3. **Rolling back never dumps.** A release that added no migration, or whose migrations never
   ran, left a record its rollback (the watch's, or the launcher's guard) does not mark: the
   previous release starts at once, a restart. **After the migrations ran** (a check failed in
   the watch, or the launcher's guard fired): the record is set to `restoring` (failed = the new
   release, dump = the one above) before the previous release starts, and that release's
   database step puts the dump back before any role runs (code every release since 38.12 has),
   with nothing dumped first. What is lost: the writes between the dump and the rollback —
   minutes, inside the window, with every pipeline worker of the cluster drained from the
   home's apply until it commits — and the failed state, which is not kept (releases before
   this rule dumped it aside first, `backups/failed-<new>-<utc>.dump`, which kept the library
   down for as long as a whole dump takes).

   **An additive rollback keeps the schema (2026-10-28).** The outage of 2026-10-28 put the
   whole database back for one migration that added a row (0168), and that restore failed. The
   migrations are additive by rule (`shared/db/migrations/README.md` rules 9 and 10, `make
   compat-prev`): the release before runs on the schema the new one made. So the rollback (the
   new release's watch, `updater.Machine.rollback`) puts the dump back only when a migration
   since the release before is not rollback-safe (`updater/schemakeep.go`): its MANIFEST row says
   "Breaking for the previous release" or "Breaking for older nodes", or its Up section drops
   anything (a table, a column with or without the `COLUMN` keyword, a default, an index, a
   constraint, a view, a type…), renames, retypes (with or without `COLUMN`), sets NOT NULL,
   adds a constraint (CHECK, UNIQUE, a key) or a unique index, adds a NOT NULL column with no
   default or a column with a constraint, or truncates. Only two drops are not destructive by
   themselves: `DROP NOT NULL`, and a trigger or a function dropped and made again in the same
   file (the same trigger on the same table; the same function with exactly the same
   parameter list: another signature is another function). Anything uncertain counts as
   destructive (review of #433: the cost is only a restore); a DROP in the Down section or in
   a comment is not read. By that rule 0168 keeps the schema and 0169 (its CHECK) does not.
   Otherwise the record stays `migrated` with `kept_for` =
   the release before (`layout.MarkRecordKept`), nothing is put back and nothing is lost: that
   release's database step accepts the newer schema for itself (no `db_refused_older`, no
   migration to run, `kept_for` carried until another release migrates) and tells its api so
   (`MAKTABA_SCHEMA_AHEAD_OK=1`: `migrate up` and `serve` accept a schema ahead of their own).
   The release before must say it does this (its migrations' `meta/schema-keep.json`, protocol
   1, shipped from this release on, in the app payloads and the runtime copies); a rollback to a
   release without it, a plain server's layout (its migrations archive carries no `meta/`), and
   the launcher's own guard (the new release is not up to judge) put the dump back as before.
   Installing the newer release again finds its migrations there: a restart, no dump.
   `kept_for` follows the schema: a start with no migration keeps it; an update that migrates
   notes it on its dump (`dump_kept_for`), and a restore of that dump says it again
   (`kept_for` of the `restored` record, which the database step accepts as it accepts
   `migrated`): the release a schema was kept for starts on it after a later rollback too.

   **A restore that is needed is whole, or says what it lacks (2026-10-28).** The outage's
   restore failed part way (`tuple concurrently updated`, then "a worker process died"): a joined
   machine kept logging in to the home's database while it was put back, and as soon as its
   functions were back asked for its copy (`cluster_node_replica_grant` →
   `cluster_copy_publication`), whose `GRANT SELECT ON <every table> TO maktaba_replicas` and
   `ALTER PUBLICATION … ADD TABLE` met pg_restore's own grants and trigger builds on the same
   `pg_class` rows (reproduced on a scratch cluster: five tries out of five). It left every
   table's rows but no vectors, none of the 630 post-data items and none of the 286 grants, and
   the database's own `GRANT CONNECT … TO maktaba_worker` (a dump never holds a database's
   grants) was gone too. And 0.3.41's own code did that restore: the rollback marked the record
   and the release before put the dump back at its start.

   Now **the new release puts the dump back itself, before it switches back** (review of #433:
   `updater.Machine.RestoreBack`, after the TV change is taken back): the record marked
   `restoring` first, the roles that use the database stopped (the api, the pipeline, the
   streaming role; the TV slots run on, on the database being replaced), the dump put back
   with this release's restore, and the record `restored` for the release before, which then
   starts on a finished database (a release from before 2026-10-28 reads `restored` as it
   always did). A restore that fails there leaves the record `restoring` and the switch back
   goes on: the release before puts it back at its start, as before. Putting a dump back into
   the bundled Postgres (`pgbundle.restoreInto`): into a database of its own
   (`maktaba_restoring`, `REVOKE ALL … FROM PUBLIC`), whose name no client knows, so nothing
   but the restore reaches it — not a machine's login, nor a process that runs on across the
   restart as the database's owner (the TV slots handed over), which taking CONNECT from PUBLIC
   never stopped; whatever they write meanwhile goes to the database being replaced, as before
   the rollback. Once whole and checked it is renamed into place in one transaction with the
   grants of the database it replaces (read from it then; the machines' CONNECT), which is
   renamed `maktaba_replaced` in that transaction (closed to new sessions, its sessions and its
   copies' slots ended first) and dropped after. A restore that fails leaves the app database
   as it was (the scratch database dropped); one with no room for both drops the old one first
   (logged). It restores pre-data with one worker, the rows in parallel, the chunks in parallel, post-data in
   parallel, then the grants and default privileges alone, last, with one worker (every object
   they name is there); checks the result against the dump (`pgbundle.VerifyRestore`: every
   table, view, sequence, index, constraint, trigger and function of `public`, every grant
   (table, column, function, schema) and default privilege, and every table's rows against the
   dump's `counts.json`, counted in the dump's own snapshot while it is taken; a dump from
   before it: the chunked tables at least hold rows when their chunks do; a `counts.json` there
   but damaged refuses the dump before anything changes); runs `ANALYZE`; and only then renames
   it into place. A parallel try that fails or comes out
   incomplete is done again whole with one worker; one that fails again halts with what failed
   or exactly what is missing (`the restored database is not whole: missing 3 indexes (…); 1
   tables without all their rows (public.segment_embeddings: 0 of 17000000 rows)`), the record
   still `restoring`, and the record is set `restored` only after the check. The in-place
   restore of another Postgres (`dbsafe.Target.Restore`, one transaction: nothing outside it
   sees the restored objects before its commit) is checked the same way. Every start that finds
   the record `migrated` or `restored` gives the workers' group its CONNECT back when the
   database lacks it (`pgbundle.GrantWorkersConnectSQL`: a restore of a release from before
   dropped it with the database).

   **A halt has a way out (2026-10-28).** The database step's halts (`db_restore_failed`,
   `db_restored`, `db_refused_older`, `db_ahead`, …) last until the database's record changes
   to `migrated` or `restored` (`supervisor.HaltUntil`, looked at every 5 s): put right by hand
   (as the owner did: the record set `restored`), the step runs again and every role that
   waits on it starts, with no restart of the server. A restore a person asks for (`restoring`
   with `by`) is not a way out: `maktaba-server restore` restarts the server for it.
4. **The dumps' format** (`pgbundle/dumpfmt.go`). Every dump is a directory-format dump
   (`pg_dump -Fd`, still named `*.dump`): one file per table, written by `DumpJobs` workers (half
   the cores, 2 to 8) in one snapshot and put back by as many `pg_restore` workers; each file
   compressed with `zstd:1`, else `lz4`, else `gzip:1` (else `-Z 1` for a `pg_dump` older than
   16), the first the `pg_dump` at hand takes (the bundled one has zlib only). A worker locks
   its table with NOWAIT: one locked out (a session asked for its table whole while the server
   served) begins the dump again, twice at most. Every dump is whole (owner, 2026-10-27: a
   backup that left the vectors out would cost days of embedding after a restore). The
   tables of `pgbundle.Chunked` are written apart: `segment_embeddings`, semantic search's
   vectors (31 of 47 GB on the home that measured it), was the bulk of a 35 GB dump that one
   pg_dump worker wrote as text for ten minutes (formatting each vector's 384 numbers is the
   cost, not the compression). A session of the dump's role exports a snapshot and stays open;
   `pg_dump --snapshot` leaves those rows out, and `DumpJobs` psql sessions set the same
   snapshot and each COPY one range of the key out in binary
   (`chunk.public.segment_embeddings.<n>.bin` in the dump's folder, which pg_restore does not
   read). Putting a dump back runs pre-data and data, then loads the chunks as many at once
   (`pgbundle.LoadChunks`; one `\copy` each in the in-place restore's single transaction),
   then post-data, so the indexes are built once over the loaded rows. Without a snapshot
   session (no psql) pg_dump writes every table itself, slower but whole.
   Measured on a scratch copy shaped like that home at full scale (24 GB: its tables, and 17M
   distinct halfvec(384) vectors; the app's PostgreSQL 16, gzip:1, 8 workers): the single-file
   dump of before took 619 s for 35.8 GB (the home's own log said ten minutes for 35 GB); this
   one took 25 s for 17.2 GB, the vectors' chunks written beside the other tables. Putting it
   back took 5 min 21 s (tables and data 91 s, the vectors 185 s, indexes 45 s; the home's
   HNSW index comes on top of that).
   The
   folder's rules (`MakeRoom`, `PruneBackups`, the Backups page, moving the backups, a restore
   by hand) take a dump's folder as they take a file, by its files' size; the single-file dumps
   of before are read, restored, moved and pruned as they were; a dump a crash cut short
   (`*.dump.partial`) goes once nothing was written to it for an hour.
5. **The machines' copies (Epic 37) across it.** A machine's copy follows only a home whose
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
6. **A restore by hand** (story 35.5 AC8): the Updates page lists the home's backups
   (`/api/cluster/updates/backups`) and puts one back after its name is typed;
   `maktaba-server restore --backup <file> --confirm` does the same. The home's supervisor
   reads the dump's schema, picks the kept release of that schema (`updater.Keeper`: a plain
   server's releases, the runtime copies, the app's payload and its kept copies), marks the
   record `restoring` with the dump and who asked (`by`), points the install at that release
   and restarts (`serve_restore.go`); that release's database step puts the dump back before
   any role, the database it replaces set aside (`replaced-<version>-<utc>.dump`).

### What the run guards against

- **A home that never finishes**: from its switch, the home has 6 hours to commit or roll back
  (or, when its own limits allow longer: its start steps' cap, from its database's size, and
  its watch's bound, `bound_sec` in its status, those and an hour more); then the
  run ends (`home_timeout`), the cluster is undrained and a person told; a home that
  commits after that raises the watermark all the same (the driver asks it), so the machines
  catch up to its release. A watch has a bound of its own (3 × the watch + the health limit,
  outside the start steps): a release whose health keeps flapping is rolled back
  (`watch_timeout`); the start steps have theirs ("The database" step 2, `start_timeout`).
  The watch judges the home's own health only (2026-10-28): a TV slot starting, stopped or
  between two starts (a staging slot starting, a slot a swap stops) is never the home
  unhealthy, but a slot that crash-loops or cannot start is the live TV off the air: fatal,
  unless the release's TV change is under way (its staging slot runs the new binary: its check
  failing holds the TV, its swap failing rolls back; the TV step's). A release that stayed healthy for its
  whole watch has passed: it commits at once, whatever its TV change waits for (the TV step
  ends `pending`, "The TV step" item 3), and no later check undoes the pass. The bound never
  counts the TV's waiting: a healthy check never times the watch out.
  For the whole watch the launcher's guard is kept 15 minutes ahead, every minute: it catches
  only a supervisor that died or hangs, never a watch still going.
- **A restart asked by hand** (`<launcher root>/updates/restart`, e.g. after writing
  `updates/payload-override` to run a kept payload): every serve under a launcher takes it and
  exits 75 for its launcher, which starts the payload (or release) it runs next. Until
  2026-10-28 only a plain server's layout watched the file (the API wrote it after a
  self-update); an app's or a runtime copy's serve, which has no release directory, never took
  it, and a restart written by hand during the outage did nothing.
- **A switch of the home** (Epic 37): no run starts while one is open, and no switch starts
  while a run is past its plan (`update_under_way`); a home already drained waits to switch. A
  run that only waits for desktop apps to stage their orders (step `waiting_app`, or an older
  run's `waiting_click`: nothing drained, nothing switching) lets a switch through:
  when it starts, the run's orders are withdrawn and the run ends (`switchover`); the new
  home's next run orders them again. No machine restarts while a switch is open.
- **No room**: the preflight wants room for the release (2 GiB; 8 GiB with a plain server's
  pipeline environment), and on the home for the dump and the rehearsal's scratch copy, with
  1 GiB to spare (`disk_space`).
- **A withdrawn release** (deleted upstream, turned prerelease on stable, re-published with
  other files): the run re-reads its release from GitHub every hour, and right before each go
  (the home's, each machine's) and each catch-up (GitHub out of reach is no withdrawal). Before
  the home switches the run ends (a waiting order withdrawn); a rollout orders no more
  machines; a catch-up or a retry never spreads it.
- **A machine's install, not the release** (`not_self_managed`, `old_launcher`, `no_app_update`,
  `launcher_protocol`, `disk_space`, `no_python`, `venv_network`, `venv_python_unsupported`,
  `needs_admin`, an agent from
  before automatic updates or one whose server does not update itself: "does not run
  update.prepare"): the machine needs a person (`needs_person`, a notice), node_failure_policy
  holds nobody for it, and on the home it counts no attempt (mended, the next window goes).
- **A run for named machines** (a Retry, a Resume, a catch-up) touches only those; the others
  wait for a catch-up of their own, inside the window.
- **A machine that comes back**: a rollout takes back a machine that was offline or joined since
  (as a catch-up would, inside the window); a catch-up never re-applies a release that failed or
  was rolled back on a machine (by itself or an admin), nor one an admin skipped.
- **A kept copy on Windows**: the launcher puts it back into the app's own folder before it runs
  (the firewall's rules name each program by path); once the app began installing (or holds
  another release), a revert names the whole kept copy (an install cut short may have replaced
  the programs before payload.json), while an app that never began keeps running its own
  untouched payload; a second switch back that does not take fails (`rollback_loop`) instead of
  looping.
- **A desktop app updated over an older one**: the Windows installer's update (`/UPDATE`)
  deletes nothing, so its pre-install hook removes the old pipeline wheel; the server and the
  pipeline's bootstrap take the wheel `payload.json` names either way.
- **Story 35.7 AC6, the launcher**: a release whose `launcher_protocol` is above the running
  launcher's (`MAKTABA_LAUNCHER_PROTOCOL`) puts its launcher in place after its commit
  (`--replace-self`; a runtime's is the new runtime's) and the running one hands over at the
  next exit 75 (`updates/replace-launcher`, re-executed in place): at once, unless the machine
  keeps TV on air (its tv-slot processes run), then at its next restart (the next run's, under
  the TV gate). The launcher it replaced (`.old`) stays until the new one's first good start
  (its server up a minute, nothing pending). One this install cannot replace (a package's, a
  Windows service's) refuses the release before it installs, naming the commands that do it by
  hand.
- **Package-managed installs** (`.deb`/`.rpm` of the server, from a release or the apt/dnf
  repositories once they are set up, and the Homebrew formula; the Linux app stays out of the
  repositories, Epic 39): the package owns the launcher, the updater owns the releases
  (`bin/releases/<v>` under `/var/lib/maktaba`, or brew's `var/maktaba/server`), so they update
  by themselves like any plain server. The package manager brings a new launcher (`apt upgrade`,
  `dnf upgrade`, `brew upgrade`) and its release only when that is newer than what the updater
  installed; the repositories and the tap follow each stable release by themselves
  (`packages.yml`, after the release is published). A release that needs a newer launcher than
  the package's refuses before it installs, naming that command (`launcher_protocol`).
- **The pipeline's environment on a plain server**: built with the extras of the environment it
  replaces (its bootstrap's `maktaba-venv.json`, else the extras whose requirements it has
  installed; `dev` never), so a worker given the STT extras keeps them; packages added by hand
  outside the extras are not carried (`MAKTABA_PYTHON` still wins over the release's
  environment). Packages come from the home's wheelhouse on a node (story 35.12: the home
  downloads each release's pins once per platform its machines run; pip installs from it alone,
  then PyPI for a package it lacks), else from PyPI (or `PIP_INDEX_URL`, a proxy): a machine
  that can reach neither needs a person (`venv_network`); one whose Python is not the one the release's
  pins are made for (`pipeline.python` in `release.json`), where pip finds no build of a
  pinned package, needs a person too (`venv_python_unsupported`, naming both versions).

### Nodes

Nodes never migrate the home's database (their own copy of it, Epic 37, is migrated by their
new release; "The database" step 4). Apply = switch, restart, watch (schema guard fits, api
`/readyz` with the new version, the machine agent back on the channel), commit; a failed check
switches back (binaries only). A node is never ordered to a version the home has not committed, and a node
that comes back behind the watermark gets a `catch_up` run of its own.

## Proven in CI: the update rehearsal (2026-10-28)

The owner, after the two outages of 2026-10-28: "THIS NEED TO BE ON CI". Every path of
this page that ran only on the owner's Mac runs in CI before any release:
`.github/workflows/update-rehearsal.yml` (verdict job `update-rehearsal`;
[docs/update-rehearsal.md](../../../docs/update-rehearsal.md)). The Mac app's install kind is
driven on Linux through the same code (the bundle's layout under a `Maktaba.app` path, the
launcher run from it, its state root, `payload-override`, `state/database.json`, the app's
order played by the test, the bundled Postgres of a published Linux payload), from the latest
published release, built from its tag, to the commit. Its scenarios: a normal update with
migrations on a seeded home (150,000 videos, 2 million vectors); a TV change that waits
through the watch; rollbacks after additive (schema kept) and destructive migrations (the
restore's safe path, checked whole against the database before); that restore while machines
log in and ask for their copy, and killed (`kill -9`) part way; halted roles starting by
themselves once the record is right; no room for two copies; the owner's Mac's state of that
evening; a joined machine never updating before its home commits. Each reports its downtime
against a budget. The tag guard refuses a tag on a commit the rehearsal did not pass on.

## What still needs a person

- Nothing on a desktop app that runs the server (owner decision D1, 2026-10-27): it installs
  what the home orders by itself. Only a machine whose supervisor is from before that decision
  waits for one last click there (its "Install … and restart"); the release it installs then
  updates it without one.
- **Installs from before this release** have automatic installs compiled off (and, in the apps,
  discovery off): they take this release once by the way they update today (the app's "Install
  update", `install.sh`, `maktaba-server update`); every release after it comes by itself.
- A Mac app in a folder this user cannot change without an admin password (`/Applications` for
  a standard user): an ordered install never prompts (the updater's prompt and its replacing of
  the app are one step, and a password typed after the server stopped waiting would install a
  release no run watches); the app reports `needs_admin` before anything changes (needs a
  person) — move the app to a folder the user can change, or have an administrator install it.
- A flat install (binaries copied by hand, no launcher): one `maktaba-server setup --service`.
- A Linux app installed as a `.deb`/`.rpm`: its server's runtime switches at the home's go (no
  root needed, D3); the app's own window and tray update with the package manager or the app's
  "Install update".
- A release whose migrations break the previous release (`breaks_previous_home`): held for an
  admin's approval (unchanged).
- A failed run blocks that version; the next release installs by itself, or an admin retries.
- A machine that needs a person (`needs_person` on the Updates page, with the fix): a plain
  server without a Python 3.12 for its pipeline (`no_python`: install it, or name one in
  `MAKTABA_PIPELINE_PYTHON`), or with another Python than the release's pins are made for
  (`venv_python_unsupported`: install that one), or without PyPI (`venv_network`: give it access, a mirror in
  `PIP_INDEX_URL`, or a proxy), no room (`disk_space`), a launcher a package manager or a
  Windows service owns when a release needs a newer one (`launcher_protocol`), a Mac app that
  needs an administrator (`needs_admin`), the cases above.

## TV handover (story 35.7)

- **The record.** The supervisor of a plain server install keeps `updates/handover.json` (in the
  launcher's install root): every TV process that runs (the streaming role, the tv-slot
  instances), with its pid, the OS's start time of that pid (`/proc/<pid>/stat`, `kern.proc.pid`,
  `GetProcessTimes`: a reused pid is never taken for it), its process group (POSIX) or named job
  (Windows), its probe, its program and release, and a slot's TV release binding. It is rewritten
  whenever one starts or ends, and removed when every process stopped.
- **The restart.** An update's exit 75 onto the new release (or back, on a rollback) is armed to
  hand over when the TV processes can run on across it and the release it goes to adopts them
  (its signed `release.json` says `handover_protocol`, from `shared/go/layout.HandoverProtocol`;
  a release from before says none, and gets a whole restart under the TV gate): every other
  process stops as at a shutdown, the record is marked `handover` by that supervisor on this
  boot, and serve exits 75 without signalling them. The run's log says which it was, and why not
  when it was not. A machine whose prepared report said `hands_over` but that cannot hand over
  at the apply refuses it while the TV is watched (`tv_busy`): the run's TV gate then holds it.
- **The launcher** (it says `MAKTABA_LAUNCHER_HANDOVER=1` to the server) leaves what such a record
  lists running at that exit 75 (Linux: those process groups are kept out of the orphans it stops;
  Windows: it keeps the kill-on-close job it runs every server in, held for its whole life, across
  that restart). So it does when the release under trial ends any other way (a crash, the guard's
  timeout) and both releases the next start may run adopt: that start (the release again, or the
  one the guard goes back to) adopts them. On every other exit, and at a stop, it stops what the
  record lists (POSIX: the process groups, each only while its leader is still that process;
  Windows: the job) and removes the record. A launcher from before the handover is replaced by
  the committed release's on POSIX where that happens by itself (a full restart, at once only
  while no TV process runs, else at the next restart, which the run's TV gate holds for a quiet
  TV); Windows keeps any launcher.
- **Windows under a launcher from before the handover**: the TV processes leave the launcher's job
  (`CREATE_BREAKAWAY_FROM_JOB`; the launcher's job has always allowed it) for the boot's named job
  `Local\Maktaba-tv-<install>` (the install's hash: never another install's, nor a test's; a
  named object ends with its last handle, so no boot's outlives it), whose handle the supervisor
  duplicates into the launcher, so it outlives the supervisor and dies with the launcher. Where
  the job the server runs in lets no child leave it, that process ends with the supervisor and
  every restart is whole (AC4's degradation). Each TV process's own named job (what ends it with
  its ffmpeg) is duplicated into the launcher too, so it and its name outlive the supervisor
  that made it; whoever ends that process closes it. A job nobody kept is made anew at the
  adoption around the process and its descendants (by their parents).
- **Adoption.** The next supervisor adopts each listed process still the same process (pid and
  start time) whose probe answers, and supervises it by probe and by polling; one that ends, or
  must restart, is replaced by an ordinary child; one that cannot be adopted is started afresh
  (`supervisor_adopt_failed`). A record of an earlier boot is removed. On Windows what the boot's
  job holds beyond the adopted processes and their descendants is ended. A supervisor that stops
  before it is ready leaves the record of what it never adopted (the launcher stops it, or the
  next start adopts it).
- **What stays.** The front door runs on only to a release that speaks its `edge_protocol` (the
  releases' `release.json`); a release a running TV process uses is never pruned. The desktop apps
  (their own installers) and a flat install restart whole, under the TV gate.

## The TV step (story 35.6)

The design puts the TV step before the restart (§5.3 step 10), after `migrate`; here every home
migrates at its new release's start, so the step runs in the **new release's** supervisor, during
its watch, where the new streaming binary never meets an older schema:

1. Once the new release is healthy, its supervisor asks its own API (`POST
   /internal/updates/tv-change`, its per-boot local token) for the run's change: the live TV
   release with `streaming = {version, sha256}` of this release's binary (its `.verified`). One
   change per run: asking again returns it (a supervisor that restarts mid-swap waits for it).
2. The change is its own: no save joins it and it joins no one's open change (story 33.12 AC3
   does not apply to it). While another change is in flight it waits as a draft (README R9.4); the
   controller stages it when its turn comes. Rule: `immediate` only for source `auto_update` with
   kinds exactly `[binary]` (a binary change by hand, or one with another kind, keeps its rules).
3. The release commits once it was healthy for its watch. Its change is then live (or there was
   nothing to swap: TV staging off, no TV release yet, the binary live already), held (item 4),
   or still on its way: **pending** (2026-10-28). The outage of 2026-10-28: 0.3.42's change #57
   passed every check but sat in `ready` for half an hour waiting for an approval (the old
   `auto_max_channels` rule), the watch's bound ran on meanwhile, the approval's swap stopped a
   slot and a transcription worker failed its probes once, and the watch rolled the release back
   (`watch_timeout`), its restore failing after. Now the TV never holds the release: a change
   that waits (for an approval, a quiet window, behind another change, a slow staging, an API
   that does not answer) leaves the release committed with the TV step `pending` and the
   journal's `tv.outcome = pending`; the change is not taken back and goes on under the TV
   change controller by itself (an admin approves it, its window comes). The run is `done`
   with `update_runs.detail.tv = {outcome: pending, change_id, number}` (audited `tv_pending`;
   no notice, nothing blocked or counted; the Updates page says the change goes on by itself,
   with Drop); the release the live TV runs is kept from pruning meanwhile. The home's health
   leaves the TV slots to the TV step (a staging slot starting is not the home starting).
   A change that could not be had at all (the API never answered: no change behind the step)
   has nothing to go on by itself: the TV step ends **held** instead (review of #433), with
   the check `no_change` and the binary the change would carry (`streaming_version`,
   `streaming_sha256`); the Updates page's **Retry** makes the change with them (then it is
   staged and checked as any), and **Drop** leaves the TV on its software with nothing to
   cancel.
4. **The owner's decision (2026-10-28):** *"when only the TV test fails, keep the update
   installed, leave TV on the old version, and notify me with Retry / Drop. Undo everything only
   if TV breaks after it has switched."*
   - **The preview fails** (the change's verification in the staging slot fails, before any swap):
     the TV step ends `held` and the release commits. The new server, web and pipeline stay
     installed; the run ends as succeeded (`done`) with `update_runs.detail.tv = {outcome: held,
     change_id, number, held_on, failed}`; the live slots stay on their release and the streaming
     software they ran (`held_on`); the change stays a draft with its verify report; the version is
     not blocked and no attempt counts. The admin is told (`update.tv_held`, warn) and the Updates
     page offers **Retry** (`POST /api/cluster/updates/runs/{id}/tv/retry`: the change is staged and
     verified again; one that passes swaps at a programme boundary under its rule, `immediate`, as
     usual; one that fails is held again) and **Drop** (`…/tv/drop`: the change is cancelled,
     `end_reason = dropped`; the TV stays on the old streaming software until a later release).
     Both are admin-only, enforced by the API. A later release that commits replaces a held change
     (`end_reason = replaced`): a Retry never airs older software. A retry that passes and then
     fails on the air is an Epic 33.9 rollback of the TV alone: the run committed long before.
   - **Full rollback stays only for** a failure of the swap itself, and the live TV failing after
     it switched (Epic 33.9 rolled it back): `failed_tv` (the release blocked, the admin told).
   - `tv_change_cancelled` (an admin cancelled it: blocked until a retry) rolls back as before.
     `tv_change_pending` (behind a change awaiting approval past the window's end) and
     `tv_timeout` (not on the air within `boundary_max_wait_minutes` + 10 of its stage, or the API
     silent for 10 minutes) no longer roll back (2026-10-28): they only say why the TV is
     pending at the commit (item 3). The API still reads them from a home from before.
5. A rollback for any reason takes the change back first: cancelled before the air (a held one
   too), rolled back (Epic 33.9, warm while the previous slot is warm, else cold) once live, and
   rolled back by the controller when a swap under way ends.

`keep_versions` pruning keeps every release a running process uses (a front door kept on its
release, a slot bound to another's binary, bound while it is between two starts too), and the
release whose streaming software the live TV ran at the TV step's last answer (the journal's
`tv.live_streaming`: a TV held there). A desktop app keeps that release's payload copy in
`updates/payloads` past later commits, and a runtime copy its runtime directory; a slot bound to
that release finds its binary there when `bin/releases/<v>` has none (`supervisor.WithKeptStreaming`).
A slot restarted with the server (every desktop app's update, and a plain server whose slots
were not handed over) starts on the streaming software the live TV ran, not the new release's
(`supervisor/held.go`, fixed 2026-10-28 after 0.3.37 → 0.3.39 put the owner's live TV on 0.3.39
before its check ran): at the slots' first start the supervisor reads the live slot's own
report (`tv_slots.version`), and while it is an older release than the one starting, TV
staging is on and the live TV release does not record the new release's streaming, every slot
the front door has not bound to a release runs that older release's binary. It stays on it
across every restart (a held change, a Drop) until a binary change passes its check and swaps
the slots: the update's Retry, or a later release's own binary change. A change made
meanwhile that brings no binary of its own (schedule, settings, lineup, art) never moves it
(fixed 2026-10-28: 0.3.40's pipeline recorded its own `0.3.40` on every schedule release while
the TV was held on v0.3.39, so its staging ran, and its swap would have aired, the software
whose check had just failed). The pipeline records on such a release the streaming software
the live TV runs (`tvrelease.snapshot.live_streaming`, read as the API's `liveStreaming` and
`HeldStreamingVersion` read it: the live slot's own report, else the live release's version
when it names a binary, else the pipeline's), with the checksum a release recorded for that
version; its staging slot and its swap bind to that release, so they run the held binary from
where the install keeps it, like the live slots. A release that names no binary (none, or a
source checkout's placeholder `0.0.1`, as the owner's live release from 2026-10-22) is bound
with no version, and a slot so bound runs what an unbound slot runs: the held binary while the
TV is held, else the release's own. The supervisor refuses at once a binding whose binary is
nowhere on the machine (409, logged at error and as `tv_held_binary_missing`): the change's
staging fails with that reason ("the staging slot did not start: … not on this machine")
instead of running other software. A staging off, no TV release, or a launcher's fallback to
an older release starts the release's own binary; a held release whose binary is nowhere at
the slots' first start is logged at error (`tv_held_binary_missing`) and the unbound slots run
the new one. `maktaba-streaming --version --json` says the front
door's `edge_protocol`, and the journal keeps the release's: the front door restarts only when they
differ (`updater.FrontDoorRestarts`, the rule the TV handover asks before it lets the front door
run on across the restart, story 35.7).
