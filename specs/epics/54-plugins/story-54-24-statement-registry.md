# Story 54.24 — Named statements and per-plugin database roles

> Epic 54 · Phase 4 · Depends on: 54.04, 54.22 · Needed by: 54.25

## Why

Today's stage code holds about 780 SQL statements and runs them with the worker's database
login (README §15.1). A first-party plugin must reach the database only through the plugin
API, which checks every call (F6). Rewriting each statement as a typed Go endpoint would take
years and split every change across two languages (H17). Named statements keep the SQL where
it is reviewed today and let the API check token, statement, scope and table on every call.

## Description

### 1. Statements in the package

Each first-party plugin has `queries/<name>.sql`, one statement per file, with named
parameters and a header:

```sql
-- name: transcribe.upsert_words
-- mode: write                 -- read | write
-- scope: subject              -- subject | library | catalog
-- tables: transcript_words    -- must be within db.read / db.write
INSERT INTO transcript_words (video_id, transcript_id, idx, start_ms, end_ms, text)
SELECT :subject_id, :transcript_id, w.idx, w.start_ms, w.end_ms, w.text
FROM unnest(:idx::int[], :start_ms::int[], :end_ms::int[], :text::text[]) AS w(idx, start_ms, end_ms, text)
ON CONFLICT (transcript_id, idx) DO UPDATE SET text = EXCLUDED.text;
```

- `:subject_id`, `:library_id` and `:libraries` are **reserved**: the API binds them from the
  token, and a call that passes them is refused (`plugin_scope_param`).
- `scope: subject` statements must use `:subject_id`; `scope: library` must use `:library_id`
  or `:libraries`; `scope: catalog` is allowed only for `subject = catalog` stages. A build-time
  check parses each statement (`pg_query`) and enforces this and the `tables` header.
- The build (54.27) compiles every plugin's statements into `statements.json` inside its
  package (name, text, mode, scope, tables, parameter types), so the signed `files.json` covers
  them.

### 2. The registry in the API

`api/internal/plugins/statements/` loads `statements.json` of each trusted package (54.22) at
start and on component change, and prepares them per connection on first use. A call
`stmt(name, args)` or `batch([{name, args}…])`:

1. checks the invocation token (54.04): valid, not expired, its plugin is the statement's owner,
   the job's grant generation current;
2. finds the statement in **that plugin's** registry (`403 plugin_capability` otherwise);
3. binds reserved parameters from the token's subject and libraries, and the rest from `args`
   with their declared types;
4. in one transaction: `SET LOCAL ROLE maktaba_fp_<plugin>`,
   `SET LOCAL maktaba.origin = 'plugin:<id>@<version>'`, the statement(s), commit;
5. answers rows as MessagePack (streamed for reads over 1 MB), counts
   `maktaba_plugin_statement_calls_total{plugin,statement}` and its duration histogram.

`batch` is how a handler gets one transaction where today's code opens one; advisory locks
used today become statements of their own inside the batch (`pg_advisory_xact_lock`). A read
outside a batch runs in its own read-only transaction.

### 3. Roles

A home migration creates, for each first-party plugin, `maktaba_fp_<plugin>` `NOLOGIN` with
`SELECT` on its `db.read` tables and `SELECT, INSERT, UPDATE, DELETE` on its `db.write`
tables (and their sequences), nothing else. The API's role and every machine's role
(Epic 31's per-machine roles) are granted membership with `NOINHERIT` so only `SET ROLE`
gives the rights. The migration is generated from the manifests by `tools/plugins/roles.py`
and checked in CI against them (a manifest change without its migration fails).

### 4. Fingerprints use the same registry

The `fp` runner's token allows only `mode: read` statements of the plugin it is computing for.

### 5. Every machine

The plugin API socket is served by each machine's own Go API (Epic 35 full nodes) with that
machine's role, so a `placement:any` stage on a node reaches the home's database exactly as
the node's worker does today, but through the checks above.

## Acceptance criteria

- **Given** plugin A with statement `a.read_video`,
  **when** plugin B's token calls `a.read_video`,
  **then** `403 plugin_capability` and nothing runs.
- **Given** a call to `transcribe.upsert_words` that passes `subject_id`,
  **when** it reaches the API,
  **then** `400 plugin_scope_param`.
- **Given** a statement whose text writes a table outside its plugin's `db.write` (forced past
  the build check in a test),
  **when** it runs,
  **then** Postgres answers a permission error, the API maps it to `403 plugin_capability`, and
  nothing is written.
- **Given** a `batch` of three writes where the third fails,
  **when** it runs,
  **then** none of the three is committed.
- **Given** a one-row read,
  **when** measured over 10,000 calls,
  **then** p95 ≤ 2 ms (N18).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (py, build) | statements with bad headers, missing reserved params, tables outside lists | build | refused with the line |
| T02 | integration (PG, go) | registry with two plugins | cross-plugin call | 403 |
| T03 | integration (PG, go) | reserved param passed | call | 400 |
| T04 | integration (PG, go) | statement touching a forbidden table | call | permission error → 403; no write |
| T05 | integration (PG, go) | batch with a failing statement | call | rollback |
| T06 | integration (PG, go) | `maktaba.origin` | write then read the audit trigger's column | `plugin:<id>@<version>` |
| T07 | unit (py) | `roles.py` vs manifests | generate | migration matches; drift fails |
| T08 | bench (go) | one-row read | 10,000 calls | N18 |
| T09 | integration (PG, go) | node API with a per-machine role | call | works through `SET ROLE`; without membership refused |

## Edge cases

- **A long read** (detect reads a whole library): streamed rows, no 64 KB value limit for
  trusted reads (the §4.7 limits are for third-party plugins); the 1 GB API response cap stays.
- **Prepared statement invalidated by a migration**: re-prepared on the error, once.
- **SQLite** (`.sqlite.sql` migrations used by tests and small installs): statements carry an
  optional `-- sqlite:` variant; the build fails if a plugin that runs on SQLite installs lacks
  one where the Postgres text uses Postgres-only syntax. SQLite has no roles, so there the
  registry and the build-time table check are the layers; CI runs T04 on Postgres only.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/plugins/statements/` (new) | §2 registry, binding, transactions, metrics. |
| `api/internal/plugins/pluginapi/routes_trusted.go` (new) | `stmt`, `batch`, `settings`, `media.path`, service door calls. |
| `tools/plugins/statements_build.py`, `roles.py` (new) | §1 build check, §3 role migration. |
| `shared/db/migrations/NNNN_fp_roles.sql` + `.sqlite.sql` | Roles and grants (regenerated per wave). |
| `plugins/sdk/python/maktaba_plugin_sdk/native/stmt.py` | Client side. |

### Tests

- Unit T01, T07; integration T02–T06, T09; bench T08. In `plugins-api` and `plugins-trusted`.

### Rollout

Dark until a wave flips a stage; the registry is empty with no first-party package.

### Rollback

Release rollback; roles are `NOLOGIN` and harmless when unused.

### Dependencies

54.04 (tokens), 54.22 (trusted packages).
