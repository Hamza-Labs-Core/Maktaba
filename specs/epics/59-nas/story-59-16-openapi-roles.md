# Story 59.16 — OpenAPI and role tests for the new admin routes

> Epic 59 · Phase 4 (each route lands with its story) · Depends on: 59.02, 59.03, 59.04, 59.07

## Why

Every role rule is enforced by the server, with a role test. Hiding a button is not a rule. This
epic adds routes that read the machine, change what may run, or run heavy work now:
- **reading the machine:** the hardware report, the library access check;
- **changing what may run:** the plugin isolation switch, the tier;
- **running heavy work now:** Run now past quiet hours.

Each must be admin-only, documented in OpenAPI, and covered in the role tables.

## Description

**The routes:**

| Route | Story | Who |
|---|---|---|
| `GET /api/system/machine` | 59.04 | admin |
| `PUT /api/system/machine/tier` | 59.04 | admin |
| `POST /api/libraries/{id}/access-check` | 59.03 | admin |
| `GET /api/libraries/{id}` (gains `access`, `access_checked_at`) | 59.03 | as today (the new fields only to admins) |
| `POST /api/jobs/{id}/run-now`, `POST /api/libraries/{id}/run-now` | 59.02 | admin |
| `PUT /api/settings/schedule` (quiet hours) | 59.02 | admin |
| `PUT /api/plugins/isolation-consent` | 59.07 | admin; audit-logged |
| `GET /api/system/updates` (gains runtime vs image) | 59.12 | admin |

- **Errors:**
  - `409 library_read_only` on convert, dedup, delete and YouTube targets (59.03), for every role
    that could otherwise call them;
  - `403` for non-admins on every route above.
- **OpenAPI:** each route and field is in `api/openapi.yaml` with its role and errors. The
  generated clients (web, Apple, Android) pick them up.
- **Role tables:** each route gets rows in the role tests for no one (401 at the gate), viewer,
  editor and admin, across the home and a full node (a node forwards to the home), as the existing
  tables do.

## Acceptance criteria

- **Given** each route, **then** the role table proves 401 / 403 / 403 / 200 for no one, viewer,
  editor and admin.
- **Given** an admin's isolation consent, **then** the audit log has who, when and the level.
- **Given** a read-only library, **then** every write route returns 409 `library_read_only` for
  admins, and 403 before that for roles that cannot write at all.
- **Given** `api/openapi.yaml`, **then** the spec lint passes, and each new route names its role.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go role table | each route × roles | call | 401/403/403/200 |
| T02 | Go role table | same, through a full node | call | same |
| T03 | Go handler | consent | PUT as admin | 200; audit row |
| T04 | Go handler | ro library, admin | convert/delete/YouTube | 409 `library_read_only` |
| T05 | OpenAPI lint | `api/openapi.yaml` | lint | pass; roles documented |

## Strings

None beyond the stories' own.

## Edge cases

- **The `access` field for viewers:** omitted, since it reveals server paths and uids. Viewers
  see a disabled action's reason only as "not available in this library".

## Build plan

| File | Change |
|---|---|
| `api/openapi.yaml` | routes, fields, errors, roles |
| `api/internal/handlers/*` | the routes (with their stories) |
| `api/internal/roles/` tests | rows per route |
