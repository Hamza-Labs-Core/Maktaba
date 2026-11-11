# Story 57.13 — OpenAPI, role tests and the bypass rehearsal

> Epic 57 · Phase 1 (registry walk, with 57.03), 4 (the rest) · Depends on: 57.01–57.12 · Needed by: —

## Why

A parental filter is only as good as its weakest route. The epic needs three proofs that keep
holding as the code changes: every route is described, every role rule is tested, and every known
way around the filter is tried in CI on every change that touches it.

## Description

### 1. OpenAPI

Describe every route of README §6.6 with request and response schemas and error codes
(`restricted`, `restricted_admin`, `restricted_guardian`, `no_library_access`,
`too_many_requests`, `locked`), plus the `parental` block of `/api/capabilities` and the `ac`
claim. Arabic summaries in `shared/api/openapi.ar.json`. Epic 56's coverage test (router walk
against the document) gains the `parental` handlers.

### 2. Role tests

One table-driven Postgres test per handler package (`parental`, and the changed `users`,
`discovery`, `channels`, `search`), with seven principals: no session, viewer, restricted child,
the child's guardian (a viewer), a guardian of another child, admin, and the restricted child
signed in through the cloud (play scope). The expected cells are 57.02 §2's table, extended by
57.06, 57.08, 57.14 and 57.17's routes. The child-through-cloud column must equal the child column for every read.

### 3. The surface registry walk

`api/internal/router/parental_surfaces_test.go` walks the router. Any mounted route whose handler
is in a content package (57.03 §2), or that is tagged `content`, must be in
`parental/surfaces.go` with a probe. A route that is not fails the test, naming it. GraphQL's
fields are walked through the bridge's field list the same way.

### 4. The bypass rehearsal

`api/internal/parental/bypass_pg_test.go`, build tag `parental_bypass`, run by the `api` job when
`api/`, `streaming/`, `shared/db/migrations/` or `shared/parental/` change (the CI's
related-jobs rules):

- seeds a home with fixtures (`v_ok`, `v_hi`, `v_unr`, a 16+ series, a 9+ and a 16+ channel with a
  masked programme, a collection, a playlist, a play queue, a plugin panel, a YouTube source);
- as a 9+ restricted child, runs **every registry probe** and attempts A1–A18 and A23–A26 of
  README §7 (the time attempts run on a fake clock);
- repeats through the relay's loopback path (`X-Maktaba-Via` from loopback with a cloud
  sign-in), and with an unrestricted account carrying `ac = 9` (device signal);
- asserts: no hidden subject's id, title, poster hash or transcript text appears in any body; by-ID
  answers equal the random-UUID answers; refused routes answer their codes;
- runs on Postgres and SQLite.

The streaming service's own tests (57.10 T03–T05) run in the `streaming` job.

### 5. Player checks

The Player UI shards (`_apple-apps.yml`) gain the restricted, guardian and admin fixtures in
`MockHome`, run in English on iPhone and Arabic on iPad, on throwaway simulators only. The catalog
check runs with `--stringsdata`. No UI test runs on a developer's Mac.

## Acceptance criteria

- **Given** a new content route added without a registry entry, **then** the walk fails, naming it.
- **Given** each role cell, **then** a test asserts it.
- **Given** the bypass rehearsal, **then** it passes on Postgres and SQLite, and adding a leak (for
  example removing the filter from `/suggest`) fails it.
- **Given** the wiki generator, **then** `python3 docs/wiki/db/generate_wiki.py --check` passes with
  the new endpoints and their Arabic summaries.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | router + document | coverage walk | every `parental` route described |
| T02 | Go role tests (Postgres) | seven principals | every route | the tables |
| T03 | Go unit | router + registry (+ GraphQL fields) | surface walk | no unregistered content route |
| T04 | Go + Postgres/SQLite (`parental_bypass`) | seeded home | probes, direct and via relay, and with `ac` | no leak |
| T05 | Go (mutation check, in the same target) | filter removed from one surface by a test hook | rehearsal | fails |
| T06 | OpenAPI lint (existing) | document | validate | valid 3.x |
| T07 | wiki `--check` | generator | run | passes |

## Strings

No user-facing strings. OpenAPI summaries in Arabic, for example:

| Summary | English | Arabic |
|---|---|---|
| set a child's policy | Set a restricted account's content limit | تعيين حدّ المحتوى لحساب مقيَّد |
| post the device's age signal | Send the age range and rating limit this device reports | إرسال الفئة العمرية وحدّ التصنيف اللذين يبلّغ عنهما هذا الجهاز |
| ask a parent | Ask a guardian to allow an item | طلب السماح بعنصر من الولي |
| unlock with a PIN | Allow an item for today with a guardian's PIN | السماح بعنصر لليوم برمز الولي |

## Build plan

| File | Change |
|---|---|
| `shared/api/openapi.json`, `shared/api/openapi.ar.json` | paths, schemas, Arabic |
| `api/internal/router/openapi_coverage_test.go` | parental handlers |
| `api/internal/router/parental_surfaces_test.go` (new) | T03 |
| `api/internal/handlers/parental/*_pg_test.go` | T02 |
| `api/internal/parental/bypass_pg_test.go` (new) | T04, T05 |
| `tools/ci/` related-jobs rules | the rehearsal's paths |
| `web/wiki-app/public/db/` | regenerated |
