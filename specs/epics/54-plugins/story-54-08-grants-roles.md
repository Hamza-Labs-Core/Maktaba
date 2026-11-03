# Story 54.08 — Grants, ratings and roles

> Epic 54 · Phase 1 · Depends on: 54.04, 54.07 · Needed by: 54.10, 54.13

## Why

The admin, not the developer, decides what a plugin may touch, the same way a phone asks
before an app reads photos. The API must store that decision, mint tokens only from it (54.04),
and make sure only an admin can change it. Every route gets the role test the standing rule
requires: no one, a viewer, a cloud-linked play sign-in, and an admin.

## Description

### 1. The grant table

`plugin_grants (plugin_id PK, capabilities TEXT[], libraries UUID[] NULL, all_libraries BOOL,
generation BIGINT, granted_by, granted_at)`. `generation` increments on every change and
`NOTIFY plugin_grants_changed` invalidates the API's cache (54.04 §6). `all_libraries = true`
includes libraries added later; otherwise only the listed ones.

Rules:

- Grants ⊆ the installed version's `required ∪ optional`. An install or enable with a required
  capability missing is refused (`400 plugin_required_capability`).
- Optional capabilities can be granted or not; the plugin learns which through `job.grants()`.
- On update (54.07 §5): required capabilities new in the version → *Waiting* `grants_needed`;
  new optional ones are not granted until the admin ticks them.

### 2. The rating

A plugin's **rating** (shown on install and on the Plugins page) is computed, like Home
Assistant's add-on rating, from 1 (worst) to 6:

- start at 6;
- −1 for each `medium` capability (54.01 `capabilities.toml`), −2 for each `high` (none in
  Phase 1);
- −1 if it has no store review (54.14 review level `none` or `developer`);
- −1 if `plugin-data.read:*` is asked;
- minimum 1.

The rating is advice, not a rule; it is computed by the same Go function in the API and in the
store (54.16), with a shared test table.

### 3. Admin routes

| Route | What |
|---|---|
| `GET /api/plugins` | installed plugins: id, version, names (en/ar), phase, grants, rating, review level, updates |
| `GET /api/plugins/{id}` | details, manifest summary, counters, last errors (54.09) |
| `POST /api/plugins/install` | 54.07 §4 (multipart file, or a store reference) with `{grants, libraries, enable}` |
| `PUT /api/plugins/{id}/grants` | change grants and libraries |
| `POST /api/plugins/{id}/enable` / `disable` / `start` / `stop` | Epic 51 desired state (`want` / `hold`) |
| `POST /api/plugins/{id}/update` / `rollback` | 54.07 §5, §6 |
| `DELETE /api/plugins/{id}?purge=` | 54.07 §7 |
| `GET /api/plugins/{id}/writes` | `plugin_writes` paged, for "what it wrote" |
| `POST /api/plugins/{id}/writes/revert` | revert selected writes (same rule as purge) |
| `GET/PUT /api/plugins/{id}/settings` | 54.11 |
| `GET/PUT /api/plugins/policy` | `allow_unreviewed` (54.10), pool size, default caps |

All are **admin-only** (`RequireAdmin`), audited (`audit_log`: who, what, before, after), and
the `PUT/POST/DELETE` ones are idempotent with the existing idempotency middleware. Cloud-linked
play sign-ins are play-scoped and get 403 even when the linked user is a home admin elsewhere,
unless a home admin granted that link "manage" (existing rule).

### 4. What viewers get

Nothing under `/api/plugins`. Plugin outputs reach viewers as ordinary data (tags, chapters,
subtitles) through the routes they already use, filtered by library ACL as today. Panels for
viewers are 54.20.

### 5. Epic 51's routes agree

`PUT /api/components/plugin.<id>` (Epic 51 51.08) works too and goes through the same store
functions, so both routes write the same rows; the Plugins page is the friendly view and the
Components page lists plugins under "Plugins".

## Acceptance criteria

- **Given** each route in §3,
  **when** called as no one, a viewer, a cloud-linked play sign-in and an admin,
  **then** the answers are 401, 403, 403 and 2xx, and nothing changes for the first three.
- **Given** an install request granting a capability the manifest does not list,
  **when** it is sent,
  **then** `400 plugin_unknown_capability` and nothing is installed.
- **Given** grants changed by an admin,
  **when** the change commits,
  **then** `generation` increments, an audit row names the admin and the before/after, and the
  next plugin call with an older token gets `401 plugin_grants_changed`.
- **Given** a plugin with `media.read` and `transcript.read` and no store review,
  **when** its rating is computed,
  **then** it is 3 in the API and in the store.
- **Given** `all_libraries = false` with libraries A and B,
  **when** library C is created,
  **then** the plugin cannot read C until an admin adds it.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | role test (go) | every route in §3 | no one / viewer / cloud-linked play / admin | 401 / 403 / 403 / 2xx; no change on refusals |
| T02 | integration (PG, go) | grants ⊄ manifest | install | 400; nothing written |
| T03 | integration (PG, go) | change grants | PUT | generation+1, audit, NOTIFY |
| T04 | unit (go) | rating table (shared with 54.16) | compute | expected ratings |
| T05 | integration (PG, go) | new library, not all_libraries | read | 403 `plugin_scope` |
| T06 | integration (PG, go) | `PUT /api/components/plugin.x` vs `/api/plugins/x/disable` | both | same rows |
| T07 | integration (PG, go) | revert writes | POST | reverted where unchanged since |
| T08 | unit (go) | idempotency key replay | POST install twice | one install |

## Edge cases

- **The only admin demoted mid-install**: the install request was authorised at start; the
  audit row shows it; later requests by that user are 403.
- **Grants for a plugin at *Waiting* `incompatible`**: allowed; take effect when it runs.
- **Library deleted**: removed from every grant's list in the same transaction.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/plugins/grants.go`, `rating.go` (new) | §1, §2. |
| `api/internal/handlers/plugins/*.go` (new) | §3 routes. |
| `api/internal/router/p54_plugins.go` | Wire with `RequireAdmin`. |
| `shared/db/migrations/NNNN_plugin_grants.sql` + `.sqlite.sql` | `plugin_grants`. |
| `shared/plugins/testdata/ratings.json` (new) | Shared rating table. |
| `api/internal/handlers/plugins/roles_test.go` (new) | T01 for every route (pattern of `TestAdminOnly` in `cluster_test.go`). |

### Tests

- Role T01; unit T04, T08; integration T02, T03, T05–T07. `plugins-api` job.

### Rollout

Routes exist from this release; the web page is 54.13. Without a plugin there is nothing to
change.

### Rollback

Additive table and routes.

### Dependencies

54.04 (token minting reads grants), 54.07 (install, lifecycle).
