# Epic 01 — Scanner

**Phase.** Pipeline (M0 — Foundation, blocks all downstream pipeline epics).
**Owner.** Pipeline Service · `pipeline/src/maktaba_pipeline/library/`
(scanner binary; see also Go `cmd/maktaba-scan` if the Go reuse path lands).

> **Goal.** Detect every video file under a library's roots, assign it a
> stable identity (`content_hash`), and create a `videos` row in state
> `DISCOVERED`. Cope with renames, moves, copies, partial downloads,
> network filesystems that lie about events, and the user dragging in a
> 200 GB folder in one go.

Source: [README](../../../specs/epics/01-scanner/README.md) ·
Architecture §3.1 (Scanner) · §8.1 (`videos`, `libraries`).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 1.1 | Bootstrap a library and walk its roots | Core | [HLB-5](../linear-map.md) | [story-01-01](../../../specs/epics/01-scanner/story-01-01-file-discovery.md) | [plan-01-01](../../../specs/epics/01-scanner/plan-01-01-file-discovery.md) |
| 1.2 | Content-addressable identity (BLAKE3) | Core | [HLB-6](../linear-map.md) | [story-01-02](../../../specs/epics/01-scanner/story-01-02-content-identity.md) | [plan-01-02](../../../specs/epics/01-scanner/plan-01-02-content-identity.md) |
| 1.3 | Watch for live filesystem changes | Core | [HLB-7](../linear-map.md) | [story-01-03](../../../specs/epics/01-scanner/story-01-03-filesystem-watcher.md) | [plan-01-03](../../../specs/epics/01-scanner/plan-01-03-filesystem-watcher.md) |
| 1.4 | Manual control surface | Core | [HLB-8](../linear-map.md) | [story-01-04](../../../specs/epics/01-scanner/story-01-04-manual-control.md) | [plan-01-04](../../../specs/epics/01-scanner/plan-01-04-manual-control.md) |
| 1.5 | Schema & ownership decisions | Gate | [HLB-9](../linear-map.md) | [story-01-05](../../../specs/epics/01-scanner/story-01-05-schema-decisions.md) | [plan-01-05](../../../specs/epics/01-scanner/plan-01-05-schema-decisions.md) |
| 1.6 | Video state machine | Gate | [HLB-10](../linear-map.md) | [story-01-06](../../../specs/epics/01-scanner/story-01-06-video-state-machine.md) | [plan-01-06](../../../specs/epics/01-scanner/plan-01-06-video-state-machine.md) |

> Linear IDs are pulled from [linear-map.md](../linear-map.md) — the
> canonical Linear ↔ story map maintained at the wiki root. The
> "Priority" column reflects whether a story is foundational (Core), a
> schema gate that other epics depend on (Gate), or polish.

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 1.1 | [admin/library-config.html](../../../web/mockups/admin/library-config.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) · [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) |
| 1.2 | [admin/duplicates.html](../../../web/mockups/admin/duplicates.html) | [entity-relationship.drawio](../../../specs/diagrams/entity-relationship.drawio) |
| 1.3 | [admin/library-config.html](../../../web/mockups/admin/library-config.html) | [data-flow.drawio](../../../specs/diagrams/data-flow.drawio) |
| 1.4 | [admin/library-config.html](../../../web/mockups/admin/library-config.html) · [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) |
| 1.5 | — | [entity-relationship.drawio](../../../specs/diagrams/entity-relationship.drawio) |
| 1.6 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [pipeline-stories.drawio](../../../specs/diagrams/pipeline-stories.drawio) · [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |

---

## DB tables owned

| Table | Purpose | Defined in |
|-------|---------|------------|
| `libraries` | One row per user-defined library; holds roots, settings (`disabled`, `follow_symlinks`, language), and timestamps. | Plan 1.1, 1.5 |
| `videos` | One row per discovered file; carries `content_hash`, `path`, `filename`, `size_bytes`, `mtime`, `state` (FSM from Story 1.6). | Plan 1.1, 1.2, 1.5, 1.6 |
| `library_scan_state` | Per-library scan progress: sweep IDs, visited / inserted / updated / skipped / marked-missing counters, timestamps. | Plan 1.4 |

**Uniqueness contract.** `UNIQUE (library_id, content_hash)` (per-library,
not global) — same bytes ingested into multiple libraries duplicates
the row but de-duplicates downstream work via library scope.

---

## API endpoints owned

| Method · Path | Purpose | Story |
|---|---|---|
| `POST /api/libraries/{id}/scan` | Trigger a one-shot scan; idempotent — returns `{status:"already_running", progress}` if a scan is in flight. | 1.4 |
| `DELETE /api/libraries/{id}/scan` | Cancel a running scan; stops within ~5 s at the next file boundary, rolls back uncommitted batches. | 1.4 |

**CLIs:**
- `maktaba-scan --library NAME` (daemon or one-shot)
- `maktaba-scan --library NAME --dry-run` (write nothing)
- `maktaba-scan --purge-missing` (hard-delete `MISSING` videos older than 7 days)

---

## gRPC services owned

None. The scanner is read/write against Postgres directly and reaches out
through `LISTEN/NOTIFY` for fan-out; the API service consumes those events
without an RPC.

---

## LISTEN/NOTIFY channels owned

| Channel | Producer | Consumer | Story |
|---------|----------|----------|-------|
| `videos.new` | scanner (`AFTER INSERT` trigger) | API → WebSocket `/ws/library/{id}` | 1.1 |
| `videos.state_changed` | reprocess + FSM transitions | API | 1.6 |

The `jobs.*` notify family belongs to Epic 6.

---

## Dependencies

**Depends on.**
- **Epic 6** (Job Queue) Stories 6.1–6.3 — `processing_jobs` schema +
  enqueue + heartbeat. The scanner enqueues `(stage='probe',
  state='pending')` rows for every newly inserted video.

**Depended on by.** All other pipeline epics. Specifically:
- Epic 02 (Audio Extraction) consumes `videos.new` and the `DISCOVERED`
  state.
- Story 1.6's FSM is the contract referenced by Epics 2, 3, 4, 5, and 9 —
  every state transition out of `DISCOVERED` is owned downstream, but the
  schema and enum live here.
- Epic 04 Story 4.3 (external subtitle auto-discovery) runs as part of
  Story 1.1's scan walk.

---

## Key technical decisions

- **BLAKE3 head+tail+size hashing.** `min(4 MiB, size)` from head +
  `min(4 MiB, size)` from tail + the size as little-endian `uint64`,
  hashed with BLAKE3. Bounded I/O (≤ 8 MiB per file regardless of file
  size); appending size guarantees the hash changes on append-in-place;
  deterministic across renames/moves.
- **Per-library uniqueness, not global.** Same content can land in two
  libraries and process twice; this simplifies "delete library" semantics
  and avoids cross-library joins on the search path.
- **Soft delete via `MISSING`.** Disappearing files transition to
  `MISSING`; derived data (transcripts, indexes) is retained until
  `--purge-missing` runs (default threshold 7 days).
- **Two fast paths during incremental scans.** Path-keyed lookup with
  `(size, mtime)` stability check skips file open. Content-hash upsert
  treats moves/renames as `path` updates without reprocessing.
- **Debounced live watcher.** `watchdog` (Python) or `fsnotify` (Go)
  events are debounced and require `(size, mtime)` stability before
  ingest, eliminating partial-write races on large copies.
- **Canonical FSM (Story 1.6).** 12 states, 7 stages, all transitions
  enumerated. `subtitle_gen` cannot precede `transcribe`; rediscovery
  (`MISSING → DISCOVERED`) cancels stale probe jobs; late-stage finishes
  on `FAILED` / `SUPERSEDED` are no-ops.

---

## Libraries / dependencies introduced

**Go path (preferred per Plan 1.4 — same binary serves CLI + HTTP):**
`go-chi/chi v5`, `google/uuid`, `jackc/pgx/v5`, `spf13/viper`,
`zeebo/blake3`. Test path: `stretchr/testify`,
`testcontainers/testcontainers-go` (Postgres). Build: `sqlc-dev/sqlc`,
`pressly/goose`.

**Python alternative (if watcher remains in Pipeline):** `watchdog`,
`blake3` (PyPI). Both implementations read the same constants from
`shared/db/queries/identity.sql`.

---

## Test coverage summary

- **Unit (hash):** determinism, head+tail overlap on small files, zero-byte
  files, I/O budget assertions on a synthetic 30 GB file.
- **Integration (testcontainers Postgres):** scan inserts row per video,
  ignores non-video extensions, enqueues exactly one `probe` job per
  insert, honours `library.disabled`, `videos.new` notify count equals
  insert count.
- **Watcher:** debounce window, partial-write storms, rename = path
  update (no reprocess), 10k-event burst without OOM.
- **Manual control:** idempotent concurrent invocation, cancellation
  cleans up cleanly (no orphaned jobs), CLI `--dry-run` writes nothing.
- **State machine:** every legal transition, every illegal transition
  rejected; rediscovery cancels stale probe; `FAILED`/`SUPERSEDED`
  late-finish is a no-op.
- **Property tests (optional, `rapid`):** event-stream consistency over
  randomised create/rename/delete sequences.

Approximate scope: ≥ 40 test cases across the 6 stories.
