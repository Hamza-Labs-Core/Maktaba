# Story 54.31 — Wave 4: classify with the language model, and enrich

> Epic 54 · Phase 4 · Depends on: 54.30, 54.21 · Needed by: 54.32

## Why

The rest of classify and enrich ask the language model and, for two of them, the internet.
Moving them makes first-party plugins use the **same** `compute.llm` interface third-party
plugins use (54.21) instead of calling the model server themselves, and puts their internet
use behind a declared, CI-checked host list.

## Description

### 1. The stages

| Stage | Plugin | Pool | Placement | Model or network |
|---|---|---|---|---|
| `detect` | `tv.maktaba.classify` | `cpu` | any | — (reads audio fingerprints through statements) |
| `tag` | `tv.maktaba.classify` | `cpu` | any | — |
| `check_tags` | `tv.maktaba.classify` | `lookup` | home (`model_rules`) | `compute.llm` |
| `check_clips` | `tv.maktaba.classify` | `lookup` | home (`model_rules`) | `compute.llm` |
| `books` | `tv.maktaba.classify` | `lookup` | any | `compute.llm`; `net:ar.wikipedia.org` (runner group `net`) |
| `review_pages` | `tv.maktaba.classify` | `reports` | home | `files.write:data.review` |
| `enrich` | `tv.maktaba.enrich` | `lookup` | any | `compute.llm`; `net:` the metadata providers in `enrich/web.py` (runner group `net`) |

### 2. The language model through the API

Today these stages call the model server at `rules.<stage>.url` themselves. After the move
they call `compute.generate(prompt, opts)` (54.21). For first-party plugins the API forwards to
the same `rules.<stage>.url` the stage used (a trusted option, `model_rules = "<stage>"`, the
same field `StageDef` has), so a node that asks a model server on another machine keeps doing
so; third-party plugins only ever get the home's own model. The `lookup` pool still allows one
question at a time.

Prompts and decoding options are passed unchanged, so recorded answers (54.26) match.

### 3. Network

`books` and `enrich` move to the `net` group. Their manifest lists the hosts from today's code
(`ar.wikipedia.org`; the providers `enrich/web.py` calls). In CI, parity runs them behind a
recording proxy that fails the job on any other host; at run time each connection's host is
logged in the plugin's log (Epic 53).

### 4. The work

As wave 1, plus §2–§3. `detect` and `tag` are the largest SQL users in the pipeline (about 40
statements between them, several reading whole libraries), so they use streamed reads
(54.24 edge cases) and `batch` for their writes.

## Acceptance criteria

- **Given** the parity fixture with recorded model answers,
  **when** `plugins-parity` runs the seven stages,
  **then** rows, review pages, results, fingerprints and enqueued jobs are identical, and N16
  holds.
- **Given** `rules.check_tags.url` pointing at another machine,
  **when** `check_tags` runs on a node,
  **then** the question goes to that URL through `compute.generate`, as today.
- **Given** `books` in the `net` runner,
  **when** it tries a host not in its list during CI,
  **then** the recording proxy fails the job.
- **Given** a classify stage in the `light` runner,
  **when** it opens a socket,
  **then** the sandbox refuses it.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | fixture, recorded model and web answers | seven stages | identical; N16 |
| T02 | integration (PG, go) | `compute.generate` with a first-party token and `model_rules` | call | forwarded to `rules.<stage>.url`; third-party token gets the home model only |
| T03 | integration (CI only) | recording proxy | `books`, `enrich` | only declared hosts |
| T04 | bench | `detect` on a 2,000-video library | run both paths | N16 |
| T05 | update rehearsal scenario 16.4 | mid-queue | update and rollback | no job lost or rerun |
| T06 | unit (`fp-classify`, `fp-enrich`) | moved tests | run | green |

## Edge cases

- **The model server is down**: the same failure cause and class as today
  (`dependency:llm`), because `compute.generate` maps the error to it.
- **An enrichment provider changes its host**: the host list is in the manifest; the change is a
  reviewed manifest edit in a release, which the CI proxy then allows.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/classify/` (the rest), `plugins/first-party/enrich/` (new) | §1–§4; `classify/`, `enrich/` move. |
| `api/internal/plugins/compute/` (54.21) | `model_rules` forwarding for trusted tokens. |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/` | Remove moved definitions after one release. |
| `shared/db/migrations/` | Roles updated; `impl = plugin` for the seven. |
| `tests/update_rehearsal/scenario_16_4_wave4.*` | T05. |

### Tests

- Parity T01; integration T02, T03; bench T04; rehearsal T05; unit T06.

### Rollout

The release flips the seven at update.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.30; 54.21 (`compute.llm`).
