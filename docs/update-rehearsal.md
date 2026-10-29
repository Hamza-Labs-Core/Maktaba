# The update rehearsal

> The owner, 2026-10-28, after losing the site twice in one day: **"THIS NEED TO BE ON CI"**.
> Every update path that was only exercised live on the owner's Mac is proven in CI before
> any release.

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
- **Every push to main** (and to a `release/*` branch), and **nightly** (06:17 UTC).
- **Before every release.** The tag guard (`tools/release/tag-guard.sh`, the first job of
  `release.yml` and of every other tag workflow) refuses a tag unless the update rehearsal
  passed on **that exact commit**, as it does for CI. A run still going is waited for; a
  failed one refuses the tag. Nothing is published without it.
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
after it, + 3, for the scenarios that need one). The rehearsal adds test-only migrations of
its own on top of this commit's: an additive one (so every update has migrations), and for
the failure scenarios a second one past what the release's API takes for its own (it
migrates, then refuses to run on what it migrated: the injected health failure), additive or
destructive.

The pipeline is the end-to-end tests' stand-in (it answers its probes; no transcription or
TV staging job ever runs). The web UI is not built in (the web answers with its placeholder
page; the API through the web is checked).

## The seeded home

`api/rehearsal_seed_e2e_test.go` (`TestUpdateRehearsalSeed`): the previous release installed
as the app makes its database, then the seed fills it as the server itself
(`generate_series`), the big tables' indexes dropped and made again from their own
definitions so the schema is the release's exactly:

| Profile | Videos (with audio tracks, transcripts) | Segments and `segment_embeddings` (halfvec 384, HNSW) | Used by |
|---|---|---|---|
| full | 150,000 | 2,000,000 | 01 |
| restore | 150,000 | 300,000 | 02–06 (a restore rebuilds the HNSW index: minutes per million) |
| small | 5,000 | 20,000 | 07–10 |

Every profile also has 2,000 series with 40,000 episodes, 600 collections with their items,
three approved full machines (logins in `maktaba_worker`), and the first machine's copy
granted (`cluster_node_replica_grant`): the cluster copy's publication, and every table
granted to `maktaba_replicas`. The CI caches each seeded machine per previous release,
profile and seed (`rehearsal-seed-<tag>-<profile>-<hash>`): made once (the full one takes
about a quarter of an hour), restored in a minute after.

## The scenarios

`api/rehearsal_scenarios_e2e_test.go`, `api/rehearsal_machines_e2e_test.go`. Downtime is the
time the home's web did not answer `/api/cluster/self` (polled every 250 ms) from the update's
start to its end.

| # | Scenario | Asserts | Downtime budget |
|---|---|---|---|
| 01 | Normal update with migrations, full seed (the rehearsal on a scratch copy skipped above `rehearse_max_db_gb`, as on the owner's 47 GB home) | the run is `done`; the API serves; every seeded table's rows unchanged; the record `migrated` for the new release; no restore ran | 2 min |
| 02 | The TV's change waits during the watch (TV on, a live TV release; its staging never runs) | no `watch_timeout`, no rollback; the run is `done`; the TV step `pending` or `held` | 2 min |
| 03 | Forced rollback after additive migrations (this commit installed, then the release after it, whose API refuses what it migrated) | `rolled_back` with nothing put back; the record `kept_for` this commit; the newer schema still there; this commit serves on it | 4 min |
| 04 | Forced rollback after a destructive migration (vectors truncated, an index and a column dropped) | the restore takes the safe path (a scratch database, checked whole, renamed into place); rows of every seeded table, every index, constraint, trigger, function, grant (table, column, function, schema), default privilege, the database's own grants and the vectors' checksum equal to before; the previous release serves | 10 min |
| 05 | 04 while three machines log in again and again and ask for their copy (`cluster_node_beat`, `cluster_node_replica_grant`: GRANTs and `ALTER PUBLICATION` on every table) | the restore completes and is whole as in 04 | 10 min |
| 06 | 04 with `kill -9` of `pg_restore` and the server that runs it mid-restore | right after the kill the library database in place is untouched (still the failed release's); the site comes back by itself, the restore finished or redone, whole | 20 min |
| 07 | The roles halted by the database step (a restore it cannot do) | once the record is written `restored` by hand, the roles start with no restart of the server | 1 min from the record |
| 08 | No room for the dump and the rehearsal's copy (a 6 GB loop volume of its own, filled to 1.5 GB free) | the documented behaviour: the run is refused before anything changes (`disk_space`); the home never switches; the record and the rows unchanged; the site serves | 5 s |
| 09 | The owner's Mac's state: the previous release running from its kept copy (`payload-override`), the app's own payload a failed newer release (`payload-override.failed`), the record put right by hand | the update to this commit is `done`; the override gone; the app's payload this commit | 2 min |
| 10a | A home and a joined full machine (a Mac app too): the home rolls back during its watch; then a run that gives up on its home mid-watch (`journal_lost`); then an update that passes | the machine is never ordered before the home commits, stays on its release, not paused, serving; in the passing run it is ordered only after the commit | 4 min, then 2 min |
| 10b | The joined machine's own updater, a newer release published while the home stays on its release | the machine does not install it; it stays on its release, not paused, serving | — |

### Expected failures

`tools/rehearsal/expected-failures.txt` lists scenarios whose fix is not merged yet, with the
fix's PR. Such a scenario still runs; its failure is reported (annotation, run summary) but
not counted, until the PR's merge commit is in the commit rehearsed: from then on it is
required, with no edit. Today: 10a and 10b, until #434 (`fix/machines-after-home`).

## Run it locally

Never against a live install: every machine of the rehearsal has its own `HOME`, folders
and free ports, and its `open`/`xdg-open` do nothing. Scenario 02 runs the TV slots, which
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

| Variable | |
|---|---|
| `MAKTABA_E2E_REHEARSAL=1` | runs the rehearsal's tests (skipped otherwise) |
| `MAKTABA_REHEARSAL_KIT` | `build.sh`'s `--out` |
| `MAKTABA_REHEARSAL_PG_HOME` | the bundled Postgres (`bin/postgres`, pgvector) |
| `MAKTABA_REHEARSAL_WORK` | the machines' folders (default: the test's temporary folder) |
| `MAKTABA_REHEARSAL_SEEDS` | the seeded homes |
| `MAKTABA_REHEARSAL_PROFILE` | `TestUpdateRehearsalSeed`'s profile |
| `MAKTABA_REHEARSAL_PROFILE_OVERRIDE` | one profile for every scenario |
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
| Expected failures | `tools/rehearsal/expected-failures.txt` |
| The release gate | `tools/release/tag-guard.sh` (`passed_on`), `tools/release/tag-guard-test.sh` |

See also [updates.md](updates.md) and Epic 35's
[install-kinds.md](../specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md).
