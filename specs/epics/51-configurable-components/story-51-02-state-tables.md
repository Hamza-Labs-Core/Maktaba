# Story 51.02 — Desired and actual state in the database

> Epic 51 · Phase 1 · Depends on: 51.01 · Needed by: 51.03, 51.04, 51.05, 51.06 (and 51.08, 51.12)

## Why

The catalog (51.01) says what parts exist. Nothing yet says which of them should run on this
home, and nothing records what each machine is really doing. README §4.3 and H1/H13 choose
Postgres for both, kept apart: admins write **desired** state, each machine writes only its
own **actual** state. This story adds the tables, the change signal, and one small store in
Go and in Python that every runner (51.04–51.06) uses, so none of them writes SQL of its own.

## Description

### 1. Migration `0175_components` (Postgres and SQLite twins)

Next free slot after `0172`. Additive only.

```sql
-- what should be true (one row per component per home); the API writes it, and the
-- supervisor once when it seeds (51.03)
CREATE TABLE IF NOT EXISTS components (
    id          text PRIMARY KEY,                       -- catalog id
    want        text NOT NULL CHECK (want IN ('absent','installed','enabled')),
    hold_by     text,                                   -- user name, or 'seed'
    hold_at     timestamptz,                            -- set: stopped until an admin starts it
    generation  bigint NOT NULL DEFAULT 1,
    updated_by  text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((hold_at IS NULL) = (hold_by IS NULL))
);

-- per-machine exceptions (51.12 gives them a UI); NULL want = follow the home
CREATE TABLE IF NOT EXISTS component_overrides (
    node_id     uuid NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    id          text NOT NULL,
    want        text CHECK (want IN ('absent','installed','enabled')),
    hold_by     text, hold_at timestamptz,
    generation  bigint NOT NULL DEFAULT 1,
    updated_by  text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, id)
);

-- what is true (one row per machine, component and part); only that part writes it
CREATE TABLE IF NOT EXISTS component_status (
    machine     text NOT NULL,            -- 'home', or the node's cluster_nodes.id
    id          text NOT NULL,
    part        text NOT NULL,            -- 'supervisor' | 'api' | 'streaming' | 'pipeline' | 'pipeline/stt-1' …
    phase       text NOT NULL,            -- one of the catalog's phases
    conditions  jsonb NOT NULL DEFAULT '[]'::jsonb,
    observed_generation bigint NOT NULL DEFAULT 0,
    version     text,                     -- release that reported it
    detail      jsonb NOT NULL DEFAULT '{}'::jsonb,  -- restarts, last_error_code, progress, rss_mb
    since       timestamptz NOT NULL DEFAULT now(),  -- when phase last changed
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (machine, id, part)
);

-- one row: the change counter every runner polls, the bundle, the seed marker
CREATE TABLE IF NOT EXISTS component_meta (
    singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision       bigint NOT NULL DEFAULT 0,
    bundle         text,                  -- 'full' | 'core' | 'worker' | 'custom'
    seeded_at      timestamptz,
    seeded_release text,
    toml_imported  jsonb NOT NULL DEFAULT '{}'::jsonb   -- key -> value seen at the import (51.03)
);
INSERT INTO component_meta (singleton) VALUES (true) ON CONFLICT DO NOTHING;
```

`conditions` holds a list of objects, one per condition type of README §4.3:

```json
[{"type":"RequirementsMet","status":"false","reason":"dependency_off","params":{"component":"llm"},
  "changed_at":"2026-11-03T10:00:00Z","generation":7}]
```

`reason` must be one of the catalog's `reasons`; `status` is `true`, `false` or `unknown`.

### 2. The change signal

A trigger on `components` and `component_overrides` (insert, update, delete), in the same
statement:

1. bumps the row's `generation` (on update) and `component_meta.revision`;
2. `pg_notify('components_changed', json_build_object('revision', r, 'id', id, 'node', node_id)::text)`.

The trigger is the only writer of `revision`. Runners `LISTEN components_changed` and also
poll `SELECT revision FROM component_meta` every 30 s; they reload desired rows only when the
revision moved. That is the N12 budget: one cheap query per runner per 30 s when nothing
changes. SQLite has no `NOTIFY`; the twin keeps the trigger that bumps `revision` and the
runners rely on the 30 s poll (single-process SQLite rigs only, architecture §8.0).

### 3. Who may write what

- `components`, `component_overrides`, `component_meta.bundle`: the API's role (51.08) and
  the supervisor's seed (51.03). Every change also writes `audit_log` (category
  `components`, action `component.<verb>`, payload before/after). The store's write
  functions take the audit row as an argument, so nobody can write one without the other.
- `component_status` on the home: the home's own processes, through the store.
- `component_status` from a node: only through
  `component_status_report(p_node uuid, p_rows jsonb)`, a `SECURITY DEFINER` function that
  calls the existing `cluster_node_guard(p_node)` and writes rows with `machine = p_node::text`
  only. `maktaba_worker` keeps `SELECT` on the table, and the migration revokes its
  `INSERT, UPDATE, DELETE` (0103 granted them on all tables). Same pattern as
  `cluster_node_stage_switches` in 0103.
- `component_desired_for(p_node uuid)` (same guard) returns the home rows with that node's
  overrides applied, so a node reads one answer and never computes the merge itself.

### 4. The stores

**Go:** package `github.com/Hamza-Labs-Core/Maktaba/shared/components/store`, on
`database/sql` (the API and supervisor use `lib/pq`, streaming uses `pgx/stdlib`).

```go
type Desired struct { ID, Want string; Hold *Hold; Generation int64 }
type Hold struct { By string; At time.Time } // no expiry: a hold lasts until Start (decided 2026-11-03)
type Status struct { Machine, ID, Part, Phase string; Conditions []Condition;
                     ObservedGeneration int64; Version string; Detail map[string]any }

func (s *Store) Revision(ctx) (int64, error)
func (s *Store) Desired(ctx, machine string) (map[string]Desired, int64, error) // merged, + revision
func (s *Store) SetDesired(ctx, d Desired, audit Audit) error                    // 51.08 uses it
func (s *Store) ReportStatus(ctx, machine, part string, rows []Status) error      // upsert; prunes this part's rows not in rows
func (s *Store) StatusOf(ctx, id string) ([]Status, error)
func Rollup(rows []Status) Status  // one phase per machine and component: worst wins
```

`Rollup`'s order, worst first: failed, waiting, draining, starting, degraded, stopped,
running, off, not_installed. A component Running in the API and Failed in the pipeline is
Failed, with the pipeline's reason. The listener is a small interface with two adapters,
`store/pqlisten` (lib/pq `pq.Listener`) and `store/pgxlisten` (pgx `WaitForNotification`),
both reconnecting with 1–30 s backoff and sending a synthetic "reload" after every
reconnect, so a lost message never leaves a runner stale.

**Python:** `maktaba_pipeline.components.store` with the same functions (`revision`,
`desired`, `report_status`, `rollup`) over the pipeline's `db` abstraction (asyncpg and
aiosqlite), and `listen()` built on `db/pubsub.py`. On a node it calls the two
`SECURITY DEFINER` functions instead of the tables.

### 5. Schema revision

The slot raises `SCHEMA_REV` to 173 in `pipeline/src/maktaba_pipeline/cluster/schema.py`
and `SchemaRev` in `streaming/internal/cluster/schema.go` (migrations README rule 8). The
migration is additive: no `maktaba_min_node_schema()` change, not breaking for the previous
release.

### 6. The catch-up stage's row

The migration also inserts the `job_stages` row for `component_catchup` (51.06: subject
`catalog`, pool `cpu`, placement `home`, enabled), so SQL that reads `job_stages` before a
worker's graph sync knows the stage (the reason 0170 exists). 51.06 adds the `StageDef`; the
sync then owns the row as usual. One slot for the whole of Phase 1, so 51.03–51.06 can be
built in parallel without racing for slots.

## Acceptance criteria

- **Given** a database at 0172,
  **when** 0175 runs,
  **then** the four tables, both functions, the trigger and the `component_catchup` stage row
  exist, `component_meta` has its one row, and running 0175 again changes nothing.
- **Given** a runner listening,
  **when** a `components` row is updated,
  **then** it receives `components_changed` with the new revision within 1 s, and the row's
  `generation` went up by one.
- **Given** a node's `maktaba_worker` login,
  **when** it inserts into `component_status` directly,
  **then** Postgres refuses (permission denied); **when** it calls
  `component_status_report` for its own node id, **then** the rows land with
  `machine = <its id>`; **when** it passes another node's id, **then** the guard refuses.
- **Given** a home row `transcription = enabled` and an override `want = installed` for node N,
  **when** N calls `component_desired_for(N)`,
  **then** it gets `installed` for `transcription` and the home rows for everything else.
- **Given** a store write of desired state,
  **when** it commits,
  **then** exactly one `audit_log` row with before and after commits with it.
- **Given** no change for 5 minutes,
  **when** a runner idles,
  **then** it ran at most 10 `revision` queries and no other query (N12).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG) | scratch DB at 0172 | migrate twice | tables exist; second run no-op |
| T02 | integration (SQLite) | twin | migrate; update a row | `revision` bumped |
| T03 | integration (PG) | listener | update `components` | notify payload has revision, id |
| T04 | integration (PG) | `maktaba_worker` login | direct insert into `component_status` | permission denied |
| T05 | integration (PG) | same | `component_status_report` own id / other id | written / refused |
| T06 | integration (PG) | home + override | `component_desired_for` | merged answer |
| T07 | unit (Go) | statuses Running(api) + Failed(pipeline) | `Rollup` | Failed with the pipeline reason |
| T08 | unit (Go) | counting fake DB | idle 5 min (fake clock) | ≤ 10 queries |
| T09 | unit (Go) | listener drops connection | reconnect | synthetic reload sent |
| T10 | unit (Py) | aiosqlite | `report_status` twice with fewer rows | missing rows of that part pruned |
| T11 | unit (Go/Py) | condition with reason `nonsense` | `ReportStatus` | refused: unknown reason |
| T12 | unit | migration lint | `make lint-migrations` | passes (idempotent, twin present) |
| T13 | integration (PG) | `SetDesired` with audit | commit | one `audit_log` row, same transaction |

## Edge cases

- **A component id not in this release's catalog** (written by a newer release before a
  rollback): stores keep the row, runners ignore it and log `component_unknown` once.
- **A node deleted**: its overrides go with it (`ON DELETE CASCADE`); its status rows are
  pruned by the home's housekeeping when the node row is gone (51.14 shows nothing for it).
- **Clock skew on nodes**: `since` and `updated_at` are set by the database (`now()`), not
  by the node.
- **Holds never expire.** There is no `hold_until` column: a hold lasts until Start
  (README §13, decision 2). A hold is a row value, so it survives restarts and
  updates, and only Start (51.08) clears it.
- **Big `detail`**: capped at 4 KB by the store; larger is truncated with `"truncated":true`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/db/migrations/0175_components.sql` (new) | §1–§3, §6. |
| `shared/db/migrations/0175_components.sqlite.sql` (new) | SQLite twin (no NOTIFY, no `SECURITY DEFINER`; triggers for revision). |
| `shared/db/migrations/MANIFEST.md` | Row for `0175`. Additive. |
| `pipeline/src/maktaba_pipeline/cluster/schema.py` | `SCHEMA_REV = 173`. |
| `streaming/internal/cluster/schema.go` | `SchemaRev = 173`. |
| `shared/components/store/store.go` (new) | Go store (§4). |
| `shared/components/store/rollup.go` (new) | `Rollup`. |
| `shared/components/store/pqlisten/listen.go` (new) | lib/pq listener adapter. |
| `shared/components/store/pgxlisten/listen.go` (new) | pgx listener adapter. |
| `shared/components/store/store_test.go`, `store_pg_test.go` (new) | T03–T09, T11, T13 (PG tests use the scratch Postgres of `tools/ci/integration-go.sh`). |
| `pipeline/src/maktaba_pipeline/components/store.py` (new) | Python store. |
| `pipeline/tests/components/test_store.py` (new) | T10, T11. |
| `pipeline/tests/db/test_components_migration.py` (new) | T01, T02, T04–T06. |
| `docs/wiki/db/` (generated) | Regenerated table pages. |

### Tests

- Unit: T07–T11 in Go and Python.
- Integration: T01–T06, T13 in the existing `integration` job (Go and pipeline Postgres
  tests); T12 in the existing `lint-migrations` step.
- The **update rehearsal** runs on this PR (it touches `shared/db/migrations/**`); 0175 is
  additive so scenarios 01–11 must pass unchanged.

### Rollout

Ships dark: tables are empty except `component_meta` and the stage row; nothing reads them
until 51.03–51.06 land.

### Rollback

A release rollback leaves the tables in place, unused (README §12). The migration is additive,
so the updater keeps the schema on rollback. The `component_catchup` stage row is harmless to
an older pipeline (its graph sync does not list it; no job is ever enqueued for it).

### Dependencies

51.01 (ids, phases and reason codes the store validates against).
