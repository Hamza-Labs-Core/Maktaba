# Story 51.08 — Admin API and CLI, with roles enforced

> Epic 51 · Phase 2 · Depends on: 51.03, 51.04, 51.05, 51.06 · Needed by: 51.09, 51.10, 51.12, 51.13, 51.14, 51.19

## Why

After Phase 1 every component has a desired row and every machine reports its status
(51.02–51.06), but the only way to change a row is SQL. Admins need one API that the web page
(51.10), the apps (51.11) and the CLI all use, with the same checks in one place: who may
change what, which dependents stop or wait, which conflicts must go together, and an
`audit_log` row for every change. Headless Linux owners need the same through
`maktaba-server components …` (F16). The API enforces the roles; hiding UI is not a rule
(standing rule, 2026-10-25).

## Description

### 1. One service, two doors

A new Go package **`api/internal/components`** holds the logic: read the catalog
(`shared/components`, 51.01), read desired state and `component_status` through 51.02's store
(`shared/components/store`), compute effects, validate a change, and write it with
`store.SetDesired(…, audit)`. The store's trigger bumps `generation` and sends
`components_changed`; the audit row commits in the same transaction. Two doors call it and nothing else writes desired state:

- the HTTP handlers in the new package **`api/internal/handlers/components`**;
- the CLI: `maktaba-api components …`, which `maktaba-server components …` delegates to, as
  `license` and `adduser` already do (`cmd/maktaba-server/cmd_passthrough.go`).

### 2. Routes (all admin-only)

| Method and path | Body | Answer |
|---|---|---|
| `GET /api/components` | — | every component: catalog fields, desired (`want`, `hold`, `generation`), the home-wide phase and reason, per-machine status, `required_by`, `install_size_mb`, the bundle line ("Based on Full, minus YouTube") |
| `GET /api/components/{id}` | — | one component, plus conditions per machine and its last 20 audit rows |
| `PUT /api/components/{id}` | `{"want":"enabled"\|"installed"\|"absent", "generation":N?, "with_conflicts":bool?}` | the component after the change |
| `DELETE /api/components/{id}` | — | the same as `PUT {"want":"absent"}`, with its refusals; `?purge=true` also resets settings (51.13) and data (51.16) |
| `POST /api/components/{id}/stop` | — | sets `hold = {by, at}`; it lasts until `start` |
| `POST /api/components/{id}/start` | — | clears `hold` |
| `POST /api/components/{id}/restart` | — | `process`: supervisor `Restart`; `loop`: cancel and start again. Other kinds: `409 not_restartable` |
| `POST /api/components/preview` | `{"changes":[{"id","want"?,"hold"?}]}` | the effect, changing nothing (§3) |
| `POST /api/components/apply` | `{"changes":[…], "generation":N?}` | all changes in one transaction, or none |

- `generation` is optional. When given and the row has moved on, the answer is
  `409 stale_generation` with the current row, so two admins never overwrite each other
  silently.
- A hold has **no end time** (README §13, decision 2: only "until I turn it on"). `stop`
  takes no body; a body with `until` is refused with `400 hold_has_no_until`, so an older
  client never thinks it set a timed hold. A hold survives restarts and updates (H2).
- `core.*` components answer `409 not_switchable` to every write.
- An unknown id is `404`. A machine override is 51.12; settings are 51.13.
- `want = installed` or `absent` on a component whose files must be downloaded or deleted
  answers `409 install_not_ready` until 51.16 wires install and remove. Changing `enabled` ↔
  `installed` for something already on disk works now.

### 3. Effects and refusals

`preview` and every write run the same function, `Effect(changes)`, over the catalog graph:

```json
{ "effects": [
    { "id": "llm", "to": "off" },
    { "id": "classify.llm", "to": "waiting", "reason": "dependency_off:llm" },
    { "id": "enrich", "to": "degraded", "reason": "want_missing:llm" } ],
  "conflicts": [],
  "refused": [],
  "queued_jobs_parked": 1840,
  "drain_s": 30 }
```

- **Dependents keep their wish** (H3). Turning a requirement off lists the dependents that go
  to *Waiting*; their `want` is not touched.
- **Conflicts.** Enabling `livetv.inprocess` while `livetv.slots` is enabled answers
  `409 component_conflicts` with `{"conflicts":["livetv.slots"]}`, unless the request says
  `"with_conflicts": true`. Then both rows change in the same transaction.
- **Remove is refused** while an enabled component requires it: `409 component_required_by`
  with `{"required_by":["transcription","subtitles"]}` (§4.6). This is the one refusal for a
  dependency.
- **Licence.** 51.19 adds `403 licence_missing`; until then nothing is gated.
- `apply` checks every change against the state *after* the earlier changes of the same batch,
  so "disable `livetv.slots`, enable `livetv.inprocess`" passes as one batch.

Problem bodies use the existing `application/problem+json` helper with a `code`, as in
`api/internal/handlers/cluster/cluster.go` `problem()`.

### 4. Roles

The package mounts behind an `adminOnly` middleware, as `queues.Mount` does
(`api/internal/handlers/queues/queues.go:120-126`). Allowed: a home admin, and an admin's
cloud link with scope `manage`. No sign-in is `401` (the auth middleware). Refused with `403`:
a viewer, a cloud-linked
**play** sign-in (`principal.ApplyCloudScope` clears `IsAdmin` for `play`, even an admin's),
a machine's cluster certificate, and an API token without the admin scope. Machines write
their status only through the cluster channel (51.12), never through these routes.

### 5. Audit

Every change writes one `audit_log` row per component, in the same transaction as the change,
with 51.02's shape: `category = 'components'`, `action = component.<verb>` with the verb one of
`enable`, `disable` (to `installed`), `remove`, `hold`, `release`, `restart`, `bundle_apply`;
`actor_user_id`, `actor_source` (`web`, `cli`, `remote`), `target_kind = 'component'`,
`target_id = <id>`, and `payload = {before, after, batch_id}`. `restart` changes no
desired row but is audited the same way. The `components` category must be in
`audit_log_category_check`; 51.02's migration `0175_components` adds it (the pattern of
`0157_feature_flags.sql` and `0160_audit_server.sql`). The Audit log page shows the rows with
no change.

### 6. The CLI

```
maktaba-server components list [--json]          id, group, want, phase, reason
maktaba-server components show <id> [--json]     conditions per machine, requires, dependents
maktaba-server components enable <id> [--with-conflicts] [--yes]
maktaba-server components disable <id> [--yes]
maktaba-server components start <id>
maktaba-server components stop <id>                  (held until `start`; no timed holds)
maktaba-server components install <id> | remove <id> [--purge] | import <file>   (wired by 51.16)
maktaba-server components bundle [<full|core|worker>] [--yes]   (wired by 51.17; no name: show)
```

- Each write prints the preview first ("Turning off LLM also stops: Smart grouping, Book
  detection. Continue? [y/N]") and asks, unless `--yes`. With no terminal and no `--yes` it
  refuses.
- It talks to the database with the same `components` service, so the checks and audit rows
  are the same as the web's. `actor_source = cli`, actor = the OS user name.
- The server must be running (the database is part of it). If it is not, the CLI says so in
  one line and exits `3`.
- `install`, `remove`, `import` and `bundle <name>` exist from this story and print "available from the
  release with packs" until 51.16 and 51.17 connect them to their routes. The CLI's output is
  English only, like every other `maktaba-server` command.

## Acceptance criteria

- **Given** an admin and `llm` enabled with `classify.llm` enabled,
  **when** `POST /api/components/preview` asks to disable `llm`,
  **then** the answer lists `classify.llm` as `waiting` with `dependency_off:llm` and nothing
  in the database changes.
- **Given** the same,
  **when** `PUT /api/components/llm {"want":"installed"}` is sent,
  **then** `llm.want = installed`, `classify.llm.want` stays `enabled`, the generation goes up by
  one, and one `audit_log` row with `category = 'components'` is written.
- **Given** `transcription` is enabled,
  **when** `PUT /api/components/runtime.ml {"want":"absent"}` is sent,
  **then** the answer is `409 component_required_by` listing `transcription` and nothing changes.
- **Given** `livetv.slots` is enabled,
  **when** `livetv.inprocess` is enabled without `with_conflicts`,
  **then** `409 component_conflicts`; with `with_conflicts: true` both change in one transaction.
- **Given** an admin stops `transcription`,
  **when** the server restarts for an update and a day passes,
  **then** it is still Stopped, and it starts only when an admin sends `start`.
- **Given** an older client,
  **when** it sends `POST /api/components/transcription/stop {"until":"…"}`,
  **then** the answer is `400 hold_has_no_until` and no hold is written.
- **Given** no sign-in, a viewer, or a cloud-linked play sign-in (even an admin's),
  **when** any route of §2 is called,
  **then** the answer is `401` (no sign-in) or `403` (the others) and no row changes.
- **Given** the server is running,
  **when** `maktaba-server components disable youtube --yes` is run,
  **then** the change, the audit row (`actor_source = cli`) and the NOTIFY are the same as the
  web's.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | catalog fixture, all enabled | `Effect(disable llm)` | `classify.llm` waiting; `enrich` degraded; `llm` off |
| T02 | unit | `transcription` enabled | `Effect(remove runtime.ml)` | refused `component_required_by` [transcription, subtitles] |
| T03 | unit | `livetv.slots` enabled | batch: disable slots, enable inprocess | passes; reverse order without `with_conflicts` refused |
| T04 | unit | stale generation | `PUT` with old `generation` | `409 stale_generation` with current row |
| T05 | unit | stop body with `until` | stop | `400 hold_has_no_until`; no row change |
| T06 | unit | `core.api` | every write route | `409 not_switchable` |
| T07 | role | every route in §2 | as nil, viewer, play (admin user), machine cert, token without admin scope | `401` for nil, `403` for the rest; rows unchanged (`TestAdminOnly` pattern) |
| T08 | role | every route | as admin, as cloud `manage` link | `2xx` |
| T09 | integration (Postgres) | scratch DB, 51.02 tables | `PUT`, then read `audit_log` | one row, payload has before and after; `NOTIFY components_changed` seen |
| T10 | integration | `apply` with a bad third change | apply 3 changes | none written |
| T11 | unit | CLI with a fake service | `disable llm` without a terminal and without `--yes` | refuses, exit 2 |
| T12 | integration | server running in the test harness | `components list --json`, `stop youtube` | matches `GET /api/components`; hold set |
| T13 | contract | routes | `TestEveryRouteHasPlacementClass` | the new routes are `home` placement |

## Edge cases

- **Two admins, two tabs.** Without `generation` the last write wins and both are audited. The
  web always sends it (51.10).
- **A hold on a component that is off.** Allowed: it takes effect when the component is enabled.
  The page shows "Off (stopped)".
- **Restart of a component that is not running** starts it if it is enabled and not held,
  otherwise `409 not_running`.
- **A batch of 200 changes** (a bundle switch) runs in one transaction. The trigger sends one
  NOTIFY per row; runners reload once per revision they see, and the capabilities push (51.09)
  is coalesced to one a second.
- **Database down.** Writes answer `503` (the existing behaviour); nothing is cached for writes.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/components/service.go` (new) | Read, `Effect`, validate, write with generation, NOTIFY, audit. |
| `api/internal/components/effect.go` (new) | The dependency and conflict walk over the catalog graph. |
| `shared/components/store/store.go` | `SetDesiredBatch(ctx, []Desired, []Audit)` in one transaction, for `apply` and `with_conflicts` (51.02 has the single-row write). |
| `api/internal/handlers/components/components.go` (new) | Routes of §2, `Mount` behind `adminOnly`. |
| `api/internal/handlers/components/components_test.go` (new) | T01–T08, `TestAdminOnly`. |
| `api/internal/handlers/components/components_pg_test.go` (new) | T09, T10. |
| `api/internal/router/p51.go` (new) | `MountP51`: the components handler, called from `api/main.go` beside the other `MountP*`. 51.09–51.14 add to it. |
| `api/internal/router/placement.go` | `/api/components*` as `home`. |
| `api/components_cli.go` (new) | `maktaba-api components …`. |
| `api/main.go` | `case "components":` beside `license` (`api/main.go:109`). |
| `cmd/maktaba-server/cmd_passthrough.go` | `newComponentsCmd()` with `delegateCmd`. |
| `cmd/maktaba-server/main.go` | Add it to `root.AddCommand` and `rootLong`. |
| `docs/wiki/api-catalog.md` | The new routes and the CLI commands. |

### Tests

- T01–T08, T11, T13 in the Go `unit` job; T09, T10, T12 in `integration` (Postgres service).
- **CI only:** `components-live` drives these routes to measure N2–N5.

### Rollout

Ships with Phase 2. The web page (51.10) is the first user. The CLI ships at the same time.

### Rollback

The routes are new; an older release has none. Desired rows written through them stay valid
for the runners. `[components] reconcile = off` (§12) stops any effect.

### Dependencies

51.02 (tables, store, the `components` audit category), 51.03 (seeded rows), 51.04–51.06 (runners that act on the rows).
