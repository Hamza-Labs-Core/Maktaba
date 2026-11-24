# Automatic updates: the server and the cluster

Maktaba installs its own releases: the home first, in a quiet window you choose, then every
machine that joined it, one at a time, each checked before the next. Nothing is installed that the
machine did not verify itself, and a release that does not hold is rolled back by itself. This page
says what happens, what you can control, and where to look when something goes wrong.

The desktop and mobile apps' own updates (their badge, the signing key, the mobile banner) are in
[auto-update.md](auto-update.md). When an update fails, the
[update runbook](cluster/runbook-updates.md) has one entry per failure. The design is
[Epic 35](../specs/epics/35-cluster-full-nodes-and-updates/README.md).

## What is automatic

| | Automatic | Yours to decide |
|---|---|---|
| Finding releases | The home reads the signed release feed every 6 hours (and when you press **Check now**): [How a home finds a release](#how-a-home-finds-a-release). | The channel (`stable` or `beta`). |
| Installing on the home | In the quiet window, once the release is old enough, with its migrations rehearsed first. | Turning it off, a pin, a deferral, skipping a version, **Update now**. |
| Installing on the machines | After the home commits the release: downloaded from the home, verified on each machine, switched one at a time. | What a machine's failure does to the others (`node_failure_policy`), when a machine that was offline catches up (`catch_up`). |
| TV | Plain server installs keep TV playing across the restart; the streaming software then moves to the new release at programme boundaries, without an approval. | The viewers a restart may interrupt (`tv_staging.quiet_max_viewers`, 0) on installs that restart whole. |
| Going back | A release that does not stay healthy is rolled back: a restart onto the previous release, the failed release's migrations undone first by their Down scripts when they ran (or kept, when they are additive). No backup is put back by itself: when the Down scripts cannot undo them, the update stops and asks. | Rolling back by hand, restoring an older backup. |
| A desktop app that runs the server (Maktaba.app, the Windows or Linux app) | It downloads and stages the release the home orders, says when it installs, and installs and restarts by itself: for an **Update now** as soon as the home has committed the release, in the window for an automatic update. No click on the machine (decision D1, 2026-10-27). Machines follow once the home commits: while the home updates, the run shows them waiting for it; a home that rolls back orders none (2026-10-28). | The same as any machine. |

Automatic installs are on by default (`rules.updates.auto_install`). With them off (Admin →
Updates → Pause, or Settings → About) the home only checks and notifies. `MAKTABA_UPDATE_DISABLE=1`
turns the checks off altogether.

## How a home finds a release

A home reads its **release feed**: one small signed file per channel and platform, which lists
only the releases a machine of that platform can install. It never reads GitHub's list of every
release. That list had grown to 10.5 MB for 30 releases of about 190 files each by 2026-11-23,
past every home's cap, and no home saw a release for a day.

- **Where.** `https://maktaba.tv/updates/v1/<channel>/<platform>.json`, with its Sigstore bundle
  beside it (`<platform>.json.sigstore.json`), and `<channel>/index.json` listing the channel's
  platforms. Platforms: `darwin-arm64`, `darwin-amd64`, `linux-amd64`, `linux-arm64`,
  `windows-amd64` (a NAS's container reads `linux-*`). maktaba.tv's Worker serves them as static
  files (`web/packages/website`); a cache holds them for a minute.
- **What it lists.** The newest 10 releases of the channel (`stable`: stable releases only;
  `beta`: every release), each with only that platform's files: the three signed files, the
  server archive, the pipeline's wheel and constraints, the migrations, the component index, on
  Linux the runtime payload, and the apps' `latest.json`. Each file has its download address,
  size and SHA-256. Each release also has its `checksums.txt` and `release.json` digests, its
  migration set (`migrations_set_sha256`) and its published time. A pulled release stays listed,
  marked `withdrawn` with the reason. A 10-release feed is about 25 KB, about 4 KB as served
  compressed.
- **Signed like a release.** `update-feed.yml` signs each file keyless with `cosign sign-blob`.
  A home verifies it with `shared/go/releaseverify` (`VerifyFeed`): the Sigstore bundle against
  the trusted root, with its Rekor inclusion proof, and the certificate exactly
  `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/update-feed.yml@refs/heads/main`.
  The trigger must be `workflow_run`, `workflow_dispatch` or `schedule`, on a GitHub-hosted
  runner, with the repository's numeric ids. Then it checks the content: the channel, platform
  and repository it asked for, valid tags each listed once, no pre-release on stable, and every
  file a plain name at that release's own GitHub address. The files are still downloaded from
  GitHub Releases and verified as before ([What is verified](#what-is-verified)). The feed only
  says which releases exist.
- **The home's machines.** The home reads the feed of its own platform, which says which releases
  there are, and the feed of every platform its machines run, for their files. A machine's platform
  with no feed is passed over. Once a release verifies, the catalog also keeps every other file its
  signed `checksums.txt` lists (packs, first-party plugins, other platforms' files), each from
  beside `checksums.txt`. So a machine's update and the home's mirror find any of them by name,
  each checked against `checksums.txt` when it is downloaded.
- **When the feed is not used.** A feed that cannot be reached (no answer, or a status other
  than 200, a 404 included) is passed over: the home reads GitHub's list instead, in pages of
  10, for that check. So does a feed signed before the last one the home read (maktaba.tv rolled
  back, or an old feed served again), or more than 21 days ago (`update-feed.yml` signs it again
  every week). A feed that is reached but fails its checks is never
  passed over: the check fails with `update feed refused: …`, and nothing of it is used. That
  covers an unsigned feed, a wrong signature, a wrong identity, content that disagrees with the
  request, or a file over 1 MiB. Admin → Updates shows where the last check read
  (`last_check_source`: `feed` or `github`) and why the feed was passed over (`feed_error`).
- **Who reads it.** The home's catalog, the run's re-check before it starts (a release pulled
  since is withdrawn), the About page's check and `maktaba-server update` all read it. Homes on
  v0.3.153–v0.3.161 read GitHub's list as before, and nothing changes for them. A fork
  (`MAKTABA_UPDATE_REPO`) reads its own GitHub list: maktaba.tv's feed is signed by this
  repository. `MAKTABA_UPDATE_FEED=off` turns the feed off. The desktop and player apps keep their
  own updater feeds (`latest.json`, `latest-player.json`), one small file each
  ([auto-update.md](auto-update.md)).

### When the feed is written

`update-feed.yml` writes the whole feed after each release, once the release is published:

1. It runs when `release.yml` or `desktop-release.yml` completes. The release is published by
   whichever of them finishes last, so a release still a draft writes nothing, and the other
   workflow's end runs it again.
2. It reads the newest 30 published releases (`tools/release/release-feed-inputs.sh`). It
   verifies each one's signed files as a home does, and writes both channels' feeds
   (`release-feed generate`). A release that does not verify is left out. When it is the new
   release, the run fails.
3. It signs every file, then verifies what it signed with the homes' own code
   (`release-feed verify`). It uploads the result as the `update-feed` artifact and starts
   maktaba.tv's deploy. Every deploy serves the artifact of the newest `update-feed.yml` run on
   main (started by its own triggers, never a pull request's run, and holding only feed files).
   When there is none, it carries over the live feed, so a deploy of the site never drops it
   (`tools/release/website-feed.sh`).
4. **The live check:** it reads `https://maktaba.tv/updates/v1` until every platform's feed lists
   the new release, signed and verified as a home verifies it (`release-feed live`). It fails the
   run after 100 minutes.

It also runs every Monday, so the newest artifact (kept 90 days) never expires. No workflow
pushes tags: the scheduled release session still does ([How a release is made](#how-a-release-is-made)).

### Pulling a release

A bad release is pulled from the feed, not marked as a pre-release on GitHub:

```sh
tools/release/pull-release.sh 0.3.158 "its migration times out on large databases"
```

The script adds the release to the repository variable `UPDATE_FEED_WITHDRAWN` (a JSON object of
version to reason, `{"0.3.158": "…"}`) and starts `update-feed.yml`. The next feed lists the
release as `withdrawn`. Every home marks it withdrawn at its next check, or at once in the re-check
before a run, and never installs it. `--restore 0.3.158` takes it back out of the variable, for
homes that have not seen the pull yet. A home that marked it withdrawn keeps it withdrawn: the next
release replaces it. Homes before the feed
(v0.3.153–v0.3.161) still learn of a pull only from GitHub: mark the release a pre-release there
too while any such home remains.

## The quiet window, and what holds a release back

A release installs by itself only when every one of these holds:

- it is on your channel and newer than the cluster's release
  ([which release is newer](#which-release-is-newer));
- it is at least `min_release_age_hours` old (24 by default: a release pulled within a day is
  never installed);
- it is not above your pin (`pinned_version`), not skipped (`skipped_versions`), and the
  deferral (`deferred_until`) has passed;
- the home's local time is inside the window (`window`, default `03:00-05:00`), at least 30
  minutes before its end;
- no other run is active, and no TV change is being swapped;
- the list of releases was read in the last 12 hours and 30 minutes (two checks at the default
  cadence; two checks and 30 minutes at a slower `MAKTABA_UPDATE_INTERVAL_SEC`). One older than
  12 hours and 30 minutes is read again first, at any cadence, so a home that slept, or started a minute ago, installs at once; only
  when that fails does the install wait (`catalog_stale` on Admin → Updates; tried again every 15
  minutes, and the admins notified once it lasts half an hour, or at once when the checks keep
  failing, once a day at most), because a stale list may not know the newest releases, nor that one
  was withdrawn. On 2026-11-24 a home whose checks had failed for a day installed a release older
  than one that had rolled back. **Update now** still installs. A home that never read the list, or
  whose background checks are off (`0`), is not held;
- it does not need your approval (a release whose migrations an older home could not run,
  `breaks_previous_home`, waits on Live TV → Changes: **Approve and update now** or **Skip this
  version**).

Admin → Updates shows what the scheduler waits for ("Not started now: …"). A started run may finish
its TV swap and its rollout up to `window_overrun_minutes` (120) after the window; then the
rollout holds until the next window.

## Update now, skip, defer, pin

- **Update now** (Admin → Updates, or Settings → About): installs the newest release at once,
  ignoring the window and the release age, through the same run (verified, rehearsed, rolled back
  if it does not hold). `POST /api/admin/system/update {"confirm":true}` does the same. **Update
  now** naming a blocked release unblocks it.
- **Check now** looks for a release at once.
- **Skip** a version (Admin → Updates, or `skipped_versions`): never installed automatically; a
  newer one is.
- **Defer** (`deferred_until`): nothing installs by itself before that instant.
- **Pin** (`pinned_version`): stay at or below a version (never below what the machine already
  installed).
- **Pause** turns `auto_install` off.

The settings are in [Settings → Rules → Updates](#settings), and the window, the channel, the
release age and the pin also on Settings → About.

## Channels

- **stable** (default): releases only.
- **beta**: pre-releases (`-beta`, `-rc`) too.

A build's channel follows its version (a pre-release suffix means `beta`), overridable with
`MAKTABA_UPDATE_CHANNEL`. Switching from `beta` to `stable` never installs a lower version: the
cluster stays where it is until stable passes it.

## What a run does

1. **The home prepares** the release: downloads and verifies it (the signature, every file),
   stages it, runs it once (`--version`), and rehearses its migrations on a scratch copy of the
   database (`rehearse_migrations`, up to `rehearse_max_db_gb`): a migration that fails on the
   real data stops here, and nothing changed.
2. **It waits for its moment**: inside the window, with the cluster's jobs drained
   (`drain_timeout_minutes`; transcriptions resume from their checkpoints). A plain server
   install's TV keeps playing across its restart, so it waits for nothing more; any other install
   restarts live TV with it, so it waits until nobody watches ([TV](#what-happens-to-tv)).
3. **The home backs its database up, then switches** to the release and restarts
   ([the database during an update](#the-database-during-an-update)). A release that adds no
   migration takes no backup at all: the switch is a restart of seconds. One that adds migrations
   is backed up while the running release keeps serving (`backups/pre-<old>-to-<new>.dump`, checked
   to read back; Admin → Updates shows the **Back up** step and how much it wrote); only the
   switch waits for it. The new release's start then migrates at once, and undoes a
   migration that fails with its Down script. Its health is watched (every role running, the API ready and answering as
   the new version) for 10 minutes, and its TV change goes on the air; then the release is kept
   (committed).
4. **Every machine follows**, after the home commits: they download and verify in parallel (at
   most 3 at once), then switch one at a time — the machines without TV streams first, then the
   ones carrying streams, inside the window — each drained of its jobs for its own switch and
   watched (`node_health_timeout_minutes`) before the next. A machine that fails rolls back by
   itself, stays drained and is reported; `node_failure_policy` decides the rest (`hold_platform`,
   the default, holds the other machines of its platform; `halt` holds them all; `continue` goes
   on). A machine offline meanwhile is caught up when it comes back (`catch_up`: `immediate`, at
   any time except a machine carrying TV streams; `window`, only inside the window), and so is one
   whose part failed for a reason since mended, or one its new release's schema paused.

A desktop app's machine is ordered as soon as it is prepared; its app downloads the release while
the others go, and it takes its turn once staged, with no click. A run waiting only for apps to
download gives way to a newer release. An app that never stages its order (not running on that
machine, or its start with the order's link failed, which the run's log now says) ends the order
`app_no_answer` at the time the run installs by (its window's end), else three hours after the
order, and never within 50 minutes of it, nor before the app had 50 minutes with the machine
awake (since it woke, or the server started); nothing changed. It is the machine's, not the
release's: it counts no attempt, and the next window tries again. The Linux app's runtime order has
nothing to stage, so an app that does not answer it within 10 minutes no longer holds the run: it
goes on, and the server switches its runtime itself at the go. A power cut at any step is
recovered at the next start from the update's journal (`updates/journal.json`).

## What happens to TV

- **Plain server installs** (the launcher over `bin/releases`: `setup --self-managed`, the
  `.deb`/`.rpm`, the Homebrew formula) **hand the TV over across the restart** (story 35.7): the
  front door and the TV slots keep serving while every other process restarts on the new release,
  and the new supervisor adopts them (`updates/handover.json` lists each one with the start time
  of its process, so a reused pid is never taken for it). The run then waits for no quiet TV. The
  front door runs on only into a release that speaks its `edge_protocol`; when the new release
  changes it, the front door restarts too. A release a running TV process uses is never pruned.
  On Windows the TV processes stay in the launcher's job across the restart (a launcher from
  before the handover: the boot's named job `Local\Maktaba-tv-<boot id>-<install>`); where the
  job lets no process leave, every restart is whole. A Linux or macOS launcher from before the
  handover would stop what the server hands over: the committed release's launcher replaces it at
  the next update, by itself.
- **Every other install** (the desktop apps, the Linux runtime copy, Docker) restarts whole: the
  home restarts only while at most `tv_staging.quiet_max_viewers` (0) viewers watch (checked before
  the drain and again right before the switch); a machine only while its own streams have no
  viewers, and the machines keeping streams on air go last. At the window's end the run stops with
  `tv_busy` and is tried again in the next window.
- **The streaming software then moves to the new release** (story 35.6), the way any TV change
  goes on the air (Live TV → Changes): a change of the streaming binary alone, from the updater
  (`source.kind = auto_update`), staged next to what airs, checked, and swapped at programme
  boundaries. Only this change goes on the air without an approval; every other change keeps its
  rule. The release commits once the change is live.
  - **Its check fails** (decided 2026-10-28): the update stays installed, and only
    the TV waits. The new server, web app and pipeline run; the live TV stays on the streaming
    software it ran; the change stays a draft with its report; the release is not blocked. The run
    ends as done, and Admin → Updates says "Installed. TV held on <old version>: its check
    failed", with what failed, **Retry** (check it again: a pass swaps at a programme boundary as
    usual) and **Drop** (withdraw it: the TV stays on the old streaming software until a later
    release, whose change replaces a held one by itself). The home's row says "TV held on …", and
    the notice `update.tv_held` links there. Both actions are admin-only. That release's streaming
    software is kept on disk while the TV runs it (`keep_versions`, a desktop app's kept payload, a
    runtime directory).

  What still rolls the release back:
  - `failed_tv`: the swap itself failed, or the TV failed on the air after it switched (rolled
    back, Epic 33.9). The release rolls back and is blocked; the admin is told.
  - `tv_change_pending`: it waited behind another change awaiting your approval until the window
    and its overrun ended. Nothing counts against the release; the next window tries again.
  - `tv_timeout`: not on the air within `boundary_max_wait_minutes` + 10 minutes of its staging.
    The release rolls back; the attempt counts.
  - `tv_change_cancelled`: an admin cancelled it. The release rolls back and stays blocked until
    **Retry**.

  A rollback for any reason takes the TV change back first: cancelled before it airs, rolled back
  (warm while the previous slot is warm, else cold) once live. Nothing is swapped when TV staging
  is off, no TV release exists yet, or the live TV runs that binary already.

## What the machines do

- **They download from the home** (story 35.12), not each from GitHub and PyPI, so a machine
  without internet access updates too. Nothing is trusted for coming from the home: every file is
  verified on the machine exactly as one from GitHub.
  - **Release files.** The home keeps a copy of each release its machines install
    (`<data_dir>/updates/mirror/<version>/`): the signed small files, and each platform's archive,
    pipeline wheel and constraints, fetched once ahead of the machines (or taken from the home's
    own staged release when its copy verifies) and kept only when their digest is the one the
    signed `checksums.txt` lists; each kept copy is checked again once per process, and one a
    power cut or a damaged block spoilt is fetched again. A machine downloads
    `GET /api/cluster/updates/artifacts/{version}/{name}` (signed with its delegation key; range
    requests; at most three downloads at once per machine) and falls back to GitHub after three
    failures, at once for a file the home does not hold, and on the next try for a file whose copy
    from the home failed its own verification. The copies follow `keep_versions`.
  - **The wheelhouse** (`wheelhouse`, on by default). The home downloads each release's pinned
    pipeline packages once for every platform its machines run (`pip download --only-binary=:all:
    --require-hashes`), and a machine builds its pipeline environment from them alone
    (`--no-index`); a package with no wheel for its platform comes from PyPI, logged. pip checks
    every file against its hash in the release's constraints, wherever it came from. Machines that
    use CUDA builds of torch take those from PyTorch's index.
  - **The Sigstore trusted root.** The home passes Sigstore's TUF repository on
    (`/api/cluster/updates/tuf/…`); a machine that cannot reach the public one reads it there, and
    its TUF client verifies every file from the root it carries.
  - Not through the home: a desktop app's own bundle, which its updater downloads from the release.
- **Worker-only machines become full nodes** on their next update (story 35.8 AC8): on the
  machine's turn the home sends its worker `update.convert`; the worker downloads the release
  from the home's mirror (with a download token the home puts in the command: that machine,
  that release, 4 hours, since a worker holds no delegation key), GitHub otherwise, verifies it
  itself (the pinned identity,
  `checksums.txt`, the server binary against `release.json`) and runs that release's
  `setup --node --converting`, which installs the side-by-side layout and the launcher's service
  and removes the old worker units once the node answers; when it misses that wait, or the
  machine stops before it removes them, the full node's own start removes them (no second
  pipeline stays), and a retried conversion picks up the release it installed. Admin → Updates
  shows it as "becoming a full node". The download token is a bearer token, not bound to the
  connection: whoever holds it can fetch that release's files (public release assets, verified
  on the machine anyway) as that machine, within its download limit, until it expires or the
  machine is revoked. The home's channel log never shows it, the worker keeps it in memory only,
  it never goes to GitHub, and the mirror never redirects; on a home reached over plain HTTP it
  crosses the LAN in clear, as the release files do. A worker whose pipeline predates the command shows "worker-only — re-run the
  installer once" ([join a machine](cluster/join-a-machine.md#a-worker-only-machine-becomes-a-full-node)).
- **Skew.** No machine is ever ordered to a release the home has not committed. A machine whose
  code does not fit the home's database pauses (its web address shows the paused page) until its
  update lands; the Updates and Machines pages say which one to update.

## When a release does not hold: rollback, backups, restore

A release that does not stay healthy is **rolled back**: the previous release runs again, at once.
Nothing is dumped before it starts. When the failed release added no migration, or its migrations
never ran, that is all: a restart onto the previous release, which knows the database as it is.
When its migrations ran, the database goes back by their own Down scripts: it stays as they
left it when every one of them is additive and the previous release runs on a newer schema;
else their Down scripts undo them, the one that failed part way by its own Down script first
(`maktaba-api migrate undo-failed`), back to the slot the database was at before the update
(recorded as `schema_before` when the migrations start). They never go below that slot: a
schema an earlier rollback kept is left as it is, still kept for the previous release, and when
its slot is not recorded no Down script runs.

A layout install (the `.deb`/`.rpm`, `setup --self-managed`) keeps the release before's
migrations in `bin/releases/<v>/` and undoes the same way; until 2026-11-24 its rollback
could only put the backup back.

**No backup is ever put back by itself.** When those Down scripts fail (`db_down_failed`), when
a migration that ran deleted or overwrote data its Down script cannot give back
(`db_held_lossy`: migration-lint refuses such a migration unless it says so), when the way back
cannot be told (`db_held_unknown`), or when the launcher's guard gave up on the new release
after its migrations ran, the update **stops and asks**. The database record becomes `held`:
the database stays as the migrations (and any Down scripts that ran) left it, the previous
release is switched back in, and every start stops on the held database (`db_held`) until a
person chooses:
- **Put the backup taken before the update back**, on the recovery page (`/recovery`, which
  the supervisor serves while the library is stopped: it names the backup) or with
  `maktaba-server restore --backup <name> --confirm`. The previous release runs on it; changes
  made since that backup are lost.
- **Install a release newer than the one that failed**, which fixes it. Its start goes on from
  the held database (its migrations run what is missing), with the same backup kept.

Each case is logged at level=error, and the update attempt's code says which on the Updates page.
A release from before 2026-11-24 that meets a held database refuses it as older than the
database (`db_refused_older`) and stops the same way, putting nothing back. A restore mark that
such a release, or its launcher, wrote (`restoring` with no person named) is held too by every
release since: the dump is not put back. Only an update to a release with this rule is
protected by it: a release from before it runs its own database step and puts the backup back
as it always did.

The first update to such a release is protected too, under the launcher and the release that were
there before it (a layout install, a container and the Linux app keep their launcher). Their
guard, and the older release coming back by itself, mark the record `restoring` only when it names
a backup (`dump`); one that names none they leave alone, as every release with a guard has. So
until its watch passes, the new release's record keeps its backup's path where only releases
with this rule read it (`dump_unconfirmed`), and names it once the release is confirmed, or when
the update is held. Given up on, the older release refuses the database as newer than its own
(`db_refused_older`) and stops, with nothing put back; its own recovery page and `restore` still
put the backup back when a person asks. Update rehearsal 21 runs the previous release's real
launcher and server against it.

**When the switch back fails** (the previous release gone, a pointer that cannot be written), a
held record stays held: its Down scripts may have run in part, and the failed release never
migrates and runs on that by itself.

The automatic restores of 2026-11-14, 2026-11-23 (v0.3.158) and 2026-11-24
(v0.3.157) each kept the library stopped for about three hours. The release
is then blocked until a newer one appears or an admin unblocks it (Admin → Updates → **Try it
again**, or **Update now** naming it). A release that rolled back (or whose migrations failed on
the rehearsal's copy) also holds back, until an admin retries it, every release below it, and
every newer release that carries the same migrations (release.json's `migrations.set_sha256`,
since 2026-11-24): the home waits for a newer release that changes them, and never steps down
through the releases in between, which have none of the fixes made after the failed one (on
2026-11-24 a home chose v0.3.157 after v0.3.158 had rolled back, and rolled back again). The
Updates page says which release holds the others back; **Update now** naming a release installs
it all the same. On a machine, the launcher's guard does the same when the
new release does not confirm in time (15 minutes) or stops three times in ten minutes. While the
new release's database step and watch run, its watch keeps that deadline ahead,
since 2026-11-24 within the running time the release that switched gave it in the guard
(`max_run_seconds`: the watch's own longest wait, its start steps' cap and its bound, plus 45
minutes; a day for a guard with none). The launcher counts that time itself while it runs, across
its starts, and never extends it: a release that hangs with its watch alive is given up on there,
wherever the clock is set. A machine switched off or asleep, or a clock set forward, is no time the
release had: a good update is not taken back after a long power cut. When there is nothing to go back
to (the kept copy gone, a full disk), the release stays up and its time starts again, twice as long
each time, so the library and its recovery page are there for a person. A rollback under way has six
hours more of its own (`rolling_back`), so its Down scripts are not stopped part way. A launcher
from before it counts nothing; the release stops pushing the deadline itself at the same time.

### The database during an update

| The release | Before the switch | The switch | Its rollback |
|---|---|---|---|
| adds no migration (none of its migrations is one the database lacks) | nothing | a restart | a restart |
| adds migrations | the backup, taken while the release before keeps serving | a restart, then its migrations | the schema kept, or undone by its Down scripts, then a restart; else held: the library stops and a person chooses (the backup, or a newer release) |

- **Which releases add migrations.** Right before the switch the home compares the staged
  release's migrations (the verified `maktaba-migrations-<v>.tar.gz` of its `release.json`) with
  the newest migration the database holds. A release whose start finds none to run records that
  the database still holds the previous release's migrations, so the previous release can run on
  it again as it is; the launcher's guard then has nothing to hold.
- **The migrations never wait long on another session.** Before an update's migrations the home
  ends the other client sessions on its database. While they run, it ends any session one of
  them has waited on for more than 10 seconds, and the log names it: a machine's copy making
  itself anew, whose table sync holds one snapshot for the whole copy of a large table, or a
  machine's session that came straight back. A `CREATE INDEX CONCURRENTLY` waits for every
  transaction older than it. Until 2026-11-23 such a wait reached the 2-minute lock timeout
  three tries running, and the update put a 35 GB backup back (2 h 44 min). An index build cut
  short leaves an INVALID index; the next try drops it and builds it again, where `IF NOT
  EXISTS` had taken it for done.
- **The machines' copies wait, then pause (since 2026-11-24).** An automatic update does not
  start while a machine is copying a table of the home's database (its first copy, or one made
  anew: `machine_copying` on Admin → Updates); **Update now** does not wait. Before the
  migrations the home waits for the table syncs still running, 10 minutes at most whatever the
  database does, then keeps the machines' copies out until the schema is settled: `pg_hba.conf`
  rejects their replication roles (`/^maktaba_rep_`; never the app role) and their sessions are
  ended. It is `pg_hba.conf` and not the roles, since a machine asks for its copy again and again
  and the home's answer (`cluster_node_replica_grant`) sets `LOGIN` on its role.
  - They come back when the database's record says the schema is settled: the migrations ran, or
    failed and were kept or undone, or the restore a person asked for is done. Each goes on from
    where it stopped (its slot kept the changes, under the home's cap).
  - They stay out, across restarts, while the record says `migrating` (the next start goes on),
    `restoring` or `held` (a person has not chosen yet), and when a rollback's Down script failed.
  - Nothing stays out for good: the pause is a marker in the data directory
    (`maktaba-copies-paused`) that `pg_hba.conf` is written from. A release from before it writes
    the file without the rejects at its start, and this one looks every minute whether the copies
    are still out on a settled schema.
  - When they cannot be kept out (the file cannot be written, the server does not reload it), that
    is logged at level=error and the migrations run all the same, as before this rule: what they
    wait on is ended, and their index builds wait on no snapshot.
  - Only a table sync that streams now counts. Its slot is permanent: one left by a machine that
    went away holds nothing up, and goes when its machine is back, or when its WAL is past the
    home's cap (`rules.cluster.replica_wal_keep_gb`) or lost, which the home looks at on its own.
- **Index builds are plain while the library is stopped (since 2026-11-24).** The migrations
  run before any role starts, so their `CREATE INDEX CONCURRENTLY` and `DROP INDEX CONCURRENTLY`
  run without `CONCURRENTLY` (`MAKTABA_MIGRATE_OFFLINE=1`, set by the database step and the
  rehearsal on a copy). A plain build takes a lock that only holds writes to that table
  back, waits for no older transaction, and rolls back whole: it leaves no INVALID index. The
  stopped library writes nothing. A joined machine's own sessions (`maktaba_node_*`) are ended
  when the migrations start and may come back: one that writes to a table while its index is
  built waits for the build, and one a migration waits on for more than 10 seconds is ended; the
  machine's workers try again once the home answers. The migration files keep `CONCURRENTLY`; a function's body or a `DO` block is
  left as it is. An INVALID index an earlier try left is dropped, and built again, plainly too.
- **When the migrations fail part way.** The library stays stopped and the start says what
  happened: `db_kept` (nothing put back: the changes so far are additive and the previous
  release runs on them), `db_undone` (their Down scripts undid them), or `db_held` (nothing
  could take it back by itself: the update stops and asks, as above, and the update attempt's
  code says why: `db_down_failed`, `db_held_lossy` or `db_held_unknown`). The update's watch
  then rolls back to the previous release, and the release is blocked as above.
- **The backup is taken on the side.** `pg_dump` reads one snapshot, so the library keeps serving
  while it runs. Writes made between its start and the switch (a minute or two, with the jobs
  drained) are not in it. A backup that fails switches nothing (`db_dump_failed`: the release
  before goes on, and the run says why). A release installed some other way (the app's own
  updater, by hand) that adds migrations still backs up at its start, as before.
- **The backup format.** Every dump is a directory (still named `*.dump`): one file per table,
  written by up to 8 `pg_dump` workers at once (half the machine's cores) and read back by as many
  `pg_restore` workers, each file compressed with zstd at level 1, else lz4, else gzip at level 1
  (whichever the `pg_dump` at hand was built with: the bundled one has gzip only). A dump of before
  (one file) is still listed, restored, moved and pruned as it was.
- **Every backup is whole, the vectors too.** Semantic search's vectors (`segment_embeddings`,
  most of a large library: 31 of 47 GB on the home that measured it) are what made a backup slow:
  `pg_dump` writes a table with one worker, as text. So the dump writes them apart, in binary,
  in as many ranges as it has workers, all read at once in the same snapshot as the rest
  (`chunk.public.segment_embeddings.<n>.bin` files in the dump's folder); putting a backup back
  loads them, as many at once, after the tables and before their indexes are built. Nothing is
  left out, so nothing has to be made again after a restore.

**Backups.** Before every update that migrates, the home dumps its database into
`<data_dir>/backups/pre-<from>-to-<to>.dump`. It keeps `backups_keep` (3) of each kind: those, the
bundled database's daily `maktaba-<time>.dump`, the database a failed update left in releases
before this rule (`failed-*`) and the one a restore replaced (`replaced-*`).

**Restoring a backup** (story 35.5 AC8) puts the home back as it was when a backup was taken:
Admin → Updates → *Backups of the library database* → **Restore** (type the backup's name to
confirm), or on the home:

```sh
maktaba-server restore                                  # the backups and the releases kept
maktaba-server restore --backup pre-0.3.5-to-0.3.6.dump # what restoring it would do
maktaba-server restore --backup pre-0.3.5-to-0.3.6.dump --confirm
```

The home restarts on the kept release whose schema is the backup's (the dump's newest applied
migration), and that release puts the backup back before anything else runs; the database it
replaces is set aside first (`replaced-<version>-<time>.dump`). **Every change made since the
backup was taken is lost.** It refuses while an update runs, and when no kept release has the
backup's schema (it names the release to install first). The machines copy the restored database
anew.

## Which release is newer

One rule for every updater (the home's choice, the plain server, the runtime, the apps' server
payload, the `update` commands): a release numbered higher but signed before the one running
here, on the same major.minor line, is an older build (an rc of a version already released,
tagged after it) and is passed by: not a downgrade, not a replay alarm, no notice
(`older_build`). A higher major.minor always counts as newer, so a hotfix on an older minor still
moves up. The same version with other checksums stays a refused replay. A release seen before its
signed files were attached (they come a few minutes after it is created) is verified as soon as
GitHub lists them.

## When a run cannot finish

- **A failure that may pass** (a download, an install that did not take) is tried again at once,
  then 15 minutes later, then in the next window; each window counts one attempt. A download that
  fails (HTTP, a timeout, the disk) is `download_failed`: retried twice at once, never a
  verification failure.
- **A failure that needs a person** (no app in the release for that machine, not enough disk, an
  install that is not self-managed, an app that cannot change itself or was built locally, a trust
  root that expired, no usable Python for the pipeline) starts no new run until the next window or
  an admin acts, and is told once.
- **A release that bundles another PostgreSQL major** than the one a machine runs is never started
  on its data: the signed `release.json` says each app's major (`app_postgres_major`), so a
  runtime install and an app's install refuse it before anything is installed (`pg_major_change`,
  needs a person, the release not blocked).
- **Only a verification failure or a rehearsal that failed on the real data** blocks a release; a
  rehearsal that could not run is an attempt.
- **A home whose window always has a viewer** (or is always outside it) is told after three
  windows ("stuck"), and the page offers **Install now**.
- The dumps are kept to `backups_keep`; scratch copies and dumps a crash cut short
  (`*.dump.partial`) go once nothing was written to them for an hour.
- **Before any dump** (nightly, before an update, a restore's set aside) the oldest dumps go
  until the new one fits: it must leave 5% of the disk free (at least 2 GiB), and all the dumps
  together stay within a fifth of the disk. The newest dump of each kind always stays. When it
  cannot fit even so, the dump is not started and the update stops at its database step with the
  reason (`db_dump_failed`: not enough free space): free some space or move the data to a bigger disk.

## How each kind of install takes a release

([install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md) has the
details.)

| Install | Automatically | Still needs a person |
|---|---|---|
| **Maktaba.app** (Mac) | The server orders the release from the app (`updates/app-install.json`); the app downloads and checks exactly that release from its own updater feed and says when it installs ("Maktaba will update to <v> and restart by <time>", or "shortly"). At the home's go, with no click: the app installs what it staged ("Maktaba is updating to <v> now") and restarts the server. Before, the server keeps a copy of the payload it ran, which the launcher runs if the new one cannot stay up. | The first update from a release before automatic updates. An app in a folder this user cannot change (`/Applications` for a standard user): reported as needing an administrator, nothing changed. |
| **Windows app** | The same; the installer stops and starts the server. | The first update from a release before automatic updates. |
| **Linux app** (AppImage, `.deb`, `.rpm`) | The server installs the release's signed Linux payload as a new runtime directory, and switches to it at the home's go, with no click in the app (no root needed); `previous` is kept for the rollback. | The first update from a release before automatic updates. The app's own window updates with its updater. |
| **Headless** (`install.sh --app`) | The same runtime switch, by itself in the window. | The first update from a release before automatic updates. |
| **Docker** (`ghcr.io/hamza-labs-core/maktaba-server`, [cloud/installers/docker](../cloud/installers/docker/docker-compose.yml)) | The headless runtime switch, on the image's volume, by itself in the window: the image only puts its payload there the first time, and the server updates it from then on. A newer image pulled later is switched to at the container's start under the launcher's guard, kept once its server has run for 3 minutes; one that cannot start is gone back from and not tried again (the image before still runs). An older image never replaces what the volume runs. | Nothing (no image to pull, no Docker socket). An image a NAS's catalog moves (TrueNAS app updates, Unraid's update check, Watchtower, a pull in Container Manager) needs nothing turned off: see [Updates on a NAS](#updates-on-a-nas). |
| **Plain server** on the side-by-side layout (`setup --self-managed`, the `.deb`/`.rpm`, the Homebrew formula) | Staged in `bin/releases/<v>` with its migrations and its own pipeline environment (`venv`, built from the release's wheel and the home's wheelhouse with the machine's Python 3.12), switched to under the launcher's guard, TV handed over. A release that needs a newer launcher replaces it after its commit (a package-owned launcher: the package manager brings it, and the run names the command). | A Python 3.12 for the pipeline (or `MAKTABA_PIPELINE_PYTHON`). |
| **Worker-only node** (the Epic 31 installer) | Made a full node by its next update ([above](#what-the-machines-do)). Automatic from then on. | A worker whose pipeline predates the command ("worker-only — re-run the installer once"). |
| Plain binary copied by hand (no launcher) | — (reported: `not_self_managed`) | One `maktaba-server setup --self-managed`, then automatic. |
| A container of the stack built from source (`deploy/compose`) | — | `docker compose pull && docker compose up -d` (Epic 28.3's `409`). |

### Updates on a NAS

Epic 59 story 59.12. On a NAS the Docker image has two triggers and one update path:

- **The self-update** runs in the household's update window as on any Docker install: the release
  verified in process, a pre-update dump, a new runtime on the data volume, `current` moved under
  the launcher's guard (kept once the new release has run, or switched back). It never changes the
  image's tag; a later pull of that release's image finds the work done.
- **A catalog moving the image's tag** (TrueNAS app updates, Unraid's update check, Watchtower, a
  household pressing pull in Container Manager): the newer image puts its release on the volume
  at the container's next start, through the same install and guard. An image older than the
  volume's runtime starts the runtime and logs "The container image (…) is older than this home's
  runtime (…), so the runtime is running." Neither needs the other turned off;
  `MAKTABA_UPDATE_DISABLE=1` is a household's choice (the image path only, the guard still on).
- **Admin → Updates** shows, on the image, the runtime's release against the image's, which runs
  and why (`GET /api/system/updates`' `container` block, an admin's only).
- **Room:** a container's update first checks the data volume for the new runtime (about 1 GB) and
  the dump (the database × 1.2). When it is short the run waits for a person with "Not enough free
  space on the data folder for a safe update: {need} needed, {free} free." (`disk_short`), and
  nothing is changed; the update never goes on without its dump.
- **Memory:** on the Light and Standard tiers the pipeline (speech to text, transcodes) stops for
  the pre-update dump; the new release starts it, or the release before again when the update goes
  no further.
- **A restart in the middle** (DSM's stop-all before its own update, a TrueNAS app restart): the
  next start resolves the half-done state as the guard does (an unconfirmed `current` goes back to
  `previous`; a newer image's switch is watched on until it is kept).

The tags: `:latest` is the highest stable release (`release.yml`), and the copied install files
name it (the published compose, the Synology files, the Unraid template). The release publishes
the compose and the Synology files pinned to its tag, and the TrueNAS app and its Custom App YAML
pinned to the image's digest (`tests/nas/test_install_files.py` checks each).

The update rehearsal proves all of it on NAS shapes before any tag: its `update-rehearsal-container`
legs (`tests/nas/update_leg.py`, `tests/nas/update_legs.json`) start the image as uid 568 with a
read-only library, under a 4 GB limit with 20,000 videos, on arm64, restarted at each step of an
update, and from an image older than its volume; each updates from the previous release, rolls a
release that cannot start back under the guard, and finds the home as it was.

`maktaba-server update` reads the same feed and verifies with the same code (`--check`,
`--version vX.Y.Z`, `--rollback`): on a layout install it switches `current` (the launcher runs the
new release at the service's restart); in the Linux runtime copy it installs a new runtime; a
plain binary is replaced in place, the previous one kept as `<binary>.bak`. A layout switch from
the command line is no update run's, so the new release confirms itself once it has run healthy
for a few minutes, as a switch by hand does. Nothing pushes such a switch's deadline: one whose
start takes over 15 minutes (a large library's backup or migrations) still goes back, and is for
an update run (**Update now**). Before 2026-11-24 the command named its switch `cli`,
which nothing confirmed, and the launcher went back after 15 minutes; a release since confirms
such a switch too.

## Package repositories and the Homebrew tap

Each release's own `.deb` and `.rpm` (and `maktaba.rb`) are attached to it on GitHub: that is the
way to install a package. Once the project sets them up, `packages.yml` also publishes every stable
release, when it is published on GitHub as the newest stable one, to an apt and a dnf repository
(the server, `maktaba-server`; signed indexes, and the `.rpm` packages signed with the repository
key) and to the Homebrew tap (the formula, and the Mac app's cask `maktaba-app`); the release notes
then name them. It publishes only the release's own files, checked first against the release's
signature (or, for the Mac app, its updater key). The Linux app is not in the repositories: it
updates itself. An installed server still installs its updates itself, verified; the package
manager keeps the package's own files (the launcher) current. Setup and configuration:
[deploy/packaging](../deploy/packaging/README.md#package-repositories-and-the-homebrew-tap).

## What is verified

Every release publishes, next to its archives:

- `release.json`: version, tag, commit, schema revisions (`schema_rev`, `min_node_schema`,
  `breaks_previous_home`), protocol numbers, the per-platform archive with the SHA-256 of every
  binary inside it, the pipeline wheel and constraints, each app's PostgreSQL major. It is listed
  in `checksums.txt`.
- `checksums.txt` and `checksums.txt.sigstore.json`, a Sigstore bundle holding the Fulcio
  certificate, the signature and the Rekor entry with its inclusion proof. The legacy
  `checksums.txt.sig` / `.pem` stay for humans.

The server, `maktaba-server update` and every machine verify with one in-process package,
`shared/go/releaseverify` (no cosign binary, no network needed):

1. the bundle against the Sigstore trusted root, with at least one Rekor inclusion proof and a
   signed log time inside the certificate's validity (checked against that time, not the local
   clock);
2. the certificate identity, exactly: issuer `https://token.actions.githubusercontent.com`, SAN
   `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>`,
   source repository `https://github.com/Hamza-Labs-Core/Maktaba`, ref `refs/tags/<tag>`, trigger
   `push`, build config the same `release.yml@refs/tags/<tag>`, a GitHub-hosted runner, and
   GitHub's numeric repository and owner ids (so a repository re-registered under the same name
   after a rename is refused). A fork, another workflow (also one calling `release.yml`), a branch
   run, a self-hosted runner or another tag's signature is refused; `release.json`'s `commit` must
   be the commit the signing run was built from;
3. `checksums.txt` (duplicates or bad digests are refused), then `release.json` against it and
   against the tag, every downloaded file against `checksums.txt`, the extracted `maktaba-server`
   against `release.json`, and every constraints line has `--hash=sha256:`;
4. the watermark, the highest version this machine ever installed: a lower version is refused, the
   same version with other checksums is refused, and so is a release signed before the installed
   one. `maktaba-server update --rollback` is the only way to run a lower version; it does not
   lower the watermark.

Releases published before story 35.1 have no bundle and no `release.json`: they are never installed
automatically.

**Trusted root.** The public-good Sigstore `trusted_root.json` is compiled into the binary
(`shared/go/releaseverify/trusted_root.json`, refreshed weekly by `trusted-root.yml`; a release
refuses to build with one older than 90 days). The server refreshes it daily through Sigstore's TUF
repository (a machine also through its home), anchored on the TUF root compiled into sigstore-go,
into `<data_dir>/sigstore/`. A failed refresh keeps the last good root; a root not refreshed for
180 days is treated as expired and verification fails closed. The cache is trusted only while no
other user can write it and its `fetched_at` is not in the future; otherwise the embedded root is
used.

`install.sh` fails closed as well: a release missing `checksums.txt`, its Sigstore bundle or the
archive's entry is refused (`MAKTABA_NO_VERIFY=1` overrides). Without `cosign` installed it can only
check the SHA-256 (it warns); `MAKTABA_REQUIRE_COSIGN=1` makes a missing cosign fatal.

### Verifying a release by hand

The release notes print these lines with the exact identity for their tag:

```bash
v=1.5.0
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

Use the exact `--certificate-identity`, never a `--certificate-identity-regexp` over the repository
name: that accepts a fork's release workflow too. `--certificate checksums.txt.pem --signature
checksums.txt.sig` in place of `--bundle` checks the same identity. A fork (`MAKTABA_UPDATE_REPO`)
uses its own `owner/name` in the identity.

## When an update fails: where it says why

Every update attempt is kept with its reason
([update-attempts.md](../specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md)): the
desktop app's own updater, a release's first start (the database step, the pipeline's
environment), and the cluster's runs. **Admin → Updates** shows, for the home and each machine, the
last attempt: when, from and to, the step it stopped at, the reason in plain words with a link to
its help, the raw error, and what to do next (Retry, the fix, or the logs). "See the logs" opens
the logs page on that update's time window with the machine's own files beside the API's lines:
`server.log` (the database step, every role), `app.log` (the desktop app's updater) and
`launcher.log`, a node's fetched over the machine channel. "Download the logs" is the diagnostics
bundle of the same window with those files in it. A machine that cannot log in to the home's
database says so on the Updates and Machines pages; the home re-makes its access by itself
([machine-database-access.md](../specs/epics/37-node-replica/machine-database-access.md)).

On the machine itself: `updates/journal.json` (the run's state), `updates/launcher.log`,
`updates/server-attempts.json` and `maktaba-launcher --status` (current, previous, pending and the
last rollback). The [runbook](cluster/runbook-updates.md#reading-a-machines-update-state) says how
to read them; where they are on each desktop app is in
[auto-update.md](auto-update.md#where-the-evidence-is-on-each-machine).

## Watching updates: metrics and events

- **Metrics** (Prometheus): the API's `/metrics` carries `maktaba_update_runs_total{result}`,
  `run_duration_seconds`, `step_duration_seconds{step}`, `migration_duration_seconds`,
  `backup_bytes`, `targets{state}`, `node_apply_duration_seconds`, `verify_failures_total{reason}`,
  `machines_behind` and `last_check_timestamp` (all prefixed `maktaba_update_`). They are read from
  the update tables at scrape time, so a restart mid-run does not reset them. A full node adds
  `maktaba_node_delegate_requests_total{result}`, `maktaba_node_delegate_cache_hits_total`,
  `maktaba_node_proxy_requests_total{class,status}` and `maktaba_node_paused{reason}`; a home or a
  worker-only machine has none of those.
- **Events** (the structured log and the hub's events file), each with `run_id`, `version` and
  `host_id`: `update_planned`, `update_step_started|finished|failed`, `update_home_committed`,
  `update_rolled_back`, `update_node_prepared|applied|failed|held|caught_up`, `launcher_rollback`,
  `release_verify_failed`, `release_withdrawn`.
- **The control centre** (Processing) shows admins a card for a run under way — the release, its
  state and step, the machines done of the total — linking to Admin → Updates; the health samples
  carry `update.state` and `update.machines_behind`.

## Settings

Settings → Rules → **Updates** (`rules.updates`; the same names and defaults as the
[epic's settings](../specs/epics/35-cluster-full-nodes-and-updates/README.md)):

| Setting (on the page) | `rules.updates.` | Default | Meaning |
|---|---|---|---|
| Install updates automatically | `auto_install` | `true` | Off: check and notify only. |
| Release channel | `channel` | `stable` | `stable` or `beta`. Switching to `stable` never downgrades. |
| Quiet window | `window` | `03:00-05:00` | The home's local time; a run starts only inside it. |
| May run past the window (minutes) | `window_overrun_minutes` | `120` | A started run may finish its TV swap and rollout this long after the window; then the rollout holds. |
| Release age before installing (hours) | `min_release_age_hours` | `24` | A release must be this old before an automatic install. `0`: as soon as seen. **Update now** ignores it. |
| Stay at or below version | `pinned_version` | `""` | A pin (never below what is installed). |
| No automatic update before | `deferred_until` | none | No automatic run before this instant. |
| Skipped versions | `skipped_versions` | `[]` | Never installed automatically (a newer one is). |
| When a machine fails | `node_failure_policy` | `hold_platform` | `hold_platform`, `halt` or `continue`. |
| A machine is healthy within (minutes) | `node_health_timeout_minutes` | `10` | A machine not healthy by then has failed. |
| Longest wait for running jobs (minutes) | `drain_timeout_minutes` | `10` | Before a restart (transcriptions resume from checkpoints). |
| Rehearse database changes first | `rehearse_migrations` | `true` | Rehearse the migrations on a scratch copy of the database. |
| Rehearse only up to (GB) | `rehearse_max_db_gb` | `20` | Above this size the rehearsal is skipped (the backup still runs). |
| Versions kept on each machine | `keep_versions` | `3` | Releases kept (never fewer than current and previous; never one a running process uses). |
| Backups before an update kept | `backups_keep` | `3` | Of each kind of backup. |
| A machine that was offline is updated | `catch_up` | `immediate` | `immediate` or `window` (machines carrying TV streams always in the window). |
| Image cache on a machine (MB) | `proxy_cache_mb` | `2048` | A full node's cache of the home's images. |
| Keep the pipeline's packages for the machines | `wheelhouse` | `true` | The home keeps each release's pipeline packages for its machines' platforms. |

TV during updates also reads `tv_staging.quiet_max_viewers` (0) and
`tv_staging.boundary_max_wait_minutes` (Live TV → Changes settings).

## Server configuration (environment)

| Variable | Default | Meaning |
|---|---|---|
| `MAKTABA_UPDATE_CHANNEL` | from the version | `stable` or `beta`. |
| `MAKTABA_UPDATE_INTERVAL_SEC` | `86400` | The release poller's cadence (the home's scheduler checks every 6 hours); `0` turns the poller off. |
| `MAKTABA_UPDATE_DISABLE` | unset | `1`/`true`: no network checks and no automatic installs at all. |
| `MAKTABA_UPDATE_REPO` | `Hamza-Labs-Core/Maktaba` | A fork's releases. It changes the expected signing identity too (the fork's own `release.yml`); a startup setting no API field can change. |
| `MAKTABA_DATA_DIR` | `maktaba-server`'s `data_dir` | The Sigstore trusted root (`sigstore/`), the mirror (`updates/mirror/`), the backups (`backups/`). |
| `MAKTABA_UPDATE_FEED` | unset | `off`: the home reads GitHub's release list, not the release feed ([How a home finds a release](#how-a-home-finds-a-release)). |
| `MAKTABA_GITHUB_TOKEN` | unset | Optional; raises the GitHub API rate limit. |
| `MAKTABA_PIPELINE_PYTHON` | the machine's Python 3.12 | The Python a layout install builds the pipeline's environment with. |

## Proven before every release

Every release's update paths are rehearsed in CI first: [update-rehearsal.md](update-rehearsal.md)
(the latest published release updated to the commit being released, the way the Mac app
does it: migrations, a TV change that waits, rollbacks with and without a restore, a
restore under logins and killed part way, halted roles, no room, a joined machine). The
release's tag guard refuses a commit the rehearsal did not pass on.

## The caps on GitHub's release list

A home reads GitHub's releases through its update check (`api/internal/updates/catalog.go`),
and the server's own updater and the About check read the same list. Each answer is held to a
cap, so a broken or hostile answer cannot exhaust a home. A cap a real answer grows past stops
every home's updates: on 2026-11-23 GitHub's list of the newest 30 releases (some 190 assets each)
passed the 8 MiB a home read, and every check failed for over a day, unnoticed.

| What a home reads | Request | Cap | Real size (2026-11-24) |
|---|---|---:|---:|
| The list, 3 pages of 10 releases | `GET /repos/<repo>/releases?per_page=10&page=N` | 16 MiB a page | 3.2 MB a page |
| One release by its tag (before a run starts) | `GET /repos/<repo>/releases/tags/<tag>` | 8 MiB | 325 KB |
| `checksums.txt`, its Sigstore bundle, `release.json` | the release's download URLs | 4 MiB each | 15 KB at most |

A home asks unauthenticated, with `Accept: application/vnd.github+json` and
`X-GitHub-Api-Version: 2022-11-28`; GitHub then answers minified JSON. (Asked with another
`Accept`, as curl does by default, GitHub pretty-prints the same list, some 23% longer: that is
not what a home reads. A token with push access lists the drafts first.) Each list page is read
one release at a time, so its cap bounds the answer, not the memory.

**Three checks keep the caps honest:**

- **The tests** read a real page, recorded as a home asks for it
  ([`tests/fixtures/github-releases`](../tests/fixtures/github-releases/README.md)): every cap must
  hold the recorded answers within 70% (`updates.ListHeadroom`), and the worst page the next
  releases can make (each release twice the largest recorded one, `AssetGrowth`) too. The About
  check, `selfupdate` and `ghasset` are tested against the same page.
- **GitHub's release list check** (`api/cmd/release-list-check`, through the home's own code: the same
  URLs, headers, pages and caps) reads the live list hourly
  ([`github-release-list.yml`](../.github/workflows/github-release-list.yml), which opens an issue on a failure
  and closes it once the list reads again), and in each release workflow once it publishes
  (`tools/release/release-list-check.sh`). It fails when any page or the newest release's own read is over
  70% of its cap, or when the newest published stable release is not one a home's catalog can
  select (not in the pages a home reads, a draft or prerelease, or without its signed files).
- **The update rehearsal**'s scenario 20 serves the list at its real size and shape, twice as
  large, and then past its cap ([update-rehearsal.md](update-rehearsal.md)).

**When a check fails:** re-record the page (`tests/fixtures/github-releases/record.sh`) and look at
what grew. Raise the cap or lower `listPerPage` in `catalog.go`, and the matching constants of the
About check (`api/internal/system/updater.go`) and `selfupdate`, so the tests pass with the new
page; a release already out reads with its own caps, so the fix ships in the next release. The
home itself raises a failing check: from two failures in a row the Updates page says "Can't check
for updates since …" with the reason, System health's **Update check** item turns yellow, and
admins get the `update.check_failing` notification.

## How a release is made

A session pushes each tag. A scheduled release session on the owner's machine runs every 30
minutes. On main the update rehearsal runs after CI, on each commit CI's push run passed on
(a `workflow_run`, since 2026-11-07, [update-rehearsal.md](update-rehearsal.md#when-it-runs)),
so a green rehearsal is a commit both passed on. The session lists main's passed runs of
both workflows and hands them to
[`tools/release/next_tag.py`](../tools/release/next_tag.py):

```sh
gh run list --branch main --workflow ci.yml --event push --status success --limit 300 \
  --json headSha,event,conclusion,displayTitle >ci.json
gh run list --workflow update-rehearsal.yml --event workflow_run --status success --limit 300 \
  --json headSha,event,conclusion,displayTitle >rehearsal.json
python3 tools/release/next_tag.py --ci-runs ci.json --rehearsal-runs rehearsal.json
```

**The broadcast rehearsal gates the tag too** (story 50.29 §4): a commit is eligible only when
the last nightly run of [`broadcast-rehearsal.yml`](cloud-rehearsal.md#the-broadcast-rehearsal)
on its broadcast code passed. Its broadcast code is the files of the workflow's pull-request
paths (`BROADCAST_CODE` in `next_tag.py`, kept equal to them by `test_next_tag.py`): the edge,
the broadcast packages of the cloud, the streaming service and the API, the relay, the
rehearsal itself. Among the nightly runs on main whose commit has the same broadcast code as the
candidate, the newest one that finished decides: passed, eligible; failed or timed out, not. So
a commit that changed no broadcast file since a green nightly is eligible, and a commit that
changed one waits for the next nightly. A commit with no such nightly is not eligible (fail
closed). `next_tag.py` fetches the broadcast rehearsal's runs itself, with both `--green` and
the run lists, so the session's command needs nothing new:

```sh
gh run list --workflow broadcast-rehearsal.yml --branch main --event schedule --limit 60 \
  --json headSha,headBranch,event,status,conclusion,displayTitle,createdAt
gh run list --workflow broadcast-rehearsal.yml --branch main --event workflow_dispatch --limit 60 \
  --json headSha,headBranch,event,status,conclusion,displayTitle,createdAt
```

If `gh` is missing, fails or prints something that is not a list of runs, nothing is tagged and
`reason=` says why (fail closed). `--broadcast-runs FILE` (the two lists' runs in one JSON
array) is optional: it is read instead of fetching, as the tests do. Every status counts (a red
run counts); a run still going, or one cancelled or skipped, is passed over. A nightly run is the schedule's (03:17 UTC) or a hand run with `set: nightly`
(`gh workflow run broadcast-rehearsal.yml -f set=nightly`), which proves a fix without waiting
for the night; it is named `Broadcast rehearsal nightly of <sha>`, and a hand run counts only
when that commit is its own. The pull-request and push runs (the short set) never count,
whatever their name: a pull request runs its own copy of the workflow. When the
newest green commit is not eligible, the newest one that is gets the tag, and `reason=` names
the commits passed over and why.

A rehearsal run of main is named `Update rehearsal of <sha>`, after the commit it rehearsed:
its own `headSha` is main's head at the moment it started, so `next_tag.py` reads the commit
from the name. (`--green FILE`, one SHA a line, still takes the commits directly.) It
chooses the commit and the tag: the newest commit of main that both passed on and the
broadcast rehearsal allows, tagged as
the next patch version (the highest `vX.Y.Z` plus one). The session pushes it as an annotated
tag whose message is its name. A commit is not tagged when:

- it has a `v*` tag already, or a newer commit does (an older commit is never tagged after a
  newer one);
- it does not contain the highest tag's commit;
- its change since the highest tag is only the Claude trail (`docs/claude-trail.md`,
  `docs/trail/**`: a push of only those starts neither workflow, so it is never tagged).

The tag's release run (`release.yml`) also embeds the plugin store's current lists (Epic 54,
#755): its `plugin-lists` job fetches `store-keys.json` and `revoked.json` from
`plugins-cdn.maktaba.tv`, verifies them with the committed plugin root, and every build of
that release embeds the same two files. Once the root lists keys, an unreachable store or a
list that does not verify, has expired or is older than the committed copy fails the release
(re-run it once the store is back); while the root lists none, it embeds nothing and says so.

The release also publishes the TrueNAS app and its Custom App YAML pinned to its image's digest
(`maktaba-truenas-app-<version>.tar.gz`, `maktaba-truenas-custom-app.yaml`,
`tools/release/truenas_app.py render`, Epic 59 story 59.10), as it pins the Docker compose file. Neither is committed back to main: a commit after each tag
would be the next release candidate, and every release would make another. Once the Maktaba app
is in TrueNAS's catalog, `packages.yml` opens each release's bump pull request there with the
release's signed app archive, verified and copied as data: that job checks out no code
([deploy/nas/truenas](../deploy/nas/truenas/README.md#releases)).

A merge that got no run of its own (main's runs replace a pending one) is released with the
next green commit's tag. A CI run that failed only on the flaky iPhone/iPad shards is not green
here, though the tag guard would let it through: the next green commit is tagged instead, or a
session tags that commit by hand.

The session pushes the tag with the owner's git credentials, and the push itself starts every
tag workflow (`on: push: tags`): `release.yml`, `desktop-release.yml`, `mobile-release.yml`,
`tv-release.yml`, `play-bundles.yml`, `testflight.yml` and `relay.yml`. `packages.yml` follows
`release.yml` and `desktop-release.yml`. The session then builds the tag's trail snapshot
(the `trail` skill, "After a v* tag").

Tags are pushed by a session, not a workflow, for two reasons. A tag pushed from a workflow
with `GITHUB_TOKEN` starts no workflow, and a release cannot be started with a dispatch
instead: the release is signed keyless, the signing certificate records the run's trigger, and
every home's updater (`shared/go/releaseverify`, `BuildTrigger`) accepts only `push`. A
dispatched `release.yml` run would sign a release that its own verify step and every home
refuse. That is how v0.3.85 was made, and its release was never published. And a workflow that
pushes tags itself needs a long-lived write key in the repository's secrets; a session's push
needs none. So `release.yml` takes no dispatch, and a failed `release.yml` run is re-run (a
re-run keeps its `push` trigger). The other tag workflows still take a dispatch on their tag by
hand (`gh workflow run <workflow> --ref vX.Y.Z -f tag=vX.Y.Z`) when their push run is missing.

A tag with no push run of `release.yml` (v0.3.83, v0.3.84 and v0.3.85, made while a workflow
pushed tags with `GITHUB_TOKEN`) can never be given a release that homes accept, short of
deleting and pushing the tag again. Those tags stay as tags with no release. Homes only take the
newest release, so the next tag replaces them.

Tagging by hand still works the same way (a tag pushed with a person's credentials starts the
workflows): the release session then sees the commit tagged and leaves it. Play's release notes
come from the tag's message ([google-play.md](google-play.md#release-notes)), so a release with
notes is still tagged by hand.
