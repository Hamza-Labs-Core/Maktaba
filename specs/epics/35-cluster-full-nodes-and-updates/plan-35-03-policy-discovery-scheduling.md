# Plan 35.3 — Update policy, discovery and scheduling — implementation

> Implementation plan for [story-35-03-policy-discovery-scheduling](story-35-03-policy-discovery-scheduling.md). Design: [design.md](design.md) §4.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Discovery and scheduling stay in the API; execution is the supervisor's (35.4). | The API has settings, the catalog and the UI; it may restart during a run without harm. |
| D2 | Verify catalog entries on sight, not at install. | A bad signature on a published release is an incident to report at once. |
| D3 | Target choice is a pure function of catalog + policy + current version. | Table-tested; the same function answers "next planned update" on the page. |
| D4 | `release_catalog`, `update_runs`, `update_run_steps`, `update_targets`, `update_watermark` in one migration S1. | They land together with 35.4; revoked from `maktaba_worker`. |
| D5 | Poll every 6 h (was 24 h). | A release is seen the same day; still far below the unauthenticated limit. |

## 1. Files

**Create**
- `api/internal/updates/policy.go` — `rules.updates` load/validate, defaults
- `api/internal/updates/target.go` — `ChooseTarget(catalog, policy, current, watermark)`
- `api/internal/updates/scheduler.go` — minute ticker, blockers, start via supervisor client
- `api/internal/updates/catalog.go` — `release_catalog` repo, verify-on-sight, withdrawn re-check
- `api/internal/updates/handlers.go` — `/api/cluster/updates`, `/settings`, `/run`, `/skip`, `/defer`
- `api/internal/updates/supervisor_client.go` — `POST /update/start` on `MAKTABA_SUPERVISOR_URL`
- `shared/db/migrations/<S1>_update_runs.sql` and `.sqlite.sql` (slot S1, next free at implementation time)
- `web/src/features/updates/UpdatesSettings.tsx` (settings card; the page itself is 35.13)

**Modify**
- `api/internal/system/updater.go` — 6 h interval, write catalog, call `releaseverify` for the small files
- `api/internal/system/selfupdate.go` — handler delegates to `/api/cluster/updates/run`
- `api/internal/router/p28.go` — mount the new routes (admin)
- `api/internal/handlers/rules/schema.json` (rules schema) — `rules.updates`
- `shared/db/migrations/MANIFEST.md` — S1 row when the file exists
- `api/internal/cluster/...` grants — revoke new tables from `maktaba_worker`

## 2. Steps

1. Migration S1 (tables of README data model) with SQLite sibling; revoke from `maktaba_worker`.
2. Policy load/validate; settings schema and card.
3. Catalog writer and verify-on-sight in the Updater; notifications through the 33.13 hook (fallback:
   audit log).
4. `ChooseTarget` with table tests.
5. Scheduler with blockers; supervisor client.
6. Handlers (update now, skip, defer), audit rows; Epic 28.3 handler mapping.

## 3. Tests

Story TCs; `TestMigrationS1Revoked` (role cannot select the new tables).

## 4. Rollout

Phase A ships with `auto_install` forced false by a build flag until 35.4–35.7 are in; the owner default
`true` takes effect at phase B.
