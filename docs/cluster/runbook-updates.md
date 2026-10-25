# Update runbook

What to do when an update of the home or of a machine goes wrong. Most failures need nothing:
Maktaba goes back to the release that worked, says why on **Admin > Updates**, and tries again
when that can help. Each entry below says what you see, what Maktaba already did, and what is left
to you. How updates work is in [updates](../updates.md); the reasons are listed in
[update-attempts.md](../../specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md).

Codes in `backticks` are the ones Admin > Updates shows beside each machine's last attempt and in a
run (the reason line links to its help in the app); the events of the same name are in the
structured log and the hub's events file ([updates](../updates.md#watching-updates-metrics-and-events)).

## Reading a machine's update state

Start on **Admin > Updates**: the run (its step on the home and on each machine), each machine's
last attempt (when, from and to, the step, the reason, the raw error, what to do), **See the logs**
(the logs page on that update's window, with the machine's own `server.log`, `app.log` and
`launcher.log`) and **Download the logs** (the same window as a bundle, a node's files under
`node-<id>/`).

On the machine itself, in its install root (`/var/lib/maktaba` for the `.deb`/`.rpm`;
`~/.local/share/maktaba` for a Linux user install or the Linux runtime copy; `~/Library/Application
Support/Maktaba/server` for a Mac `setup --self-managed`; `$(brew --prefix)/var/maktaba/server` for
Homebrew; `%LOCALAPPDATA%\Maktaba` on Windows; the desktop apps' folders are in
[auto-update.md](../auto-update.md#where-the-evidence-is-on-each-machine)):

- `maktaba-launcher --status` — the release that runs (`current`), the one it falls back to
  (`previous`), one not yet confirmed (`pending`), and the last rollback with its reason.
- `updates/journal.json` — the machine's run, rewritten at every step: `job.version` (the
  release), `from`, `state` (`preparing`, `prepared`, `prepare_failed`, `applying`, `restarting`,
  `watching`, `committed`, `rolling_back`, `rolled_back`, `apply_failed`, `failed`), `step` and
  `steps` (each with its start, end and detail), `error` (`code` and `detail`), `hands_over` and
  `handover_why` (whether the TV ran on across the restart, and why not), `tv` (the TV change of
  the run), `backup` (the dump taken before the migrations), `reason` (why it rolled back).
  `committed` or `rolled_back` is the end of a run; a power cut mid-run is resumed from here at the
  next start.
- `updates/logs/<run_id>.log` — the run's own log on this machine, one timed line per step (the
  run's log link on Admin > Updates shows the home's).
- `updates/launcher.log` — one line per launcher event: start, restart (exit 75), the guard's
  rollback (`launcher_boot_loop`, `launcher_pending_timeout`), a refused release
  (`launcher_refused`), the TV handover (`launcher_handover`).
- `updates/server-attempts.json` (and `app-attempts.json` beside a desktop app's server) — the
  last 20 attempts with their reasons.
- `updates/handover.json` — the TV processes kept across the last restart.
- `server.log` — every role and the database step (`[db]` lines: the dump, the migrations, the
  restore).

## Pausing all updates

- **Stop automatic installs**: Admin > Updates > **Pause** (or Settings > About > **Install
  updates automatically** off, `rules.updates.auto_install = false`). The home still checks and
  tells you about releases; nothing installs until you turn it back on or press **Update now**.
- **Not before a date**: `rules.updates.deferred_until`.
- **Not this version**: **Skip** it (`skipped_versions`), or pin below it (`pinned_version`).
- **A run under way**: **Cancel** stops it before the home switches; after that, let it finish
  or roll back.
- **No network checks at all**: `MAKTABA_UPDATE_DISABLE=1` in the server's environment.

## Rolling back by hand

- **A machine of the cluster**: Admin > Updates > the run > **Roll back** on the machine's row.
- **The home, or any self-managed install, on the machine**: `maktaba-server update --rollback`,
  then restart the service (`sudo systemctl restart maktaba-server`; `systemctl --user restart
  maktaba-server`; `launchctl kickstart -k gui/$(id -u)/com.maktaba.server`; on Windows end and
  run the "Maktaba" task, or restart the service). It makes `previous` current
  again (the Linux runtime copy: its previous runtime; a plain binary: `<binary>.bak`). The
  watermark is not lowered: the release you left is not installed again automatically until
  you press **Try it again** on Admin > Updates, and the next release moves past it.
- **A desktop app**: install the release you want from its GitHub release by hand.
- **The database** stays on the newer schema, which the release before keeps working on. When it
  cannot (it says `db_ahead` and refuses to start), restore the backup taken before the update
  (next entry).

## Restoring a backup

Use it when the database must go back to an earlier state: a release's data change you do not want,
or a release you rolled back by hand that refuses the newer schema.

- **Where**: Admin > Updates > *Backups of the library database* > **Restore** (type the backup's
  name to confirm), or on the home `maktaba-server restore` (the backups and the releases kept),
  `maktaba-server restore --backup <name>` (what it would do), `... --confirm` (do it).
- **What happens**: the home restarts on the kept release whose schema is the backup's, and that
  release puts the backup back before anything else runs. The database it replaces is set aside
  as `backups/replaced-<version>-<time>.dump` (restore that one to undo). The machines copy the
  restored database anew.
- **What it loses**: every change made since the backup was taken — added videos, watch history,
  settings, jobs done. A `pre-<from>-to-<to>.dump` is from the start of that update.
- **Refusals**: `restore_not_confirmed` (no confirmation), `restore_update_under_way` (cancel the
  run, or wait for it), `restore_no_release` (no kept release has the backup's schema: install the
  release it names, then restore), `restore_bad_backup` / `restore_no_backup` (not a backup's
  name), `restore_unreadable` (the dump cannot be read), `restore_no_database` (the home's
  database is not one this restore handles), `restore_not_home` (a machine of a home has no
  backups: restore on the home), `restore_under_way` (one is already being put back),
  `restore_switch_failed` (the release could not be made current: the detail says why).

## Recovering a corrupt launcher

A launcher that cannot start (its file damaged, a disk error) makes the service manager restart it
in a loop and nothing runs. A launcher replaced by an update kept the one before as
`maktaba-launcher.old` until its next good start.

1. Stop the service.
2. In the install root, put the previous one back: rename `maktaba-launcher.old` over
   `maktaba-launcher` (`.exe` on Windows). Without an `.old`, copy `maktaba-launcher` from the
   current release's folder (`bin/releases/<current>/`).
3. `./maktaba-launcher --status` and `./maktaba-launcher --verify bin/releases/<current>` check it
   reads the layout and the release checks out.
4. Start the service.

A package's launcher (`/usr/bin/maktaba-launcher`, Homebrew's) comes back with the package:
`sudo apt install --reinstall maktaba-server`, `sudo dnf reinstall maktaba-server`,
`brew reinstall maktaba`. A desktop app's launcher comes with the app: install the app again.

## Verifying a release by hand

The release notes print the exact command for their tag
([updates](../updates.md#verifying-a-release-by-hand)):

```sh
v=1.5.0
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v$v" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

Never a `--certificate-identity-regexp`: it accepts a fork's workflow too.

## Failures

### Power loss during an update

- **Before the migrations** (on the home): the next start resumes the run from its journal; past
  the window it ends `interrupted` and the next window tries again. Nothing to do.
- **During the migrations**: the next start runs them again; Postgres undid the unfinished
  transaction, and the files that run outside one are written to run twice safely. Nothing to do.
- **After the switch**: the launcher starts the new release with `pending`; it confirms itself or
  the guard goes back, and the journal finishes the run either way. A start that never finished is
  recorded as `start_interrupted` at the next start. Nothing to do.

### The new release did not hold

`rolled_back`, `did_not_start`, `did_not_stay_up`, `boot_loop`, `restarts`, `start_failed`,
`start_timeout`, `watch_timeout`, `pending_timeout`, `launcher_boot_loop`,
`launcher_pending_timeout`, `launcher_refused`.

- **Maktaba did**: put the previous release back (and, after its migrations, the database dump
  taken before them; the failed state is kept as `backups/failed-<version>-<time>.dump`), and
  blocked the release. The machines were not touched: they follow only a committed release.
- **You**: read the reason and **See the logs** (the start's `[db]` lines, a role that kept
  stopping). `start_timeout` on a large database or a slow disk: the database step or the
  Python environment needed longer than the start allows. `launcher_refused`: the release's files
  on this machine did not check out against what was verified (a disk error, a file changed by
  hand): free the disk, and **Try it again**. A release that keeps failing: skip it and wait for
  the next.

### The release could not be verified

`verify_signature`, `verify_identity`, `verify_transparency_log`, `verify_checksum`,
`verify_manifest`, `verify_unhashed_dependencies`, `checksums_changed`, `payload_invalid`,
`version_mismatch`.

- **Maktaba did**: refused it and blocked it; a critical notification. Nothing was installed.
- **You**: nothing, unless it repeats for every release: then check the machine's clock and
  network (a proxy rewriting downloads), and [verify the release by hand](#verifying-a-release-by-hand).
  `verify_trust_root_expired` / `trust_root` / `no_trust_root`: this build's Sigstore root is too
  old or unreadable; install one newer release by hand.

### A release offered again, or older

- `verify_replay`: a version you have was published again with other files: refused, keep what
  you have.
- `verify_downgrade`: lower than what this machine installed: refused.
- `older_build`: numbered higher but built before the release running here (an rc tagged after
  its release): passed by, not a failure, no notice.

### GitHub unreachable

The last successful check shows on Settings > About; after 14 days a warning notification (a
machine kept from new releases on purpose looks the same). Check the home's internet access and
`MAKTABA_GITHUB_TOKEN` when the rate limit is the cause; **Check now** tries again. The machines
need no internet: they download from the home.

### The release was withdrawn

`withdrawn`, `turned_prerelease`: checked again right before a run; not installed. Already
installed: a notification says so, and nothing is downgraded by itself (roll back by hand if you
want to).

### Not enough disk

`disk_space` (the preflight's check), `disk_full` (mid-run). The staging folders were removed and the
running release was not touched. Free space on the disk of the install root (old releases go by
`keep_versions`, dumps by `backups_keep`) and **Try it again**, or wait for the next window.

### The database step

- `db_dump_failed`: the dump before the migrations failed, so nothing was changed. Usually the
  disk (see above) or the database's own error in the detail.
- `rehearsal_failed`: the migrations failed on a scratch copy of the real data. Nothing was
  changed; the release is blocked. Send the detail with a bug report; skip the release.
- `db_migrate_failed`: a migration failed for real; the dump was put back and the previous
  release runs. As above.
- `db_restore_failed`: putting the dump back failed. **Needs you**: the release before refuses
  the database as it is; [restore a backup](#restoring-a-backup) (the `pre-…` dump of this
  update).
- `db_ahead` / `db_refused_older`: the release is older than the database (a newer one migrated
  it): install that newer release again, or restore a backup.
- `pg_major_change`: the release bundles another PostgreSQL major than this machine's database;
  nothing was installed and the release is not blocked. Stay on this release until one carries
  the upgrade path, or move the database by hand (`pg_upgrade`).

### The pipeline's Python environment

`venv_failed`, `venv_network`, `venv_python_unsupported`, `venv_interrupted`, `no_python`,
`python_store_stub`.

- A machine builds each release's pipeline environment from the home's wheelhouse; `venv_network`
  means neither the home nor PyPI had a package (a machine without internet whose home could not
  fetch the wheel for its platform: the home's log says which).
- `no_python` / `venv_python_unsupported`: install the Python the detail names (3.12), or set
  `MAKTABA_PIPELINE_PYTHON`. `python_store_stub` (Windows): only the Microsoft Store's `python3`
  alias exists; install Python from python.org or turn the alias off.
- `venv_interrupted`: the start was cut short; the next start finishes it.

### TV during the update

- `tv_busy`: someone watched until the window ended, on an install that restarts TV with it. Tried
  again in the next window; after three windows the page offers **Install now**. Plain server
  installs hand TV over and never wait for this.
- `tv_change_pending`: the release's TV change waited behind another change awaiting your approval
  (Live TV > Changes) until the window and its overrun ended. Approve or reject that change; the
  next window tries again. Nothing counts against the release.
- `failed_tv`: the TV change's checks or its swap failed, or the change was rolled back. The
  release rolled back and is blocked; the notice lists the failing checks (Live TV > Changes has
  the change's report). Skip the release or **Try it again** once the cause is fixed.
- `tv_timeout`: the change did not reach the air within `boundary_max_wait_minutes` + 10 minutes.
  The release rolled back; the attempt counts. Check the TV slots on System health.
- `tv_change_cancelled`: an admin cancelled the change; the release rolled back and stays blocked
  until **Try it again**.

### The TV restarted with the machine

A plain server install hands TV over across the restart; when it could not, the run log and the
journal's `handover_why` say why, and the machine restarted whole (under the TV gate): the front
door's `edge_protocol` changed in the new release (it must restart), a Linux or macOS launcher from
before the handover (the committed release's launcher replaces it, by itself, at the next update),
or a Windows server whose job lets no process leave it under a launcher from before the handover.
An adopted TV process that dies is started afresh by the new supervisor; one it could not adopt is
logged `supervisor_adopt_failed` and started afresh. Nothing to do.

### A machine did not follow

`failed_prepare`, `prepare_failed`, `prepare_refused`, `prepare_timeout`, `apply_failed`,
`apply_refused`, `node_health_timeout`, `held`, `window_over`, `outside_window`.

- **Maktaba did**: the machine rolled back by itself, stays drained, and is reported;
  `node_failure_policy` held the others of its platform (`held`), or all of them (`halt`), or let
  them go on (`continue`).
- **You**: read the machine's reason and logs; **Retry** it once mended, **Skip** it to let the
  rollout go on without it, or **Update the held machines** to resume. A machine that was offline
  is caught up when it comes back (`catch_up`), as is one whose part failed for a reason since
  mended.
- **A machine loses the home mid-apply**: it finishes what it can from its own journal; without
  the home its checks fail and it rolls back after `node_health_timeout_minutes`. Retry it.
- **The home unreachable for a machine's API**: past 30 seconds the machine serves read-only from
  its copy of the home's database; changes answer `home_unreachable`. Nothing to do once the home
  is back.
- **A machine paused by the schema** (its paused page; `update.node_paused_schema`): its code
  does not fit the home's database until its update lands; it is caught up at once.

### A worker-only machine did not become a full node

- `worker_only`: its pipeline predates the conversion command: run the node installer of the
  home's release once ([join a machine](join-a-machine.md#a-worker-only-machine-becomes-a-full-node)).
- `download_failed`, `release_incomplete`: the worker could not get the release (from the home's
  mirror, then GitHub). Check the machine's network; Retry.
- `verify_*`, `version_mismatch`: the release did not verify on the machine; nothing ran.
- `install_failed`: `setup --node` stopped; its own words are in the detail. The old worker units
  stay until the node answers, so the machine keeps working as a worker.
- `convert_refused`: another conversion was under way on the machine.
- `convert_timeout`: not back as a full node on the release within two hours. Look at the
  machine's `server.log`; the old worker may still run (its units are removed only once the node
  answered).

### An install that cannot update itself

- `not_self_managed`: a binary copied by hand: run `maktaba-server setup --self-managed` once.
- `old_launcher`: its launcher is from before automatic updates: install one release by hand.
- `launcher_protocol`: the release needs a newer launcher than a package manager's: the run names
  the command (`sudo apt install --only-upgrade maktaba-server`, `sudo dnf upgrade
  maktaba-server`, `brew upgrade maktaba`).
- `orchestrator_unavailable`, `needs_person`: this kind of install does not install releases by
  itself (a container of the stack built from source: `docker compose pull && docker compose up
  -d`).
- `local_build`: a desktop app built locally: install the release's app by hand once.
- A newer Docker image whose release could not start: `docker logs` says "could not start here: the
  launcher went back to …". Nothing to do: the volume runs what ran before, that image is not
  tried again, and the next release comes by the server's own update or a newer image.
- The desktop apps' own install reasons (`needs_admin`, `translocated`, `app_management`,
  `quarantined`, `login_item_unsigned`, `installer_not_finished`, `relaunch_failed`, …) and their
  fixes are in [update-attempts.md](../../specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md)
  and [auto-update.md](../auto-update.md#the-desktop-apps-own-updates).

### Two runs at once

Cannot happen: a machine holds `updates/lock` while it runs one, and the home keeps one active
run in `update_runs`. A run that seems stuck says what it waits for on Admin > Updates ("Not
started now: …"; "waiting for a click" on a desktop app's machine; "stuck" after three windows
with a viewer).

## Known issues

- **TV across a supervisor restart on three systems** is covered by unit and process tests, not
  yet by an end-to-end test with a live HLS client (story 35.7 TC7).
- **Windows**: a server started inside a job that refuses breakaway (some logon task setups) under
  a launcher from before the handover restarts TV with every update.
- **Worker-only conversion** has no end-to-end test (the two-machine test has no worker-only
  machine). The installer's `--full` path downloads the release from GitHub, not from the home.
- **A desktop app from before automatic updates**, or built locally, takes one release by hand.
- **The multi-service stack built from source** (`deploy/compose`) does not update itself; the
  one-container Docker install does.
