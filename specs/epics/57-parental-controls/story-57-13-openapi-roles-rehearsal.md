# Story 57.13 — OpenAPI, role tests and the bypass rehearsal

> Epic 57 · Phase 1 (registry walk, with 57.03), 4 (the rest) · Depends on: 57.01–57.12 · Needed by: —
> **Status:** built (checked against the code 2026-11-12). The proofs live in `api/internal/router/`; the rehearsal runs
> on Postgres only.

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

One table, `api/internal/router/p57_roles_pg_test.go`, walks every Epic 57 route behind the
production router on a migrated Postgres, with eight callers: no session, viewer, restricted
child, the child's guardian (a viewer), a guardian of another child, admin, the admin token, and
the restricted child signed in through the cloud (play scope). The expected cells are 57.02 §2's
table, extended by 57.06, 57.08, 57.10, 57.12, 57.14 and 57.17's routes. Another child's guardian
gets 404, not 403, on a child's routes that a guardian may use. The child-through-cloud column must equal the child column for every read. The
list of Epic 57's routes is read from the walk of every mount (`router.P57Routes`), so a new
parental route fails `TestP57RoleTableNamesEveryRoute` until it has a row. Each story's own role
test keeps the detail (bodies, what each caller reads).

### 3. The surface registry walk

`api/internal/router/parental_walk_test.go` walks the router. Every mounted route, content or
not, must have an entry in `parental.Surfaces` (`api/internal/parental/surfaces.go`) that says
what it is to a restricted account (57.03 §3). A route that has none fails the test, naming it.
GraphQL replays the REST handlers, so the registry covers it.

### 4. The bypass rehearsal

`api/internal/router/p57_bypass_pg_test.go`, build tag `parental_bypass`, run by the CI job
`parental-bypass` (`.github/workflows/_integration.yml`) when the API, the streaming service,
the migrations or the rating tables change (the CI's related-jobs rules):

- seeds a home with fixtures (`v_ok`, `v_hi`, `v_unr`, a 16+ series, a 9+ and a 16+ channel with a
  masked programme, a collection, a playlist, a play queue, a plugin panel, a YouTube source);
- as a 9+ restricted child, runs **every registry probe** and the surfaces a GET cannot ask
  (search, opening a stream, starting a watch, a channel's signed URL, GraphQL, the live feeds,
  a thumbnail by its hash, a play queue made before the limit was tightened);
- repeats as the child signed in through Maktaba Cloud, through the relay agent's loopback path
  (`X-Maktaba-Via` from loopback with a cloud sign-in), and with an unrestricted account
  carrying `ac = 9` (device signal);
- asserts: no hidden subject's id, title, content hash or transcript word appears in any answer;
  a hidden id is answered exactly as an id that names nothing (status, headers, body);
- proves itself two ways: an unrestricted viewer asking the same questions must find the hidden
  subjects, and with the filter taken off one surface the rehearsal must fail naming it;
- runs on Postgres only. The job also runs the role table, the time rules on a fake clock, the
  device signal's ratchet and the registry walk, fails when a test was skipped for want of
  Postgres, writes the probe and leak counts to the job summary, and keeps the home's log as the
  artifact `parental-bypass-log`.

Locally, with a Postgres to use (`MAKTABA_TEST_PG_URL`), from `api/`:

```sh
go test -tags parental_bypass -count=1 -run 'TestParentalBypass' ./internal/router/
go test -count=1 -run 'TestP57RoleTable' ./internal/router/
```

The streaming service's own tests (57.10 T03–T05) run in the `streaming` job.

### 5. Player checks

The Player UI shards (`_apple-apps.yml`) gain the restricted, guardian and admin fixtures in
`MockHome`, run in English on iPhone and Arabic on iPad, on throwaway simulators only. The catalog
check runs with `--stringsdata`. No UI test runs on a developer's Mac.

## Acceptance criteria

- **Given** a new content route added without a registry entry, **then** the walk fails, naming it.
- **Given** each role cell, **then** a test asserts it.
- **Given** the bypass rehearsal, **then** it passes on Postgres, and adding a leak (for
  example removing the filter from `/suggest`) fails it.
- **Given** the wiki generator, **then** `python3 docs/wiki/db/generate_wiki.py --check` passes with
  the new endpoints and their Arabic summaries.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | router + document | coverage walk | every `parental` route described |
| T02 | Go role table (Postgres) | eight callers | every Epic 57 route | the table |
| T03 | Go unit | router + registry (+ GraphQL fields) | surface walk | no unregistered content route |
| T04 | Go + Postgres (`parental_bypass`) | seeded home | probes, direct, through the cloud, via relay, and with `ac` | no leak |
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
| `api/internal/router/parental_walk_test.go` (new) | T03 |
| `api/internal/router/p57_roles_pg_test.go`, `p57_roles_routes_test.go` (new) | T02 |
| `api/internal/router/p57_bypass_pg_test.go` (new) | T04, T05 |
| `.github/workflows/_integration.yml` (`parental-bypass`), the related-jobs rules | the rehearsal's job and paths |
| `web/wiki-app/public/db/` | regenerated |
