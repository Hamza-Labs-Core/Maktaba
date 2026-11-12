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
> `tv.maktaba.semantic-search`. **Wave 5 has moved**: the conversion to MP4 runs in
> `tv.maktaba.convert`, and finding and merging duplicates, the file trash and restoring
> from it in `tv.maktaba.dedup`. **Wave 6 has moved**: the YouTube listings, downloads and
> exact dates run in `tv.maktaba.youtube`, and the repair of broken downloads in
> `tv.maktaba.repair`, the first stages that reach the network. **Wave 7 has moved**: the
> channel planner and writer run in `tv.maktaba.livetv`, and staging, swapping, rolling back
> and cleaning up TV changes in `tv.maktaba.livetv-slots`. **Wave 8 has moved**: Plex Live
> TV runs in `tv.maktaba.plex`, through the home's service door (the Plex library sync and
> the dizqueTV plugin were removed since). **Wave 4 has moved**: `detect`, `tag`, `check_tags`,
> `check_clips`, `books` and `review_pages` run in `tv.maktaba.classify`, the model's questions
> through the plugin API's `compute.generate`; `enrich` stays in the worker for now. **Wave 9
> has moved the core stages**: finding the videos, reading their length, identity, thumbnail
> and audio fingerprints, linking them to their YouTube pages and the access check run in
> `tv.maktaba.core-media`; `thumbnail` joins `tv.maktaba.thumbnails` and `extract` joins
> `tv.maktaba.transcription`. Two stages stay in the core: `component_catchup` and
> `categorize`.

Maktaba's own stages (thumbnails, transcription, the classifiers, the YouTube downloads and
the rest) become **first-party plugins**: the same manifest, the same tokens and the same plugin
API as yours, with a few rights no third-party plugin can have. This page says what those are,
how they are kept in check, and how a stage moves.

## Native, trusted, behind the same interface

A first-party plugin runs as Python in `maktaba-plugin-runner`, one process per **runner group**
(`light`, `files` for the plugins that rewrite or move library files, `ml`, `index`, `net`,
`service`, and `fp` for fingerprints). A shared group's runner starts when the worker has a
job for it and stops after an idle minute, so an idle home runs none; until it is up, the
worker runs the job itself. A group whose components are all off is held: nothing starts it.
A runner has no
database password and no secrets: the supervisor strips them from its environment and the
runner refuses to start if any is there. Before it imports a plugin it

1. checks that the first-party bundle and every file of its group's plugins are the bytes the
   release's signed wheel installed (`plugin_trusted_unsigned` otherwise);
2. applies its OS sandbox to itself: Seatbelt on macOS; Landlock and seccomp on Linux. The
   sandbox allows only what the group's manifests declare: the files of its areas, its media
   tools, the GPU, and the network only for a group that lists hosts. On Windows the runner
   runs in its group's own AppContainer and Job Object, which the supervisor's launcher
   applies before it starts, and the runner refuses to serve outside it (see
   [the sandbox](sandbox.md)).

The queue worker dispatches a job to the runner with the same kind of per-job token as a
third-party plugin's. The job's progress, log and events go back to the worker, which is the
only one that writes the queue's tables.

### What a runner may publish

A runner's live events (the `publish` request on the job's connection) go onto the `jobs`
channel that every web client and app reads, so the worker checks each one before it
publishes it:

| Kind | Payload |
| --- | --- |
| `job.progress` | `{"job": JobRef}`: the job's id, stage, state, subject and library, its label, attempts, times and progress document; no other key |
| `job.log` | `job_id`, `stage`, `level` (`debug`, `info`, `warn` or `error`), `kind`, `message`, and optionally `subject_label` and a `data` object; no other key |

1. **Only these kinds.** `job.started`, `job.finished` and `job.failed` are the worker's
   own; any other kind is refused (`unknown_kind`).
2. **At most 64 KiB** of compact JSON (`oversize`).
3. **The kind's schema**: every required key, no unknown key, each value of its type, strings
   of at most 4096 characters (`schema`).
4. **Only its own topic**: the job id and stage the payload names are the running job's
   (`topic`).
5. **Only ids in its scope**: a `job.progress` ref's subject kind, subject id and library id
   are the job's, compared as uuids, the way the plugin API compares a statement's ids
   (`scope`).

A refused event is an error back to the runner's code (`plugin_publish_refused: <reason>:
<detail>`), nothing is published, and the worker logs `plugin_publish_refused` with the
plugin, the kind, the reason and the payload's size; never the payload, which can carry a
title. The job's progress and log lines have their own requests, which the worker writes
with the job's own values, so a stage rarely publishes at all.

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

### The scope a write must meet

A write is checked against the job, never bound to it by the plugin's own code: its header
lists a check for **every** id that binds the rows it touches, and the API runs each one before
the statement (any one outside the job's scope is `403 plugin_scope`):

```sql
-- name: promo_links.run.3
-- mode: write
-- scope: subject $1, library $2, series $3, season $4
-- tables: promo_links*
INSERT INTO promo_links (video_id, library_id, series_id, season_id, ...) VALUES ($1,$2,$3,$4, ...)
```

`sync` writes these checks itself, from the statement's parse tree and
`tools/plugins/scope_tables.toml`, which says which column of which table holds which kind of
id: `subject` (a video), `library`, or a row the API looks up (`season`, `series`,
`transcript`, `segment`, `audio_track`, `file_action`, `dup_group`, `clip_group`,
`topic_collection`, `audio_cache`, `youtube_source`, `youtube_channel`, `youtube_playlist`,
`youtube_video`, `collection`, `job`, `job_subject`). `check` fails a write that does not meet
these rules:

- an `UPDATE` or `DELETE` ties its rows to an owner column by a top-level `AND` term of its
  `WHERE`: `col = $N`, `col = ANY($N)`, or `col IN (SELECT r.c FROM r WHERE …)` whose inner rows
  are bound the same way (an `OR` binds nothing); an owner column its `SET` fills from a `$N`
  is checked too;
- an `INSERT` checks every owner column it fills from a `$N`, and fills at least one;
- `ON CONFLICT … DO UPDATE` has an owner column in its conflict target (or a
  `DO UPDATE … WHERE t.col = EXCLUDED.col`), so the update cannot reach another scope's row;
- a table no library owns is listed under `[home]` in `scope_tables.toml` with its reason; one
  marked `catalog_only` (the TV lineup) is written only by a plugin whose stages all take the
  catalog as their subject;
- a table only your plugin has gets its owner columns in your `statements.toml`
  (`[tables.<table>]`), and a write the parse tree cannot bind gets its checks by hand,
  with the reason they are right, reviewed with the plugin:

```toml
[scope."pgstore.write_vectors"]
checks = "subject $1, library $2, segment $5"
reason = "each row's segment_id comes from unnest($5), the conflict target"
```

The API passes an id when no row has it (an insert's new id), when the row belongs to no
library, or when it is the job's library's (and, for a video job, the job's own video's); a
job whose subject is the catalog spans every library. A YouTube row is in the scope of every
library that follows its channel or downloads it. An array argument is checked element by
element, and every id is compared as a uuid, never as text.

### The scope a read by id must meet

A read is checked the same way. Wherever a statement compares a column to a `$N` (in a
`WHERE`, a join's `ON`, a subquery, or the source rows of a write), that `$N` chooses whose row
it reads, so `sync` lists a check for it when the column holds a kind of id, and the API runs
it before the statement:

```sql
-- name: propagate.last_success
-- mode: read
-- scope: job_subject $2
-- tables: processing_jobs
SELECT id, output_hash FROM processing_jobs WHERE subject_kind = $1 AND subject_id = $2 AND ...
```

Every comparison counts, not only the top-level `AND` terms of a write: `col = $N`,
`col IS NOT DISTINCT FROM $N`, `col = ANY($N)`, `col IN ($N, $M)` and
`col IN (SELECT … FROM json_each($N))` (or `unnest($N)`). A read of another library's row, or,
from a video job, of another video's, is `403 plugin_scope`. A job's own row is `job $N` (its
library, and its video when its subject is one), and a job's `subject_id` is `job_subject $N`
(a video, a library, a series or a season, whichever has the id). `check` fails a read that
compares a `$N` to:

- a column of a table `scope_tables.toml` does not list (under `[tables]` or `[home]`);
- an id-named column (`id`, `…_id`) of a listed table that is no owner column, unless
  `[not_ids]` lists it with the reason a read by it needs no check (a public YouTube video id,
  for example, names no one library's row);
- an id-named column the build cannot place in one table: qualify it (`t.video_id`).

A header with fewer checks than its comparisons need is stale, and `check` fails it until
`sync` rewrites it. A read the parse tree cannot type gets its checks by hand in
`statements.toml`, with its `reason`, as a write does.

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

Each such fallback is counted: per machine, stage and reason, the worker adds what it counted
to `stage_fallbacks` every 30 seconds. An admin reads them with `GET /api/queues/fallbacks`
(`?stage=` for one stage) or under **Ran in the worker instead** on the stage's page. The
reasons are `runner_not_serving_here`, `windows_share_library`, `role_unusable`,
`index_store_chroma`, `runner_areas_stale`, `unregistered`, `sandbox_refused`,
`fingerprint_inproc` (the fingerprint runner did not answer) and `sqlite` (on SQLite every
built-in plugin stage runs in the worker). A stage set to `inproc` by hand is not counted.

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

## The language model and the encyclopedia: the rest of `tv.maktaba.classify`

Wave 4 (story 54.31) moves the rest of classify: `detect`, `tag`, `check_tags`, `check_clips`,
`books` and `review_pages` join the wave 1 stages in `tv.maktaba.classify`.

- **The model through the plugin API.** `check_tags` and `check_clips` no longer call the model
  server themselves. In the runner they ask `compute.generate` with the same messages, format
  and options. For a first-party stage that names `model_rules = "tagcheck"`, the API forwards
  them unchanged to `rules.tagcheck`'s server and model, so a node that asks a model on another
  machine still does, and recorded answers match. A third-party plugin only ever gets the
  home's own model, and may not send chat messages. A model that is down fails the job as it
  did before.
- **One stage in the `net` group.** `books` looks the book up in the encyclopedia
  (`ar.wikipedia.org`, the manifest's `net`). It sets its own `runner = "net"`. Only the `net`
  runner gets a socket, so classify's other stages in `light` still have none. A stage that runs
  outside its plugin's own group brings only the network there: the `net` runner does not get
  classify's thumbnails or reports folder.
- **The review pages** read the small thumbnails (`files.read: data.thumbnails`) and write each
  library's page in the reports folder (`files.write: data.review`).
- **Parity with the model on.** A fake model server answers both sides over HTTP (the plugin's
  questions reach it through `compute.generate`). The encyclopedia is a recorded answer, and
  the test fails if the plugin looks a book up on a host its manifest does not list. The
  questions, the lookups, the rows, the review pages and the jobs must all be the same.

`enrich` (Find metadata) stays in the worker for now. It reads whatever page a web search
links to, and its search keys are sealed in the home's secrets, which the plugin API does not
serve. A declared host list cannot cover either.

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

### A library that is read-only for Maktaba

On a NAS, Maktaba's user often may read a library's folders but not write them. Maktaba
checks each library's folders (at setup, when a library is added or its folders change, from
**Check access** on the library's page, and after a write fails on a permission) and keeps
one of three states: `rw` (read and write), `ro` (read only) or `none` (it cannot list the
folder). A library never checked counts as `rw`. A read-only library is supported, not an
error, and the manifest decides what runs there:

- **A stage whose `files.write` names `library` or `trash` is not run** on a library that is
  `ro` or `none`: the conversion, merging duplicates, restoring from the trash, the YouTube
  downloads and the repair. Nothing in its folders is touched.
- **`library.subtitles` still runs.** On a read-only library its subtitle files are written to
  the database (`subtitle_files.content`, by video and language) instead of beside the video,
  and streaming serves them like side-car files: a side-car file beside the video first, then
  the row.
- **Reading stages are unchanged**: scans, thumbnails, transcripts and playback only read.

The admin API refuses an action that would write in such a library with a problem `409`,
`type` `https://maktaba.tv/problems/library-read-only` and `code` `library_read_only`, before it
queues anything:

| Route | |
|---|---|
| `POST /api/libraries/{id}/convert`, `/dedup-scan` | conversion, the duplicate search |
| `POST /api/libraries/{id}/youtube/refresh`, `/youtube/backfill`, `/youtube/retry`, `/youtube/tartube`, `/youtube/sources` | YouTube downloads into this library |
| `DELETE /api/videos/{id}`, `DELETE /api/libraries/{id}?purge=true` | deleting files |
| `POST /api/queues/duplicates/{gid}/apply`, `POST /api/queues/file-actions/{id}/restore` | a merge, a restore |

With `all=true`, `convert`, `dedup-scan` and `youtube/refresh` leave read-only libraries out
instead: their results carry `"outcome": "library_read_only"` and the answer counts them in
`skipped_read_only`. A scan of a library that is `none` answers `409` `library_no_access`.

Each library's JSON carries `access` (`rw`, `ro`, `none`, or `null` when never checked),
`access_checked_at` and `access_partial` (some sampled files, or a later write, failed). An
admin reads the details with `GET /api/libraries/{id}/access` and checks again with
`POST /api/libraries/{id}/check-access`. Both answer, per folder, its state, up to five files
it could not read, whether the folder is mounted read-only in the container, and the fix in
the platform's terms (`fix.key` is `library.fix.dsm`, `library.fix.truenas`,
`library.fix.unraid` or `library.fix.other`, with string `params`), along with the last five
writes that failed, the user and group Maktaba runs as (`runs_as`) and the detected
`platform`.

## Heavy work: the ml and index runners

`tv.maktaba.transcription` (Whisper and the second listen) and `tv.maktaba.semantic-search`
(the embedder) load large models, so they run in runners of their own rather than the
shared ones:

- **`ml`**, one per transcription worker. A machine's `pipeline/stt-<i>` claims the GPU jobs
  and runs them in its own `plugin-runner/ml-<i>`, which holds the model and has the
  worker's slot and settings. With `gpu = "preferred"` its sandbox opens the GPU (Metal on
  macOS, the NVIDIA devices on Linux).
- **Memory.** On Apple Silicon an `ml` runner holds Whisper (about 1.6 GB) and the second
  listen's Cohere model (about 3.8 GB) in MLX, plus the files it is decoding and MLX's cache
  of freed buffers, capped at `MAKTABA_STT_GPU_CACHE_GB` whichever model loads first. After
  `MAKTABA_STT_UNLOAD_IDLE_S` seconds without transcription work (default 600; 0 keeps them)
  it lets both models go and logs `stt.model_unloaded`; the next job loads them again. A plugin
  that keeps a large model in a runner should let it go when idle the same way.
- **`index`**, beside `pipeline/index`, which keeps answering search. While the home's
  vectors are in Chroma (one process may open its folder), the index and chapter stages
  stay in the worker; once they are in Postgres, the runner writes them like any table.
- **No network.** A runner reads the model caches (`files.read = ["models"]`) with the
  Hugging Face libraries offline; a stage whose model is not downloaded yet runs in the
  worker, which fetches it.
- **Upkeep stays in the worker.** A stage's `refresh` (rules read and jobs queued before
  each selection) runs in the worker; `statements.toml` names such functions under `worker`
  so their statements are not registered.

## Changing the owner's media: what the worker does for a plugin

A stage that rewrites, merges or moves media writes some things no plugin role may hold.
The plugin asks the worker for them over the run's connection, and the worker checks each
against the job before it writes it with its own database connection:

| Ask | What the worker checks |
|---|---|
| `enqueue` (from `enqueue_job`) | the stage is the job's own, one it may request, or joined to it by an edge, or where the graph starts a new subject (`probe` for a video); the subject is of the job's library |
| `dedup_commit` (a duplicate group's merge) | the group is the job's subject, and the keeper and the others are its members |
| `unsupersede` (a restored duplicate) | the video is a superseded video of the job's library |
| `tv_commit`, `tv_rollback` (a TV change on the air, or the previous release back) | only `tv_swap` / `tv_rollback` may ask; the worker runs it for the change in the job's own payload, and the plugin sends nothing else |
| `service_request` (a Plex call on a joined machine) | the service is one the stage's plugin lists; the worker forwards it through its channel to the home's service door |
| `advance` (a video's state, from `advance_after_stage`) | only `probe`, `thumbnail` and `extract` may ask, each by its own trigger, and only for the job's own video; the state machine checks the outcome |
| `requeue_failed` (a video's failed stages queued again) | `access_check` for the job's own video, `scan` for a video of the job's library |
| `storage_canonicalize` (the home's path conversion) | only that stage's job may ask; the worker runs the conversion with its own mounts, and the plugin sends nothing else |

The same code runs in-process, where these calls write directly. Writes go only to the
library's folders and their trash (`files.write = ["library", "trash"]`), each move recorded
in the file-action journal first.

Within those folders a runner job is narrower still. Before a job of `convert_mp4` (for a
video), `dedup_apply` or `file_restore` starts, the runner reads its subject's files through
the plugin's statements: the video's file and its MP4, the duplicate group's members, or the
journal rows the restore names. Its audit hook then refuses, as `plugin_scope`, any write,
rename or removal of another library file; the swap's staged copy and the subtitle files
beside the job's own files are allowed, and the trash (`.maktaba/`) is not checked, since a
file enters or leaves it only by a checked move. The job fails, not retried, even when its
code caught the error, and the file stays where it is. In-process jobs keep no such scope.

## The network: `tv.maktaba.youtube` and `tv.maktaba.repair`

Wave 6 moves the stages that reach YouTube: `youtube_sync` lists a channel or playlist,
`youtube_fetch` and `youtube_backfill` download what it found into the library,
`exact_dates` fetches the exact upload day of series members, and `repair_broken` fetches a
broken download again and swaps the good copy in, keeping the broken one beside it.

```toml
[trusted]
runner      = "net"
settings    = ["exactdates"]                # the rules sections the plugin reads
files.read  = ["library"]
files.write = ["library", "data.cache"]     # data.cache: the downloads' work folder
exec        = ["yt-dlp", "ffmpeg", "ffprobe"]
net         = ["www.youtube.com", "youtube.com", "*.googlevideo.com", "www.googleapis.com", "i.ytimg.com"]
```

- **The `net` group** is the only runner with sockets beyond its own and the plugin API's.
  The sandbox opens outbound connections for it as a whole; it does not filter by host
  (YouTube's media hosts change from one download to the next). The `net` list is what the
  plugin is meant to reach: reviewed with the manifest and shown on the Plugins page.
  Every night, `.github/workflows/youtube-hosts-nightly.yml` runs the real yt-dlp with the
  command lines the stages build (a channel listing, one public video with its thumbnail and
  subtitles) through a proxy that notes each host it contacts (`tools/ci/youtube_hosts.py`);
  a host outside the list fails the run. The fix is a reviewed edit of the manifest, never a
  wider sandbox. When YouTube refuses the CI runner (its bot check), that step is a warning
  for the night, and the hosts reached before it are still checked.
- **A download starts a new video's pipeline.** The video row is written in the plugin's own
  transaction, which the worker cannot see yet, so the worker holds the `probe` the download
  asks for and queues it once the job's writes are committed, after the same checks.
- **Rules are scoped too.** A stage reads a rules section only when the manifest lists it in
  `settings` (`exactdates` for `exact_dates`, `repair` for `repair_broken`); any other is
  refused as `plugin_scope`.
- **Parity** runs a channel listing, two downloads and a back-catalogue fill with a recorded
  yt-dlp, the five ways a repair can go, and the exact dates of a series, the same on both
  sides; files, rows, the queue and the job log are compared, then N16.

## Live TV: `tv.maktaba.livetv` and `tv.maktaba.livetv-slots`

Wave 7 moves the channel planner (`plan_channels`), the channel writer (`write_channels`) and
Epic 33's TV change stages (`tv_stage`, `tv_swap`, `tv_rollback`, `tv_gc`). The loops that
keep the channels playing (top-up, the change controller, streaming's TV loops) stay where
they are.

- **Files.** `plan_channels` writes the plan's page to the reports folder
  (`files.write = ["data.review"]`). Both plugins read the channels' art (`data.channels`):
  the art an admin uploaded and the logo and watermark folders the planner's rules name. The
  worker writes these folders into the runners' areas; a changed rule restarts the runner
  with the new folders.
- **The stored TV settings stay the engine's.** A swap puts the release's TV settings on the
  air in the same transaction as its lineup, and only the queue engine writes
  `app_settings`. So the worker runs the swap commit and the rollback for the plugin's job
  (`tv_commit`, `tv_rollback` above), for the change the job's payload names.
- **Extensions add to the plan.** An extension may add data to each channel of the plan
  (a plan exporter; no built-in extension registers one today). A runner loads no extension, so the worker sends the names it has
  registered with every job, and the runner takes the same functions from the extensions it
  finds. A name it cannot load runs the job in the worker.
- **Parity** plans a library into channels with staging off (written to the air) and on (a
  staged TV change), and takes a TV change through stage, swap, rollback and clean-up, with
  the TV clock frozen on both sides.

## Services through the home's door: `tv.maktaba.plex`

Wave 8 moves the Plex stage (`plex_livetv`, Plex Live TV) into the `service` runner, which
has no network at all.

```toml
[trusted]
runner    = "service"
component = "ext.plex"            # the extension keeps its settings and its health check
service   = ["plex"]              # the services its calls may reach, through the door
```

- **Every Plex call goes through the home's service door.** The plugin's Plex client sends
  each request to the plugin API (`trusted/service`); the API hands it to the door, which
  checks it against the allow-list, adds the Plex token and scrubs the token out of the
  answer. On a joined machine the runner asks its worker, which forwards the call through its
  channel to the home's door. A call off the allow-list is refused `plugin_service_path`.
- **The plugin never learns the token or Plex's address.** Its jobs get the extension's
  settings without secret fields, and `service:plex` in place of the address.
- **Live TV is the home's.** The door allows the Live TV calls `plex_livetv` makes (DVRs,
  tuners, the channel map, guide reloads) only for a caller on the home.
- **Parity** runs the Plex stage against a fake Plex behind HTTP on each side: the calls Plex
  receives (method, path, query and the token) are identical, then N16.

## The core stages: `tv.maktaba.core-media`

Wave 9 moves the stages every home always runs, the stages of `core.pipeline`: `scan`,
`probe`, `content_hash`, `thumb_hash`, `link`, `fingerprint`, `style_sample`, `fp_gate`,
`storage_canonicalize` and `access_check`.

```toml
[trusted]
runner      = "light"
component   = "core.pipeline"          # part of the server: never switched off
files.read  = ["library", "data.thumbnails"]
files.write = []
exec        = ["ffprobe", "ffmpeg"]    # chromaprint runs through ffmpeg
settings    = ["queue_health"]
```

- **The component stays part of the server.** `core.pipeline` is not switchable: every write
  to it on `/api/components` is `409 not_switchable`, whichever process runs its stages.
- **Only the engine writes a video's state.** `probe` (and `thumbnail`, `extract`) move a
  video along its states through `advance_after_stage`; in a runner the worker applies the
  transition (`advance` above). The same is true of a failed stage queued again
  (`requeue_failed`) and of the path conversion, which pauses stages and writes the share map
  (`storage_canonicalize`).
- **Each stage sits with its component.** A plugin's stages are its component's, so
  `thumbnail` runs in `tv.maktaba.thumbnails` (`media.thumbnails`) and `extract` in
  `tv.maktaba.transcription`. `extract` runs in the `ml` runner; a machine whose workers have
  no plain `ml` runner runs it in the worker.
- **What stays in the core.** The filesystem watcher and the manual scans only queue `scan`.
  `component_catchup` is the engine's own upkeep. `categorize` is a placeholder in the graph
  with no handler; whether it becomes a stage of this plugin is still to be decided.
- **No SQL built at run time.** The signal upserts that built their column list per call are
  three fixed statements, so each is a named statement of its own.
- **Parity** scans a library of real files, probes it (a talk, a silent video, one ffprobe
  cannot read), links a video from its yt-dlp sidecar, then runs each video's identity,
  fingerprint, style sample and access check; the video's state is written by the engine on
  both sides. Rows, the queue and the job log are compared, then N16.

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
