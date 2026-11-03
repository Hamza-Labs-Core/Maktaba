# Story 54.30 — Wave 3: transcription and semantic search

> Epic 54 · Phase 4 · Depends on: 54.29 · Needed by: 54.31

## Why

Transcription and the search index are Maktaba's heaviest stages: the GPU, Whisper and refine
models, the embedder, a shared batched model per process, and today their own supervised
processes (`pipeline/stt-N`, `pipeline/index`). Moving them proves the runner can carry the
work no WebAssembly plugin could (H16), at the same speed.

## Description

### 1. The stages

| Stage | Plugin | Today | Pool | Placement | Trusted needs |
|---|---|---|---|---|---|
| `extract` (legacy) | `tv.maktaba.transcription` | `handlers.py:extract_handler`, `audio/` | `nas_read` | any | `files.read:library`, `exec:ffmpeg`, its audio cache folder |
| `transcribe` | `tv.maktaba.transcription` | `stages/transcribe.py`, `stt/` | `gpu` | any | `files.read:library, models`, `gpu`, `exec:ffmpeg`; `net` only when the OpenAI-compatible backend (`stt/openai_api.py`) is configured (§3) |
| `index` | `tv.maktaba.semantic-search` | `stages/index.py`, `search/` | `cpu` | home | `files.write:data.index`, `files.read:models` |
| `chapter_infer` | `tv.maktaba.semantic-search` | `stages/chapter_infer.py` | `cpu` | home | `files.read:data.index` |
| `topic_assign` | `tv.maktaba.semantic-search` | `stages/topic_assign.py` | `cpu` | any | — |
| `topic_recluster` | `tv.maktaba.semantic-search` | `stages/topic_recluster.py` | `cpu` | any | — |

Runner groups: `ml` (own process, transcription) and `index` (own process, semantic-search).

### 2. Processes

- The supervisor's `pipeline/stt-N` instances become `plugins.runner.ml` instances: same count,
  same `MAKTABA_STT_SLOT` slot names, same `stt.*` metrics series, same pinned stage
  (`transcribe`), so dashboards and Epic 53 health keep their history.
- `pipeline/index` keeps its gRPC semantic query server (the `search.semantic` process
  component, used by the API's search); only its queue work moves to `plugins.runner.index`.
  Both load the embedder; Epic 51's `search.semantic` component starts both.
- Batching: the `ml` runner keeps today's `stt/batched.py` behaviour (54.23 §5); the `gpu` pool
  size is still the batch.
- Crash recovery: today's `stt/crash_recovery.py` and `pause_resume.py` checkpoints become the
  runner's `checkpoint` (54.02 `Cancel` returns it), so a drained or crashed transcription
  resumes where it stopped, as today.

### 3. The remote STT backend

`rules.transcription` can name an OpenAI-compatible URL. The manifest declares
`net = ["rules:transcription.openai_url"]`: the `ml` runner's sandbox allows outbound network
only when that setting is set, and restarts (draining first) when it changes. The CI host
check (§15.4) records that only that host is contacted.

### 4. Legacy first

`extract`'s hand chaining becomes declared edges (as wave 2's §2), landed and green before the
move.

### 5. The work

As wave 1, plus §2–§4. About 160 statements (transcripts, segments, words, chapters, topics,
index state).

## Acceptance criteria

- **Given** the parity fixture with the fake STT backend and the fake embedder,
  **when** `plugins-parity` runs the six stages,
  **then** transcripts, segments, words, chapters, topics and index files are identical, and
  N16 holds for each.
- **Given** the CPU `tiny` Whisper run (54.26 §4),
  **when** both paths transcribe the three clips,
  **then** the outputs are byte-identical.
- **Given** 32 queued `transcribe` jobs on a Mac,
  **when** the `ml` runner takes them,
  **then** they run as one batch on the GPU, and the per-job time is within N16 of today.
- **Given** a transcription drained mid-file by a disable,
  **when** it is enabled again,
  **then** it resumes from its checkpoint, not from the start.
- **Given** no remote backend configured,
  **when** the `ml` runner starts,
  **then** its sandbox denies all network.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | fixture, fakes | six stages | identical; N16 |
| T02 | parity | CPU `tiny` model, 3 clips | transcribe both paths | byte-identical |
| T03 | integration (py) | `ml` runner, fake backend | 32 jobs | one batch |
| T04 | integration (PG, py) | drain mid-file | resume | from checkpoint |
| T05 | unit (go) | supervisor | `stt-N` → `plugins.runner.ml` | same slots, env, metrics names |
| T06 | integration (CI only) | `ml` sandbox with and without the remote URL | connect | denied / allowed to that host only (recording proxy) |
| T07 | update rehearsal scenario 16.3 | mid-transcription | update and rollback | no job lost or restarted from zero |
| T08 | unit (`fp-transcription`, `fp-semantic-search`) | moved tests | run | green |

## Edge cases

- **A home on Core** (no `runtime.ml`): the plugins' stages report `unavailable:
  runtime_missing`, exactly as `unavailable` does today; nothing queues for them.
- **A cluster node with a GPU and the home without**: `transcribe` stays `placement:any`; the
  node's `ml` runner serves it through the node's own API (54.24 §5).
- **The embedder model changes** (an Epic 51 model switch): the fingerprint includes the model
  id as today, so the same subjects rerun as today, no more.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/transcription/`, `plugins/first-party/semantic-search/` (new) | §1–§5; `stt/`, `audio/`, `search/` move. |
| `pipeline/src/maktaba_pipeline/handlers.py`, `engine/graph.py`, `stages/` | Remove moved code after one release; §4. |
| `cmd/maktaba-server/internal/supervisor/instances.go`, `stt*.go` | §2. |
| `pipeline/src/maktaba_pipeline/grpc_server.py` | Stays; imports the search read side from the plugin's public module via `_lib` (no stage code). |
| `shared/db/migrations/` | Roles; `impl = plugin` for the six. |
| `tests/update_rehearsal/scenario_16_3_wave3.*` | T07. |

### Tests

- Parity T01, T02; integration T03, T04, T06; unit T05, T08; rehearsal T07.

### Rollout

The release flips the six at update, after the `ml` and `index` runners report `Hello`.

### Rollback

Per stage switch (the `ml` runner stops when `transcribe` is `inproc`, and the old `stt-N`
role comes back); per release Epic 51 rollback.

### Dependencies

54.29.
