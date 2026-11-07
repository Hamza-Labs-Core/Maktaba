# Maktaba's own plugins

> **Status: the foundation is on `main`** (Epic 54 Phase 4, stories 54.22-54.27): the trusted
> manifest, the native runner and its sandbox, the named statements and their roles, the
> graph from manifests with the per-stage switch, and the parity proof. No stage has moved
> yet: every stage still runs in the pipeline worker until its wave ships (story 54.28 on).

Maktaba's own stages (thumbnails, transcription, the classifiers, the YouTube downloads and
the rest) become **first-party plugins**: the same manifest, the same tokens and the same plugin
API as yours, with a few rights no third-party plugin can have. This page says what those are,
how they are kept in check, and how a stage moves.

## Native, trusted, behind the same interface

A first-party plugin runs as Python in `maktaba-plugin-runner`, one process per **runner group**
(`light`, `ml`, `index`, `net`, `service`, and `fp` for fingerprints). A runner has no
database password and no secrets: the supervisor strips them from its environment and the
runner refuses to start if any is there. Before it imports a plugin it

1. checks that the first-party bundle and every file of its group's plugins are the bytes the
   release's signed wheel installed (`plugin_trusted_unsigned` otherwise);
2. applies its OS sandbox to itself: Seatbelt on macOS; Landlock and seccomp on Linux. The
   sandbox allows only what the group's manifests declare: the files of its areas, its media
   tools, the GPU, and the network only for a group that lists hosts. On Windows there is no
   runner and the stages run in the worker, as before.

The queue worker dispatches a job to the runner with the same kind of per-job token as a
third-party plugin's. The job's progress, log and events go back to the worker, which is the
only one that writes the queue's tables.

## The trusted manifest

A first-party manifest is the [manifest](manifest.md) you know with a `[trusted]` table and
richer `[[stage]]` entries. Every key marked `x-trusted-only` in the schema is refused in any
other manifest with `plugin_trusted_field`, and so is the `dup_group` subject. (A stage of
yours may share a name with one of Maktaba's: its queue is always `p_<hash>_<name>`.) Only
the manifests built into the release are read with
their `[trusted]` table: the server embeds them, and nothing on disk can add one.

```toml
id         = "tv.maktaba.classify"
runtime    = "native-python"

[trusted]
runner     = "light"
component  = "classify"            # its Epic 51 component: the Components page's switch
db.read    = ["series_seasons", "videos", "app_settings"]
db.write   = ["promo_links"]
settings   = ["groups", "detect"]  # the rules.* sections it reads

[[stage]]
name       = "promo_links"         # the exact name: queue rows carry over
subject    = "library"
pool       = "cpu"
lane       = "seasons"
lane_order = 22
version    = 1
entry      = "maktaba_fp_classify.stages.promo_links:IMPL"
```

## Named statements

A first-party plugin never runs SQL text. Its code keeps the pipeline's database interface
(`db.fetch(...)`, `db.execute(...)`, `async with db.transaction()`), so the same code runs in
the worker and in the runner; in the runner every call looks its text up in the plugin's
`queries/*.sql` and sends the plugin API only the statement's **name** and its arguments. The
API runs the text it registered from the release, in a transaction that is
`SET LOCAL ROLE maktaba_fp_<plugin>` (a role granted only the manifest's tables) with
`maktaba.origin` set to the plugin, and checks the statement's scope against the job's token:

```sql
-- name: promo_links.run.2
-- mode: write
-- scope: library $1
-- tables: promo_links*
DELETE FROM promo_links WHERE library_id = $1
```

`tools/plugins/statements_build.py sync <plugin>` writes `queries/` from the code (it reads
every statement the code passes to the database, and the parse tree of each for its tables,
mode and scope); `check` in CI fails when they drift, when a statement touches a table the
manifest does not list, writes a queue table or `videos.state`, or is built at run time. A
statement the runner meets that the plugin did not register never leaves the process: that
one job runs in the worker instead, and the parity proof fails.

## Where a stage runs: the switch

`job_stages.impl` says, per stage, `inproc` (the worker) or `plugin` (the runner). Each wave's
release flips its stages at update; an admin can flip one back in seconds:

```sh
maktaba-server stages impl promo_links inproc
```

or with **Runs in** on the stage's page under Queues (`PUT /api/queues/{stage}/impl`, admin
only, audited). A flip stops new claims of the stage, lets its running jobs finish (or pauses
them with their checkpoints: no attempt is spent), sets the stage and resumes. No job is lost
or rerun. A machine whose runner does not serve the stage runs it in the worker, which writes
the same data.

## The parity proof

Each wave must prove its stages do **exactly** what they did. `tools/plugins/parity.py` runs
the same jobs on two copies of one seeded database, in-process on one and through the plugin on
the other (the real plugin API, its statements and its role), and compares the rows of every
table the plugin writes, the files under its areas, each job's result and the jobs it
enqueued, every subject's fingerprints, and the job times (p50 at most 1.10 times, p95 at most
1.15 times the in-process path). Any difference fails, naming the table, the key and the
column.

## The repo layout

```
plugins/
  sdk/python/maktaba_plugin_sdk/   the native mode, the named statements, the parity helpers
  first-party/<name>/              one folder per plugin, as each wave adds it:
    manifest.toml  i18n/  queries/*.sql  statements.toml  parity.toml
    src/maktaba_fp_<name>/         its stages, fingerprints and domain code
    tests/                         unit, contract and parity tests
```

The plugins ride in the release's signed pipeline wheel; the server embeds their bundle
(`shared/plugins/firstparty.json`, `tools/plugins/bundle.py`).
