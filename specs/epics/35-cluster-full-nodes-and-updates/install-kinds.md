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
| The app's updater is the only updater inside an app; the server's own is off (Epic 36 contract-update). | The server decides **what** and the order of the machines (policy, home first, one node at a time); a desktop app that runs the server stages what the home orders and installs it **when someone clicks there** (owner decision D1, "Ask first" below); the app's updater still does the swap for Mac and Windows. Headless and plain server installs update by themselves in the window. |
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
| **Maktaba.app** (Mac, Epic 36) | `MAKTABA_APP_BUNDLE` inside `*.app/Contents/Resources/server` | verify `release.json`; check the app feed (`latest.json`) of that release lists this platform; keep a copy of the running payload (`<state>/updates/payloads/<v>`, an APFS clone); rehearse (home) | **order** the release from the app (`<state>/updates/app-install.json`; start it with `maktaba://install-update` when it does not answer): it downloads and checks its update and shows it; **after a click there and the home's go**, the app's updater swaps the bundle (from what it staged, checked again) and restarts the server | `<state>/updates/payload-override` names the kept payload; the launcher runs it |
| **Windows app** (Epic 38) | `MAKTABA_APP_BUNDLE` in `<install>\server` beside `Maktaba.exe` | same (a plain copy) | same; the NSIS installer's hooks stop and start the server | same |
| **Linux app** (Epic 39; `MAKTABA_RUNTIME_OWNER=app`) | the launcher runs from a runtime copy | download the release's `maktaba-linux-payload-<v>-<arch>.tar.gz`, verified against the signed `checksums.txt`, installed as a new runtime directory; rehearse (home) | order it from the app (nothing for it to fetch: it shows it); after a click there and the home's go, `runtime/current` → the new directory, `previous` → the old; exit 75 (the launcher starts `runtime/current` again) | `current` → `previous`; exit 75 |
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
replaces an order still waiting; Abort withdraws it (the app forgets what it staged). A retry of
a release the app already holds (its own update went through, the new server did not stay up,
the launcher runs the copy kept before it) is a show-only order (`kind: server`): at the go the
server starts the app's own payload again, under the guard. The app stages and installs only
the exact ordered version (its updater's version comparator), only from that release's feed in
the pinned repository, signed with the version (`requireSignedVersion`), re-reads the order
right before it installs, and shows one OS notification per order.

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
   migration puts the dump back and the run is `rolled_back` with nothing changed. Before an
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
5. **A restore by hand** (story 35.5 AC8): the Updates page lists the home's backups
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
  For the whole watch the launcher's guard is kept 15 minutes ahead, every minute: it catches
  only a supervisor that died or hangs, never a watch still going.
- **A switch of the home** (Epic 37): no run starts while one is open, and no switch starts
  while a run is past its plan (`update_under_way`); a home already drained waits to switch. A
  run that only waits for clicks (nothing drained, nothing switching) lets a switch through:
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

## What still needs a person

- **A click on each desktop app that runs the server** (owner decision D1): the home's first,
  then each machine's; the release is staged and waiting. The rollout goes on with the others
  meanwhile, and a newer release replaces an order nobody clicked.
- **Installs from before this release** have automatic installs compiled off (and, in the apps,
  discovery off): they take this release once by the way they update today (the app's "Install
  update", `install.sh`, `maktaba-server update`); every release after it comes by itself (in
  a desktop app, staged and waiting for its click).
- A Mac app in a folder this user cannot change without an admin password (`/Applications` for
  a standard user): an ordered install never prompts (the updater's prompt and its replacing of
  the app are one step, and a password typed after the server stopped waiting would install a
  release no run watches); the app reports `needs_admin` before anything changes (needs a
  person) — move the app to a folder the user can change, or have an administrator install it.
- A flat install (binaries copied by hand, no launcher): one `maktaba-server setup --service`.
- A Linux app installed as a `.deb`/`.rpm`: its server's runtime switches on the click (no root
  needed, D3); the app's own window and tray update with the package manager or the app's
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
  hand over when the TV processes can run on across it: every other process stops as at a
  shutdown, the record is marked `handover` by that supervisor on this boot, and serve exits 75
  without signalling them. The run's log says which it was, and why not when it was not.
- **The launcher** (it says `MAKTABA_LAUNCHER_HANDOVER=1` to the server) leaves what such a record
  lists running at that exit 75 (Linux: those process groups are kept out of the orphans it stops;
  Windows: it keeps the kill-on-close job it runs every server in, held for its whole life, across
  that restart). On every other exit, and at a stop, it stops what the record lists (POSIX: the
  process groups, each only while its leader is still that process; Windows: the job) and removes
  the record. A launcher from before the handover is replaced by the committed release's on POSIX
  where that happens by itself (the same full restart, in the window and under the TV gate, as a
  `launcher_protocol` change); Windows keeps any launcher.
- **Windows under a launcher from before the handover**: the TV processes leave the launcher's job
  (`CREATE_BREAKAWAY_FROM_JOB`; the launcher's job has always allowed it) for the boot's named job
  `Local\Maktaba-tv-<boot id>-<install>` (the install's hash: never another install's, nor a
  test's), whose handle the supervisor duplicates into the launcher, so it outlives the
  supervisor and dies with the launcher. Where the job the server runs in lets no child leave it,
  that process ends with the supervisor and every restart is whole (AC4's degradation).
- **Adoption.** The next supervisor adopts each listed process still the same process (pid and
  start time) whose probe answers, and supervises it by probe and by polling; one that ends, or
  must restart, is replaced by an ordinary child; one that cannot be adopted is started afresh
  (`supervisor_adopt_failed`). A record of an earlier boot is removed. On Windows what the boot's
  job holds beyond the adopted processes is ended.
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
3. The release commits once it was healthy for its watch **and** its change is live (or there was
   nothing to swap: TV staging off, no TV release yet, the binary live already).
4. Failures roll the release back: `failed_tv` (its verification or swap failed, or it was rolled
   back: the release blocked, the admin told with the checks that failed), `tv_change_pending`
   (still behind a change awaiting approval at the window's end and overrun, which the apply
   passes: a gate like `tv_busy`, no attempt counted), `tv_timeout` (not on the air within
   `boundary_max_wait_minutes` + 10 of its stage: an attempt), `tv_change_cancelled` (an admin
   cancelled it: blocked until a retry).
5. A rollback for any reason takes the change back first: cancelled before the air, rolled back
   (Epic 33.9, warm while the previous slot is warm, else cold) once live, and rolled back by the
   controller when a swap under way ends.

`keep_versions` pruning keeps every release a running process uses (a front door kept on its
release, a slot bound to another's binary). `maktaba-streaming --version --json` says the front
door's `edge_protocol`, and the journal keeps the release's: the front door restarts only when they
differ (`updater.FrontDoorRestarts`, the rule the TV handover asks before it lets the front door
run on across the restart, story 35.7).
