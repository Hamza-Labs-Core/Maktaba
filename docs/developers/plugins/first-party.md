# Maktaba's own plugins

> **Status: the foundation is on `main`** (Epic 54 Phase 4, stories 54.22-54.27): the trusted
> manifest, the native runner and its sandbox, the named statements and their roles, the
> graph from manifests with the per-stage switch, and the parity proof. **Wave 1 has moved**:
> `promo_links`, `clip_groups`, `topic_collections` and `rate_style` run in
> `tv.maktaba.classify` ([its folder](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/plugins/first-party/classify)).
> **Wave 2 has moved**: `thumb_small` and `chapter_thumbs` run in `tv.maktaba.thumbnails`, and
> `subtitle_gen` in `tv.maktaba.subtitles`, the first stages that read media, run ffmpeg and
> write files. **Wave 3 has moved**: `transcribe` and `second_listen` run in
> `tv.maktaba.transcription`, and the search index, chapters and topics in
> `tv.maktaba.semantic-search`. The other stages move wave by wave.

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

## An example: `tv.maktaba.classify`

`plugins/first-party/classify/` is a whole first-party plugin:

- `manifest.toml`: four `[[stage]]` entries, each field the stage had in the graph, and the
  tables its statements read and write (`db.read`, `db.write`), the `rules.*` sections it reads
  (`groups`, `detect`), runner group `light`.
- `src/maktaba_fp_classify/stages/`: the four stages' code, moved from the pipeline unchanged
  but for its imports.
- `statements.toml`: the shared pipeline modules whose statements the stages also run (the
  leftovers loader, the season helpers, the rules reader).
- `queries/`: 36 named statements, written by `statements_build.py sync classify`.
- `parity.toml` and `tests/test_classify_parity.py`: a seeded channel library (two shows, a playlist
  across both, clips, questions, a promo, talks) drained through the whole graph on two
  databases, the four stages in-process on one and through the plugin on the other.

When the release that moved a stage starts, each worker's graph sync sets the stage to run in
its plugin (a stage set back by hand stays as it was).

## Files and tools: `tv.maktaba.thumbnails` and `tv.maktaba.subtitles`

A stage that reads media or writes files names its **file areas** and its **tools**:

```toml
[trusted]
runner      = "light"
files.read  = ["library"]
files.write = ["data.thumbnails"]   # tv.maktaba.subtitles: ["library.subtitles"]
exec        = ["ffmpeg"]
```

- **The areas are this machine's folders.** The worker writes them for its runners
  (`areas.json`: the library roots through the machine's share-path mapping, the thumbnail
  cache). A runner builds its sandbox from them, and starts again when they change (a library
  was added); until it has, a stage that reads or writes files runs in the worker.
- **`library.subtitles`** is the library's folders, written only by the names Maktaba gives
  subtitle files (`<video stem>.<lang>.srt|vtt`). On macOS the sandbox matches the name; on
  both systems the runner refuses any other name there, and the job fails `plugin_scope`.
- **`exec`** lets the runner start only those tools (and loads their own libraries); a shell
  or any other program is refused.
- **A file the sandbox refuses** for another reason (a folder the runner was not started
  with) runs the job in the worker instead (`plugin_sandbox_refused` in the log), so the job
  still writes what it always wrote.

The parity proof of a file-writing plugin gives each side the same files to start from, and
compares the files each side leaves (`files` in `parity.toml`, a sha256 per file), along with
the rows, jobs and log lines.

## Heavy work: the ml and index runners

`tv.maktaba.transcription` (Whisper and the second listen) and `tv.maktaba.semantic-search`
(the embedder) load large models, so they run in runners of their own rather than the
shared ones:

- **`ml`**, one per transcription worker. A machine's `pipeline/stt-<i>` claims the GPU jobs
  and runs them in its own `plugin-runner/ml-<i>`, which holds the model and has the
  worker's slot and settings. With `gpu = "preferred"` its sandbox opens the GPU (Metal on
  macOS, the NVIDIA devices on Linux).
- **`index`**, beside `pipeline/index`, which keeps answering search. While the home's
  vectors are in Chroma (one process may open its folder), the index and chapter stages
  stay in the worker; once they are in Postgres, the runner writes them like any table.
- **No network.** A runner reads the model caches (`files.read = ["models"]`) with the
  Hugging Face libraries offline; a stage whose model is not downloaded yet runs in the
  worker, which fetches it.
- **Upkeep stays in the worker.** A stage's `refresh` (rules read and jobs queued before
  each selection) runs in the worker; `statements.toml` names such functions under `worker`
  so their statements are not registered.

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
