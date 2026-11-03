# Story 54.04 — The plugin API: tokens, capabilities and scope

> Epic 54 · Phase 1 · Depends on: 54.01 · Needed by: 54.05, 54.06, 54.08, 54.12, 54.21

## Why

The sandbox stops a plugin from reaching anything by itself. The plugin API is the one door it
has, so every rule about what a plugin may read and write lives here, in the Go API, enforced
on every call, the same way the API already enforces every user role (standing rule,
2026-10-25). The host and the pipeline are not trusted to enforce anything: a token minted
from the API's own grant table is the only thing that opens a call.

## Description

### 1. Two listeners, two audiences

| Listener | Who | Routes |
|---|---|---|
| The normal API (TCP, `:8088` via the supervisor) | machines (the pipeline worker's cluster identity) | `POST /internal/plugins/invocations` (mint), `DELETE /internal/plugins/invocations/{id}` (end) |
| `<data>/run/plugin-api.sock` (unix socket only, mode 0660, group shared with the host) | `maktaba-plugind` | `/plugin-api/v1/*` |

The plugin API is **never** served on TCP. A request reaching it carries `Authorization:
Plugin <token>`; anything else is 401. The admin routes (`/api/plugins/*`) are 54.08's.

### 2. Invocation tokens

- **Minted** by `POST /internal/plugins/invocations {job_id}` from a machine principal. The API
  loads the job (stage, subject, attempt) from `jobs`, finds the plugin from the stage's
  `extension = 'plugin:<id>'`, reads the plugin's **current grants and library scope from
  `plugin_grants`** (54.08), checks the plugin is enabled and not revoked (54.17), and returns:
  `{invocation_id, token, grants, libraries, exp}`. The caller cannot pass grants.
- **Format:** an opaque 256-bit random string; the API keeps `plugin_invocations(id, token_hash,
  plugin_id, version, job_id, subject_kind, subject_id, libraries, grant_generation, exp,
  calls, bytes_written, ended_at)` in memory with a Postgres copy for multi-replica APIs (one
  row per job; pruned after 7 days).
- **Lifetime:** `exp = now + timeout_s + 30 s`. Ended early by `DELETE` when the job finishes or
  is cancelled. A token whose `grant_generation` is older than the plugin's current one answers
  `401 plugin_grants_changed` (a revoked grant applies on the next call; README §4.5).
- **The guest never sees it**: the host keeps it in the store's host state (54.02 §3).

### 3. Routes

One route per WIT function, `POST /plugin-api/v1/<interface>/<function>`, JSON bodies
generated from the WIT (54.12 generates the Go types and the mock). Phase 1 set:

| Route | Capability | Scope check |
|---|---|---|
| `job/settings`, `job/grants`, `job/progress`, `job/checkpoint` | — | token's job |
| `log/write` | — | token's plugin; rate limit |
| `data/{get,put,delete,list}` | — | `plugin_data` rows with this plugin id only; quota |
| `library/{video,series,list}` | `library.read` | item's library ∈ token libraries |
| `transcript/{segments,words}` | `transcript.read` | same |
| `write/{set-field,add-tags,set-chapters,add-subtitle,set-artwork}` | per function | subject = token's subject, or (library/catalog jobs) item in token libraries; field allow-list |
| `suggest/add` | `suggest` | same |
| `queue/request` | `jobs.enqueue` | target stage belongs to this plugin and is in the calling stage's `may_request`; ≤ 1,000 per job |
| `notify/admin` | `notify.admin` | ≤ 10 per plugin per day; code must exist in the plugin's strings |
| `exports/read` | `plugin-data.read:<id>` | the other plugin marked the key exported |
| `media/*` | 54.05 | 54.05 |

### 4. What is written where

- **`plugin_data`** (new table): `(plugin_id, subject_kind, subject_id, key, value JSONB,
  exported bool, updated_at)`, PK on the first four; value ≤ 64 KB; per-plugin byte total ≤
  `resources.data_mb`.
- **Core writes** go through the existing write paths the admin API uses (so triggers, search
  reindex and `NOTIFY` behave the same), plus one row in **`plugin_writes`**: `(id, plugin_id,
  version, job_id, target, subject_id, field, before JSONB, after JSONB, at)`. That row is what
  "what it wrote", per-plugin undo and purge use (54.07, 54.13), and what exporters filter on
  (README §9 T2).
- **Field allow-list** for `set-field` (Phase 1): `description`, `sort_title`, `year`,
  `content_rating`, `original_language`, `local_ids.<plugin short id>`. Never `title` when an
  admin set it (`title_source = 'admin'`); never `videos.state`; never paths, libraries, users,
  ACLs.
- **Subtitles** are parsed (WebVTT or SRT) and re-serialised by Maktaba; ≤ 4 MB; language a
  BCP 47 code; stored as a track with `source = 'plugin:<id>'` and written next to the video
  only through the existing subtitle writer, under a name Maktaba chooses.

### 5. Errors and counting

Refusals answer `403` with `{code}`: `plugin_capability`, `plugin_scope`, `plugin_field`,
`plugin_quota`, `plugin_rate`; `413 plugin_too_large`; `401 plugin_token`,
`plugin_grants_changed`. Each refusal is logged with the plugin, job, route and code, and
increments `maktaba_plugin_refusals_total{plugin,code}`; 54.09's circuit breaker reads it.

### 6. Performance

The token check is an in-memory map lookup (sha256 of the token) plus a generation compare.
Grants are cached per plugin and invalidated by `NOTIFY plugin_grants_changed`. Target N4: p95
≤ 2 ms for `library/video` on the unix socket.

## Acceptance criteria

- **Given** a plugin granted `library.read` on library A only,
  **when** it asks for a video in library B,
  **then** it gets `403 plugin_scope` and the refusal is logged and counted.
- **Given** a plugin without `tags.write`,
  **when** the host (or a forged request with its valid token) calls `write/add-tags`,
  **then** `403 plugin_capability`.
- **Given** a running job,
  **when** the admin revokes `transcript.read`,
  **then** the next `transcript/segments` call answers `401 plugin_grants_changed`.
- **Given** a pipeline worker asking to mint a token with extra grants in its body,
  **when** the API answers,
  **then** the extra fields are ignored and the grants equal `plugin_grants`.
- **Given** an admin-set title,
  **when** a plugin calls `set-field(title, …)`,
  **then** `403 plugin_field`; with `suggest`, a suggestion appears in the review queue instead.
- **Given** a request to `/plugin-api/v1/…` over TCP on any port,
  **when** it is sent,
  **then** it is 404 (the route does not exist on TCP).
- **Given** a viewer's, an admin's or a cloud-linked play sign-in's credentials,
  **when** they call `POST /internal/plugins/invocations`,
  **then** 403; only a machine principal may mint.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go) | table of route × capability present/absent | call | 200 / 403 `plugin_capability` |
| T02 | unit (go) | table of route × subject in/out of scope × library in/out | call | 200 / 403 `plugin_scope` |
| T03 | integration (PG, go) | mint with injected grants in body | mint | grants from table only |
| T04 | integration (PG, go) | revoke a grant mid-job | call | 401 `plugin_grants_changed` |
| T05 | unit (go) | expired token, ended token, unknown token | call | 401 `plugin_token` |
| T06 | integration (PG, go) | `set-field` each allow-listed and forbidden field; admin title | call | allowed write + `plugin_writes` row / 403 `plugin_field` |
| T07 | unit (go) | subtitle with HTML, 5 MB, bad language | add | sanitised / 413 / 400 |
| T08 | unit (go) | 300 calls/s | call | 429-style 403 `plugin_rate` after 200 |
| T09 | role test (go) | mint route | no one / viewer / admin / cloud-linked play / machine | 401 / 403 / 403 / 403 / 200 |
| T10 | unit (go) | router | list TCP routes | no `/plugin-api/` |
| T11 | bench (go) | `library/video` over UDS | 10k calls | p95 ≤ 2 ms |
| T12 | integration (PG, go) | `queue/request` to another plugin's stage, to a core stage, to own stage not in `may_request` | call | 403 each; own allowed stage enqueued |

## Edge cases

- **A video moved to another library mid-job**: scope is checked per call against the current
  library; the next call is refused.
- **The plugin uninstalled mid-job**: tokens are ended at drain (54.07); a late call gets 401.
- **Multi-replica API** (cluster full nodes): only the home's API serves the plugin socket in
  this epic (README H12).
- **Clock skew** does not matter: tokens are checked by the API that minted them.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/plugins/tokens.go`, `grants_cache.go`, `scope.go`, `quota.go` (new) | §2, §5, §6. |
| `api/internal/plugins/pluginapi/server.go`, `routes_*.go` (new) | Unix socket listener and §3 routes, generated types from WIT. |
| `api/internal/handlers/plugins/invocations.go` (new) | Mint and end routes, machine-only. |
| `api/internal/router/p54_plugins.go` (new) | Wire routes, beside `MountP51` (`api/internal/router/p51.go`); start the UDS listener when `plugins.host` is enabled. |
| `shared/db/migrations/NNNN_plugins_api.sql` + `.sqlite.sql` (next free slot, 0182; Epic 51 uses 0175–0177 and 0181) | `plugin_data`, `plugin_writes`, `plugin_invocations`. |
| `api/internal/plugins/*_test.go`, `api/internal/handlers/plugins/invocations_test.go` (new) | T01–T12. |

### Tests

- Unit: T01, T02, T05, T07, T08, T10, T11. Integration: T03, T04, T06, T12. Role: T09.
- **CI:** `plugins-api` job (README §13).

### Rollout

Dark: the socket opens only while `plugins.host` is enabled (its desired row, read with Epic
51's `store.Watcher`, which follows `NOTIFY components_changed`).

### Rollback

Tables are additive; an older release ignores them.

### Dependencies

54.01 (capabilities list). The grant table's admin side is 54.08; tests seed it directly.
