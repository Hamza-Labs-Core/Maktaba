# Plan 1.6 — Video State Machine (implementation)

> Implementation plan for [story-01-06-video-state-machine.md](story-01-06-video-state-machine.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: schema lands via the migration system in
> [plan-01-05](story-01-05-PLAN.md) §6; the Go scanner that consumes
> these states lives in [plan-01-01](plan-01-01-file-discovery.md) and
> [plan-01-05](story-01-05-PLAN.md) §3.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The state-machine package lives in **both** Go (`shared/states/` consumed by the Go scanner per [plan-01-01 D1](plan-01-01-file-discovery.md)) and Python (`pipeline/src/maktaba_pipeline/domain/states.py` per the story). Both implementations import the same JSON manifest at `shared/states/states.json` so the enum and transition table cannot drift. | Story names Python paths only. | The Go scanner (plan-01-01) needs the same enum to enqueue `DISCOVERED` rows and run the watcher's `→ MISSING` flip from [plan-01-03](story-01-03-filesystem-watcher-plan.md). Generating two language bindings from one manifest is cheap; letting them diverge is not. |
| D2 | `advance_after_stage(video_id, trigger, outcome)` accepts a **superset** of the canonical 7 stage names. The extra triggers — `filesystem`, `library`, `integrity` — cover the side-channel transitions the story enumerates (any → MISSING / SUPERSEDED / CORRUPTED). | Refines the story's "advance_after_stage is the only mutator" rule. | The story names `advance_after_stage` as the sole writer and lists three non-stage callers (filesystem watcher, library merge, integrity sweep). Renaming the function or adding parallel mutators would re-introduce the very fragmentation the story closes. |
| D3 | The migration filename is **`0003_video_states_and_stages.sql`**, owning both `videos.state` and `processing_jobs.stage` CHECK constraints in one transaction. | Matches the story's "`000X_video_states_and_stages.sql`" placeholder; slot 0003 reserved by [plan-01-05](story-01-05-PLAN.md) §6.2. | Two CHECKs, one rewrite of legacy `'thumb'` rows, one transaction. Splitting into two migrations buys nothing and exposes a window in which one CHECK is in place but the other is not. |
| D4 | Transition validity is enforced **in application code**, not by a SQL trigger. The CHECK constraint enforces only set membership. | Story acceptance: "an attempt to transition outside the allowed set raises `IllegalStateTransition`" — application-layer language. | Transitions depend on the *trigger* (a stage outcome vs. a filesystem event), which a CHECK cannot see. Application-layer enforcement gives rich errors and is identically testable from Go and Python. A future ADR can promote a subset to a SQL trigger if observability demands it; for v1 the row-level lock taken by `advance_after_stage` is sufficient. |
| D5 | `FAILED` is **terminal with no exit transitions**. A user wishing to retry resets via a separate operator command (`maktaba video reset --id …`) that drops and re-creates the row from `content_hash`; that command is not part of this story. | Story marks FAILED as "terminal-bad" with no listed exit. | Avoids ambiguity. The reset path can be added in Epic 24 (Disaster Recovery) without changing this enum. |
| D6 | The state machine module ships **no public mutator other than** `AdvanceAfterStage` / `advance_after_stage`. Any direct `UPDATE videos SET state = …` outside the module is a lint failure (`forbidigo` rule in Go, `ruff` custom check in Python). | Story acceptance: "the **only** code path that mutates `videos.state`". | The story's invariant is meaningful only if it is mechanically enforced. A hand-rolled UPDATE somewhere in Epic 9 would silently break the FSM. Lint catches it at PR time. |

If D1 is rejected (Python-only, no Go binding), the test plan §10.1, code
scaffolding §7, and lint rules §11.2 are the only sections that change;
the migration §6, transition table §4, and enum §3 are language-agnostic.

---

## 1. Architecture diagram — where the state machine lives

```
                   ┌──────────────────────────────────────────────────────┐
                   │           shared/states/states.json                  │
                   │           (the single source of truth)               │
                   │  - 12 states                                         │
                   │  - 7  stages                                         │
                   │  - allowed transitions (from, to, trigger, outcome)  │
                   └────────────────────────────┬─────────────────────────┘
                                                │
                  ┌─────────── go generate ─────┼──── python codegen ─────┐
                  ▼                             ▼                         ▼
        ┌──────────────────┐         ┌──────────────────┐        ┌──────────────────┐
        │ shared/states/   │         │ pipeline/.../    │        │ shared/db/       │
        │ states_gen.go    │         │ domain/states.py │        │ migrations/      │
        │ (Go enum +       │         │ domain/stages.py │        │ 0003_*.sql       │
        │  transition tbl) │         │ (Py enum + tbl)  │        │ (CHECK in lock)  │
        └────────┬─────────┘         └────────┬─────────┘        └────────┬─────────┘
                 │                            │                           │
                 ▼                            ▼                           ▼
        ┌──────────────────┐         ┌──────────────────┐        ┌──────────────────┐
        │ Go callers:      │         │ Python callers:  │        │ Postgres / SQLite│
        │  - api/scanner   │         │  - pipeline      │        │  enforces SET    │
        │  - watcher       │         │    orchestrator  │        │  membership only │
        │  - library mgr   │         │  - probe/extract │        │  (transition     │
        │                  │         │    /transcribe / │        │  validity is in  │
        │  call:           │         │    index/thumbnl │        │  app code)       │
        │ states.Advance(…)│         │  call:           │        │                  │
        │                  │         │ advance_after_   │        │                  │
        │                  │         │   stage(…)       │        │                  │
        └────────┬─────────┘         └────────┬─────────┘        └──────────────────┘
                 │                            │
                 └────────── pgx tx ──────────┘
                                              │
                                              ▼
                              ┌─────────────────────────────┐
                              │  videos.state UPDATE         │
                              │  (single SQL fragment, run   │
                              │   inside both bindings)      │
                              └─────────────────────────────┘
```

Two bindings, one manifest. The DB enforces membership; the bindings
enforce the transition graph; both are derived from `states.json`.

---

## 2. Canonical state diagram

The diagram below is the authoritative source. Anything in a binding
that contradicts it is the bug. Side-channel transitions
(MISSING / SUPERSEDED / CORRUPTED / FAILED) are split out below the main
flow for readability; they apply to **every** non-terminal-bad source
state listed in §4.

```
                        ┌──────────────┐
                        │  DISCOVERED  │◄─────────────────┐
                        └──────┬───────┘                  │
                               │ probe/ok                 │
                               ▼                          │
                        ┌──────────────┐                  │
                        │    PROBED    │                  │
                        └──────┬───────┘                  │
                  probe/       │       extract/ok         │
                  no_audio     ▼                          │
                ┌──────────────┐ ┌──────────────────┐     │
                │READY_NO_AUDIO│ │ AUDIO_EXTRACTED  │     │
                └──────────────┘ └────────┬─────────┘     │
                                          │ transcribe/ok │
                                          ▼               │
                                  ┌──────────────┐        │
                                  │ TRANSCRIBED  │        │
                                  └──────┬───────┘        │
                            index/ok AND subtitle_gen/ok  │
                                          ▼               │
                                  ┌──────────────┐        │
                                  │   INDEXED    │        │
                                  └──────┬───────┘        │
                                         │ thumbnail/ok   │
                                         ▼                │
                                  ┌──────────────┐        │
                                  │ THUMBNAILED  │        │
                                  └──────┬───────┘        │
                                         │ orchestrator   │
                                         │ /all_gates_ok  │
                                         ▼                │
                                  ┌──────────────┐        │
                                  │    READY     │        │
                                  └──────────────┘        │
                                                          │
   ── side channels ───────────────────────────────────── │ ──────
   any non-terminal ─── filesystem/deleted ──► MISSING ───┘
                                                  │ scan/rediscovered
                                                  ▼
                                            (DISCOVERED)
   any non-terminal ─── any_stage/exhausted ──► FAILED        (terminal-bad)
   any non-terminal ─── integrity/fail      ──► CORRUPTED     (terminal-bad)
   any non-terminal ─── library/replace     ──► SUPERSEDED    (terminal-soft)
```

Legend:
- `trigger/outcome` labels each edge. Triggers are the canonical 7 stage
  names (`scan, probe, extract, transcribe, subtitle_gen, index,
  thumbnail`) plus three side-channel triggers (`filesystem, library,
  integrity`). Outcomes are short tokens documented in §4.
- "non-terminal" means any state except `READY`, `READY_NO_AUDIO`,
  `MISSING`, `SUPERSEDED`, `CORRUPTED`, `FAILED` for the FAILED edge;
  for SUPERSEDED / CORRUPTED / MISSING the source set is broader (see §4).
- `subtitle_gen` is a *stage* but **not** a state. The video stays
  `TRANSCRIBED` until both `subtitle_gen` and `index` reach `done`, at
  which point the orchestrator advances to `INDEXED` (story
  acceptance, restated in §4).

---

## 3. Canonical enums

### 3.1 States — `shared/states/states.json` keys

| Constant            | DB value          | Class    | Outbound edges (count) |
|---------------------|-------------------|----------|------------------------|
| `DISCOVERED`        | `discovered`      | open     | 5                      |
| `PROBED`            | `probed`          | open     | 6                      |
| `AUDIO_EXTRACTED`   | `audio_extracted` | open     | 5                      |
| `TRANSCRIBED`       | `transcribed`     | open     | 5                      |
| `INDEXED`           | `indexed`         | open     | 5                      |
| `THUMBNAILED`       | `thumbnailed`     | open     | 5                      |
| `READY`             | `ready`           | terminal-good | 3                |
| `READY_NO_AUDIO`    | `ready_no_audio`  | terminal-good | 3                |
| `MISSING`           | `missing`         | sink     | 1                      |
| `SUPERSEDED`        | `superseded`      | terminal-soft | 0                |
| `CORRUPTED`         | `corrupted`       | terminal-bad  | 0                |
| `FAILED`            | `failed`          | terminal-bad  | 0                |

DB values are lowercase; constants follow each language's idiom (Go
`StateDiscovered`, Python `State.DISCOVERED`).

### 3.2 Stages — `processing_jobs.stage`

```
scan | probe | extract | transcribe | subtitle_gen | index | thumbnail
```

Notes:
- `subtitle_gen` is added (resolves REVIEW §1.3.b). It does **not**
  introduce a new video state.
- The pre-story enum used `thumb`; the migration §6 rewrites those rows
  to `thumbnail` before adding the CHECK (resolves REVIEW §1.3.c).
- `scan` is a stage so the watcher can record `(video_id, 'scan',
  'rediscovered')` outcomes through the same `processing_jobs` ledger
  the rest of the pipeline uses (this is the table that drives the
  `MISSING → DISCOVERED` transition).

### 3.3 Triggers (superset of stages)

```
scan | probe | extract | transcribe | subtitle_gen | index | thumbnail
filesystem | library | integrity
```

The three extra triggers carry side-channel transitions. They are
**not** valid `processing_jobs.stage` values — only the seven canonical
stage names appear in the DB CHECK on `processing_jobs.stage`. Triggers
are an in-process concept consumed by `advance_after_stage`.

### 3.4 Outcomes

Open vocabulary (string), but the table in §4 names every outcome the
state machine recognizes. Anything else is a programmer error and the
function raises `IllegalStateTransition` even if the source state is
otherwise compatible.

| Outcome           | Used by                                  | Means                                                             |
|-------------------|------------------------------------------|-------------------------------------------------------------------|
| `ok`              | every stage                              | Stage finished cleanly; advance per the main flow.                |
| `no_audio`        | `probe`                                  | The probed file has zero audio tracks; route to READY_NO_AUDIO.   |
| `partial`         | `subtitle_gen`, `index`                  | One of the two finished; the other is still pending. No-op transition. |
| `exhausted`       | every stage                              | `attempts >= max_attempts`. Drives → FAILED.                      |
| `rediscovered`    | `scan`                                   | A previously-MISSING content_hash reappeared. Drives → DISCOVERED.|
| `deleted`         | `filesystem`                             | The watcher / sweeper saw the file gone. Drives → MISSING.        |
| `replaced`        | `library`                                | Library merge / re-mount points the row at a different identity. Drives → SUPERSEDED. |
| `fail`            | `integrity`                              | Hash mismatch on a non-network volume. Drives → CORRUPTED.        |
| `late`            | any stage                                | Stage finished after the video moved to a terminal state. No-op; logs `late_stage_finish`. |

---

## 4. Allowed transitions table

Source of `shared/states/states.json` `transitions` array. Each row is
`(from, trigger, outcome) → to`. The runtime table built from this is a
`map[(from, trigger, outcome)] -> to` keyed by the triple; lookups that
miss raise `IllegalStateTransition`.

| From              | Trigger        | Outcome         | To                |
|-------------------|----------------|-----------------|-------------------|
| `DISCOVERED`      | `probe`        | `ok`            | `PROBED`          |
| `PROBED`          | `extract`      | `ok`            | `AUDIO_EXTRACTED` |
| `PROBED`          | `probe`        | `no_audio`      | `READY_NO_AUDIO`  |
| `AUDIO_EXTRACTED` | `transcribe`   | `ok`            | `TRANSCRIBED`     |
| `TRANSCRIBED`     | `subtitle_gen` | `partial`       | `TRANSCRIBED`     |
| `TRANSCRIBED`     | `index`        | `partial`       | `TRANSCRIBED`     |
| `TRANSCRIBED`     | `index`        | `ok`            | `INDEXED`         |
| `TRANSCRIBED`     | `subtitle_gen` | `ok`            | `INDEXED`         |
| `INDEXED`         | `thumbnail`    | `ok`            | `THUMBNAILED`     |
| `THUMBNAILED`     | `scan`         | `all_gates_ok`  | `READY`           |
| `MISSING`         | `scan`         | `rediscovered`  | `DISCOVERED`      |
| *any non-terminal-bad/soft* | `filesystem` | `deleted`     | `MISSING`         |
| *any non-terminal*          | *any stage*  | `exhausted`   | `FAILED`          |
| *any non-terminal-bad/soft* | `integrity`  | `fail`        | `CORRUPTED`       |
| *any non-terminal-bad*      | `library`    | `replaced`    | `SUPERSEDED`      |

Two clarifications for the `(TRANSCRIBED, *, *)` rows: the orchestrator
calls `advance_after_stage` once per finished stage. If `index` finishes
before `subtitle_gen`, it issues `(TRANSCRIBED, index, partial) →
TRANSCRIBED` (a no-op state-wise, but the call still records it for
observability), and when `subtitle_gen` finishes later it issues
`(TRANSCRIBED, subtitle_gen, ok) → INDEXED`. The "ok" vs "partial"
decision lives inside `advance_after_stage`: it queries
`processing_jobs` for the *other* of the two, treats two `done` rows as
`ok` and a single `done` row as `partial`. This avoids leaking that
joint-condition logic into callers.

The `(THUMBNAILED, scan, all_gates_ok) → READY` row uses the `scan`
trigger as the orchestrator's pseudo-stage for the final gate sweep; no
new trigger is introduced.

Source-set shorthand:
- `non-terminal-bad/soft` = open ∪ terminal-good ∪ {MISSING}
  = `DISCOVERED, PROBED, AUDIO_EXTRACTED, TRANSCRIBED, INDEXED, THUMBNAILED, READY, READY_NO_AUDIO, MISSING`.
- `non-terminal-bad`     = `non-terminal-bad/soft` ∪ {SUPERSEDED}.
  SUPERSEDED *can* re-supersede in pathological library merges; the
  state stays SUPERSEDED but the `superseded_by` pointer in
  `videos.metadata` is updated.
- `non-terminal`         = `non-terminal-bad` ∪ {FAILED}. FAILED can
  only ever leave on `(integrity, fail)`-style edges? No — FAILED is
  terminal-bad per D5. **FAILED is therefore excluded** from
  `non-terminal-bad/soft` in the MISSING / CORRUPTED / SUPERSEDED rows.
  The only "any non-terminal" row is the `exhausted → FAILED` self-loop
  guard, which is a no-op when source is already FAILED.

These set definitions are encoded explicitly in `states.json` as
`source_classes` so the bindings agree by construction.

---

## 5. The `states.json` manifest

Lives at `shared/states/states.json`. Generated bindings are checked
in (not built at deploy time) so the test suite catches drift.

```json
{
  "version": 1,
  "states": [
    {"name": "DISCOVERED",      "db": "discovered",      "class": "open"},
    {"name": "PROBED",          "db": "probed",          "class": "open"},
    {"name": "AUDIO_EXTRACTED", "db": "audio_extracted", "class": "open"},
    {"name": "TRANSCRIBED",     "db": "transcribed",     "class": "open"},
    {"name": "INDEXED",         "db": "indexed",         "class": "open"},
    {"name": "THUMBNAILED",     "db": "thumbnailed",     "class": "open"},
    {"name": "READY",           "db": "ready",           "class": "terminal-good"},
    {"name": "READY_NO_AUDIO",  "db": "ready_no_audio",  "class": "terminal-good"},
    {"name": "MISSING",         "db": "missing",         "class": "sink"},
    {"name": "SUPERSEDED",      "db": "superseded",      "class": "terminal-soft"},
    {"name": "CORRUPTED",       "db": "corrupted",       "class": "terminal-bad"},
    {"name": "FAILED",          "db": "failed",          "class": "terminal-bad"}
  ],
  "stages": [
    "scan", "probe", "extract", "transcribe",
    "subtitle_gen", "index", "thumbnail"
  ],
  "triggers": [
    "scan", "probe", "extract", "transcribe",
    "subtitle_gen", "index", "thumbnail",
    "filesystem", "library", "integrity"
  ],
  "transitions": [
    {"from": "DISCOVERED",      "trigger": "probe",        "outcome": "ok",             "to": "PROBED"},
    {"from": "PROBED",          "trigger": "extract",      "outcome": "ok",             "to": "AUDIO_EXTRACTED"},
    {"from": "PROBED",          "trigger": "probe",        "outcome": "no_audio",       "to": "READY_NO_AUDIO"},
    {"from": "AUDIO_EXTRACTED", "trigger": "transcribe",   "outcome": "ok",             "to": "TRANSCRIBED"},
    {"from": "TRANSCRIBED",     "trigger": "subtitle_gen", "outcome": "partial",        "to": "TRANSCRIBED"},
    {"from": "TRANSCRIBED",     "trigger": "index",        "outcome": "partial",        "to": "TRANSCRIBED"},
    {"from": "TRANSCRIBED",     "trigger": "subtitle_gen", "outcome": "ok",             "to": "INDEXED"},
    {"from": "TRANSCRIBED",     "trigger": "index",        "outcome": "ok",             "to": "INDEXED"},
    {"from": "INDEXED",         "trigger": "thumbnail",    "outcome": "ok",             "to": "THUMBNAILED"},
    {"from": "THUMBNAILED",     "trigger": "scan",         "outcome": "all_gates_ok",   "to": "READY"},
    {"from": "MISSING",         "trigger": "scan",         "outcome": "rediscovered",   "to": "DISCOVERED"}
  ],
  "broadcast_transitions": [
    {"source_class_in": ["open", "terminal-good", "sink"],  "trigger": "filesystem", "outcome": "deleted",  "to": "MISSING"},
    {"source_class_in": ["open", "terminal-good"],           "trigger": "*",          "outcome": "exhausted","to": "FAILED"},
    {"source_class_in": ["open", "terminal-good"],           "trigger": "integrity",  "outcome": "fail",     "to": "CORRUPTED"},
    {"source_class_in": ["open", "terminal-good", "terminal-soft"], "trigger": "library","outcome": "replaced","to": "SUPERSEDED"}
  ]
}
```

The `"*"` wildcard in `broadcast_transitions` matches any stage trigger
(not the side-channel triggers). Generators expand wildcards; runtime
tables hold only fully-specified `(from, trigger, outcome) → to` rows.

---

## 6. Migration `0003_video_states_and_stages.sql`

### 6.1 Postgres

```sql
-- +goose Up
-- ============================================================================
-- 0003 — Video states & processing-job stages.
--
-- Adds CHECK constraints on videos.state and processing_jobs.stage to enforce
-- the canonical enums in shared/states/states.json. Rewrites legacy
-- processing_jobs rows that used 'thumb' to 'thumbnail' (REVIEW §1.3.c).
--
-- Owner: specs/epics/01-scanner/story-01-06-video-state-machine.md
-- Companion plan: specs/epics/01-scanner/plan-01-06-video-state-machine.md
--
-- Lock note: ALTER TABLE … ADD CONSTRAINT takes ACCESS EXCLUSIVE on the
-- target for the duration of validation. On a household-scale ~50k-row
-- videos table the validation is sub-second; on the much larger
-- processing_jobs table (jobs accumulate over time) it can take longer.
-- For the latter we DROP CONSTRAINT IF EXISTS / ADD CONSTRAINT NOT VALID
-- + VALIDATE CONSTRAINT to avoid a long-held write lock.
-- ============================================================================

BEGIN;

-- 1. Rewrite legacy stage names to the canonical set.
UPDATE processing_jobs SET stage = 'thumbnail' WHERE stage = 'thumb';

-- 2. CHECK on videos.state.
ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_valid;     -- idempotent re-run
ALTER TABLE videos
    ADD CONSTRAINT videos_state_valid CHECK (state IN (
        'discovered','probed','audio_extracted','transcribed','indexed',
        'thumbnailed','ready','ready_no_audio','missing','superseded',
        'corrupted','failed'
    ));

-- 3. CHECK on processing_jobs.stage. NOT VALID + VALIDATE keeps the
--    write window short on a hot table.
ALTER TABLE processing_jobs
    DROP CONSTRAINT IF EXISTS processing_jobs_stage_valid;
ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_stage_valid CHECK (stage IN (
        'scan','probe','extract','transcribe','subtitle_gen','index','thumbnail'
    )) NOT VALID;
ALTER TABLE processing_jobs
    VALIDATE CONSTRAINT processing_jobs_stage_valid;

COMMIT;

-- +goose Down
BEGIN;

ALTER TABLE videos
    DROP CONSTRAINT IF EXISTS videos_state_valid;

ALTER TABLE processing_jobs
    DROP CONSTRAINT IF EXISTS processing_jobs_stage_valid;

-- We do NOT rewrite 'thumbnail' back to 'thumb' because pre-0003 code
-- read either name from a string column; the legacy code path tolerated
-- the new value as readonly. Restoring the old spelling is a manual
-- decision, not a migration default.

COMMIT;
```

### 6.2 SQLite variant

SQLite cannot drop a CHECK constraint in place; the down direction
rebuilds the table. The up direction can use `CREATE TABLE IF NOT
EXISTS` plus index re-creation. The full SQLite SQL is in
`shared/db/migrations/0003_video_states_and_stages.sqlite.sql`,
selected by `goose -dialect sqlite3` per [plan-01-05](story-01-05-PLAN.md) §6.

### 6.3 Idempotency

Both sides start with `DROP CONSTRAINT IF EXISTS`, so a half-applied
migration recovers cleanly. The legacy-rewrite UPDATE is naturally
idempotent (a second run rewrites zero rows). Tests in §10 nail this
down (`test_migration_0003_idempotent`).

### 6.4 Why no transition trigger

Per D4. A SQL trigger can enforce `state IN allowed[old.state]`, but
cannot read the *trigger* (probe vs filesystem) or the *outcome*
(`ok` vs `partial`). The transition rules in §4 depend on both, so a
trigger would either reject legitimate transitions or accept invalid
ones. We enforce in application code where the call site has full
context, and rely on §11.2's lint rule to keep direct UPDATEs out.

---

## 7. Go code scaffolding — `shared/states/`

### 7.1 Generated enum + table — `shared/states/states_gen.go`

Generated from `states.json` by `go run ./shared/states/cmd/gen`. The
generator is committed (hand-written, ~40 lines); the output is also
committed (no codegen at build time).

```go
// Code generated by gen.go from states.json; DO NOT EDIT.
package states

// State is the canonical video-state enum. The string values are what
// land in videos.state.
type State string

const (
    StateDiscovered     State = "discovered"
    StateProbed         State = "probed"
    StateAudioExtracted State = "audio_extracted"
    StateTranscribed    State = "transcribed"
    StateIndexed        State = "indexed"
    StateThumbnailed    State = "thumbnailed"
    StateReady          State = "ready"
    StateReadyNoAudio   State = "ready_no_audio"
    StateMissing        State = "missing"
    StateSuperseded     State = "superseded"
    StateCorrupted      State = "corrupted"
    StateFailed         State = "failed"
)

// AllStates is the full set; iteration order is stable and matches the
// manifest. Used by the migration test (§10) to assert CHECK parity.
var AllStates = []State{
    StateDiscovered, StateProbed, StateAudioExtracted, StateTranscribed,
    StateIndexed, StateThumbnailed, StateReady, StateReadyNoAudio,
    StateMissing, StateSuperseded, StateCorrupted, StateFailed,
}

// Class returns the FSM class of a state — open / terminal-good /
// terminal-soft / terminal-bad / sink.
func (s State) Class() string { return classOf[s] }

// Stage is the canonical processing_jobs.stage enum.
type Stage string

const (
    StageScan         Stage = "scan"
    StageProbe        Stage = "probe"
    StageExtract      Stage = "extract"
    StageTranscribe   Stage = "transcribe"
    StageSubtitleGen  Stage = "subtitle_gen"
    StageIndex        Stage = "index"
    StageThumbnail    Stage = "thumbnail"
)

var AllStages = []Stage{
    StageScan, StageProbe, StageExtract, StageTranscribe,
    StageSubtitleGen, StageIndex, StageThumbnail,
}

// Trigger is a superset of Stage — it includes the side-channel
// triggers (filesystem, library, integrity) that drive transitions
// outside the main pipeline.
type Trigger string

const (
    TriggerScan         Trigger = "scan"
    TriggerProbe        Trigger = "probe"
    TriggerExtract      Trigger = "extract"
    TriggerTranscribe   Trigger = "transcribe"
    TriggerSubtitleGen  Trigger = "subtitle_gen"
    TriggerIndex        Trigger = "index"
    TriggerThumbnail    Trigger = "thumbnail"
    TriggerFilesystem   Trigger = "filesystem"
    TriggerLibrary      Trigger = "library"
    TriggerIntegrity    Trigger = "integrity"
)

// transitionKey is the lookup key into the runtime table.
type transitionKey struct {
    From    State
    Trigger Trigger
    Outcome string
}

// allowed is the runtime-built transition map. Built once at package
// init from the manifest. nil-target sentinels are not used; missing
// keys mean "no such transition."
var allowed = map[transitionKey]State{
    {StateDiscovered,     TriggerProbe,       "ok"}:           StateProbed,
    {StateProbed,         TriggerExtract,     "ok"}:           StateAudioExtracted,
    {StateProbed,         TriggerProbe,       "no_audio"}:     StateReadyNoAudio,
    {StateAudioExtracted, TriggerTranscribe,  "ok"}:           StateTranscribed,
    {StateTranscribed,    TriggerSubtitleGen, "partial"}:      StateTranscribed,
    {StateTranscribed,    TriggerIndex,       "partial"}:      StateTranscribed,
    {StateTranscribed,    TriggerSubtitleGen, "ok"}:           StateIndexed,
    {StateTranscribed,    TriggerIndex,       "ok"}:           StateIndexed,
    {StateIndexed,        TriggerThumbnail,   "ok"}:           StateThumbnailed,
    {StateThumbnailed,    TriggerScan,        "all_gates_ok"}: StateReady,
    {StateMissing,        TriggerScan,        "rediscovered"}: StateDiscovered,

    // Broadcast rows expanded by the generator follow.
    // (filesystem/deleted from every non-terminal-bad/soft source ...)
    // (any-stage/exhausted from every non-terminal source ...)
    // (integrity/fail from every non-terminal-bad/soft source ...)
    // (library/replaced from every non-terminal-bad source ...)
    // -- ~70 expanded rows total; elided here for readability.
}

// classOf maps each state to its FSM class.
var classOf = map[State]string{ /* generated */ }
```

### 7.2 Hand-written API — `shared/states/advance.go`

```go
package states

import (
    "context"
    "errors"
    "fmt"
    "log/slog"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

// Outcome labels the result of a stage or side-channel event. The set
// is open at the type level but closed at the runtime-table level;
// unknown outcomes raise IllegalStateTransition even when the source
// state is otherwise valid.
type Outcome string

// IllegalStateTransition is returned when (current, trigger, outcome)
// has no entry in the transition table. Carries enough context for
// observability without leaking the whole table.
type IllegalStateTransition struct {
    VideoID uuid.UUID
    From    State
    Trigger Trigger
    Outcome Outcome
}

func (e *IllegalStateTransition) Error() string {
    return fmt.Sprintf(
        "illegal state transition for video %s: from=%s trigger=%s outcome=%s",
        e.VideoID, e.From, e.Trigger, e.Outcome,
    )
}

// AdvanceAfterStage is the SOLE function that mutates videos.state.
// All callers — the orchestrator, the watcher (filesystem), Epic 9
// (library), Epic 24 (integrity) — funnel through here.
//
// The function:
//   1. Opens a transaction and takes a row-level lock on videos via
//      SELECT … FOR UPDATE. This serializes concurrent advances.
//   2. Re-reads the current state inside the lock.
//   3. If current state is terminal-soft / terminal-bad, drops the
//      update and returns nil with a structured log. The work is
//      wasted but the state is correct (story edge case 1).
//   4. Looks up (from, trigger, outcome) in the transition table.
//      Returns *IllegalStateTransition if missing.
//   5. UPDATE videos.state, COMMITs, returns the new state.
func AdvanceAfterStage(
    ctx context.Context,
    pool *pgxpool.Pool,
    log *slog.Logger,
    videoID uuid.UUID,
    trigger Trigger,
    outcome Outcome,
) (State, error) {
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return "", err
    }
    defer tx.Rollback(ctx)

    var current State
    err = tx.QueryRow(ctx,
        `SELECT state FROM videos WHERE id = $1 FOR UPDATE`,
        videoID,
    ).Scan(&current)
    if err != nil {
        return "", fmt.Errorf("lock video %s: %w", videoID, err)
    }

    // Story edge case 1: race between stage finish and library deletion.
    // If the row is already terminal-soft / terminal-bad, we drop the
    // stage's success update and log it. Returning the current state
    // (not an error) lets the caller close out cleanly.
    if isTerminalDrop(current) {
        log.Info("late_stage_finish",
            "video_id", videoID, "current", current,
            "trigger", trigger, "outcome", outcome,
        )
        return current, tx.Commit(ctx)
    }

    target, ok := lookup(current, trigger, outcome)
    if !ok {
        return current, &IllegalStateTransition{
            VideoID: videoID, From: current,
            Trigger: trigger, Outcome: Outcome(outcome),
        }
    }

    // Self-loop on (TRANSCRIBED, *, partial) is a real edge in the
    // table; do the UPDATE anyway so updated_at advances.
    _, err = tx.Exec(ctx,
        `UPDATE videos SET state = $1, updated_at = now() WHERE id = $2`,
        target, videoID,
    )
    if err != nil {
        return "", err
    }
    if err := tx.Commit(ctx); err != nil {
        return "", err
    }
    return target, nil
}

func lookup(from State, trig Trigger, out Outcome) (State, bool) {
    t, ok := allowed[transitionKey{From: from, Trigger: trig, Outcome: string(out)}]
    return t, ok
}

func isTerminalDrop(s State) bool {
    switch s {
    case StateSuperseded, StateCorrupted, StateFailed:
        return true
    default:
        return false
    }
}

// errors.Is(err, ErrIllegalTransition) convenience.
var ErrIllegalTransition = errors.New("illegal state transition")
func (e *IllegalStateTransition) Is(target error) bool {
    return target == ErrIllegalTransition
}
```

### 7.3 Joint-condition helper for `(TRANSCRIBED, *, *)`

The orchestrator does **not** decide `partial` vs `ok` itself — the
stage callback computes the joint condition by reading
`processing_jobs`. The helper lives in
`pipeline/orchestrator/pkg/gates/transcribed.go` (Go variant for the
API-side trigger; Python equivalent on the pipeline side):

```go
// FinishedTranscribed reports the outcome to pass to AdvanceAfterStage
// when subtitle_gen or index just finished. It returns "ok" iff BOTH
// stages have reached state='done' for this video; otherwise "partial".
func FinishedTranscribed(ctx context.Context, q *db.Queries, videoID uuid.UUID) (Outcome, error) {
    row, err := q.CountTranscribedGates(ctx, videoID)
    if err != nil {
        return "", err
    }
    // Both subtitle_gen and index 'done'?
    if row.SubtitleGenDone && row.IndexDone {
        return "ok", nil
    }
    return "partial", nil
}
```

with the matching SQL:

```sql
-- name: CountTranscribedGates :one
SELECT
    COALESCE(BOOL_OR(stage = 'subtitle_gen' AND state = 'done'), false) AS subtitle_gen_done,
    COALESCE(BOOL_OR(stage = 'index'        AND state = 'done'), false) AS index_done
FROM processing_jobs
WHERE video_id = $1
  AND stage IN ('subtitle_gen', 'index');
```

---

## 8. Python parity — `pipeline/src/maktaba_pipeline/domain/states.py`

The Python module mirrors the Go package one-for-one. Generator is
`pipeline/scripts/gen_states.py`; output is committed.

```python
# Generated from shared/states/states.json; DO NOT EDIT.
from __future__ import annotations
import enum
from dataclasses import dataclass

class State(str, enum.Enum):
    DISCOVERED      = "discovered"
    PROBED          = "probed"
    AUDIO_EXTRACTED = "audio_extracted"
    TRANSCRIBED     = "transcribed"
    INDEXED         = "indexed"
    THUMBNAILED     = "thumbnailed"
    READY           = "ready"
    READY_NO_AUDIO  = "ready_no_audio"
    MISSING         = "missing"
    SUPERSEDED      = "superseded"
    CORRUPTED       = "corrupted"
    FAILED          = "failed"

class Stage(str, enum.Enum):
    SCAN          = "scan"
    PROBE         = "probe"
    EXTRACT       = "extract"
    TRANSCRIBE    = "transcribe"
    SUBTITLE_GEN  = "subtitle_gen"
    INDEX         = "index"
    THUMBNAIL     = "thumbnail"

class Trigger(str, enum.Enum):
    # superset of Stage
    SCAN         = "scan"
    PROBE        = "probe"
    EXTRACT      = "extract"
    TRANSCRIBE   = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX        = "index"
    THUMBNAIL    = "thumbnail"
    FILESYSTEM   = "filesystem"
    LIBRARY      = "library"
    INTEGRITY    = "integrity"

@dataclass(frozen=True)
class _Key:
    frm: State
    trigger: Trigger
    outcome: str

# allowed_transitions: dict[(State, Trigger, str), State]
# Note: the story names this `dict[State, set[State]]`. Our richer
# triple key satisfies the same invariant (every (from, to) pair in the
# diagram is reachable iff some triple maps to that target) and lets us
# distinguish (TRANSCRIBED, index, partial) from (TRANSCRIBED, index, ok).
# A view dict[State, set[State]] is exposed as `allowed_targets` for
# tests that prefer the simpler shape.
allowed_transitions: dict[_Key, State] = {
    _Key(State.DISCOVERED,      Trigger.PROBE,        "ok"):           State.PROBED,
    _Key(State.PROBED,          Trigger.EXTRACT,      "ok"):           State.AUDIO_EXTRACTED,
    _Key(State.PROBED,          Trigger.PROBE,        "no_audio"):     State.READY_NO_AUDIO,
    _Key(State.AUDIO_EXTRACTED, Trigger.TRANSCRIBE,   "ok"):           State.TRANSCRIBED,
    _Key(State.TRANSCRIBED,     Trigger.SUBTITLE_GEN, "partial"):      State.TRANSCRIBED,
    _Key(State.TRANSCRIBED,     Trigger.INDEX,        "partial"):      State.TRANSCRIBED,
    _Key(State.TRANSCRIBED,     Trigger.SUBTITLE_GEN, "ok"):           State.INDEXED,
    _Key(State.TRANSCRIBED,     Trigger.INDEX,        "ok"):           State.INDEXED,
    _Key(State.INDEXED,         Trigger.THUMBNAIL,    "ok"):           State.THUMBNAILED,
    _Key(State.THUMBNAILED,     Trigger.SCAN,         "all_gates_ok"): State.READY,
    _Key(State.MISSING,         Trigger.SCAN,         "rediscovered"): State.DISCOVERED,
    # ... broadcast rows expanded by the generator
}

# Convenience view: dict[State, set[State]] — the shape the story names.
allowed_targets: dict[State, set[State]] = {
    s: {t for k, t in allowed_transitions.items() if k.frm is s}
    for s in State
}

class IllegalStateTransition(Exception):
    def __init__(self, video_id, from_: State, trigger: Trigger, outcome: str):
        self.video_id, self.from_, self.trigger, self.outcome = (
            video_id, from_, trigger, outcome,
        )
        super().__init__(
            f"illegal state transition for video {video_id}: "
            f"from={from_.value} trigger={trigger.value} outcome={outcome}"
        )
```

with the orchestrator entry point in
`pipeline/src/maktaba_pipeline/orchestrator/advance.py`:

```python
async def advance_after_stage(
    db: Database,
    video_id: UUID,
    trigger: Trigger,
    outcome: str,
    *,
    log: structlog.BoundLogger,
) -> State:
    async with db.tx() as tx:
        row = await tx.fetchrow(
            "SELECT state FROM videos WHERE id = $1 FOR UPDATE", video_id
        )
        current = State(row["state"])

        # Edge case 1: late-stage finish on a terminal video.
        if current in {State.SUPERSEDED, State.CORRUPTED, State.FAILED}:
            log.info("late_stage_finish",
                     video_id=str(video_id), current=current.value,
                     trigger=trigger.value, outcome=outcome)
            return current

        key = _Key(current, trigger, outcome)
        target = allowed_transitions.get(key)
        if target is None:
            raise IllegalStateTransition(video_id, current, trigger, outcome)

        await tx.execute(
            "UPDATE videos SET state = $1, updated_at = now() WHERE id = $2",
            target.value, video_id,
        )
        return target
```

---

## 9. Lint enforcement — `videos.state` write quarantine

Per D6, no code outside `shared/states/` (Go) or
`pipeline/.../orchestrator/advance.py` (Python) may issue a
`UPDATE videos SET state` statement.

### 9.1 Go — `forbidigo`

`tools/forbidigo.yaml` adds a pattern that matches the literal:

```yaml
- 'UPDATE\s+videos\s+SET\s+state'
```

with an exemption for `shared/states/advance.go`. The lint runs in CI
(`make lint`). Sqlc-generated `queries.sql.go` derives from `*.sql`
files — those source `.sql` files are scanned by `tools/check_state_writes.sh`
which greps for the same pattern and exempts
`shared/db/queries/states/*.sql`.

### 9.2 Python — custom `ruff` rule

`tools/ruff_state_writes.py` walks the AST looking for SQL literals
that match the same pattern. False positives are tagged with
`# noqa: STATE001 — managed write`.

### 9.3 Why both layers

The Go scanner writes through `sqlc` (compiled queries) but the Python
pipeline writes through `asyncpg` with literal SQL strings. One linter
cannot cover both. Both run in `make lint`.

---

## 10. Test plan

| ID | Type | Layer | Coverage |
|----|------|-------|----------|
| **Manifest / generator** | | | |
| `test_states_manifest_well_formed` | Unit | both | `states.json` parses, every transition's `from`/`to`/`trigger`/`outcome` is valid. |
| `test_go_binding_matches_manifest` | Unit | go | The compiled `allowed` table equals the expansion of `states.json`. |
| `test_py_binding_matches_manifest` | Unit | py | Same, for `allowed_transitions`. |
| `test_state_enum_matches_spec` | Unit | py | `set(State) == {…12 names…}`; from story acceptance. |
| `test_stage_enum_matches_spec` | Unit | py | `set(Stage) == {…7 names…}`; from story acceptance. |
| **Migration** | | | |
| `test_migration_0003_adds_check_videos` | DB-fixture | go | After 0003, `videos_state_valid` exists; INSERT of `state='nonsense'` fails. |
| `test_migration_0003_adds_check_jobs` | DB-fixture | go | After 0003, `processing_jobs_stage_valid` exists; INSERT of `stage='thumb'` fails. |
| `test_migration_0003_rewrites_thumb` | DB-fixture | go | Pre-seed `processing_jobs(stage='thumb')` at version 0002; run 0003; row now reads `'thumbnail'`. |
| `test_check_constraints_reject_legacy` | DB-fixture | go | Story acceptance restated: `videos(state='thumb')` and `processing_jobs(stage='thumb')` both fail INSERT. |
| `test_migration_0003_idempotent` | DB-fixture | go | Running 0003 twice is a no-op (DROP IF EXISTS / ADD pattern). |
| `test_migration_0003_down_then_up` | DB-fixture | go | `goose down 0003 && goose up` round-trips the constraints. |
| **Transition matrix** | | | |
| `test_allowed_transitions_table_complete` | Unit | both | Story acceptance: every pair (from, to) in the diagram is reachable; every pair *not* in the diagram is rejected by `lookup` (Go) / `allowed_transitions.get` (Py). |
| `test_advance_each_canonical_edge` | Integration | both | Parametrized over the 11 explicit rows in §4: seed a video at `from`, call `advance_after_stage(trigger, outcome)`, assert state == `to`. |
| `test_advance_each_broadcast_edge` | Integration | both | Parametrized over the 4 broadcast rows × every legal source: same shape. |
| `test_advance_rejects_invalid_outcome` | Unit | both | `(DISCOVERED, probe, weird)` raises `IllegalStateTransition`. |
| `test_advance_rejects_invalid_trigger_for_state` | Unit | both | `(DISCOVERED, transcribe, ok)` raises `IllegalStateTransition`. |
| `test_advance_rejects_extract_on_no_audio` | Unit | both | `(READY_NO_AUDIO, extract, ok)` raises. |
| **Specific story-named tests** | | | |
| `test_missing_to_discovered_round_trip` | Integration | both | Video → MISSING; `advance_after_stage(scan, rediscovered)` flips it to DISCOVERED; `transcripts` and `media_info` rows survive. |
| `test_subtitle_gen_does_not_advance_to_indexed_alone` | Integration | both | Finish only `subtitle_gen` (other gate `pending`) → state stays `TRANSCRIBED`; finish `index` too → state advances to `INDEXED`. Drives §7.3 helper. |
| `test_supersede_preserves_transcripts` | Integration | both | Set video to SUPERSEDED via `(library, replaced)`; `transcripts.video_id = $videoID` rows still queryable. |
| `test_corrupted_blocks_further_processing` | Integration | both | Set video to CORRUPTED; `advance_after_stage` from `(CORRUPTED, *, *)` returns `late_stage_finish` and enqueues no new `processing_jobs`. |
| **Edge cases** | | | |
| `test_late_stage_finish_on_superseded` | Integration | both | Race: video is SUPERSEDED; a stage worker calls `advance_after_stage(thumbnail, ok)`. State stays SUPERSEDED; log includes `late_stage_finish`. |
| `test_late_stage_finish_on_failed` | Integration | both | Same shape, source = FAILED. |
| `test_concurrent_advances_serialize` | Integration | both | Two goroutines / coroutines call `advance_after_stage` simultaneously on the same video. Outcome is one success, one no-op (or one success, one rejection if outcomes contradict). State is consistent. |
| `test_missing_revival_cancels_stale_probe` | Integration | both | (Story edge case 2) Video MISSING with a stale `processing_jobs(stage='probe', state='pending')` from before the disappearance. On rediscovery, the stale probe is cancelled (Epic 6 Story 6.4 contract) and a fresh probe is enqueued. |
| **Lint** | | | |
| `test_no_direct_state_writes_go` | CI | go | `make lint` fails if any file outside `shared/states/` contains the forbidden pattern. |
| `test_no_direct_state_writes_py` | CI | py | Same on Python side via `ruff`. |

### 10.1 Go test scaffolding — `shared/states/advance_test.go`

```go
package states_test

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "github.com/maktaba/api/internal/testdb"
    "github.com/maktaba/shared/states"
    "github.com/stretchr/testify/require"
)

func TestAdvance_EachCanonicalEdge(t *testing.T) {
    pool := testdb.MustOpen(t)
    log := testdb.NewLogger(t)
    cases := []struct {
        from    states.State
        trigger states.Trigger
        outcome states.Outcome
        to      states.State
    }{
        {states.StateDiscovered,     states.TriggerProbe,       "ok",            states.StateProbed},
        {states.StateProbed,         states.TriggerExtract,     "ok",            states.StateAudioExtracted},
        {states.StateProbed,         states.TriggerProbe,       "no_audio",      states.StateReadyNoAudio},
        {states.StateAudioExtracted, states.TriggerTranscribe,  "ok",            states.StateTranscribed},
        {states.StateTranscribed,    states.TriggerSubtitleGen, "ok",            states.StateIndexed},
        {states.StateTranscribed,    states.TriggerIndex,       "ok",            states.StateIndexed},
        {states.StateIndexed,        states.TriggerThumbnail,   "ok",            states.StateThumbnailed},
        {states.StateThumbnailed,    states.TriggerScan,        "all_gates_ok",  states.StateReady},
        {states.StateMissing,        states.TriggerScan,        "rediscovered",  states.StateDiscovered},
    }
    for _, c := range cases {
        t.Run(string(c.from)+"->"+string(c.to), func(t *testing.T) {
            id := testdb.SeedVideoAtState(t, pool, c.from)
            got, err := states.AdvanceAfterStage(
                context.Background(), pool, log, id, c.trigger, c.outcome,
            )
            require.NoError(t, err)
            require.Equal(t, c.to, got)
            require.Equal(t, c.to, testdb.VideoState(t, pool, id))
        })
    }
}

func TestAdvance_RejectsInvalidTriple(t *testing.T) {
    pool := testdb.MustOpen(t)
    log := testdb.NewLogger(t)
    id := testdb.SeedVideoAtState(t, pool, states.StateDiscovered)

    _, err := states.AdvanceAfterStage(
        context.Background(), pool, log, id,
        states.TriggerTranscribe, "ok", // invalid from DISCOVERED
    )
    require.ErrorIs(t, err, states.ErrIllegalTransition)

    var ist *states.IllegalStateTransition
    require.ErrorAs(t, err, &ist)
    require.Equal(t, states.StateDiscovered, ist.From)
    require.Equal(t, states.StateDiscovered, testdb.VideoState(t, pool, id),
        "state must NOT have changed on a rejected transition")
}

func TestAdvance_LateStageFinishOnSuperseded(t *testing.T) {
    pool := testdb.MustOpen(t)
    log := testdb.NewLogger(t)
    id := testdb.SeedVideoAtState(t, pool, states.StateSuperseded)

    got, err := states.AdvanceAfterStage(
        context.Background(), pool, log, id,
        states.TriggerThumbnail, "ok",
    )
    require.NoError(t, err, "drop, do not error")
    require.Equal(t, states.StateSuperseded, got)
    require.Equal(t, states.StateSuperseded, testdb.VideoState(t, pool, id))
    require.Contains(t, testdb.LogLines(t, log), "late_stage_finish")
}

func TestAdvance_ConcurrentSerializes(t *testing.T) {
    pool := testdb.MustOpen(t)
    log := testdb.NewLogger(t)
    id := testdb.SeedVideoAtState(t, pool, states.StateDiscovered)

    errs := make(chan error, 2)
    for i := 0; i < 2; i++ {
        go func() {
            _, err := states.AdvanceAfterStage(
                context.Background(), pool, log, id,
                states.TriggerProbe, "ok",
            )
            errs <- err
        }()
    }
    e1, e2 := <-errs, <-errs
    // The first wins; the second sees state=PROBED already and finds
    // no (PROBED, probe, ok) row → IllegalStateTransition.
    successes := 0
    rejections := 0
    for _, e := range []error{e1, e2} {
        switch {
        case e == nil:
            successes++
        case errors.Is(e, states.ErrIllegalTransition):
            rejections++
        default:
            t.Fatalf("unexpected error: %v", e)
        }
    }
    require.Equal(t, 1, successes)
    require.Equal(t, 1, rejections)
    require.Equal(t, states.StateProbed, testdb.VideoState(t, pool, id))
}
```

### 10.2 Migration test — `api/internal/db/migrations_state_test.go`

```go
func TestMigration0003_RewritesThumb(t *testing.T) {
    pool := testdb.OpenAtVersion(t, "0002")
    lib  := testdb.MustCreateLibrary(t, pool, "x", nil)
    vid  := testdb.MustInsertVideo(t, pool, lib.ID, "h", "/p")

    // At version 0002, processing_jobs.stage has no CHECK; insert legacy.
    testdb.MustExec(t, pool,
        `INSERT INTO processing_jobs (video_id, stage, state) VALUES ($1, 'thumb', 'pending')`,
        vid.ID,
    )

    require.NoError(t, testdb.MigrateUpTo(pool, "0003"))

    var stage string
    require.NoError(t, pool.QueryRow(ctx,
        `SELECT stage FROM processing_jobs WHERE video_id = $1`, vid.ID,
    ).Scan(&stage))
    require.Equal(t, "thumbnail", stage)

    // Now the CHECK is in place: 'thumb' is rejected.
    _, err := pool.Exec(ctx,
        `INSERT INTO processing_jobs (video_id, stage, state) VALUES ($1, 'thumb', 'pending')`,
        vid.ID,
    )
    require.Error(t, err)
}
```

### 10.3 Python test — `pipeline/tests/domain/test_states.py`

```python
import pytest
from maktaba_pipeline.domain.states import (
    State, Stage, Trigger, allowed_transitions, allowed_targets,
    IllegalStateTransition,
)
from maktaba_pipeline.orchestrator.advance import advance_after_stage

def test_state_enum_matches_spec():
    assert {s.name for s in State} == {
        "DISCOVERED", "PROBED", "AUDIO_EXTRACTED", "TRANSCRIBED",
        "INDEXED", "THUMBNAILED", "READY", "READY_NO_AUDIO",
        "MISSING", "SUPERSEDED", "CORRUPTED", "FAILED",
    }

def test_stage_enum_matches_spec():
    assert {s.value for s in Stage} == {
        "scan", "probe", "extract", "transcribe",
        "subtitle_gen", "index", "thumbnail",
    }

@pytest.mark.parametrize("frm,trig,out,to", [
    (State.DISCOVERED,      Trigger.PROBE,        "ok",            State.PROBED),
    (State.PROBED,          Trigger.EXTRACT,      "ok",            State.AUDIO_EXTRACTED),
    (State.PROBED,          Trigger.PROBE,        "no_audio",      State.READY_NO_AUDIO),
    (State.AUDIO_EXTRACTED, Trigger.TRANSCRIBE,   "ok",            State.TRANSCRIBED),
    (State.TRANSCRIBED,     Trigger.INDEX,        "partial",       State.TRANSCRIBED),
    (State.TRANSCRIBED,     Trigger.INDEX,        "ok",            State.INDEXED),
    (State.INDEXED,         Trigger.THUMBNAIL,    "ok",            State.THUMBNAILED),
    (State.THUMBNAILED,     Trigger.SCAN,         "all_gates_ok",  State.READY),
    (State.MISSING,         Trigger.SCAN,         "rediscovered",  State.DISCOVERED),
])
async def test_advance_each_canonical_edge(db, frm, trig, out, to):
    vid = await db.seed_video_at(frm)
    got = await advance_after_stage(db, vid, trig, out, log=db.test_log)
    assert got == to
    assert await db.video_state(vid) == to

async def test_subtitle_gen_does_not_advance_to_indexed_alone(db):
    vid = await db.seed_video_at(State.TRANSCRIBED)
    await db.seed_processing_job(vid, Stage.SUBTITLE_GEN, "done")
    # index still pending.
    got = await advance_after_stage(db, vid, Trigger.SUBTITLE_GEN, "partial",
                                    log=db.test_log)
    assert got == State.TRANSCRIBED

    await db.seed_processing_job(vid, Stage.INDEX, "done")
    got = await advance_after_stage(db, vid, Trigger.INDEX, "ok",
                                    log=db.test_log)
    assert got == State.INDEXED

async def test_corrupted_blocks_further_processing(db):
    vid = await db.seed_video_at(State.CORRUPTED)
    got = await advance_after_stage(db, vid, Trigger.THUMBNAIL, "ok",
                                    log=db.test_log)
    assert got == State.CORRUPTED  # late-stage drop
    assert await db.count_pending_jobs(vid) == 0
```

---

## 11. Edge cases — the long tail

The story enumerates four; this section pins down the ones that
appeared during plan review.

### 11.1 Race: stage finish vs library deletion (story edge 1)

`advance_after_stage` opens a transaction, takes `SELECT … FOR UPDATE`
on the row, re-reads `state`, and if the state is terminal-soft /
terminal-bad it commits a no-op and returns. The stage's commit /
work is wasted but the state is correct.

Test: `test_late_stage_finish_on_superseded` (§10.1, §10.3).

### 11.2 `MISSING → DISCOVERED` while a stale `probe` job is pending (story edge 2)

The orchestrator's `(MISSING, scan, rediscovered) → DISCOVERED` path
also calls into the **job cancel** API exposed by Epic 6 Story 6.4:

```python
async def advance_after_stage(...):
    ...
    # After the state UPDATE, side-effects scoped to this transition:
    if (current, target) == (State.MISSING, State.DISCOVERED):
        await cancel_pending_jobs(tx, video_id, stages={Stage.PROBE})
        await enqueue_probe(tx, video_id)
```

The cancel and the enqueue happen in the same transaction as the
state flip, so a crash between them cannot leave a stale probe in the
queue.

Test: `test_missing_revival_cancels_stale_probe`.

### 11.3 Stage finish after move to FAILED (story edge 3)

Identical to 11.1 with source = FAILED. The `processing_jobs.state`
column is left at `done` so observability still reflects what
happened; `videos.state` stays FAILED.

Test: `test_late_stage_finish_on_failed`.

### 11.4 Adding a new state in a future version (story edge 4)

The migration template:

```sql
-- 000N_add_state_FOO.sql
BEGIN;
ALTER TABLE videos DROP CONSTRAINT videos_state_valid;
ALTER TABLE videos ADD CONSTRAINT videos_state_valid CHECK (state IN (
    'discovered', ..., 'foo'
));
COMMIT;
```

`DROP CONSTRAINT` + `ADD CONSTRAINT` (without `NOT VALID`) takes a
short ACCESS EXCLUSIVE on `videos`. For a household-scale ~50k row
table this is sub-second; for a hypothetical multi-million-row table
the operator should use `ADD CONSTRAINT … NOT VALID` followed by
`VALIDATE CONSTRAINT` to keep the write window short. The same
manifest update propagates to both bindings via codegen; tests fail
loudly if the SQL CHECK and the manifest disagree (`test_check_in_sync_with_manifest`).

### 11.5 Two libraries, same content_hash, one goes MISSING

Per [story-01-02](story-01-02-content-identity.md) and
[plan-01-05](story-01-05-PLAN.md) §2.5, identical bytes in two
libraries produce two `videos` rows. When library A's file disappears
and B's does not, only A's row flips to MISSING. The state machine
treats them independently — `advance_after_stage` is keyed by
`video_id`, not by content_hash.

Test: `test_two_libraries_same_hash_independent_states` (extends
plan-01-05's `test_two_libraries_same_bytes_two_rows`).

### 11.6 `subtitle_gen` finishes before `transcribe` (impossible)

A worker that processes `subtitle_gen` must read `transcripts.is_active=true`
to know what segments to render. An attempt to enqueue `subtitle_gen`
on a video whose `transcribe` stage hasn't reached `done` is a
programmer error caught by the orchestrator's enqueue gate (Epic 4
Story 4.1 owns the enqueue rule). The state machine doesn't need to
defend against it: if it ever happens, `(AUDIO_EXTRACTED, subtitle_gen,
ok)` is an unmapped triple and `advance_after_stage` raises
`IllegalStateTransition`.

Test: `test_advance_rejects_subtitle_gen_before_transcribe`.

### 11.7 Re-supersede

Epic 9's library-merge logic can SUPERSEDE the same video twice — the
`superseded_by` pointer in `videos.metadata` is updated each time.
The state stays SUPERSEDED. The transition table includes
SUPERSEDED in the source set for `(library, replaced)` to make this
explicit (see §4 footnote on `non-terminal-bad`).

Test: `test_supersede_idempotent`.

### 11.8 SQLite vs Postgres CHECK semantics

SQLite enforces CHECK at INSERT and UPDATE time, like Postgres. The
single behavioral diverge is that SQLite cannot use
`ADD CONSTRAINT … NOT VALID`; the SQLite migration variant just
rebuilds the table with the new CHECK. Tests cover both backends via
the `testdb` driver-switch fixture.

---

## 12. Acceptance checklist

Story 1.6 ships when **all** of the following are true. Each item maps
to a test in §10.

### Manifest & bindings
- [ ] `shared/states/states.json` exists and validates against the
  generator's JSON Schema.
- [ ] Go binding at `shared/states/states_gen.go` is regenerated and
  committed.
- [ ] Python binding at `pipeline/src/maktaba_pipeline/domain/states.py`
  and `domain/stages.py` are regenerated and committed.
- [ ] `set(State)` in Python equals exactly the 12 names in §3.1
  (`test_state_enum_matches_spec`).
- [ ] `set(Stage)` in Python equals exactly the 7 names in §3.2
  (`test_stage_enum_matches_spec`).
- [ ] `set(Trigger)` is the 10-element superset.

### Migration
- [ ] `0003_video_states_and_stages.sql` exists in `shared/db/migrations/`
  (Postgres + SQLite variants).
- [ ] `videos_state_valid` CHECK constraint is present after migration.
- [ ] `processing_jobs_stage_valid` CHECK constraint is present after
  migration.
- [ ] Legacy `'thumb'` rows are rewritten to `'thumbnail'` before the
  CHECK lands (`test_migration_0003_rewrites_thumb`).
- [ ] Migration is idempotent (`test_migration_0003_idempotent`).
- [ ] Down + up round-trips (`test_migration_0003_down_then_up`).

### Transition enforcement
- [ ] `advance_after_stage(video_id, trigger, outcome)` is the **only**
  function that issues `UPDATE videos SET state = …`.
- [ ] CI lint (Go `forbidigo` + Python `ruff`) fails on any
  unsanctioned write site.
- [ ] Every `(from, trigger, outcome)` triple in §4 is reachable; every
  unmapped triple raises `IllegalStateTransition`
  (`test_allowed_transitions_table_complete`).
- [ ] Every canonical edge round-trips through DB
  (`test_advance_each_canonical_edge` / `test_advance_each_broadcast_edge`).

### Story-named acceptance tests (§10)
- [ ] `test_check_constraints_reject_legacy` passes.
- [ ] `test_missing_to_discovered_round_trip` passes.
- [ ] `test_subtitle_gen_does_not_advance_to_indexed_alone` passes.
- [ ] `test_supersede_preserves_transcripts` passes.
- [ ] `test_corrupted_blocks_further_processing` passes.

### Edge cases
- [ ] `test_late_stage_finish_on_superseded` and `_on_failed` pass.
- [ ] `test_concurrent_advances_serialize` passes (row-lock works).
- [ ] `test_missing_revival_cancels_stale_probe` passes (stage cancel
  is in the same tx as the state flip).

### Documentation
- [ ] [story-01-06-video-state-machine.md](story-01-06-video-state-machine.md)
  cross-links to this plan.
- [ ] [architecture.md §3](../../architecture.md) state-machine snippet
  is updated to point to `states.json` as the source of truth.
- [ ] [architecture.md §7.1](../../architecture.md) stage comment is
  updated from `scan|probe|extract|transcribe|index|thumb` to the
  canonical 7 (resolves REVIEW §1.3.b/c here).

---

## 13. Open questions / deferred decisions

1. **Should `FAILED` have a recovery edge?** D5 says no; a future
   `maktaba video reset --id` operator command would re-create the
   row from `content_hash`. If Epic 24 (Disaster Recovery) wants
   in-place retry, add `(FAILED, integrity, retry_ok) → DISCOVERED`
   to the table; the manifest is the only thing that changes.
2. **Should the transition table also live in SQL?** D4 says no. If
   observability ever wants `EXPLAIN`-able transition queries, a
   `state_transitions(from, trigger, outcome, to)` reference table
   could be generated from the manifest in the same migration. The
   trigger that uses it is still the open question — a trigger that
   fires on `UPDATE videos.state` cannot read the *trigger* attribute
   without an explicit `SET LOCAL maktaba.trigger = …` per
   transaction, which adds friction to every caller.
3. **Per-stage outcome vocabulary.** Today the table has 9 outcomes
   (`ok, no_audio, partial, exhausted, rediscovered, deleted,
   replaced, fail, all_gates_ok` + `late` as a sentinel). If a future
   stage wants finer-grained outcomes (e.g. `ok_low_confidence` for
   `transcribe`), the manifest is the only place to add them. We
   defer that pressure until a concrete need appears.
