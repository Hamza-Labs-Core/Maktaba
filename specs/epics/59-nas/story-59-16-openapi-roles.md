# Story 59.16 — OpenAPI and role tests for the new admin routes

> Epic 59 · Each route and its role test land with the story that adds it (Phases 1–4); this story is the checklist · Depends on: 59.02, 59.03, 59.04, 59.07, 59.12

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
| `GET /api/system/updates` (gains runtime vs image) | 59.12 | public, as today; the new fields only to admins |

- **Errors:**
  - `409 library_read_only` on convert, dedup, delete and YouTube targets (59.03), for every role
    that could otherwise call them;
  - `403` for non-admins on every admin route above.
- **OpenAPI:** each route and field is in `shared/api/openapi.yaml` with its role and errors.
  The generated clients (web, Apple, Android) pick them up.
- **Role tables:** each route gets rows in its handler's `roles_test.go` (for example
  `api/internal/handlers/libraries/roles_test.go`) for every principal the existing tables
  cover, as Epic 58's story 58.17 lists them: no one (401 at the gate), viewer, editor, admin, a
  restricted child, a guardian, a cloud play grant and a cast grant. Each runs on the home and
  through a full node (a node forwards to the home).

## Acceptance criteria

- **Given** each admin route, **then** the role table proves 401 for no one, 200 for admin, and
  403 for every other principal.
- **Given** an admin's isolation consent, **then** the audit log has who, when and the level.
- **Given** a read-only library, **then** every write route returns 409 `library_read_only` for
  admins, and 403 before that for roles that cannot write at all.
- **Given** `shared/api/openapi.yaml`, **then** the spec lint passes, and each new route names its
  role.
- **Given** `GET /api/system/updates` without a session, **then** it still answers, without the
  runtime and image fields.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go role table | each admin route × every principal | call | 401 no one; 200 admin; 403 the rest |
| T02 | Go role table | same, through a full node | call | same |
| T03 | Go handler | consent | PUT as admin | 200; audit row |
| T04 | Go handler | ro library, admin | convert/delete/YouTube | 409 `library_read_only` |
| T05 | OpenAPI lint | `shared/api/openapi.yaml` | lint | pass; roles documented |
| T06 | Go handler | `GET /api/system/updates` as no one / viewer / admin | call | 200 each; new fields only for admin |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.not_available_here` | Not available in this library | غير متاح في هذه المكتبة |

## Edge cases

- **The `access` field for viewers:** omitted, since it reveals server paths and uids. Viewers
  see a disabled action's reason only as `library.not_available_here`.

## Build plan

| File | Change |
|---|---|
| `shared/api/openapi.yaml` | routes, fields, errors, roles |
| `api/internal/handlers/*` | the routes (with their stories) |
| `api/internal/handlers/*/roles_test.go` | rows per route and principal |
