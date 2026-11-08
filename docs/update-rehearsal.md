# The update rehearsal

> Requirement (after two outages in one day, 2026-10-28): every update path that was only
> exercised live on the owner's Mac is proven in CI before any release.

The update rehearsal (`.github/workflows/update-rehearsal.yml`, its verdict job
**update-rehearsal**) updates a seeded home from the **latest published release** to **this
commit**, the way the owner's Maktaba.app does, and breaks it the ways it broke on
2026-10-28. Each scenario is its own job; each ends with the site serving, reports its
downtime and fails above its budget, and keeps the servers' logs as an artifact when it
fails.

## When it runs

- **Every pull request** that touches the update, the restore, the migrations, the
  supervisor, the launcher, TV staging or the schema (the workflow's `paths`, with
  `shared/db/migrations/**` and the rehearsal itself).
- **Every commit of main CI passed on**, after CI: when `ci.yml`'s run of a push to main
  succeeds, its `workflow_run` starts the rehearsal on that same commit (every job checks out
  `github.event.workflow_run.head_sha`). Main's rehearsals share one concurrency group, not
  cancel-in-progress: the running one finishes, and the newest CI-green commit's run replaces
  an older pending one. So a green rehearsal of main is always on a commit CI passed on: a
  commit that can be tagged. (Until 2026-11-07 it ran on every push to main, beside CI. Both
  workflows replaced their pending main run, so they finished on different commits; from
  00:54 that day no commit had both green, and nothing could be tagged.) A CI run that did not
  pass, most of them cancelled while pending, still starts a run here. That run runs no job and
  is named "Update rehearsal not run (CI <conclusion>)", in a group of its own, so it never
  replaces the pending rehearsal. A trail-only push to main starts no CI run, so no rehearsal
  follows it.
- **Every push to a `release/*` branch** (its own push trigger, with CI's `paths-ignore`), and
  **nightly** (06:17 UTC).
- **Where main's result is.** A `workflow_run` run's own `head_sha` is main's head at the
  moment it started, not the commit it rehearsed. So its checks show on that newer commit.
  The run is named `Update rehearsal of <sha>` after the commit it rehearsed, and its verdict
  sets that commit's `update-rehearsal` commit status. The tag guard and
  `tools/release/next_tag.py` find main's run by its name, and never count a `workflow_run`
  run for its `head_sha`.
- **Before every release.** The tag guard (`tools/release/tag-guard.sh`, the first job of
  `release.yml` and of every other tag workflow) refuses a tag unless the update rehearsal
  passed on **that exact commit**, as it does for CI. A run still going is waited for; a
  failed one refuses the tag. Nothing is published without it.
- **It makes the release.** A session pushes each tag: when CI and the rehearsal have both
  passed on a commit of main, the scheduled release session tags the newest such commit as the
  next patch version, and that tag push starts every tag workflow on it (see
  [updates.md](updates.md#how-a-release-is-made)).
- By hand: Actions → Update rehearsal → Run workflow (`prev_tag` to update from another
  release; `cur_ref` to rehearse another ref's code with this checkout's harness).

## What it reproduces

The Mac app's install kind runs on Linux through the same code, not a stand-in:

| On the owner's Mac | In the rehearsal |
|---|---|
| `/Applications/Maktaba.app/Contents/Resources/server` (bin/, postgres/, migrations/, payload.json) | `<machine>/home/Applications/Maktaba.app/Contents/Resources/server`, same layout; the launcher finds the bundle by its path, on any OS (`cmd/maktaba-launcher/bundle.go`) |
| launchd runs `maktaba-launcher serve` from the bundle | the test runs it, with the machine's own `HOME` |
| `~/Library/Application Support/Maktaba` (the launcher's guard `updates/pending.json`, `payload-override`, `payload-override.failed`, `updates/payloads/<v>`, `state/database.json`, `updates/app-install.json`, `updates/restart`) | the same folder under the machine's `HOME`; `server.toml` there (`data_dir` is it) |
| the bundled PostgreSQL 16 + pgvector 0.8.7 | the published release's own Linux payload's `postgres/` (`tools/rehearsal/postgres.sh`): what the Linux app and the Docker image run |
| the Tauri app stages the home's order and, at its go, stops the server, swaps the bundle and starts it again | the test's `runApp` does exactly that with the order file (`autoinstall.rs`, `shell.rs`) |
| GitHub releases signed by Sigstore | a fake GitHub and a fake Sigstore; the binaries are built with `-tags e2etrust`, which only changes where the trust root and the releases API come from |

The two releases are real builds (`tools/rehearsal/build.sh`): the **previous** one from its
tag, and **this commit** as the next release (its version the previous one + 2; the release
after it, + 3, for the scenarios that need one; + 4, a release after a rollback, scenario 13). With
the release's own pack build (`tools/release/packs/build.sh --only tool.yt-dlp`, then
`gen-components-index`), the kit also holds `packs/<version>/`: this commit's `tool.yt-dlp`
pack of each of those three releases for the runner's platform, and its components index (the
harness signs it with the release; scenarios 13 and 14). The rehearsal adds test-only migrations of
its own on top of this commit's: an additive one (so every update has migrations), and for
the failure scenarios a second one past what the release's API takes for its own (it
migrates, then refuses to run on what it migrated: the injected health failure), additive or
destructive.

The pipeline is the end-to-end tests' stand-in (it answers its probes; no transcription or
TV staging job ever runs: scenario 11 plays the `tv_stage` job's database writes itself),
except in scenarios 16.x. There each release runs its own pipeline: the kit carries each
release's real pipeline wheel, stamped with its version, without the ML extras
(`build.sh`), and `tools/rehearsal/venvs.sh` makes one environment of each, once per release.
The home's Python is a small script that runs the environment of the release whose server
started it, so a rollback runs the previous release's pipeline again. Only the work of the
wave's stages changes (`tools/rehearsal/sleepy`): each job sleeps 8 s wherever its release
runs it (in the worker, or in its runner), and logs where it ran. The claims, the drain,
the flip and the runners are the releases' own code. The
streaming binary is built with `-tags tvstaging`, as the release builds it. The web UI is
not built in (the web answers with its placeholder page; the API through the web is checked).

## The seeded home

`api/rehearsal_seed_e2e_test.go` (`TestUpdateRehearsalSeed`): the previous release installed
as the app makes its database, then the seed fills it as the server itself
(`generate_series`), the big tables' indexes dropped and made again from their own
definitions so the schema is the release's exactly:

| Profile | Videos (with their probe, audio tracks, transcripts) | Segments and `segment_embeddings` (halfvec 384, HNSW) | Used by |
|---|---|---|---|
| full | 150,000 | 2,000,000 | 01 |
| restore | 150,000 | 300,000 | 02–06 (a restore rebuilds the HNSW index: minutes per million) |
| small | 5,000 | 20,000 | 07–14 |

Every profile also has 2,000 series with 40,000 episodes, 600 collections with their items,
three approved full machines (logins in `maktaba_worker`), and the first machine's copy
granted (`cluster_node_replica_grant`): the cluster copy's publication, and every table
granted to `maktaba_replicas`. The CI caches each seeded machine per previous release,
profile and seed (`rehearsal-seed-<tag>-<profile>-<hash>`): made once (the full one takes
about a quarter of an hour), restored in a minute after.

The seeded machine is made with the TV slots off. From v0.3.81 the previous release seeds
its components when the seeded machine is made, and from then on the database decides
which TV runs (story 51.03): `[tv] slots = "on"` in `server.toml` no longer turns the slots
on. A scenario that airs TV (02, 11, 12) starts its home with `startWithTV`, which turns
`livetv.slots` on (and `livetv.inprocess` off) with the components' apply, as the owner does
in Settings, waits for the reconciler's cache and starts the home again.

## The scenarios

`api/rehearsal_scenarios_e2e_test.go`, `api/rehearsal_machines_e2e_test.go`,
`api/rehearsal_tv_e2e_test.go`, `api/rehearsal_components_e2e_test.go`,
`api/rehearsal_packs_e2e_test.go`. Downtime is the
time the home's web did not answer `/api/cluster/self` (polled every 250 ms) from the update's
start to its end.

| # | Scenario | Asserts | Downtime budget |
|---|---|---|---|
| 01 | Normal update with migrations, full seed (the rehearsal on a scratch copy skipped above `rehearse_max_db_gb`, as on the owner's 47 GB home) | the run is `done`; the API serves; every seeded table's rows unchanged; the record `migrated` for the new release; no restore ran | 2 min |
| 02 | The TV's change waits during the watch (TV on, a live TV release; its staging never runs) | no `watch_timeout`, no rollback; the run is `done`; the TV step `pending` or `held` | 2 min |
| 03 | Forced rollback after additive migrations (this commit installed, then the release after it, whose API refuses what it migrated) | `rolled_back` with nothing put back; the record `kept_for` this commit; the newer schema still there; this commit serves on it | 4 min |
| 03b | Forced rollback after a lossless migration the schema-keep rule cannot keep: as 03, but the failing release's migration adds a CHECK to `rehearsal_probe`, which loses no data and has a Down script | `rolled_back` with nothing put back (no restore, no dump); the log says the failing release undid its migrations with their Down scripts; the record is this commit's own (not `kept_for`), with `down_from` the failing release; the CHECK is gone, this commit's own migration (`rehearsal_probe`) stays; the seeded counts unchanged; this commit serves | 4 min |
| 04 | Forced rollback after a destructive migration (vectors truncated, an index and a column dropped) | the restore takes the safe path (a scratch database, checked whole, renamed into place); rows of every seeded table, every index, constraint, trigger, function, grant (table, column, function, schema), default privilege, the database's own grants and the vectors' checksum equal to before; the previous release serves | 10 min |
| 05 | 04 while three machines log in again and again and ask for their copy (`cluster_node_beat`, `cluster_node_replica_grant`: GRANTs and `ALTER PUBLICATION` on every table) | the restore completes and is whole as in 04 | 10 min |
| 06 | 04 with `kill -9` of `pg_restore` and the server that runs it mid-restore | right after the kill the library database in place is untouched (still the failed release's); the site comes back by itself, the restore finished or redone, whole | 20 min |
| 07 | The roles halted by the database step (a restore it cannot do) | once the record is written `restored` by hand, the roles start with no restart of the server | 1 min from the record |
| 08 | No room for the dump and the rehearsal's copy (a 6 GB loop volume of its own, filled to 1.5 GB free) | the documented behaviour: the run is refused before anything changes (`disk_space`); the home never switches; the record and the rows unchanged; the site serves | 5 s |
| 09 | The owner's Mac's state: the previous release running from its kept copy (`payload-override`), the app's own payload a failed newer release (`payload-override.failed`), the record put right by hand | the update to this commit is `done`; the override gone; the app's payload this commit | 2 min |
| 10a | A home and a joined full machine (a Mac app too): the home rolls back during its watch; then a run that gives up on its home mid-watch (`journal_lost`); then an update that passes | the machine is never ordered before the home commits, stays on its release, not paused, serving; in the passing run it is ordered only after the commit | 4 min, then 2 min |
| 10b | The joined machine's own updater, a newer release published while the home stays on its release | the machine does not install it; it stays on its release, not paused, serving | — |
| 11 | The home updates while its TV slots run (the live TV on the previous release's streaming; roles live blue, standby amber, so green, started with every server, is the free instance a staging takes); then the update's TV change is staged (the pipeline's `tv_stage` job played by the test: the stand-in runs no job) | the slot the staging takes was running before its bind; it is started again on the change's release (a new process, answering that release, no mismatch); the change moves to verifying on this commit's streaming, never back to a draft with `slot.health` (`release_mismatch`: change #58, 2026-10-31). Not covered: the change's soak and swap (no channel here airs real media) | 2 min |
| 12 | No behaviour change: an update leaves what a home runs alone (Epic 51, [story 51.03](../specs/epics/51-configurable-components/story-51-03-seed-from-today.md)). The small seed's home on the previous release (which seeded its components when the seeded home was made, v0.3.81 on) with the TV slots turned on in the components, the relay off, Plex enabled, `youtube_backfill` turned off by an operator and the HDHomeRun device off; after the update the server is restarted once more | the supervisor's processes (role, instance, wanted, running), `job_stages` (enabled, disabled_reason, paused), `extensions` and `hdhr_device.enabled` equal before and after; `component_meta.seeded_at` unchanged, bundle `full`; every component row of the previous release keeps its want, generation and writer, with the want §2 gives this home; the loops the previous release runs, read from its own `component_status` (part `api` or `streaming`) before the update once every switchable component it left enabled with a loop there reports one `running`, are `running` after it, and no other switchable loop of those hosts is, but one of a component the previous release had no row for (one this release adds) that the Full bundle has; each component the previous release had no row for and this release's seed wrote follows the bundle (on in it, off outside it, an asset installed in it and absent outside it unless a component that seed turned on requires it), while one the previous release seeded keeps its row whatever the bundle says; the restart moves neither `seeded_at` nor any `generation`, one seed in the audit log, no `toml_key_superseded` after the update. The first seed (from a release before the component model) was rehearsed from v0.3.80 when v0.3.81 was tagged; `componentseed`'s tests keep it. The pipeline's loops and stage claiming are not here (the stand-in runs no job) | 2 min |
| 13 | An update carries the packs (Epic 51, [story 51.18](../specs/epics/51-configurable-components/story-51-18-update-rehearsal.md)). The small seed's home on this commit (installed by hand, as in 03) with the `tool.yt-dlp` pack of this commit imported (`POST /components/import` on the supervisor's admin, an offline folder of the release's signed files, its index and the pack); an update to the release after it, with its own pack, that fails its watch (03's injected failure); then an update to the release after that, which passes | the import installed the pack and `current` names its folder (`<version>_<sha12>`); the run ends `rolled_back` with no restore; the journal's `packs` step is `done` and ended before the apply's first step (`order`, `backup`, `switch`) started; the journal names the staged folder (the new release's, replacing the running one's); `current` was seen naming the new release's folder while it ran, and names the kept folder again after the rollback, which is still there; the progress route says installed; the seeded counts unchanged; the second update ends `done` within the same budget as an update without packs (N13); `current` names its folder; after its commit every pack folder belongs to a kept release (the app's payload and `updates/payloads/*/.kept`) or the running one, the kept releases' folders are there, and the rolled-back release's is gone unless it is kept | 4 min (the rollback), 2 min (the update after) |
| 14 | A pack that fails its verification stops the update. The home of 13 (this commit, the pack imported); the release after it is published with its pack's bytes changed by one after signing (its `checksums.txt` and index list the bytes it was built with) | the run is neither `done` nor `rolled_back`; the journal ends `prepare_failed` with `pack_verify_failed` at the `packs` step, said in the run's error; the journal never went `applying`, `restarting` or `watching`; the pack folders and `current` unchanged; the database's record unchanged; the progress route still says installed; the site serves this commit; the seeded counts unchanged | 5 s |
| 15 | Plugins follow an update and its rollback (Epic 54, [story 54.07](../specs/epics/54-plugins/story-54-07-lifecycle.md) §8, T10, T14). The small seed's home on this commit (installed by hand, as in 03) with the kit's `maktaba-plugind` (`plugins/`, built by `build.sh` when cargo is there); the admin allows unreviewed plugins and installs three packages of the host's test guest through `POST /api/plugins/install`: A (any server version, on), B (a range that excludes the next releases, on, 20 jobs queued) and C (turned off by the admin: kept off); an update that fails its watch (03's injected failure), then one that passes | A and B are Running on this commit; while the failing release runs (its API crash-loops by design, so B's status row is read from the database: the supervisor's `plugins` part, story 54.07 §8), B is seen waiting `incompatible` and never failed; after the rollback A and B are Running again; C is `installed` and kept off after every step; B's jobs are all there after the rollback, none failed or cancelled; on the release that passes, A is Running, B waits `incompatible` (still enabled) and its newly queued jobs are kept and not run. The stand-in pipeline runs no job: the state is the home's (the supervisor's part), and plugin jobs run in the components CI (`components-chaos`, `plugins-live`) | 4 min (the rollback), 2 min (the update after) |
| 16.1–16.6, 16.8, 16.9 | Epic 54 Phase 4's waves across an update ([stories 54.28–54.36](../specs/epics/54-plugins/story-54-28-wave1-leaf-classify.md), each one's T05): the small seed's home on the previous release, running each release's own pipeline, with jobs of the wave's stages queued and running (16.1: `promo_links`, `clip_groups`, `topic_collections`, `rate_style` where the seed has seasons; 16.2: `thumb_small`, `chapter_thumbs`, `subtitle_gen`; 16.3: `topic_assign`, `chapter_infer`; 16.4: `detect`, `tag`; 16.5: `dedup_scan`; 16.6: `exact_dates`; 16.8: `plex_groups`, the Plex extension turned on through Settings first; 16.9: `scan`, `probe`, `link`, `fp_gate`, the seed given a series with two seasons first (the small seed has none, and `fp_gate` claims seasons), core stages of `tv.maktaba.core-media` in the light runner, which no admin can switch: the release flips them); the update to the release that fails its watch; more jobs; the update to this commit; then two more jobs of each stage | every job ends done, none failed or cancelled, none took a second attempt or started twice; each ran where its release runs the stage: in its runner (`plugin`) on a release whose built-in plugins have it, in-process on one without, so a rollback to a release from before the wave runs it in-process again; on a release with the plugin, a job claimed before its runner says Hello runs in-process (story 54.25 §3); once this commit serves, every stage of the wave is `plugin` and the last jobs run in their runners; no restore. 16.9 also fails if a round queued no job of one of its stages. Every other wave's row is in `api/rehearsal_waves_e2e_test.go` (16.7 has its own, below); each is skipped until its wave's plugins are in the commit. A scenario first waits out any open job of its stages (an enqueue on a subject with one joins it: a home scans its libraries itself). The jobs a scenario queues are due at once (a stage's debounce, 300 s for `tag` and `exact_dates`, is not what it rehearses), and after the flip it runs warm-up jobs until each stage that got a job has run in its runner (a runner restarting on new areas leaves a moment with none serving; a runner started on demand exits after a minute idle, so a job may wait for it to start again, and still runs there) | 4 min (the rollback), 2 min (the update) |
| 16.7 | Wave 7's TV change across an update ([story 54.34](../specs/epics/54-plugins/story-54-34-wave7-livetv.md), T02): the small seed's home on the previous release with its TV slots on, a live TV release on that release's streaming, and each release's own pipeline, the stages `tv_stage`, `tv_swap`, `tv_rollback` and `tv_gc` doing their real work (only witnessed: where each job ran). A TV setting saved with staging on makes a change; `tv_stage` stages it in-process (the stage's switch set to `inproc` when that release has the plugin); its verification is played (the soak needs channels airing real media, and the seed has none); the admin approves it to swap six hours later. The update to this commit; once the stages are `plugin` and a `tv_gc` ran in the runner, Swap now; then the admin's rollback | the stage ran once, in-process, on the previous release; the update is `done` with the change still approved on its release (not set aside, not lost); `tv_swap` ran once, in the runner, on this commit, and the swap completes: the change is `live`, `tv_routing` airs its release with the old one as previous, its slot is live and the old live slot is previous; the rollback is warm (the front door's operation): `tv_rollback` ran once, in the runner, the change is `rolled_back`, the previous release is live again on the previous slot; the seeded counts unchanged; no restore | 2 min (the update) |

### Expected failures

`tools/rehearsal/expected-failures.txt` lists scenarios whose fix is not merged yet, with the
fix's PR. Such a scenario still runs; its failure is reported (annotation, run summary) but
not counted, until the PR's merge commit is in the commit rehearsed: from then on it is
required, with no edit. None today: 10a and 10b were listed until #434
(`fix/machines-after-home`) merged on 2026-10-31.

## Run it locally

Never against a live install: every machine of the rehearsal has its own `HOME`, folders
and free ports, and its `open`/`xdg-open` do nothing. Scenarios 02, 11 and 16.7 run the TV slots, which
listen on fixed ports (8791–8793, 9111–9113): it refuses to run on a machine where another
Maktaba airs TV (the owner's Mac). Scenario 08 needs a volume of its own
(`MAKTABA_REHEARSAL_SMALL_VOLUME`) and is skipped without one.

On Linux (the CI's way):

```sh
git worktree add /tmp/rh/src-prev v0.3.42          # the previous release
tools/rehearsal/build.sh --prev-src /tmp/rh/src-prev --prev-version 0.3.42 --out /tmp/rh/kit
GH_TOKEN=… tools/rehearsal/postgres.sh --tag v0.3.42 --out /tmp/rh/kit/pg
cd api
MAKTABA_E2E_REHEARSAL=1 MAKTABA_REHEARSAL_KIT=/tmp/rh/kit MAKTABA_REHEARSAL_PG_HOME=/tmp/rh/kit/pg \
  MAKTABA_REHEARSAL_WORK=/tmp/rh/work MAKTABA_E2E_LOG_DIR=/tmp/rh/logs \
  go test -count=1 -timeout 60m -run '^TestUpdateRehearsal01NormalUpdate$' -v .
```

On a Mac, build with `CGO_ENABLED=0` (as `build.sh` does) and point
`MAKTABA_REHEARSAL_PG_HOME` at a **copy** of a Postgres 16 with pgvector (for instance
`cp -R /Applications/Maktaba.app/Contents/Resources/server/postgres /tmp/rh/pg`: a copy of
the programs, never the app's database). `MAKTABA_REHEARSAL_PROFILE_OVERRIDE=small` runs every
scenario on the small seed (minutes instead of a quarter of an hour for the first seed).
A missing seed is made on the fly in `MAKTABA_REHEARSAL_SEEDS` (default
`$MAKTABA_REHEARSAL_WORK/seeds`).

Scenarios 16.x need the releases' pipeline environments (Python 3.12 and uv):
`tools/rehearsal/venvs.sh --kit /tmp/rh/kit` makes them in the kit's `venvs/`, and keeps
one already made.

| Variable | |
|---|---|
| `MAKTABA_E2E_REHEARSAL=1` | runs the rehearsal's tests (skipped otherwise) |
| `MAKTABA_REHEARSAL_KIT` | `build.sh`'s `--out` |
| `MAKTABA_REHEARSAL_PG_HOME` | the bundled Postgres (`bin/postgres`, pgvector) |
| `MAKTABA_REHEARSAL_WORK` | the machines' folders (default: the test's temporary folder) |
| `MAKTABA_REHEARSAL_SEEDS` | the seeded homes |
| `MAKTABA_REHEARSAL_PROFILE` | `TestUpdateRehearsalSeed`'s profile |
| `MAKTABA_REHEARSAL_PROFILE_OVERRIDE` | one profile for every scenario |
| `MAKTABA_REHEARSAL_VENVS` | the releases' pipeline environments, scenarios 16.x (`venvs.sh`'s `--out`; default `$MAKTABA_REHEARSAL_KIT/venvs`) |
| `MAKTABA_REHEARSAL_SMALL_VOLUME` | scenario 08's volume (filled; never this machine's disk) |
| `MAKTABA_REHEARSAL_REQUIRE_ALL=1` | a scenario that cannot run here fails instead of skipping (CI) |
| `MAKTABA_E2E_LOG_DIR` | each machine's whole log |

## Where things are

| Piece | Code |
|---|---|
| The workflow, its jobs, the verdict | `.github/workflows/update-rehearsal.yml` |
| The two releases' binaries | `tools/rehearsal/build.sh` |
| The bundled Postgres | `tools/rehearsal/postgres.sh` |
| The app-bundle machine, its app, the fake GitHub and Sigstore, downtime, the site's checks | `api/rehearsal_harness_e2e_test.go` |
| The seed | `api/rehearsal_seed_e2e_test.go` |
| Scenarios 01–09, the database's fingerprint | `api/rehearsal_scenarios_e2e_test.go` |
| Scenario 10 | `api/rehearsal_machines_e2e_test.go` |
| Scenario 11 | `api/rehearsal_tv_e2e_test.go` |
| Scenario 12, the loops the previous release runs on the rehearsal's home | `api/rehearsal_components_e2e_test.go` (read from the previous release's `component_status` and component rows before the update: nothing to keep per release) |
| Scenarios 13 and 14, the releases' packs and their index, the offline import | `api/rehearsal_packs_e2e_test.go`; the packs in the kit: `tools/rehearsal/build.sh`, `tools/release/packs/build.sh` |
| Scenarios 16.x, the waves' stages across an update | `api/rehearsal_waves_e2e_test.go`; each release's pipeline wheel: `tools/rehearsal/build.sh`; their environments: `tools/rehearsal/venvs.sh` (CI: the kit job builds them, the previous release's from its cache by tag, and hands them to the 16.x jobs as the `rehearsal-venvs` artifact); the sleeping stages: `tools/rehearsal/sleepy/maktaba_rehearsal_sleepy.py` |
| Expected failures | `tools/rehearsal/expected-failures.txt` |
| The release gate | `tools/release/tag-guard.sh` (`passed_on`), `tools/release/tag-guard-test.sh` |

See also [updates.md](updates.md) and Epic 35's
[install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md).
