# Story 58.17 — OpenAPI, role tests and the new-clients rehearsal

> Epic 58 · Phase 5 · Depends on: 58.01, 58.02, 58.03, 58.06, 58.14 · Needed by: —

## Why

The TV apps use the same API and the same roles as every other client. The server enforces every
role rule, with a role test for each, and hiding a button is never a rule. This epic adds four
things to the server:
- the cloud code approval;
- the cast grant and its scope;
- `device_caps` on stream sessions;
- the compatibility fields and `client_floor`.

Each needs its OpenAPI entry, a role table and a place in a rehearsal that runs the whole path.

## Description

**OpenAPI** (`shared/api/openapi.yaml`; the cloud has no OpenAPI document, so its routes are written up in a new `cloud/docs/tv-codes.md`):

| Route | Story |
|---|---|
| `GET /api/discovery/home` gains `api_level`, `api_level_oldest`, `client_floor` | 58.01 |
| 426 `client_too_old` on sign-in, refresh and pairing token routes | 58.01 |
| `POST /api/stream/sessions` gains `device_caps` | 58.02 |
| `POST /api/pairing/cloud-approve` | 58.03 |
| cloud: `POST /v1/tv/codes`, `POST /v1/tv/codes/poll`, `GET /v1/tv/codes/{code}/qr.png`, `POST /v1/tv/codes/{code}/approve` (signed-in cloud account) | 58.03 |
| `POST /api/cast/grants` | 58.06 |

The API level check of 58.01 runs on the result.

**Role tables** (Go, the pattern of 57.13). The principals are:
- no one;
- a viewer;
- a viewer without the item's library;
- a restricted child;
- that child's guardian;
- an admin;
- a cloud play sign-in (linked, `ApplyCloudScope`);
- a cast grant for item A.

| Route | no one | viewer | viewer, no library | restricted child | guardian | admin | cloud play | cast grant (A) |
|---|---|---|---|---|---|---|---|---|
| `GET /api/discovery/home` | 200 | 200 | 200 | 200 | 200 | 200 | 200 | 200 |
| `POST /api/pairing/cloud-approve` | 401 | 200 (TV is viewer) | 200 | 200 (TV restricted) | 200; `as_user` own child only | 200 | 403 (play scope cannot add sign-ins) | 403 |
| `POST /api/cast/grants` for A | 401 | 200 | 404 | 200 if A allowed, else 404 | 200 | 200 | 200 if A allowed | 403 (a grant cannot mint grants) |
| `POST /api/stream/sessions` for A | 401 | 200 | 404 | 200 if allowed, else 404 | 200 | 200 | 200 | 200 |
| `POST /api/stream/sessions` for B | 401 | 200 | 404 | as above | 200 | 200 | 200 | 403 |
| `POST …/progress` for A | 401 | 200 | 404 | as above | 200 | 200 | 200 | 200 |
| any admin route | 401 | 403 | 403 | 403 | 403 | 200 | 403 | 403 |
| `GET /api/videos?…` (list) | 401 | 200 | scoped | filtered | 200 | 200 | 200 | 403 (grant reads only its items) |

A role test fails when a route mounted on the router is missing from the table (the 57.13 router
walk, extended to the `cast` principal).

**The new-clients rehearsal** (a Go target, `tv-clients`, in the `api` job, on Postgres and
SQLite, plus a cloud rehearsal case). It drives the server the way each TV does, with recorded
request shapes from each platform's tests:
1. **Compat.** Discovery is read, a level outside the range is refused, and a `client_floor`
   refusal answers 426.
2. **Cloud code.** Run end to end through the cloud rehearsal's tunnel: create, approve as a
   viewer, poll, then redeem on the home through the relay path and through the LAN path.
3. **Sessions.** Opened with each platform's recorded `device_caps`. The mode matches 58.02's
   table: Fire TV direct, Samsung DV-p8 as an HDR10 remux, Cast HEVC as HLS fMP4, Roku AV1 as
   DASH.
4. **Cast grants.** Minted, used for its own items, refused for others, and re-checked after a
   policy change.
5. **Parental.** 58.14's A23–A27.

## Acceptance criteria

- **Given** a new route added without an OpenAPI entry, **then** the existing OpenAPI coverage
  check fails.
- **Given** a new route without a role table row, **then** the router walk fails.
- **Given** the rehearsal, **then** all five steps pass on Postgres and SQLite, directly and through
  the relay path.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go role table | the principals × routes above | each call | the table's status codes |
| T02 | Go router walk | the mounted router | walk | every route in a table |
| T03 | Go rehearsal `tv-clients` (Postgres, SQLite) | seeded home | steps 1–5 | pass |
| T04 | Cloud rehearsal | a home over the tunnel | step 2 through the cloud | pass |

## Strings

None.

## Edge cases

- **A cast grant on a cluster** (Epic 35): grants are signed by the home's key and checked on any
  node, the same as access tokens.

## Build plan

| File | Change |
|---|---|
| `shared/api/openapi.yaml`, `cloud/docs/tv-codes.md` (new) | the routes |
| `api/internal/…/roles_test.go` (the 57.13 table) | rows and the `cast` principal |
| `api/internal/rehearsal/tvclients/` (new) | the rehearsal |
| `cloud/rehearsal/tvcode_test.go` (new) | T04 |
