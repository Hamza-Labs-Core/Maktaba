# Story 53.09 — Log index, search and live tail

> Epic 53 · Phase 2 · Depends on: 53.02 · Needed by: 53.10, 53.11, 53.12, 53.13

## Why

Segments (53.02) keep everything but can only be read by time. Admins need to type a word, pick a
component and a level, follow one request, and watch lines arrive. The viewer today polls three
10,000-line rings.

## Description

### 1. The `logs` role

A new child role, `maktaba-server logs` (same binary, its own process, README D3), supervised like
the others (backoff, cut-off, `/healthz`, `/readyz`). It listens on loopback only
(`127.0.0.1:<logs port>`, added to the port registry `shared/go/ports/registry.go`, checked at start since PR #440).

### 2. The index

`<data>/logs/index/logs.db`, SQLite (WAL mode) via `modernc.org/sqlite` (README D2):

```sql
CREATE TABLE lines (
  id INTEGER PRIMARY KEY,           -- rowid
  ts INTEGER NOT NULL,              -- unix ms
  level INTEGER NOT NULL,           -- 10 debug … 50 fatal
  service TEXT NOT NULL, node TEXT NOT NULL,
  msg TEXT NOT NULL,
  request_id TEXT, job_id TEXT, session_id TEXT, video_id TEXT, user_h TEXT,
  error_fp TEXT,                    -- 53.10
  seg TEXT NOT NULL, off INTEGER NOT NULL  -- where the full line is in its segment
);
CREATE INDEX lines_ts ON lines(ts);
CREATE INDEX lines_svc_lvl_ts ON lines(service, level, ts);
CREATE INDEX lines_req ON lines(request_id) WHERE request_id IS NOT NULL;
CREATE INDEX lines_job ON lines(job_id) WHERE job_id IS NOT NULL;
CREATE VIRTUAL TABLE lines_fts USING fts5(msg, text, err, content='', tokenize='unicode61 remove_diacritics 2');
```

- The index stores ids and where the line is, **not the whole line** (the segment has it), so it
  stays small; `text` and `err` are the searchable parts (the raw text, `err.msg`, and the `attrs`
  values flattened). Arabic text is tokenized by `unicode61`, with diacritics removed so a search
  for «مكتبة» finds «مَكْتَبَة».
- **Ingest:** the role tails every segment (inotify/kqueue + the `.idx` files), batches 1,000
  lines or 200 ms per transaction. It keeps its read position per segment in the database, so a
  restart continues where it stopped; a corrupt `logs.db` is moved aside and rebuilt from the
  segments still on disk.
- **Retention:** rows older than the segments' retention, and rows whose segment was deleted, are
  removed hourly in 10,000-row batches; `VACUUM` never runs in the foreground (incremental
  vacuum).
- **Cap:** 1.5 GB (README §4.2); over it, the oldest `debug` then `info` rows go first.

### 3. Search API (loopback, the API proxies it)

`GET /search?q=&service=&level=&node=&since=&until=&request_id=&job_id=&user_h=&cursor=&limit=`

- `q` is FTS5 syntax limited to words, quoted phrases, `-word` and `prefix*` (no column filters
  from the user); empty `q` means filters only.
- Answers newest first, ≤ 500 lines per page with a cursor, each line read from its segment; a
  query is cut at **3 s** and returns what it has with `"partial": true`.
- `GET /tail?…same filters…` is a **Server-Sent Events** stream: new matching lines as they are
  indexed (≤ 2 s after they are written), heartbeat every 15 s, at most 10 open tails.
- `GET /around?id=&before=&after=` gives the lines of all components around one line (the
  "what else happened then" view).

The API exposes them to admins only: `/api/admin/logs/search`, `/api/admin/logs/tail`,
`/api/admin/logs/around` (role test: nobody and viewer get 401/403). The existing `/stream`,
`/files` and `/export` stay; `/export` gains `request_id`.

### 4. A joined machine's logs

Each full machine runs its own `logs` role; the home's API asks a machine's role over the
cluster channel (as `/api/admin/logs/files?node=` does today, `503 node_offline` when it can't).
Search across machines is a fan-out with a 3 s budget per machine; the answer says which
machines didn't answer.

## Acceptance criteria

- **Given** 14 days of lines (30 M) on the reference machine,
  **when** an admin searches a word with no filters,
  **then** the first page comes back in ≤ 1.5 s p95.
- **Given** a request id,
  **when** the admin searches it,
  **then** every line of every component with that id comes back in ≤ 300 ms p95, in time order.
- **Given** a live tail on `level>=warn`,
  **when** the API logs a warning,
  **then** it appears in the tail in ≤ 2 s.
- **Given** the `logs` role is killed mid-ingest,
  **when** it restarts,
  **then** no line is indexed twice or skipped.
- **Given** a corrupt `logs.db`,
  **when** the role starts,
  **then** it moves the file aside, rebuilds from the segments, and logs `log_index_rebuilt`.
- **Given** a viewer's token,
  **when** it calls `/api/admin/logs/search`,
  **then** the answer is 403.
- **Given** an Arabic search with or without diacritics,
  **then** both find the same lines.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | segments fixture | ingest, restart mid-way | exactly-once by position |
| T02 | unit | FTS queries incl. Arabic, prefix, phrase, `-` | search | right lines; user column filters rejected |
| T03 | unit | 3 s budget with a slow fake | search | `partial: true` |
| T04 | unit | retention, deleted segments | sweep | rows gone in batches |
| T05 | role test (API) | nobody, viewer, admin | the three routes | 401, 403, 200 |
| T06 | integration | corrupt db file | start | rebuilt |
| T07 | bench (53.13) | 30 M lines | the latency targets | within budget |

## Edge cases

- **A line too big for the index row** (64 KB): only the first 8 KB of `text` is indexed; the full
  line is in the segment.
- **Time zones:** stored UTC; the UI shows the admin's local time and the home's.
- **A segment rewritten by hand:** offsets no longer match; the reader checks each line's `ts`
  and reports "line moved" instead of showing a wrong line.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/logindex/` (new): `schema.sql`, `ingest.go`, `search.go`, `tail.go`, `retention.go`, `server.go` | §1–§3. |
| `cmd/maktaba-server/cmd_logs.go` (new) | The `logs` subcommand. |
| `cmd/maktaba-server/internal/supervisor/` | Add the role. |
| `shared/go/ports/registry.go` | The logs port. |
| `api/internal/handlers/logs/` | Proxy routes, roles, `request_id` on export. |
| `api/internal/cluster/` | Fan-out to machines. |

### Tests

T01–T06 in unit and integration jobs; T07 in 53.13's benchmark job. **CI only:** fault scenario
F08.

### Rollout

The role starts in a release with the old viewer unchanged; 53.11 moves the UI to it.

### Rollback

Remove the role from the supervisor's list; segments are unaffected.

### Dependencies

53.02. Needed by 53.10, 53.11, 53.12, 53.13.
