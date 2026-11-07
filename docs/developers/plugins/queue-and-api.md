# Queue stages and the plugin API

> **Status:** plugin stages in the queue, the plugin API and media handles are **on `main`**.
> Grants and roles and settings are **on `main`** too, and so are panels (coming in the next
> release); local compute is planned for Phase 3. The exact functions, capabilities, events and refusal codes are in
> the [plugin reference](reference.md), generated from the WIT and the API's code.

## Stages and events

Your plugin's stages never attach to Maktaba's internal stage names, which change between
releases. They follow a short list of stable **events**:

| Event | Subject | Fires when |
|---|---|---|
| `video.added` | video | a new video is probed |
| `video.changed` | video | its file changed |
| `audio.ready` | video | audio was extracted |
| `transcript.ready` | video | a transcript was written or replaced |
| `subtitles.ready` | video | subtitles were written |
| `thumbnails.ready` | video | thumbnails were made |
| `metadata.changed` | video | title, tags or grouping changed |
| `series.changed` | series | a series or season was formed or changed |
| `library.scanned` | library | a library scan finished |
| `schedule` | catalog | your own schedule (every N minutes, minimum 15) |

The event list is part of the plugin API version: events are only removed in a new major.

A stage gets a job in three ways:

- **A trigger:** `[[stage.trigger]] event = "audio.ready"`.
- **A schedule:** `[[schedule]]` on a `catalog` stage, with the interval read from a settings
  field.
- **A request:** one of your stages calls `queue.request` for a stage listed in its
  `may_request`. Requests to Maktaba's or other plugins' stages are refused. At most 1,000
  requests per job.

New stages do not back-fill your existing library unless the admin ticks "Run on my existing
library" when enabling.

## How a job runs

You do not claim jobs yourself. The pipeline owns the queue: claiming, retries, failure
classes, drain and parking work as for Maktaba's own stages.

1. A pipeline worker claims your stage's job from the `plugins` pool.
2. It asks the API for an **invocation token** for that job. The API builds it from the grants
   it stores, never from what the worker or host says.
3. The worker sends the job to the plugin host, which creates a fresh sandbox and calls your
   component's `run(stage)` export. The SDK routes it to the function named by `export`.
4. Your code calls `maktaba:plugin` imports. The host attaches the token; your code never sees
   it. The API checks the token, capability, scope and quota on every call.
5. You return a result. Maktaba maps it to done, changed or unchanged, a retry, or a
   permanent failure (see [The sandbox](sandbox.md#failure-causes)).

Use `job.progress(pct)` to report progress, and `job.checkpoint(bytes)` to save state so a
drained or retried job can resume.

## The plugin API

One WIT package, `maktaba:plugin@1.x`, versioned with semver. A minor version only adds
functions, so a plugin built for 1.2 runs on any 1.x host from 1.2 up. Maktaba supports the
current and previous major for at least 12 months after a new major.

| Interface | Functions | Capability |
|---|---|---|
| `job` | `job-subject`, `payload`, `settings`, `grants`, `progress`, `checkpoint`, `last-checkpoint`, `scratch-put`, `scratch-get` | none |
| `log` | `write(level, code, fields)` | none |
| `data` | `get`, `put` (optionally `exported`), `delete` in your own namespace | none (quota applies) |
| `library` | `video(id)`, `series(id)` | `library.read` |
| `transcript` | `segments(video)`, `words(video)` | `transcript.read` |
| `media` | `open`, `read`, `size`, `close`; `frame`; `audio`; `info` | `media.read`; `media.frames`; `media.audio`; `library.read` |
| `write` | `set-field`, `add-tags`, `set-chapters`, `add-subtitle`, `set-artwork` | `metadata.write`, `tags.write`, `chapters.write`, `subtitles.write`, `artwork.write` |
| `suggest` | `add(subject, field, value, reason)` into the admin's review queue | `suggest` |
| `queue` | `request(stage, subject, payload)` for your own stages | `jobs.enqueue` |
| `notify` | `admin(code, params)`, a notice from your strings | `notify.admin` |
| `exports` | `read(plugin, subject, key)` of data another plugin exported | `plugin-data.read:<id>` |
| `compute` (Phase 3) | `embed(texts)`, `generate(prompt, opts)` | `compute.embed`, `compute.llm` |

Every signature is in the [plugin reference](reference.md#the-plugin-api).

Interfaces you were not granted are still linked, so you can degrade gracefully, but each call
answers a capability error.

### Rules on every call

- **Scope to the job.** `media` and `write` act only on the job's subject, or, for library and
  catalog jobs, on items in granted libraries. Outside scope: `403 plugin_scope`.
- **Capabilities are checked.** Without the grant: `403 plugin_capability`, logged and counted
  toward the circuit breaker.
- **Core fields are guarded.** `set-field` writes only `description`, `sort_title`, `year`,
  `content_rating`, `original_language` and `local_ids.<your short id>`. It never overwrites a
  title the admin set; use `suggest` instead.
- **Every write is attributed** to `plugin:<id>@<version>`, so the admin can list it, undo it
  per plugin, and keep it out of anything that leaves the home.
- **Subtitles** are parsed as WebVTT or SRT and rewritten by Maktaba, up to 4 MB, under a name
  Maktaba chooses.

Other refusals: `plugin_field`, `plugin_quota`, `plugin_rate`, `413 plugin_too_large`,
`401 plugin_token`, `401 plugin_grants_changed`.

## Media handles

| Call | Capability | You get |
|---|---|---|
| `media.open(video)`, then `media.read(handle, offset, len)`, `media.size(handle)` and `media.close(handle)` | `media.read` | raw bytes, up to 8 MB per read |
| `media.frame(video, t-ms, max-edge)` | `media.frames` | one decoded frame, longest edge up to 1,280 px |
| `media.audio(video, t0-ms, t1-ms, rate)` | `media.audio` | mono 16-bit PCM at 8, 16 or 22.05 kHz, up to 10 minutes per call |
| `media.info(video)` | `library.read` | duration, streams, codecs |

A handle is bound to your job, the video and the file's content hash. If the file changes
mid-job, reads answer `409 media_changed`. Budgets per job: bytes read up to twice the file
size, 2,000 frames, audio up to four times the duration. You never see paths, share names or
other files in the folder.

## Grants and roles

At install the admin sees your capabilities in Maktaba's own words, in English or Arabic,
unticks optional ones, and ticks each library you may use. **No library is ticked by
default.** With none ticked, your plugin installs and runs, but every library-scoped call
answers `plugin_scope`. Check `job.grants()` to see what you have.

- An update that adds a **required** capability is installed only with the admin granting it
  in the same step; until then the old version keeps running.
- New optional capabilities stay off until the admin ticks them.
- A revoked grant applies on your next call.
- A **rating** from 1 to 6 is shown at install: it starts at 6 and drops for each medium-risk
  capability, for no store review, and for reading another plugin's data.

Only admins can install, configure or see plugins. Viewers see plugin outputs as ordinary data
(tags, chapters, subtitles) and panels, only on subjects their library access allows.

## Settings

Declare an admin form as `ui/settings.schema.json`, a JSON Schema subset:

- a root object with up to 40 properties, at most one level of nesting;
- types `string`, `integer`, `number`, `boolean`, and `array` of enum strings;
- a `default` for every property;
- `x-maktaba-title` and `x-maktaba-help` as keys into your strings;
- `x-maktaba-widget`: `text`, `textarea`, `select`, `slider`, `switch`, `library-picker`,
  `language-picker`.

No secret fields: no `format: password`, and no field named `token`, `password`, `api_key` or
`secret`. A plugin with no internet has nothing to sign in to. Read values with
`job.settings()`. A settings change reruns stages whose inputs depend on it. The home checks
the schema at install (`settings_secret_forbidden`, `settings_schema_invalid`) and every value
on save (`settings_invalid`); after an update, a field whose type changed goes back to its
default. Try the form's values with `mkp test --settings '{"threshold": 3}'`.

## Panels

A panel is a read-only view of a video, series, season or library that every app draws
natively (web, iPhone, iPad, Mac, Apple TV, Android phone, tablet and TV) from data you
declare. It is never code: the apps never run anything of yours.

```toml
[[panel]]
on = "video"               # or series, season, library
schema = "ui/panel.json"
```

`ui/panel.json` (schema [`panel.schema.json`](https://maktaba.dev/plugins/panel.schema.json), `ui@1`):

```json
{"ui": 1, "title": "panel.title", "blocks": [
  {"type": "kv", "key": "loudness", "label": "panel.levels"},
  {"type": "markers", "key": "silences", "label": "panel.silences"},
  {"type": "text", "key": "summary"}
]}
```

`title`, `label` and a table's `columns` are keys of your `i18n` files. Each block's `key` is
a key of your `data` for the subject (`data.put`); the value it holds:

| Block | Value | Limit |
|---|---|---|
| `kv` | `[{"label": text, "value": text or number}]` | 20 rows |
| `badges` | `[{"text": text, "tone": "neutral" \| "good" \| "warning"}]` | 10 |
| `chips` | `[text]`; tapping one searches Maktaba for it | 20 |
| `table` | `[[cell, …]]`, at most one cell per column (`columns`, up to 6) | 50 rows |
| `markers` | `[{"t_ms": integer, "label": text}]`; tapping one plays from there | 200 |
| `image` | no key: the PNG or JPEG you set with `artwork.write`, kind `panel` | 1 MB |
| `text` | text | 1,000 characters |

Other text is at most 200 characters. **Everything is plain text**: when you write a key a
panel uses, the plugin API refuses a URL, HTML, markdown, a control or direction character
(`plugin_text_forbidden`), a value of the wrong shape (`plugin_invalid`) or one over its
limit (`plugin_too_large`), and every app escapes again when it draws. A key no panel uses is
your own data, unchecked.

- **Who sees it**: everyone in the home who can see the subject, viewers included, in their
  language, under **From plugins** (collapsed) below Maktaba's own sections. At most three
  panels a page. A plugin turned off, held or withdrawn shows no panel.
- **When it shows**: only for a subject with data for at least one of its keys. Markers
  written before the video's file last changed are not shown.
- **Apple TV and Android TV** show `kv`, `badges` and `markers` only.
- **Apps learn** from `GET /api/capabilities`: `plugin_panels: "on"` while the plugin host
  runs. An app reads a missing key as off, so it never asks an older server.
- The store gives every first version, and every version that adds a panel, a human review.

## Local compute (Phase 3)

- `compute.embed`: embeddings from the home's search model, up to 64 texts per call.
- `compute.llm`: `generate` on the home's own language model, up to 1,024 tokens.

Calls go through the plugin API to services on the home, never a remote model. If the model is
off, the call answers `unavailable`. Daily quotas apply, and prompts are not logged.
