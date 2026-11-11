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

**The routes**, as the merged stories mount them (`x-maktaba-role: admin` marks each admin
operation in the OpenAPI description):

| Route | Story | Who | OpenAPI | Role test |
|---|---|---|---|---|
| `GET /api/server/machine` (the hardware, the tier, the quiet hours, the video verdict, the plugin isolation level) | 59.04, 59.02, 59.05 | admin | `getMachine` | table; `serversettings` `TestMachineRefusesNonAdmins` |
| `PUT /api/server/machine` (`tier`, `quiet_hours` or `quiet_default`, `data_disk`) | 59.04, 59.02 | admin; audited (`tier-changed`, `machine-changed`) | `putMachine` | table; `TestMachinePutCarriesQuietHoursAndTheDisk` |
| `GET /api/server/speech` (speech by tier, the measured speed, the backlog's estimate) | 59.06 | admin | `getSpeech` | table; `serversettings` speech tests |
| `PUT /api/server/speech` (speech on or off, the model) | 59.06 | admin; audited | `putSpeech` | table; speech tests |
| `POST /api/server/speech/measure` | 59.06 | admin; audited | `measureSpeech` | table; speech tests |
| `GET /api/server/settings`, `PUT /api/server/settings` (gains `server.trusted_proxies`) | 59.08 | admin; a change audited | `getServerSettings`, `putServerSettings` | table; `serversettings` tests |
| `GET /api/libraries/{id}/access` (each folder's access, the user Maktaba runs as, the fix) | 59.03 | admin | `getLibraryAccess` | table; `libraries` `TestMutationsRefuseNonAdmins` |
| `POST /api/libraries/{id}/check-access` | 59.03 | admin; audited | `checkLibraryAccess` | table; `TestMutationsRefuseNonAdmins` |
| `GET /api/libraries`, `GET /api/libraries/{id}` (gain `access`, `access_checked_at`, `access_partial`) | 59.03 | as today | `Library` schema | as today |
| `POST /api/queues/jobs/{id}/run-now`, `POST /api/queues/{stage}/run-now`, `POST /api/libraries/{id}/run-now` | 59.02 | admin | `runJobNow`, `runStageNow`, `runLibraryNow` | table; `queues` run-now tests |
| `GET /api/queues` (a stage's `waiting_reason` `quiet_hours` and `quiet_at`) | 59.02 | as today | `QueueSummary` schema | as today |
| `GET /api/plugins/isolation`, `PUT /api/plugins/isolation` (Allow plugins with container-only isolation) | 59.07 | admin; audited with who, when and the level | `getPluginIsolation`, `putPluginIsolation` | table; `TestContainerIsolationSwitchIsAdminOnlyAndAudited` |
| `GET /api/stream/capabilities` (gains `software_only`); `POST /api/stream/sessions` (503 `transcode_busy`) | 59.05 | as today | `StreamCapabilities` schema, `/sessions` 503 | as today |
| `GET /api/components` and its component and machine views (gain `degraded_reason`, `degraded_reason_params`) | 59.07 (#1135) | admin | `listComponents` | table; `TestDegradedReason` |
| `GET /api/system/updates` (gains `container`: the volume's runtime beside the image, which runs and why) | 59.12 | every signed-in user, as today; `container` only to admins | `getSystemUpdates` | `system` `TestStatusHandlerContainerBlockIsAdminsOnly` |
| `GET /api/cluster/storage/home-shares`, `PUT /api/cluster/storage/home-shares` (a NAS home's own folders as SMB shares) | 59.13 | admin | `getHomeShares`, `putHomeShares` | table; `cluster` `TestHomeSharesRoles` |

Stories 59.01, 59.09, 59.10, 59.11 and 59.14 add no route.

- **Errors:**
  - `409 library_read_only` on the writes that would change a read-only library's files (59.03):
    delete with purge, a video's delete, convert, dedup, YouTube targets and retries, a
    duplicate's apply, a file action's restore; `409 library_no_access` on a scan of a library
    whose folders cannot be listed;
  - `403` for non-admins on every admin route above.
- **OpenAPI:** each route and field is in `shared/api/openapi.json` (English), its Arabic in
  `shared/api/openapi.ar.json` (maktaba.dev/ar/api/), and in `shared/api/openapi.yaml` (the
  wiki's), with its role and errors.
- **Role tables:** `api/internal/router/openapi_roles_test.go` reads the description and runs
  every operation marked `x-maktaba-role: admin` through the auth gate on the home's whole
  router, as each principal: no one (401 at the gate), a viewer, a viewer with the library, a
  viewer's API token, an admin's cloud play sign-in (403 each), an admin and an admin's cloud
  manage sign-in (past the role check). A restricted child and a guardian are viewers to the role
  check; this server has no cast grant principal yet. The handlers' own tests prove an admin's
  200 with their fixtures.
- **Through a full node:** a node forwards every admin route to the home (placement `home`), so
  the home's gate and handler, the table's, answer a node's callers too; the same test file
  fails when an admin route is placed anywhere else.

**The checks that keep it so:**
- `TestOpenAPIDescribesEveryRoute`: every `/api` route the home mounts is in `openapi.json`,
  except those `api/internal/router/testdata/openapi_undocumented.txt` lists (the routes older
  than the check). A new route fails until it is described; a listed route that is described,
  or no longer mounted, fails until its line goes, so the list only shrinks.
- `TestOpenAPIAdminRoutesRoleTable`: every operation marked admin gets the role table above,
  with no test to write; an operation marked admin and not mounted fails.
- The maktaba.dev build fails when an English text of the description has no Arabic.

## Acceptance criteria

- **Given** each admin route, **then** the role table proves 401 for no one, 403 for every
  principal that is not an admin, and an admin past the role check (200 in its handler's tests).
- **Given** an admin's isolation consent, **then** the audit log has who, when and the level.
- **Given** a read-only library, **then** every write route returns 409 `library_read_only` for
  admins, and 403 before that for roles that cannot write at all.
- **Given** `shared/api/openapi.json`, **then** every mounted `/api` route is described or on the
  shrinking list, each admin route says `x-maktaba-role: admin`, and each text has its Arabic.
- **Given** `GET /api/system/updates` as a viewer, **then** it still answers, without the
  `container` block (runtime and image); without a session the gate answers 401, as before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go role table | each admin route × every principal | call through the gate | 401 no one; 403 the non-admins; an admin past the check |
| T02 | Go | each admin route | its placement | `home`: a full node forwards it to the home |
| T03 | Go handler | consent | PUT as admin | 200; an audit row with who, when and the level |
| T04 | Go handler | ro library, admin | convert/delete/YouTube | 409 `library_read_only` |
| T05 | Go and maktaba.dev | `shared/api/openapi.json` | every route, the Arabic overlay | described; roles marked; every text in Arabic |
| T06 | Go handler | `GET /api/system/updates` as a viewer / admin | call | 200 each; `container` only for the admin |
| T07 | Go handler | `PUT /api/server/machine` with quiet hours, the data disk | PUT as admin | the supervisor gets those fields only; audited |
| T08 | Web unit | a read-only library's refusal, as an admin and as a non-admin, in English and Arabic | its reason; a 409's text | admin: the read-only reason and the fix; anyone else: `library.not_available_here`, no fix |

## Strings

| Key | English | Arabic |
|---|---|---|
| `library.not_available_here` | Not available in this library | غير متاح في هذه المكتبة |

Web catalogs (`web/src/i18n/en.json`, `ar.json`); no native app shows a library action a
library's access refuses.

## Edge cases

- **The `access` field for viewers:** a library's `access` (`rw`, `ro`, `none`) is shown to
  every reader of the library, so an app can disable a write it would refuse; the folders'
  paths and the uids are only in the admin's `GET /api/libraries/{id}/access`. Anyone who is not
  an admin sees a library's refusal only as `library.not_available_here`: the web's one reason
  for it (`libraryRefusalText`) and its 409 text (`libraryAccessErrorText`, `errorText` with
  `admin: false`) give an admin the read-only reason with the fix, and anyone else this string.
  Every action a library's access can refuse (convert, dedup, YouTube downloads, repair, empty
  trash, delete, restore, a duplicate's apply) is an admin's, so a non-admin is never offered
  one: the queue pages tell a non-admin they are an admin's, the library and video pages show
  them no such action, and the native apps have none.
- **Quiet hours from the web:** Settings → This machine saves quiet hours and the data disk
  through `PUT /api/server/machine`; the API forwards each field sent and nothing else (an empty
  tier would be refused as no tier).

## Build plan

| File | Change |
|---|---|
| `shared/api/openapi.json`, `openapi.ar.json`, `openapi.yaml` | routes, fields, errors, roles |
| `api/internal/router/openapi_roles_test.go` | the route walk and the role table |
| `api/internal/router/testdata/openapi_undocumented.txt` | the routes older than the check |
| `api/internal/handlers/*` | the routes (with their stories) |
