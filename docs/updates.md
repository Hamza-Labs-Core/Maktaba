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
| Finding releases | The home checks GitHub Releases every 6 hours (and when you press **Check now**). | The channel (`stable` or `beta`). |
| Installing on the home | In the quiet window, once the release is old enough, with its migrations rehearsed first. | Turning it off, a pin, a deferral, skipping a version, **Update now**. |
| Installing on the machines | After the home commits the release: downloaded from the home, verified on each machine, switched one at a time. | What a machine's failure does to the others (`node_failure_policy`), when a machine that was offline catches up (`catch_up`). |
| TV | Plain server installs keep TV playing across the restart; the streaming software then moves to the new release at programme boundaries, without an approval. | The viewers a restart may interrupt (`tv_staging.quiet_max_viewers`, 0) on installs that restart whole. |
| Going back | A release that does not stay healthy is rolled back, its database put back from the dump taken before its migrations. | Rolling back by hand, restoring an older backup. |
| A desktop app that runs the server (Maktaba.app, the Windows or Linux app) | It downloads and stages the release the home orders. | **The click** that installs it (owner decision D1). |

Automatic installs are on by default (`rules.updates.auto_install`). With them off (Admin →
Updates → Pause, or Settings → About) the home only checks and notifies. `MAKTABA_UPDATE_DISABLE=1`
turns the checks off altogether.

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
3. **The home switches** to it and restarts. The new release's start dumps the database
   (`backups/pre-<old>-to-<new>.dump`, checked to read back), migrates it, and puts the dump back
   if a migration fails. Its health is watched (every role running, the API ready and answering as
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

A desktop app's machine waits for its click while the others go; its click takes the next turn. A
run waiting only for clicks gives way to a newer release. A power cut at any step is recovered at
the next start from the update's journal (`updates/journal.json`).

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
  rule. The release commits once the change is live. What can stop it:
  - `failed_tv`: its check or its swap failed, or the TV change was rolled back. The release rolls
    back and is blocked; the notice lists the checks that failed.
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
    pipeline wheel and constraints, fetched once ahead of the machines and kept only when their
    digest is the one the signed `checksums.txt` lists. A machine downloads
    `GET /api/cluster/updates/artifacts/{version}/{name}` (signed with its delegation key; range
    requests; at most three downloads at once per machine) and falls back to GitHub after three
    failures, or at once for a file the home does not hold. The copies follow `keep_versions`.
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
  and removes the old worker units once the node answers. Admin → Updates shows it as "becoming a
  full node". A worker whose pipeline predates the command shows "worker-only — re-run the
  installer once" ([join a machine](cluster/join-a-machine.md#a-worker-only-machine-becomes-a-full-node)).
- **Skew.** No machine is ever ordered to a release the home has not committed. A machine whose
  code does not fit the home's database pauses (its web address shows the paused page) until its
  update lands; the Updates and Machines pages say which one to update.

## When a release does not hold: rollback, backups, restore

A release that does not stay healthy is **rolled back**: the previous release runs again. When it
had already migrated the database, the dump taken before its migrations is put back first (every
release refuses a database a newer one migrated); writes made between that dump and the rollback
are lost — minutes, inside the window, with the jobs drained — and the failed state is dumped
aside (`backups/failed-<version>-<time>.dump`). The release is then blocked until a newer one
appears or an admin unblocks it (Admin → Updates → **Try it again**, or **Update now** naming it).
On a machine, the launcher's guard does the same when the new release does not confirm in time
(15 minutes) or stops three times in ten minutes.

**Backups.** Before every update that migrates, the home dumps its database into
`<data_dir>/backups/pre-<from>-to-<to>.dump`. It keeps `backups_keep` (3) of each kind: those, the
bundled database's daily `maktaba-<time>.dump`, the database a failed update left (`failed-*`) and
the one a restore replaced (`replaced-*`).

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
anew. The updater itself never runs a down migration.

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
- The dumps are kept to `backups_keep`; scratch copies a crash left behind go after an hour.

## How each kind of install takes a release

([install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md) has the
details.)

| Install | Automatically | Still needs a person |
|---|---|---|
| **Maktaba.app** (Mac) | The server orders the release from the app (`updates/app-install.json`); the app downloads and checks exactly that release from its own updater feed and shows it ("Install Maktaba <v>"). After the click, the home's go: the app installs what it staged and restarts the server. Before, the server keeps a copy of the payload it ran, which the launcher runs if the new one cannot stay up. | **The click** in the app. The first update from a release before automatic updates. An app in a folder this user cannot change (`/Applications` for a standard user): reported as needing an administrator, nothing changed. |
| **Windows app** | The same; the installer stops and starts the server. | The click. The first update from a release before automatic updates. |
| **Linux app** (AppImage, `.deb`, `.rpm`) | The server installs the release's signed Linux payload as a new runtime directory, and switches to it after the click in the app (no root needed); `previous` is kept for the rollback. | The click. The first update from a release before automatic updates. The app's own window updates with its updater. |
| **Headless** (`install.sh --app`) | The same runtime switch, by itself in the window. | The first update from a release before automatic updates. |
| **Docker** (`ghcr.io/hamza-labs-core/maktaba-server`, [cloud/installers/docker](../cloud/installers/docker/docker-compose.yml)) | The headless runtime switch, on the image's volume, by itself in the window: the image only puts its payload there the first time, and the server updates it from then on. A newer image pulled later is switched to at the container's start under the launcher's guard, kept once its server has run for 3 minutes; one that cannot start is gone back from and not tried again (the image before still runs). An older image never replaces what the volume runs. | Nothing (no image to pull, no Docker socket). An image managed elsewhere (Watchtower, a tag you move): set `MAKTABA_UPDATE_DISABLE=1` (or Pause on Admin > Updates) so only one thing updates it. |
| **Plain server** on the side-by-side layout (`setup --self-managed`, the `.deb`/`.rpm`, the Homebrew formula) | Staged in `bin/releases/<v>` with its migrations and its own pipeline environment (`venv`, built from the release's wheel and the home's wheelhouse with the machine's Python 3.12), switched to under the launcher's guard, TV handed over. A release that needs a newer launcher replaces it after its commit (a package-owned launcher: the package manager brings it, and the run names the command). | A Python 3.12 for the pipeline (or `MAKTABA_PIPELINE_PYTHON`). |
| **Worker-only node** (the Epic 31 installer) | Made a full node by its next update ([above](#what-the-machines-do)). Automatic from then on. | A worker whose pipeline predates the command ("worker-only — re-run the installer once"). |
| Plain binary copied by hand (no launcher) | — (reported: `not_self_managed`) | One `maktaba-server setup --self-managed`, then automatic. |
| A container of the stack built from source (`deploy/compose`) | — | `docker compose pull && docker compose up -d` (Epic 28.3's `409`). |

`maktaba-server update` reads the same feed and verifies with the same code (`--check`,
`--version vX.Y.Z`, `--rollback`): on a layout install it switches `current` (the launcher runs the
new release at the service's restart); in the Linux runtime copy it installs a new runtime; a
plain binary is replaced in place, the previous one kept as `<binary>.bak`.

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
| `MAKTABA_GITHUB_TOKEN` | unset | Optional; raises the GitHub API rate limit. |
| `MAKTABA_PIPELINE_PYTHON` | the machine's Python 3.12 | The Python a layout install builds the pipeline's environment with. |
