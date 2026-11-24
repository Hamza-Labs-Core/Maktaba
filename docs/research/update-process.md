# Research: a separate update process for Maktaba

Status: research, 2026-11-24. Nothing here is built yet. It covers:
- every issue found in today's updater;
- how other software updates itself, from primary sources;
- a recommended design for Maktaba, a migration path for existing homes, and the changes ranked
  by impact.

What the updater does today is in [`docs/updates.md`](../updates.md); the design it was built
from is [Epic 35](../../specs/epics/35-cluster-full-nodes-and-updates/design.md). The incidents
this starts from are in the trail:
- [`d20261123-cic-lock-timeout`](../trail/events/d20261123-cic-lock-timeout.md);
- [`d20261124-stale-catalog-step-down`](../trail/events/d20261124-stale-catalog-step-down.md);
- [`d20261107-update-watch-rollback`](../trail/events/d20261107-update-watch-rollback.md).

Evidence is cited as `file:line` on main at `e66c74e4a` (after #1538 and #1592). "In flight"
names work open on 2026-11-24:
- **#1597**: reports a failing feed read and checks the real feed in CI.
- **The own feed**: a signed, per-platform update feed, on branch `t3/signed-release-update-feed`,
  no PR yet.
- **#1598, the Down rule**: a failed update is undone by its Down scripts, and the dump goes
  back only in three cases, each stated and logged:
  - the Down scripts fail;
  - a completed migration lost data that no Down script gives back;
  - the way back cannot be told.

## Summary

**The core problem.** From the moment it starts, the release being installed decides whether it
is healthy and how to undo itself, including putting a 34 GB dump back. So:
- a fix to the update path protects only updates *to* a release that already has it;
- a bad release judges itself.

Every mature updater studied keeps an old, trusted judge apart from the release it installs:
- Chrome's updater;
- Android's and ChromeOS's update_engine;
- systemd's boot assessment;
- Mender and balena;
- Home Assistant's Supervisor;
- TrueNAS.

**The ten most serious issues** (all of them are in §2):

| # | Issue | Severity | Status |
|---|---|---|---|
| 1 | A1: the new release judges and rolls itself back, so fixes reach homes one release late, and a bad release cannot be stopped by the one before | Critical | open |
| 2 | P1: on the desktop apps, the live home's kind, even the launcher is the new release's; a new bundle that cannot start its agent or launcher has no way back | Critical | open |
| 3 | G1/A4/P3: a failed update puts a full dump back by itself. Three outages of 2 h 44 min or more (2026-11-14, -23, -24); the launcher's own path still always restores | Critical | in flight (#1598); the launcher's path open |
| 4 | B2: every home on a release before #1592 still cannot see any release (the list is 10.5 MB against an 8 MiB cap) | Critical | open |
| 5 | A3: migrations run after the switch, with the library stopped, so every update that migrates is an outage and a failure leaves it stopped | High | open |
| 6 | D1/D2: the rehearsal on a copy is skipped above 20 GB (the live home is 35 GB), and it could not see the sessions that blocked the migrations anyway | High | open |
| 7 | E8/E4: the machines' copies pause and fall behind while the home is ahead, are copied anew, and their table syncs block index builds; nothing fences the database during the migrations | High | open (E8 needs a measurement) |
| 8 | A2/P5: the launcher's guard, the only old code watching, has its deadline pushed without limit by the release it judges | High | open |
| 9 | B3: a failed update check does not stop automatic installs from a stale catalog | High | partly in flight (#1597) |
| 10 | I1/R1: CI proves an update only from the latest release, on Ubuntu, with a stand-in app, and never with a release whose own update code is broken | High | open |

**The recommendation** (§6). Close the incident class first; each of these takes days:
- no automatic restore;
- unstick the homes that cannot see releases;
- hold installs while the catalog is stale;
- a replication gate before the migrations, and plain index builds while offline;
- four small bugs that cause outages (P3, P4, P5, P10).

Then the structural change:
- **a separate `maktaba-updater`**, small and rarely changed, that judges each release and undoes
  it with its own code;
- **expand-only migrations**, run before the switch while the old release serves, so a failed
  migration is not an outage and a rollback is a binary switch;
- **filesystem clones** for the rehearsal and the safety point, instead of a 34 GB dump.

A no-migration bridge release brings homes in the field onto the new updater.

## 1. How an update runs today

The home's update, on the owner's Mac (Maktaba.app). The question that matters is in the last
column: whose code runs each step.

| # | Step | Process | Whose code |
|---|---|---|---|
| 1 | Find releases: GitHub's release list, every 6 hours | `maktaba-api` (`api/internal/updates/catalog.go`) | old |
| 2 | Choose a target, wait for the window and the release's age | `maktaba-api` (`scheduler.go`, `target.go`) | old |
| 3 | Download, verify (Sigstore, `checksums.txt`, `release.json`), stage, smoke test | `maktaba-server serve` (`internal/updater/machine.go:570`) | old |
| 4 | Rehearse the migrations on a scratch copy (dump, then restore), **skipped above 20 GB** | `maktaba-server` (`internal/dbsafe/dbsafe.go:413`) | old supervisor, new `maktaba-api migrate` |
| 5 | Drain jobs; back the database up (`pg_dump`, about 34 GB) while the old release serves | `maktaba-server` (`serve_update.go:404`) | old |
| 6 | The app stages the new bundle and replaces `/Applications/Maktaba.app`; everything restarts | Tauri app + `maktaba-server` (`internal/updater/appbundle.go`) | old app, old server |
| 7 | The database step: end sessions, `maktaba-api migrate up`, with the library stopped | new `maktaba-server` (`serve_appbundle.go:1004`) | **new** |
| 8 | A failed migration: keep, Down scripts, or **restore the dump** | new `maktaba-server` (`serve_appbundle.go:1203`, `internal/updater/failedback.go:47`) | **new** |
| 9 | Watch health for 10 minutes; roll back when it is not healthy | new `maktaba-server` (`internal/updater/machine.go:1220`) | **new** |
| 10 | Rollback: take the TV change back, keep/Down/restore the database, switch back | new `maktaba-server` (`machine.go:1447`; "this release puts it back now, with its own code", `machine.go:1487`) | **new** |
| 11 | The boot-loop guard: no confirmation in 15 minutes, or 3 crashes in 10, switches back | `maktaba-launcher` (`cmd/maktaba-launcher/run.go:75`) | on the Mac app, **the new release's own launcher**, which ships inside the swapped bundle (P1); its deadline is pushed every minute by the new release (`machine.go:105`, `machine.go:1333`) |
| 12 | Commit; then each LAN machine updates the same way, one at a time | new `maktaba-api` driver (`api/internal/updates/driver.go`) and each machine's new `maktaba-server` | **new** |

From step 7 on, the release being installed decides whether it is healthy and how to undo
itself.
- **On a layout install**, the launcher's guard is old code. It catches a supervisor that dies or
  hangs, and the new release keeps pushing its deadline back.
- **On the Mac app**, even the launcher is the new release's, so no old code watches at all (§4).

So:
- **A fix to the update path only protects updates to a release that already has it.** #1538's
  rules for a failed migration did nothing for the update to v0.3.157 on 2026-11-24. That
  update ran v0.3.157's own database step and rollback, from before the fix.
- **A release with a broken database step or rollback cannot be stopped by the release before
  it.**

Epic 35's own design put this differently. The old supervisor was to run the new release's
migrations before the switch ("the old API keeps serving; migrations are additive",
[design §5.3 step 9](../../specs/epics/35-cluster-full-nodes-and-updates/design.md#53-steps)).
A rollback was to be "binaries only; the schema stays" (§5.6). What was built:
- the migrations run at the new release's first start, with the library stopped;
- a full-dump restore became the fallback for anything the rules could not prove safe.

On 2026-10-28, the restore moved again, from the old release's start to the new release's
rollback ([install-kinds.md](../../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md),
"0.3.41's own code did that restore").

## 2. Every issue found

**Severity:**
- **Critical:** takes a home down for hours, or stops updates for every home, with no person able
  to prevent it.
- **High:** can cause an outage or a stuck home under conditions that occur in the field.
- **Medium:** costs time, disk, or the ability to diagnose, or holds a risk that has not yet hit.
- **Low:** friction or untidiness.

**Status:**
- **fixed** (with the PR);
- **in flight** (open work named above);
- **open** (nobody is on it).

### 2.1 Architecture

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| A1 | **The release being installed judges its own health and rolls itself back.** Its database step, its migration-failure rules, its watch and its rollback (including the restore) are all its own code. A fix helps only updates to a release that has it, and a release with a broken update path cannot be stopped by the one before. | `cmd/maktaba-server/internal/updater/machine.go:1220` (watch), `machine.go:1447` (rollback), `machine.go:1487` ("this release puts it back now, with its own code"), `cmd/maktaba-server/serve_appbundle.go:1004` (database step). 2026-11-24: v0.3.157's pre-#1538 code restored the dump, though #1538 was released (v0.3.161). | Critical | open |
| A2 | **The only old-code safety net is weak, and the new release controls it.** The launcher's guard fires only on 3 crashes in 10 minutes, or 15 minutes without confirmation. The new release pushes that deadline 15 minutes ahead every minute for as long as its watch runs. | `cmd/maktaba-launcher/run.go:75`; `machine.go:105` (`keepGuard`), `machine.go:1333`, `guardMargin` at `machine.go:62` | High | open |
| A3 | **Migrations run after the switch, with the library stopped, by the new release.** Epic 35's design had the old supervisor migrate before the switch, the old API serving ([design §5.3](../../specs/epics/35-cluster-full-nodes-and-updates/design.md#53-steps), step 9). As built, every update that migrates is an outage for the length of its migrations, and a failure leaves the library stopped until the way back finishes. | `serve_appbundle.go:1004`–`1215`; the incident of 2026-11-23: library stopped 08:12–11:02Z | High | open |
| A4 | **A full-dump restore is the built-in fallback** for anything the rules cannot prove safe. This contradicts the design's "the schema stays" (§5.6) and the rule that a rollback uses Down scripts first. | `serve_appbundle.go:1203`–`1215`; `machine.go:1480`–`1497`; `failedback.go:47`–`88` (every unknown outcome is `FailedRestore`) | Critical | in flight (#1598: the dump only when Down fails, data was lost, or the way back is unknown) |
| A5 | **The update logic is large and spread out:** about 33,000 lines with their tests across `internal/updater` (11,700), `api/internal/updates` (18,300), `serve_update.go` and `serve_appbundle.go` (2,900), and the launcher. It has nine kinds of install ([updates.md](../updates.md#how-each-kind-of-install-takes-a-release)) and a database record whose states each release reads in its own way ("a release from before 2026-10-28 reads `restored` as it always did"). Every release changes this code, so the code that rolls back is never the code that was proven in the field. | `wc -l`; [install-kinds.md](../../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md) | Medium | open |
| A6 | **The run's own record is a cross-release protocol with no version.** The database step reads and writes `apppayload.Database` states (`migrating`, `restoring`, `kept_for`, `down_from`, `dump_kept_for`), and every release must read the states every older and newer release writes. #1538's review found a kept record overwritten, which cleared a halt. | `serve_appbundle.go:1004`–`1100`; d20261123-cic-lock-timeout, "The rollback cleared the failed release's halt" | Medium | open |

### 2.2 Discovery (finding and choosing a release)

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| B1 | A byte cap on a response that grows with every release: `/releases?per_page=30` passed 8 MiB, and every home's check failed silently. | d20261124-stale-catalog-step-down; old `catalog.go` | Critical | fixed (#1592: 3 pages of 10); in flight (#1597: a 16 MiB cap, a real-feed check in CI, hourly) |
| B2 | **Homes on releases before #1592 still cannot see any release.** Anonymous `GET /releases?per_page=30` measured 10,474,679 bytes on 2026-11-24 (4,964 assets over 30 releases). 1,320 of those assets are `lang-hunspell-*` packs, 44 a release. | measured with `curl` on 2026-11-24; `selfupdate.go:130` and `system/updater.go:387` in old releases | Critical | open (manual install per home, or shrink the list: see §6.4) |
| B3 | **A failed check does not hold automatic installs.** The scheduler chose from a catalog frozen for a day. The only warning (`StaleAfter`) waits 14 days. | `api/internal/updates/scheduler.go:302`–`341` (no check of the last good read); `catalog.go:30` | High | in flight (#1597 warns after 2 failures); holding installs on a stale catalog is open |
| B4 | The home stepped down below a rolled-back release and chose v0.3.157 after v0.3.158 rolled back. | d20261124-stale-catalog-step-down | High | fixed (#1592: held back by the rolled-back release and its migration set) |
| B5 | Discovery depends on GitHub's REST API: its shape, its pagination, unauthenticated rate limits shared behind one IP, and drafts listed first with a token. | `catalog.go:259`; #1597's notes | Medium | in flight (the own feed) |
| B6 | Three readers of the same list (the catalog, the About page, `maktaba-server update`), each with its own copy of the cap. | `catalog.go:251`, `api/internal/system/updater.go:407`, `cmd/maktaba-server/internal/selfupdate/selfupdate.go:115` | Low | partly fixed (#1592, #1597 sweep) |
| B7 | **No way to withdraw a bad release from every home.** On 2026-11-24 a person turned v0.3.154–v0.3.160 into prereleases on GitHub by hand, so the pre-run recheck would pass them over. | d20261124 event; #1597 "Verified facts" | High | in flight (the own feed); a signed "withdrawn" list is open |
| B8 | **No staged rollout across homes.** Every home on a channel installs a release in its window once it is `min_release_age_hours` old (24 by default). The live home, at 1 hour, is the only canary. | `api/internal/updates/policy.go:81`; [updates.md](../updates.md#the-quiet-window-and-what-holds-a-release-back) | Medium | open |

### 2.3 Download and verify

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| C1 | **The signing identity is the exact workflow file, ref and trigger.** Renaming `release.yml`, or releasing from a dispatch, makes a release that no home accepts (v0.3.85). This is a deliberate fail-closed rule, but it has no rotation path. | [updates.md](../updates.md#what-is-verified) (identity), "How a release is made" | Medium | open (by design) |
| C2 | The Sigstore trusted root expires after 180 days without a refresh. A home offline that long refuses every release and needs a person. | `shared/go/releaseverify/root.go:38` | Medium | open |
| C3 | Verification is strong: Sigstore bundle, Rekor proof, pinned identity, `checksums.txt`, per-binary digests, a watermark against downgrades and replays. No gap was found here beyond C1 and C2. | `shared/go/releaseverify/` | — | — |

### 2.4 Prepare and rehearse

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| D1 | **The rehearsal on a copy is skipped above 20 GB**, so the 35 GB live home has never had one. The homes that need it most skip it. | `cmd/maktaba-server/internal/dbsafe/dbsafe.go:424`; `policy.go:87` (`RehearseMaxDBGB: 20`) | High | open |
| D2 | **The rehearsal could not have caught the incidents.** Its scratch database has no walsenders, no machines and no other sessions, so a `CREATE INDEX CONCURRENTLY` never waits there. | `dbsafe.go:413`–`497` | High | partly (#1538's CI scenario 19 holds a walsender across an update); the rehearsal on the home is open |
| D3 | **The rehearsal costs a full dump and restore** (hours and twice the disk on a large library), with a 30-minute limit on the migrations. | `dbsafe.go:458`–`480` | Medium | open (PostgreSQL 18 clone: §6) |
| D4 | The rehearsal is skipped when the role may not create databases (an operator's PostgreSQL). | `dbsafe.go:434` | Low | open |

### 2.5 Migrations

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| E1 | `lock_timeout` plus 3 identical retries into the same wait. | d20261123 event | Critical | fixed (#1538: ends what a migration waits on) |
| E2 | `CREATE INDEX CONCURRENTLY IF NOT EXISTS` took an INVALID leftover for done. | d20261123 event | Critical | fixed (#1538: `migrate drop-invalid` first) |
| E3 | **CONCURRENTLY buys nothing while the library is stopped, and adds the snapshot wait and the INVALID leftover.** A plain `CREATE INDEX` in the migration's transaction takes a SHARE lock, so no writes, which the stopped app does not need. It does not wait for old snapshots, and it rolls back whole. | [CREATE INDEX](https://www.postgresql.org/docs/18/sql-createindex.html); `shared/db/migrations/README.md` rule 4 requires CONCURRENTLY | High | open |
| E4 | **Nothing fences the database during the migrations.** The step ends sessions every 2 s after a 10 s wait, and the machines reconnect at once (25–28 sessions ended per try on 2026-11-23). Ending a table sync's walsender restarts that machine's whole copy. | `serve_appbundle.go:716`–`820`, `482` | Medium | open |
| E5 | **Whether a failed update can skip the restore is decided by regular expressions over the SQL text** (`RollbackSafe`, `losesData`, `partWayLeft`); anything they cannot tell falls to the restore. | `failedback.go:47`–`150`; `schemakeep.go` | High | in flight (#1598) |
| E6 | **The migration rules contradict the code.** Rule 6 says "Down migrations are dev-only … never run in production", while `FailedDown` and the rollback's `migrateDown` run them in production. No CI job runs each Down script; only rehearsal 03b runs one path. | `shared/db/migrations/README.md:44`; `failedback.go:84`; `serve_update.go:694` | High | in flight (#1598: lint rules 9 and 10, every Down run after a partial Up in CI) |
| E7 | **Contract migrations are allowed with a marker**, and with them a rollback cannot be a plain binary switch. | `shared/db/migrations/README.md` rule 10 | Medium | open |
| E8 | **A likely cause of the copy that keeps falling behind:** while the home's schema is ahead, each machine pauses its subscription until its own update. The home's slot keeps WAL; past `replica_wal_keep_gb` (20) the slot is dropped, and the machine copies the database anew. That re-copy's table syncs are what blocked the index builds. **Not verified** (the d20261123 event leaves the cause open). | `cmd/maktaba-server/internal/nodecopy/copy.go:957`; `api/internal/cluster/copies.go:22`; `api/internal/nodemode/nodemode.go:208` | High | open (needs a measurement) |
| E9 | **No per-migration time budget or production-sized timing in CI.** The 2-hour plus 15 min/GiB start cap is the only bound. | `machine.go:67` | Medium | open |

### 2.6 Health and the watch

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| F1 | **Healthy means "every role runs, the API is ready and says the new version".** No check that the library works: a search, a playback start, a read of the new tables. | `cmd/maktaba-server/serve_update.go:1044`–`1150` | Medium | open |
| F2 | **The watch can keep a home "starting" for most of a day.** `pendingCap` is 2 h plus 15 min per GiB (about 10 h 45 min for 35 GB) plus three times the rehearsal; `stallLimit` is 1 h. Meanwhile it keeps pushing the launcher's guard. | `machine.go:67`–`86`, `1285` | Medium | open |
| F3 | The watch once rolled back a healthy release under load (`watch_timeout`, 0.3.90). | d20261107-update-watch-rollback | Medium | fixed (2026-11-07) |

### 2.7 Rollback and backups

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| G1 | **Three automatic full-dump restores, 2 h 44 min or more each** (2026-11-14, -23, -24), each losing the writes made since its backup. | the two incident events | Critical | in flight (#1598: automatic only in three stated cases) |
| G2 | **A 34 GB `pg_dump` before every update that migrates.** It costs time, disk (all dumps capped at a fifth of the disk) and pipeline memory on small NAS tiers. A filesystem clone would cost seconds. | [updates.md](../updates.md#the-database-during-an-update); `serve_update.go:404` | Medium | open |
| G3 | A rollback that fails twice ends `rollback_loop`, and a person decides. | `machine.go:1462` | Low | open (by design) |
| G4 | **The database step and the rollback disagree about the record.** On 2026-11-23 and -24, the new release's database step rewrote the record to `restored` before the rollback ran, so the rollback's `keepSchema` and `migrateDown` never ran and logged nothing; the Down scripts would have worked (reproduced in #1598). | `serve_appbundle.go:1077`–`1113` in v0.3.153–v0.3.158; `machine.go:1480`; #1598's description | High | in flight (#1598) |
| G5 | **When the launcher switches back by itself** (a boot loop, `cameBackOld`), the *old* release's start puts the dump back, with whatever rules the old release had. | `machine.go:1164`; #1598, "One path still depends on the old release" | Medium | open |

### 2.8 Observability

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| H1 | A failed update check was only logged. | d20261124 event | High | in flight (#1597) |
| H2 | **The project cannot see its fleet.** No home reports that it cannot see releases, or that an update failed. Both outages were found on the live home only. | no update-result reporting in `api/internal/updates` | High | open |
| H3 | **The evidence for a failed update is in six places:** `updates/journal.json`, `launcher.log`, `server-attempts.json`, `update_runs`, the run's log, the API's log segments. A person reads several of them to know why. | [updates.md](../updates.md#when-an-update-fails-where-it-says-why) | Low | open |

### 2.9 Testing and CI

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| I1 | **The rehearsal updates only from the latest published release.** Homes jump many releases (v0.3.153 → v0.3.161, 6 migrations); no CI run updates from an older release. | `.github/workflows/update-rehearsal.yml:246`–`257` | High | open |
| I2 | The rehearsal's GitHub was a fixture, never the real feed's size or shape. | #1597's summary | High | in flight (#1597: scenario 20, the real-feed check) |
| I3 | Rehearsal databases are small. No run is near 35 GB, so the size-dependent behaviour (D1, F2, dump time) is never exercised. | `update-rehearsal.yml` profiles | Medium | open |
| I4 | **CI cannot protect homes from a release whose own update path is broken**, because that release judges itself (A1). | follows from A1 | High | open |

### 2.10 What a person sees

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| J1 | During a restore the library is stopped for hours. The detail says "Maktaba stays stopped … starting Maktaba again tries this update again", which invites the action that repeats the outage. | `serve_appbundle.go:1457` | High | in flight (#1598 makes the restore rare and says why) |
| J2 | A home stuck on an old updater needs an admin to act: pause, defer or skip, then install by hand. A non-admin household cannot. | d20261124 event, "Open" | High | open |

### 2.11 LAN machines

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| K1 | Each machine's new release judges itself too (its own watch), with the launcher's guard behind it. | [updates.md](../updates.md#what-a-run-does), step 4 | High | open (follows A1) |
| K2 | Machines are paused from the home's commit until their own update (E8). After a restore, every machine copies the database anew. | [updates.md](../updates.md#when-a-release-does-not-hold-rollback-backups-restore) ("The machines copy the restored database anew") | Medium | open |

### 2.12 Install kinds: the desktop apps, the launcher, Linux, Docker and NAS, Windows, the CLI

| ID | Issue | Install kinds | Evidence | Severity | Status |
|---|---|---|---|---|---|
| P1 | **No way back when the new app shell, agent or launcher is broken.** On the desktop apps the launcher ships *inside* the swapped bundle, and Tauri keeps no previous app. The kept payload is used only if the new agent and the new launcher both run. A new bundle that cannot start them leaves the home down with nothing to switch back. The live home is this install kind: on it, **even the launcher is the new release's**. | Mac, Windows, Linux app | `cmd/maktaba-server/internal/updater/appbundle.go:27` ("Tauri keeps no previous app"); `apps/desktop/src-tauri/src/agent.rs:11`; `cmd/maktaba-launcher/main.go:96` | Critical | open |
| P2 | **Launcher fixes never reach layout installs.** A launcher is replaced only when `launcher_protocol` rises (still 1), or when the running one lacks the handover flag, which every launcher since 2026-10-25 has. The Epic 53 cut-off (10d685bdd), the MPTCP CVE build flag (6e1295cc1) and the Go 1.26.11 security build (1dce3198f) are stranded on older launchers. | deb/rpm, self-managed, Windows service | `cmd/maktaba-server/internal/updater/launcher.go:52`; `cmd/maktaba-launcher/main.go:42` (`LauncherProtocol = 1`) | High | open |
| P3 | **When the launcher's guard fires after the migrations, the old release's start always puts the whole dump back.** Keep and Down are never tried. #1598 does not change this path. | every launcher path | `cmd/maktaba-launcher/run.go:340`; `shared/go/layout/guard.go:177`; `serve_appbundle.go:1041` | High | open |
| P4 | **`maktaba-server update` on a layout install is never confirmed** (a bug). It activates with run id `cli`, and the server confirms only a switch with no run id, so the launcher rolls the release back after 15 minutes. If that release migrated, the old release then halts on `db_refused_older`. The watermark is also committed before the release proves itself. | deb/rpm, self-managed | `cmd/maktaba-server/cmd_update.go:192`; `cmd/maktaba-server/serve_launcher.go:162`; `cmd/maktaba-server/internal/appruntime/install.go:191` | High | open (since 4a8a3bb6b) |
| P5 | **The guard's deadline has no cap.** `ExtendGuard` takes any later time, and the launcher uses the later of its own deadline and the file's. A new release that hangs while its `keepGuard` goroutine lives is never timed out (A2). | every launcher path | `shared/go/layout/guard.go:198`; `cmd/maktaba-launcher/run.go:317`; `machine.go:105` | High | open |
| P6 | **After a commit, a release that cannot start has no way back on bundle, runtime or Docker installs, and the "can't start" page listens on loopback only.** A NAS container waits for a person with no page anyone can reach. Docker's restart policy never fires, because the launcher stays alive. | Mac, Windows, Linux app, Docker/NAS | `cmd/maktaba-launcher/cantstart.go:136`, `:198`; `run.go:229`, `:451` | High | open |
| P7 | **The boot-loop counters live in the launcher's memory**, though the design says "nothing in memory is needed". A crash that takes the launcher down too (memory, power, the app quitting) resets the count. | every launcher path | `cmd/maktaba-launcher/run.go:115`, `:212`, `:307`; [design §2.2](../../specs/epics/35-cluster-full-nodes-and-updates/design.md#22-the-launcher) | Medium | open |
| P8 | **A newer Docker image switches under a fixed 2-hour guard that nothing extends.** A large dump plus a long index build passes it, so the switch rolls back in the middle of the migrations and P3's full restore follows. The keep check is only "answers its version for 3 minutes". | Docker/NAS | `cmd/maktaba-server/cmd_container.go:62`, `:267`, `:311` | High | open |
| P9 | **The image path and the CLI ignore the home's hold-back of rolled-back releases.** A Watchtower `:latest` pull or `maktaba-server update` takes again a release that rolled back, with its migrations. | Docker, CLI | `cmd_container.go:222`; `selfupdate.go:127` | Medium | open |
| P10 | **An order the app never answers waits forever.** There is no timeout, and the nudge's error is dropped. On Linux the runtime kind still waits for the app's `staged` though nothing is staged. A Mac or Linux home whose app is not running never updates. | Mac, Linux app | `machine.go:941`; `driver.go:1169`; `request.go:237` | High | open |
| P11 | **The Mac and Windows servers run only while the user is logged in.** No update runs while logged out, and a reboot in the middle resumes only at login. The default 03:00–05:00 window overlaps overnight OS updates. | Mac, Windows | `apps/desktop/src-tauri/src/agent.rs:3`; `policy.go:79` | Medium | by design |
| P12 | **On a Mac, quitting the app between the server's pause and the swap leaves the server stopped** until the app is opened or the next login. A relaunched app ignores an `installing` order, so the server waits 30 minutes before failing. | Mac | `apps/desktop/src-tauri/src/shell.rs:1662`; `apps/desktop/src-tauri/src/autoinstall.rs:166`; `appbundle.go:516` | Medium | open |
| P13 | **A standard (non-admin) user, or an `/Applications` they cannot write, gets `needs_admin` on every release.** There is no privileged helper. | Mac | `apps/desktop/src-tauri/src/autoinstall.rs:527`; `request.go:183` | Medium | by design |
| P14 | **Three runtime switches have no guard:** `update` on a runtime, `runtime install --activate`, and the Linux app's `sync_runtime`. Each raises the watermark at once and has no PostgreSQL-major check. | Linux app, headless | `cmd/maktaba-server/cmd_update_runtime.go:134`; `appruntime/activate.go:74`–`129`; `apps/desktop/src-tauri/src/agent_linux.rs:598` | High | open |
| P15 | **A new launcher is installed after commit, outside any guard.** It `exec`s in place, and a broken one is restarted as the same binary. | layout, POSIX | `updater/launcher.go:16`, `:145`; `cmd/maktaba-launcher/status.go:83` | Medium | open |
| P16 | **Windows service and layout installs can never take a new launcher.** Any `launcher_protocol` rise blocks updates until an administrator runs a command. | Windows | `updater/launcher.go:52`, `:108` | Medium | by design |
| P17 | **After a desktop rollback, the new launcher, agent and shell run the older payload.** That mix of versions is never rehearsed. | Mac, Windows | `cmd/maktaba-launcher/run.go:368`; `materialize.go:53` | Medium | open |
| P18 | **The desktop bundle is checked only with the Tauri minisign key**, not against the release's Sigstore-signed `checksums.txt`: a second, weaker trust root. | Mac, Windows | `appbundle.go:273`; `shell.rs:1409`; `agent_orders.rs:153` | Medium (security) | open |
| P19 | **The old app decides how its replacement is fetched.** Its feed URL check wants an exact `<repo>/releases/download/v<v>/latest.json`, so a repository rename or a feed change makes every old app refuse forever. | Mac, Windows, Linux app | `apps/desktop/src-tauri/src/autoinstall.rs:378`; `agent_orders.rs:1` | Medium | open |
| P20 | **The Windows installer ignores a failed `--server-stop`** and overwrites the payload while it may still run. The database and state share `$INSTDIR` with the program. | Windows | `apps/desktop/src-tauri/windows/installer-hooks.nsh:6`, `:27` | Medium | open |
| P21 | **What is rehearsed is not what is installed.** The smoke test and the migrations' rehearsal use the platform archive's `maktaba-api`; the app then installs the bundle that `desktop-release.yml` built. Machines run no smoke test. | Mac, Windows | `appbundle.go:322`, `:463` | Low | open |
| P22 | Byte caps on artifacts that keep growing, with no CI headroom check (2 GiB runtime, 512 MiB archive); the payload now carries two PostgreSQL majors. | all | `runtime.go:175`; `shared/go/releaseverify/fetch.go:21` | Low | open (#1597 covers the list's caps only) |
| P23 | The deb's `postinst` compares the wrong launcher path, so an upgrade that changes only the launcher never restarts it. | deb | `deploy/packaging/scripts/postinstall.sh:69`, `:100` | Low | open |

### 2.13 Update rehearsal and CI (in addition to I1–I4)

| ID | Issue | Evidence | Severity | Status |
|---|---|---|---|---|
| R1 | **The rehearsal never runs a release whose own update or rollback code is broken, never mixes versions, and never runs the real apps.** The previous release is the latest published, and "cur", "next" and "after" are all this commit. The desktop app is a Go stand-in, every job runs on Ubuntu (no macOS, no Windows), and no scenario fires the launcher's guard after migrations. | `tools/rehearsal/build.sh:9`; `api/rehearsal_harness_e2e_test.go:756`; `.github/workflows/update-rehearsal.yml` | High | open |
| R2 | **The NAS container legs never run the self-update, a migration, a dump or a restore.** The "bad" image exits at once. | `tests/nas/update_leg.py:15` | Medium | open |
| R3 | The fake GitHub ignores `per_page` and `page` and serves a few tiny releases. | `api/autoupdate_e2e_test.go:129` | Medium | in flight (#1597) |

### 2.14 Docs that say something the code does not

- **"The updater itself never runs a down migration"** (`docs/updates.md` "Restoring a backup";
  `deploy/packaging/upgrade.md`) and migrations rule 6 ("Down migrations are dev-only"). Both
  contradict `schemadown.go` and `failedback.go`. #1598 edits `docs/updates.md` and the rules, not
  `upgrade.md`.
- **The launcher.** Design §2.2 says "< 1 000 lines, standard library + x/sys … nothing in memory
  is needed". It is about 2,800 lines, depends on `shared/log`, and keeps its boot-loop state in
  memory (P7).
- **Design §2.3–2.4** still lists Docker as "409 / no rollout" and the Mac home as a
  LaunchDaemon. The code and `install-kinds.md` say Docker updates itself and the Mac app uses a
  LaunchAgent.
- **Story 59.12** says the image switch "confirms within 3 minutes". The code keeps an image after
  3 minutes of answering, under a 2-hour guard.
- **Design §5.3 step 9** (old supervisor, migrations before the switch) and **§5.6** (rollback of
  binaries, "the schema stays") describe a different update from the one built (§1 above).


## 3. How other software updates itself

Primary sources only: official docs, design docs and source code. Each row answers the same
five questions:
- who runs the update;
- how the download is verified;
- what happens to data and migrations;
- how a rollback works;
- what a failure looks like to the user.

### 3.1 Desktop and OS updaters

| System | Who updates | Verified by | Data / migrations | Rollback | A failure, as the user sees it |
|---|---|---|---|---|---|
| **Chromium updater / Omaha** (replaces Keystone on macOS) | A separate, versioned updater program with its own service. A new updater version installs side by side as *inactive* and stays "unqualified" until it updates a test app. Only one instance is ever active ([design doc](https://chromium.googlesource.com/chromium/src/+/main/docs/updater/design_doc.md), [functional spec](https://chromium.googlesource.com/chromium/src/+/main/docs/updater/functional_spec.md)). | CUP-signed server answers (sound "even in the presence of compromised TLS"), SHA-256 per download, CRX3 publisher signatures ([protocol v4](https://chromium.googlesource.com/chromium/src/+/main/docs/updater/protocol_4.md)) | A user-data snapshot after each major update (3 kept), put back on an admin rollback ([Chrome help](https://support.google.com/chrome/a/answer/9917429?hl=en)) | Never automatic; admin policy, last 3 majors. Server-side staged rollout to "a small percentage" first ([release cycle](https://chromium.googlesource.com/chromium/src/+/main/docs/process/release_cycle.md)) | An error code with a help link, and an error ping to the server |
| **Sparkle** (macOS) | The app finds the update; a separate **Autoupdate** process from the *old* app's framework installs it after the app quits ([sandboxing](https://sparkle-project.org/documentation/sandboxing/)) | EdDSA signature per download, optional signed appcast; downgrades refused in the installer ([SUPlainInstaller.m](https://github.com/sparkle-project/Sparkle/blob/2.x/Autoupdate/SUPlainInstaller.m)) | None (the app's) | Only when the swap fails; never on an unhealthy new version. `phasedRolloutInterval`, `criticalUpdate`, `minimumAutoupdateVersion` ([publishing](https://sparkle-project.org/documentation/publishing/)) | An alert; the old version runs on |
| **Squirrel / Velopack** | `Update.exe` (Windows), ShipIt (Mac), after the app exits ([Squirrel.Windows](https://github.com/Squirrel/Squirrel.Windows/blob/develop/docs/using/install-process.md), [Velopack](https://docs.velopack.io/integrating/overview)) | Hashes in the release file; Squirrel.Mac also checks the code-signing requirement ([SQRLInstaller.m](https://github.com/Squirrel/Squirrel.Mac/blob/master/Squirrel/SQRLInstaller.m)) | App hooks, 15–30 s limits | Only at swap time; "no built-in support for rolling back" ([Squirrel.Windows](https://github.com/Squirrel/Squirrel.Windows/blob/develop/docs/using/install-process.md)) | "The previous version was restored", or a broken install |
| **Tauri updater** | The app itself ([docs](https://v2.tauri.app/plugin/updater/)) | minisign; the endpoint's JSON is not signed, so without `require_signed_version` an old signed build can be replayed ([updater.rs](https://github.com/tauri-apps/plugins-workspace/blob/v2/plugins/updater/src/updater.rs)) | None | A backup restored only when the swap fails | An error to the app |
| **systemd-sysupdate + boot assessment** | `systemd-sysupdate`, by its own timers, into an inactive A/B slot ([man](https://www.freedesktop.org/software/systemd/man/latest/systemd-sysupdate.html)) | `SHA256SUMS` + `SHA256SUMS.gpg`; `MinVersion=` ([sysupdate.d](https://www.freedesktop.org/software/systemd/man/latest/sysupdate.d.html)) | Out of scope | Boot counting: tries left are in the entry's file name, outside the new version. A boot is blessed only after health units reach `boot-complete.target` ([boot assessment](https://systemd.io/AUTOMATIC_BOOT_ASSESSMENT/)) | Silent: the machine comes back on the old version |
| **Android A/B, ChromeOS** | `update_engine`, a separate daemon, writes the inactive slot ([Android A/B](https://source.android.com/docs/core/ota/ab), [update_engine](https://chromium.googlesource.com/aosp/platform/system/update_engine/+/HEAD/README.md)) | Signed manifest and payload; the rollback index advances only from a slot marked successful ([AVB](https://android.googlesource.com/platform/external/avb/+/main/README.md)) | Virtual A/B merges its snapshot only "after confirming a successful boot" ([Virtual A/B](https://source.android.com/docs/core/ota/virtual_ab)); ChromeOS wipes on rollback | `markBootSuccessful` by a verifier, else the old slot after N tries ([ChromeOS](https://www.chromium.org/chromium-os/chromiumos-design-docs/filesystem-autoupdate/)) | Invisible |
| **Mender, balena** | The Mender client; balena's on-device Supervisor ([Mender](https://docs.mender.io/overview/introduction), [balena](https://docs.balena.io/reference/OS/updates/self-service/)) | Signed artifacts ([Mender signing](https://docs.mender.io/artifact-creation/sign-and-verify)) | A separate `/data` partition | Commit only after `ArtifactCommit` health scripts; automatic rollback when the new version "can not reach the Mender Server" ([state scripts](https://docs.mender.io/artifact-creation/state-scripts)); balena `rollback-health` for 15 minutes ([rollbacks](https://docs.balena.io/reference/OS/updates/rollbacks/)) | A failed deployment with logs on the server; balena retries twice, then waits 2 days |

### 3.2 Media servers and NAS platforms

| System | Who updates | Verified by | Data / migrations | Rollback | A failure, as the user sees it |
|---|---|---|---|---|---|
| **Plex** | Automatic only on Windows and macOS (a separate `Plex Update Service`); Linux and NAS through the package manager or store ([settings](https://support.plex.tv/articles/200289496-general/), [repos](https://support.plex.tv/articles/235974187-enable-repository-updating-for-supported-linux-server-distributions/)); Docker installs at container start ([pms-docker](https://github.com/plexinc/pms-docker)) | GPG-signed repos | SQLite migrated at start; a backup every 3 days, not before an update ([scheduled tasks](https://support.plex.tv/articles/201553286-scheduled-tasks/)) | Manual: stop, copy a dated backup in ([restore](https://support.plex.tv/articles/202485658-restore-a-database-backed-up-via-scheduled-tasks/)) | "Server is starting", 503 while migrating |
| **Jellyfin** | No updater: installer, apt, `docker pull` ([docs](https://jellyfin.org/docs/general/installation/linux)) | GPG repo key, checksummed install script | Migrations at start. Each one **declares what it touches**, and the runner backs up exactly that and puts it back if the migration throws ([JellyfinMigrationService.cs](https://github.com/jellyfin/jellyfin/blob/v10.11.0/Jellyfin.Server/Migrations/JellyfinMigrationService.cs)) | "Jellyfin does not have a downgrade mechanism… restore a backup" ([backup](https://jellyfin.org/docs/general/administration/backup-and-restore/)); "Do not skip major releases" | 10.11 added a LAN-only startup page with progress and log lines ([10.11](https://jellyfin.org/posts/jellyfin-release-10.11.0)) |
| **Emby** | The app, on Windows only; "not … when running as a service"; none on Linux (the runtime account cannot install); Docker by pull ([forum, staff](https://emby.media/community/topic/88398-automatic-server-updates/)) | Not documented | Not documented | Reinstall by hand | A "restart to finish applying updates" banner that sometimes never clears |
| **Home Assistant** | The **Supervisor**, a separate privileged container, updates Core and add-ons and judges them ([core.py](https://github.com/home-assistant/supervisor/blob/main/supervisor/homeassistant/core.py)). It updates itself by retagging and restarting; a dumb systemd script re-pulls a known-good version when it never came up ([haos-supervisor](https://github.com/home-assistant/operating-system/blob/dev/buildroot-external/rootfs-overlay/usr/sbin/haos-supervisor)) | RAUC-signed OS bundles ([update system](https://contributors.home-assistant.io/os/operating-system/update-system/)); images by tag | Core migrates at start; an optional partial backup before an update | Core: the Supervisor checks `http`, `frontend`, `websocket_api` and reinstalls the previous **image only**, never the data ("doesn't undo the changes that the newer version made to your data", [docs](https://www.home-assistant.io/common-tasks/os)). OS: A/B slots, 3 tries | A repair issue and `home-assistant-rollback.log`; users report the old Core failing on the newer data |
| **Synology Package Center** | DSM's Package Center, a system service ([configure](https://kb.synology.com/en-id/DSM/help/DSM/PkgManApp/configure?version=7)) | DSM 7 trusts Synology-signed packages only | The package's scripts: `preupgrade` can abort before anything changes; a failing `postupgrade` marks it "corrupted" ([scripts](https://help.synology.com/developer-guide/synology_package/scripts.html)) | None for packages; DSM cannot be downgraded | "Corrupted" with a Repair button |
| **TrueNAS SCALE** | `middlewared` (system, into a new boot environment); apps by the user ([install.py](https://github.com/truenas/middleware/blob/master/src/middlewared/middlewared/plugins/update_/install.py)) | SHA-256 against an HTTPS manifest | Apps migrate at start; `snapshot_hostpaths` before an upgrade ([API](https://api.truenas.com/v25.10/api_methods_app.upgrade.html)) | Apps: roll back to a revision with ixVolume snapshots, host paths not covered ([apps](https://apps.truenas.com/managing-apps/managing-installed-apps/)); system: boot environments ([docs](https://www.truenas.com/docs/scale/25.10/scaletutorials/systemsettings/managebootenvironscale/)) | The old boot environment stays in the boot menu |
| **Watchtower** | A separate container with the Docker socket. **Archived and unmaintained since 2025-12-17** ([repo](https://github.com/containrrr/watchtower), [#2135](https://github.com/containrrr/watchtower/discussions/2135)) | Registry digest | The app's | None | A notification, or a crash-looping container |

### 3.3 Server software and database migrations

| System or practice | What it does | Source |
|---|---|---|
| **GitLab** | Regular migrations (at most 3 minutes) run before the new code; destructive ones are **post-deploy** migrations, run after every node runs the new code (`SKIP_POST_DEPLOYMENT_MIGRATIONS`). Data changes are throttled background batches, finished in a later release (**required stops**). `add_concurrent_index` drops an **INVALID** index of the same name and builds it again. Every migration MR runs on a thin clone of production before it may merge. A downgrade needs "a database backup created under the exact same version". | [post-deploy](https://docs.gitlab.com/development/database/post_deployment_migrations/), [style guide](https://docs.gitlab.com/development/migration_style_guide/), [zero downtime](https://docs.gitlab.com/update/zero_downtime/), [upgrade paths](https://docs.gitlab.com/update/upgrade_paths/), [index_helpers.rb](https://gitlab.com/gitlab-org/gitlab/-/raw/master/lib/gitlab/database/migrations/index_helpers.rb), [migration pipeline](https://docs.gitlab.com/development/database/database_migration_pipeline/), [downgrade](https://docs.gitlab.com/update/package/downgrade/) |
| **Discourse** | The same post-deploy split. `Migration::SafeMigrate` refuses drops and renames outside `post_migrate`, and **any `CREATE INDEX CONCURRENTLY` not preceded by a `DROP INDEX` of the same name**. A failed web upgrade logs "FAILED TO UPGRADE" and scales the workers back up; there is no database rollback. | [safe_migrate.rb](https://github.com/discourse/discourse/blob/main/lib/migration/safe_migrate.rb#L176-L208), [post-deploy](https://meta.discourse.org/t/introducing-post-deployment-migration/98974), [upgrader.rb](https://github.com/discourse/docker_manager/blob/main/lib/docker_manager/upgrader.rb) |
| **Expand/contract** | Expand (add beside the old), migrate the clients, contract (remove the old) in a later release. After an expand alone, the previous release runs on the new schema, so a rollback is a binary switch. | [ParallelChange](https://martinfowler.com/bliki/ParallelChange.html), [Stripe](https://stripe.com/blog/online-migrations), [strong_migrations](https://github.com/ankane/strong_migrations) |
| **`CREATE INDEX CONCURRENTLY`** | Waits for every transaction in the database with an older snapshot (`WaitForOlderSnapshots`); a logical table sync holds a REPEATABLE READ snapshot for its whole COPY. `lock_timeout` covers that wait. A failure leaves an INVALID index that `IF NOT EXISTS` takes for done; it is still updated on every write. `pg_stat_progress_create_index.current_locker_pid` names whom it waits on. A plain `CREATE INDEX` takes SHARE (it blocks writes only), does not wait for old snapshots, and rolls back whole. | [CREATE INDEX](https://www.postgresql.org/docs/18/sql-createindex.html), [indexcmds.c](https://github.com/postgres/postgres/blob/REL_18_STABLE/src/backend/commands/indexcmds.c), [tablesync.c](https://github.com/postgres/postgres/blob/REL_18_STABLE/src/backend/replication/logical/tablesync.c), [lock_timeout](https://www.postgresql.org/docs/18/runtime-config-client.html), [progress](https://www.postgresql.org/docs/18/progress-reporting.html), [locking](https://www.postgresql.org/docs/18/explicit-locking.html) |
| **Copies for rehearsal and rollback** | PostgreSQL 18's `file_copy_method = clone` makes `CREATE DATABASE … STRATEGY FILE_COPY` use `copyfile` on APFS and `copy_file_range` on Linux and FreeBSD, but needs a template with no sessions. `pg_upgrade --clone` gives "near-instantaneous copying". A file-level copy of a data directory needs the server stopped, or a filesystem snapshot (ZFS). | [runtime-config-resource](https://www.postgresql.org/docs/18/runtime-config-resource.html), [CREATE DATABASE](https://www.postgresql.org/docs/18/sql-createdatabase.html), [pg_upgrade](https://www.postgresql.org/docs/18/pgupgrade.html), [file backup](https://www.postgresql.org/docs/18/backup-file.html), [zfs snapshot](https://openzfs.github.io/openzfs-docs/man/master/8/zfs-snapshot.8.html) |
| **Logical replication and DDL** | DDL is not replicated. "Intermittent errors can be avoided by applying additive schema changes to the subscriber first". `ALTER SUBSCRIPTION … DISABLE` pauses a copy, and its slot keeps WAL meanwhile (`max_slot_wal_keep_size` caps it). | [restrictions](https://www.postgresql.org/docs/18/logical-replication-restrictions.html), [ALTER SUBSCRIPTION](https://www.postgresql.org/docs/18/sql-altersubscription.html), [logical decoding](https://www.postgresql.org/docs/18/logicaldecoding-explanation.html) |
| **Kubernetes, Argo Rollouts** | A Deployment stalls with `ProgressDeadlineExceeded` and only reports it; "higher level orchestrators can… rollback". Argo aborts on a failed analysis and switches traffic back. Neither touches the database: a schema changed before the cutover "must be backward compatible" with the old version. | [Deployment](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/), [Argo analysis](https://argo-rollouts.readthedocs.io/en/stable/features/analysis/), [AWS blue/green](https://docs.aws.amazon.com/whitepapers/latest/blue-green-deployments/best-practices-for-managing-data-synchronization-and-schema-changes.html) |

### 3.4 What they have in common, and where Maktaba differs

| Pattern | Who does it | Maktaba today |
|---|---|---|
| **The new version never judges itself.** A separate, rarely changing updater or supervisor decides commit or rollback | Chromium updater, update_engine, systemd boot assessment, Mender, balena, Home Assistant Supervisor, TrueNAS middlewared | No: the new release judges and rolls back itself (A1) |
| **The updater updates itself through a qualify-then-activate step, or a dumb layer re-fetches a known-good one** | Chromium updater (inactive until it updates a test app), Home Assistant (`haos-supervisor` re-pulls `stable`) | The server updates itself with every release |
| **A try counter kept outside the version being judged** | systemd-boot, Android, ChromeOS, Mender, balena, HAOS | Partly: the launcher counts crashes, but the new release pushes its deadline (A2) |
| **Irreversible steps only after commit** | Android Virtual A/B merge, AVB rollback index, GitLab and Discourse post-deploy migrations | No: every migration runs before the watch, and a contract migration may ship beside its code (E7) |
| **Rollback swaps code only; the data stays readable by the old version** | Kubernetes/Argo with backward-compatible schema, GitLab, Discourse; Home Assistant without it, and its users report the mismatch | Partly: the keep rule, but restore is the fallback (A4) |
| **A snapshot instead of a dump** | TrueNAS boot environments and ixVolume snapshots, HAOS A/B, Chrome data snapshots | No: a 34 GB `pg_dump` and a 2 h 44 min restore (G1, G2) |
| **Migrations proven on a production-sized copy before they ship** | GitLab (thin clones gate every merge) | Only below 20 GB, and with no other sessions (D1, D2) |
| **Server-side staged rollout and a kill switch** | Omaha cohorts, Sparkle phasing, `minimumAutoupdateVersion` | No (B7, B8) |
| **Not skipping versions that finish data work** | GitLab required stops, Jellyfin "Do not skip major releases" | Any release to any newer one; CI proves only N-1 to N (I1) |
| **On NAS and Linux, the platform's package manager owns the update** | Plex, Jellyfin, Emby, Synology, TrueNAS | Maktaba updates itself inside the container and the packages |

No mature system above lets the version being installed be the only judge of its own health.
They differ in how data comes back:
- Home Assistant reinstalls the old image over the new data, and its users report the old
  version failing on it.
- Chrome, TrueNAS and Android use snapshots or defer the irreversible step.
- GitLab and Discourse keep every schema readable by the previous release, and roll forward
  otherwise.

## 4. How each install kind updates today

Every row is a decision in an update. Each cell names the code that makes it: **old** (the release
being replaced) or **new** (the release being installed).

| Decision | Layout (deb/rpm, `setup --self-managed`, Windows service) | Mac and Windows app | Linux app runtime, headless, Docker self-update | Docker: a newer image pulled |
|---|---|---|---|---|
| Download, Sigstore verify, order, switch (writes the guard) | old server | old server | old server | new image's entrypoint (`cmd_container.go:260`) |
| Download and swap the app | — | old app (Mac `shell.rs`; Windows the old agent, `agent_orders.rs`), checked with the minisign key only | — | — |
| Migrations, the watch, commit or rollback, keep/Down/restore | **new** server | **new** server | **new** server | **new** server, plus "answers its version for 3 minutes" |
| The guard's deadline | set by old, **pushed without limit by new** | same | same | fixed 2 hours |
| The boot-loop guard (the launcher) | old launcher; replaced after commit, in practice never (P2) | **new** launcher, inside the swapped bundle | old launcher process until the service or container restarts, then new | new |
| The dump put back after the launcher's guard fires | old release's start, always the whole dump (P3) | same | same | same |

**The only judge that does not belong to the new release is the layout installs' launcher**, and
the new release can push its deadline. On every desktop, runtime and NAS install, the new
release's code does all of the judging.

- **Mac (Maktaba.app; a LaunchAgent in the user's session).**
  1. The old server verifies the release, keeps an APFS clone of its payload, and orders the app
     (`app-install.json`). After 90 seconds without an answer it opens
     `maktaba://install-update`.
  2. The old app checks that it may swap the bundle without a password, downloads the update,
     checks it with minisign and says `staged`.
  3. The server drains, dumps and writes the guard, then says `go`. The app pauses the server,
     swaps the bundle and relaunches.
  4. launchd starts the **new** agent, which runs the **new** launcher. The new server migrates
     and judges itself.
  5. On a rollback, the new launcher runs the kept old payload, and the app shell stays new.

  If the new bundle cannot start its agent or launcher, nothing goes back (P1).
- **Windows (a per-user install, a sign-in task).** The same, but the old agent answers the order.
  - At `go` it runs the new installer, whose hooks stop the server (ignoring a failure, P20),
    replace the program and payload, and start the new agent and launcher.
  - Nothing runs while the user is signed out (P11).
- **Linux app (a runtime copy, a systemd user unit).**
  - The old server installs the signed payload as a new runtime folder, waits for the app's
    `staged`, points `current` at it and exits.
  - The old launcher process starts the new server, which judges itself.
  - At the app's next start, `sync_runtime` may switch to the app's bundled payload with no guard
    (P14).
- **Headless Linux and the Docker self-update.**
  - The same runtime flow with no app. In Docker, the entrypoint runs
    `current/bin/maktaba-launcher`.
  - A newer image pulled by a NAS catalog or Watchtower takes another path: its entrypoint
    installs its payload under a fixed 2-hour guard (P8). It ignores the hold-back list (P9).
- **Layout installs (deb/rpm, self-managed, the Windows service).**
  - The old server stages into `bin/releases/<v>`, writes `previous`, `pending` and `current`, and
    exits 75.
  - The old launcher starts the new release and enforces 3 exits in 10 minutes and the deadline,
    which the new release keeps pushing (P5).
  - `maktaba-server update` on these installs is never confirmed (P4).


## 5. The candidates, compared

The candidates are weighed for Maktaba's case:
- **Platforms:** a self-hosted home server on Mac, Windows, Linux and NAS.
- **Database:** a bundled PostgreSQL of tens of GB, with LAN machines copying it by logical
  replication.
- **People:** households that are not administrators.

| Candidate | What it fixes | What it costs | Verdict |
|---|---|---|---|
| **A separate, rarely changing updater that judges the new release and undoes it with its own code** | A1, A2, I4, K1: a fixed updater protects every later update, and a bad release cannot stop its own rollback | A new process on every install kind, a stable contract between it and the release, and its own careful self-update | **Yes.** The structural fix; every mature system has one |
| **Expand/contract: a release's migrations stay readable by the release before; contract steps ship later, after commit** | A3, A4, E5, E7, G1: a rollback becomes a binary switch with no schema undo, and the migrations can run before the switch while the old release serves | Two releases for a drop or rename; a lint and CI to enforce it (`make compat-prev` already runs the previous release's tests on the new schema) | **Yes.** It makes every other part simpler |
| **A rehearsal on a copy before switching, the copy made by a filesystem clone** | D1, D3: no 20 GB limit, seconds instead of hours | Depends on the filesystem: APFS, ZFS, Btrfs and XFS clone; ext4 and some Docker volumes do not. It cannot reproduce other sessions (D2) | **Yes, where the filesystem clones**; the dump rehearsal stays the fallback below a size |
| **A clone as the safety point instead of a 34 GB dump** | G2, and a manual restore in seconds | The same filesystem limits; writes after the clone are lost on a restore, as with a dump | **Yes, as the manual way back only**; automatic rollback stays code-only (#1598) |
| **Blue/green slots for the whole database** (two Postgres clusters) | Instant switch back | Twice the disk, and replication between the slots; not a fit for a 35 GB home database on one disk | **No.** Blue/green for the binaries (already the `current`/`previous` layout) is enough |
| **Stop replication during the migrations** | E4, E8, the incident class | A short pause of the machines' copies; WAL kept meanwhile, under the existing cap | **Yes, as a pre-flight gate:** no table sync running and the machines' copies paused (`nodecopy.Quiesce` exists) |
| **Plain `CREATE INDEX` while the library is stopped** | E3 | Writes blocked for the build, which nothing needs while the app is stopped | **Yes, now**, for as long as migrations run offline. Online migrations (after expand/contract) keep CONCURRENTLY with drop-first and no short `lock_timeout` |

## 6. Recommended design

### 6.1 The shape

```
 launcher (small; state on disk) ─ runs ─►  maktaba-updater  (new; rarely changes; own version and protocol)
     │  boot-loop guard, current/previous         │  reads the signed feed, verifies, stages
     │  (no one else may push its deadline)        │  pre-flight: disk, copies quiet, rehearsal on a clone
     │                                            │  migrations (expand only) BEFORE the switch, old release serving
     └── runs ──►  maktaba-server serve (release) │  switch current, judge health by a versioned contract
                    UI, scheduling policy, reports│  commit, or switch back with ITS OWN code
                    (writes the wanted state;     │  post-commit (contract) migrations, after every machine follows
                     never commits or rolls back) │  machines: the same updater on each machine
```

1. **`maktaba-updater` owns the update.** It is a separate binary and process. It has its own
   version, its own `updater_protocol`, and its own small set of dependencies:
   `shared/go/releaseverify`, a Postgres client, the layout package. It reuses Epic 35's parts:
   the verify code, the `current`/`previous` layout, the journal, the launcher.
   - It moves out of the release: the database step, the watch, the commit and the rollback.
   - The release keeps the Updates page, the policy (window, channel, pin, skip), notifications,
     and the cluster's view. It writes what it wants installed; the updater reports back.
2. **A small, stable contract between the updater and the release** decides health. A release
   that does not answer the contract is not healthy:
   - `maktaba-server health --contract N`, or `GET /healthz/update` answering a fixed JSON schema:
     its version, every role's state, and a few **functional probes** (a read of each new table,
     a search, the start of a playback);
   - a `migrate` subcommand with fixed flags and exit codes (`maktaba-api migrate up`, `status`,
     `down-to` and `drop-invalid` exist).

   The contract changes rarely and only with a protocol number. An updater refuses a release
   whose `release.json` asks for a protocol it does not speak, and says which updater it needs.
3. **The release never extends the guard.** The updater alone keeps the launcher's guard alive
   while it judges.
   - The updater's own limits are short and fixed: minutes to come up after the migrations,
     10 minutes healthy.
   - Once the migrations run before the switch, nothing in the release's start is slow (F2).
   - The launcher keeps its boot-loop count and deadline on disk (P7), and the deadline has a
     hard cap (P5).
4. **The judge lives outside anything the update replaces.** On the desktop apps, the launcher
   and the updater move out of the app bundle into the app's data folder, each with a kept
   previous copy. Swapping `/Applications/Maktaba.app` then never replaces the judge (P1). The
   OS service (launchd, the sign-in task, the systemd user unit) starts the launcher from there.
5. **The updater updates itself last and rarely**, the Chromium way:
   - Updater N+1 ships as its own signed file in a release.
   - Updater N installs it beside itself, inactive.
   - N+1 must qualify: verify a release, read the feed, run the health contract against the
     running release.
   - Only then does the launcher switch to it, and N stays as the fallback.
   - An updater change gets its own rehearsal matrix (§6.3, item 10), across every install kind and from
     the oldest updater in the field.
   - Most releases carry no updater change at all.
6. **Migrations become expand-only and run before the switch** (Epic 35's original §5.3 step 9).
   - **Run by the updater, with the old release serving.** The release's
     `maktaba-api migrate up` runs as a child of the updater, under the updater's timeouts.
     `make compat-prev` already proves the previous release runs on the new schema; it becomes
     a hard gate for every migration.
   - **A failure changes nothing that matters.** The old release keeps serving on a schema it
     can read. The updater drops INVALID leftovers and stops. No switch, no restore, no outage.
   - **Contract steps go to a post-commit set** (`shared/db/migrations/post/`, GitLab's
     post-deploy). It runs only after the release committed on the home and on every machine.
     A release's own post-commit set may only remove what the release before already stopped
     using.
   - **Data changes stay jobs** (rule 5), finished before a later release may rely on them
     (GitLab's required stops).
   - **Index builds use CONCURRENTLY**, since the library serves. Before each build the updater:
     - drops a same-named INVALID index (Discourse's lint);
     - sets no short `lock_timeout`;
     - waits until no table sync runs;
     - logs `pg_stat_progress_create_index.current_locker_pid`.
7. **The safety point is a clone.**
   - **Where it can clone** (the Mac's APFS, ZFS on TrueNAS, Btrfs on Synology, XFS), the updater
     takes a clone just before the migrations. It uses PostgreSQL 18's `file_copy_method =
     clone`, or a data-directory clone with Postgres stopped for seconds.
   - **Rehearsal.** The same clone is where the migrations are rehearsed, with no size limit.
   - **A manual restore takes seconds.** It is offered on the Updates page and never run by
     itself (#1598).
   - **Where it cannot clone**, today's dump stays, smaller and optional once migrations are
     expand-only.
8. **The cluster follows the subscriber-first rule** for additive changes.
   - Each machine's updater runs the release's expand migrations on its own copy first, which an
     older release's code tolerates.
   - Then the home migrates.
   - The copies never need pausing, so the slot does not grow and nothing is copied anew (E8).
9. **The feed is Maktaba's own**, as the own-feed work builds it:
   - signed, small, per platform;
   - with a withdrawn list (B7);
   - staged rollout percentages by home id (B8);
   - a minimum updater version (Sparkle's `minimumAutoupdateVersion`).
10. **The fleet can be seen.** A feed read could carry only the version and install kind, with no
   id or address, so the feed's logs count how many homes run each version and how many have
   stopped reading. Any result ping is opt-in. This needs a product decision on privacy before it
   is built.

### 6.2 What changes in Maktaba

| Area | Today | After |
|---|---|---|
| `cmd/maktaba-updater` (new) | — | The judge: verify, stage, clone, migrate, switch, watch, commit, roll back. Code moves from `internal/updater` (`machine.go`'s prepare/apply/watch/rollback, `layout.go`, `runtime.go`, `appbundle.go`), `internal/dbsafe`, and the database step in `serve_appbundle.go` |
| `maktaba-server serve` | The database step at its start; the watch; the rollback | Starts on a database already migrated; answers the health contract; no update decisions |
| `maktaba-launcher` | Guard deadline pushed by the release | Runs the updater and the server; only the updater may push its guard; keeps `previous` of both |
| `api/internal/updates` | Scheduler, driver, catalog | Policy, display, the cluster's order; hands each machine's updater the release to install |
| `shared/db/migrations` | Rule 6 "dev-only Down"; contract allowed with a marker; CONCURRENTLY required | Expand-only in `migrations/`, contract in `migrations/post/`; a lint for CIC without a prior drop; every Down script run in CI (#1598) |
| Update rehearsal | From the latest release | From the latest, from the oldest release a home still runs, and from one updater to the next; a large-database profile |
| Desktop apps (Tauri) | The app replaces itself and the server payload | The app's own updater updates the window only; the server payload goes through the updater (§4 and §2.12 list the per-platform details) |

### 6.3 Rank by impact

| # | Change | Fixes | Effort | Status |
|---|---|---|---|---|
| 1 | **No automatic restore**: a failed update keeps or undoes its schema with Down scripts. #1598 still restores by itself when Down fails, data was lost, or the way back is unknown; turning those three into "stop and ask" finishes it | G1, A4, G4, J1 | small | in flight (#1598); the last three cases open |
| 2 | **Unstick the homes that cannot see releases** (§6.4) | B2, J2 | small | open |
| 3 | **Small bugs that cause outages:** confirm a `maktaba-server update` switch (P4); try keep and Down before the dump on the launcher's path (P3); cap the guard's deadline (P5); time out an order the app never answers (P10) | P3, P4, P5, P10 | small each | open |
| 4 | **Hold automatic installs while the catalog is stale**, and alert (#1597) | B3, H1 | small | #1597 in flight; the hold is open |
| 5 | **A pre-flight replication gate and plain index builds while offline**: no table sync, copies quiesced; `CREATE INDEX` (not CONCURRENTLY) in offline migrations | E3, E4, E8, D2 | small–medium | open |
| 6 | **`maktaba-updater`, the separate judge** (§6.1 items 1–5), with a kept previous copy of the launcher and updater outside the app bundle | A1, A2, A5, I4, K1, F2, P1, P2, P6, P15 | large (a story per install kind) | open |
| 7 | **Expand-only migrations before the switch; post-commit contract migrations** | A3, A4, E5, E7, and the outage of every migrating update | medium–large | open |
| 8 | **Clones for the rehearsal and the safety point** | D1, D3, G2 | medium | open |
| 9 | **The own signed feed with a withdrawn list, staged rollout and a minimum updater version** | B5, B7, B8 | medium | in flight (the feed); the rest open |
| 10 | **Rehearse from older releases, a large database, macOS and Windows runners with the real apps, and a release whose own rollback is broken** | I1, I3, R1, R2 | medium | open |
| 11 | **Subscriber-first migrations on the machines** | E8, K2 | medium | open |
| 12 | **Functional health probes in the contract** | F1 | small | open |
| 13 | **Fleet visibility** (counts from the feed's reads; opt-in results) | H2 | small, after a privacy decision | open |

Items 1–5 are small and close the incident class on their own. Items 6 and 7 are the structural
change: after them, no release decides its own fate, and a migration failure is not an outage.

### 6.4 The way there for homes in the field

1. **Homes that cannot see releases** (anything before #1592) read
   `GET /releases?per_page=30` with an 8 MiB cap. There are two ways to reach them:
   - **A person installs a release with #1592 by hand.** It must be guided: the Updates page
     cannot tell these homes anything, because they cannot read the feed, so the guidance has to
     reach households through the release notes, the website and support.
   - **Shrink the newest 30 releases' list below 8 MiB.**
     - On 2026-11-24 it was 10,474,679 bytes: 4,964 assets, 1,320 of them `lang-hunspell-*`
       packs (44 a release).
     - Moving the packs out of the GitHub releases (into the packs' own index, or one release of
       their own) would bring the list to about 7.7 MB, estimated from the measured 2.1 KB per
       asset. That is under the cap with little room. Every home would then see releases again
       by itself.
     - The risk: a pack fits only the server version it was built with
       (`internal/updater/packs.go:4`), so editing past releases would break pack installs for
       homes on those versions unless the packs stay reachable at their recorded URLs. Doing it
       only for new releases takes about 30 releases to fill the window.
     - This needs a decision and a check of how packs are found before anything is deleted.
2. **The bridge release.** The first release with `maktaba-updater` is installed by each home's
   *old* in-server updater, so that one update is still judged by old code. Keep it cheap:
   - **No migrations.** Its update is a restart, so the old path's riskiest step does not run.
   - **It only installs the updater beside the server** and hands the next update to it.
   - Hold migrations back from the releases just before it, so homes that take them through the
     old path meet as little risk as possible.
   - Set `min_upgrade_from` on the next releases to the bridge, so no home skips it.
3. **From the bridge on**, the updater judges every update. The in-server path stays as a fallback
   for two releases (an updater that will not start), then is removed.
4. **Homes stuck on a release before the bridge** for any reason (offline, paused) reach it by the
   same route as item 1. A release before the bridge that fails is undone by its own code, as
   today, so the rehearsal from older releases (item 10 of §6.3) matters most until the bridge is
   out.

### 6.5 Risks and costs

| Risk or cost | How large | What limits it |
|---|---|---|
| **A bug in the updater now hits every update** | High. It is the single judge | It changes rarely; the self-update qualifies first and keeps the previous updater; its own rehearsal matrix |
| **The health contract and `migrate` CLI become an API between versions** | Medium | A protocol number; release.json says which it needs; CI runs the newest updater against the previous releases and the oldest supported updater against the new release |
| **One more process on each install kind** (launchd, systemd, Windows service, Docker entrypoint, the Tauri app) | Medium: a story per kind | The launcher already starts processes on each; the updater is one more child |
| **Expand-only discipline** | Two releases for a drop or rename; schema clutter for a release | A lint; `make compat-prev` as a gate; the post-commit set |
| **Clones depend on the filesystem** | ext4 and some Docker volumes cannot clone | Fall back to today's dump; the Updates page says which a home has |
| **A clone's blocks diverge as the library writes** | Disk use grows until the clone is dropped | Drop it at commit plus a grace period; it counts in the room check |
| **Moving code risks regressions in a path that only runs during updates** | High while it moves | Move behind the bridge; the in-server path as a fallback for two releases; the rehearsal on every PR that touches it |
| **Effort** | Items 1–5 of §6.3: days. Items 6–8: several stories each across every install kind | Items 1–5 first; they stop the outages while 6–8 are built |

