# Story 51.12 — Per-machine components

> Epic 51 · Phase 2 · Depends on: 51.08 · Needed by: —

## Why

A home with cluster machines (Epic 31) often wants different parts on different machines:
transcription on the machine with the GPU, YouTube downloads on the one near the disk, nothing
heavy on the old laptop. Today the only per-machine tool is the per-stage switch on the
Machines page (`cluster_node_stages`), one row per stage, about 55 of them. 51.02 already has
a `component_overrides` table and `component_desired_for(node)`. This story gives it a route,
a UI on the Machines page and a choice when a machine joins, and keeps the old stage switches
and the new component switches in agreement (README §6.3).

## Description

### 1. What an override is

An override is one row in `component_overrides` (51.02): `(node_id, id, want, hold_*)`. A null
`want` means "follow the home". The effective wish on a machine is the override when set, else
the home's row; `component_desired_for(node)` computes it, so a node never merges by itself.

Rules:

- **Mostly it narrows.** It turns off, or holds, on one machine something the home has on.
  It may also turn on, for one machine, a `placement = any` component the home has off (a GPU
  machine that transcribes for a home that doesn't). It can never turn on a `placement = home`
  component on a node (`409 home_only`).
- **`core.*`** has no override (`409 not_switchable`).
- **Remove** (`want = absent`) on one machine follows 51.16's rules on that machine only.

### 2. Routes (admin-only, as 51.08)

| Method and path | Body | Effect |
|---|---|---|
| `GET /api/components?machine=<node_id>` | — | the list with that machine's effective wish, override and status |
| `PUT /api/components/{id}/machines/{node_id}` | `{"want":"enabled"\|"installed"\|null, "generation":N?}` | set or clear the override |
| `POST /api/components/{id}/machines/{node_id}/stop` | — | per-machine hold, until `start` (no timed holds) |
| `POST /api/components/{id}/machines/{node_id}/start` | — | clear it |
| `POST /api/components/preview`, `/apply` | changes may carry `"machine": "<node_id>"` | same effect walk, on that machine's graph |

The CLI gets `--machine <name|id>` on `enable`, `disable`, `start`, `stop` and `show`
(`maktaba-server components disable transcription --machine old-laptop`). Every change writes
an `audit_log` row (`component.override`, payload with `node_id`, before and after).

### 3. Stage groups write through `cluster_node_stages`

For a `stages` component the override is written **twice, in one transaction**: the
`component_overrides` row, and one `cluster_node_stages` row per stage of the component
(`enabled = false` for off or held, the row deleted for "follow the home"). The stage rows go
through the existing `cluster.Store.SetStages` (`api/internal/cluster/pgstore.go:245`).

Why both: a machine on an older release has no component runner but already obeys
`cluster_node_stages` (`switched_off` in `pipeline/src/maktaba_pipeline/cluster/stages.py`).
Writing both means an override works on old and new machines alike, and the Machines page's
stage table shows the same thing as the component switch.

The other way round: when an admin flips one stage on the Machines page, the component row for
that machine shows **"On, 1 stage off"** with the stage named. Turning the component on or off
for the machine rewrites all its stage rows, so it wins. Nothing silently disagrees.

`process` and `loop` components have no stage rows. The machine's supervisor reconciler
(51.04) and, on a full node, its API runner (51.05) read `component_desired_for(node)` as on
the home.

### 4. A worker machine's bundle

The **Worker machine** bundle (§4.8) is a choice of pipeline components for one machine.

- **When approving** a machine (`POST /api/cluster/nodes/{id}/approve`), the request gains
  `"components": ["transcription", "youtube", …]` (optional). The Machines page's approve form
  shows the components marked W in README §5 that are enabled on the home, all ticked, each
  with its memory need and whether this machine can run it (from the machine's reported
  capabilities: RAM, GPU, platform). Unticked ones become overrides with `want = installed`.
- Approving without the field keeps today's behaviour: the machine follows the home.
- **Later**, the machine's Components section on the Machines page changes the same overrides.
- `maktaba-server setup --node` prints the home's choice once the machine is approved; the
  choice is the admin's, never the machine's (as `full_allowed`, slot 0151).

### 5. Machines report their status

Each runner on a machine reports through the cluster channel the machine already uses to talk
to its home: its database connection as `maktaba_worker`, calling 51.02's guarded
`component_status_report(node, rows)` (the same pattern as `cluster_node_report` in
`0103_multi_instance.sql`). The guard refuses another node's id. The home never trusts a
`machine` value from the body. A machine that has not reported for 90 s shows its last status
greyed with "not heard from for 3 min".

### 6. The Machines page

`web/src/pages/Admin/Machines.tsx` gets a **Components** section per approved machine (anchor
`#components-<node_id>`, linked from 51.10), above the existing stage table:

```
Components on old-laptop                               Worker machine: 3 of 8
  Transcription     ○ Off here (home: on)     [Follow home ▾]   needs 6 GB, this machine has 4 GB
  YouTube           ● Running                  [Follow home ▾]
  Thumbnails        ● Running · 1 stage off    [Follow home ▾]
```

The menu offers *Follow home*, *Off on this machine*, *Stop on this machine* (held until
someone starts it, as 51.10). Changes go through the same preview dialog as 51.10. Every string in
`en.json` and `ar.json` together (المكوّنات على هذا الجهاز، يتبع الجهاز الرئيسي، متوقف على هذا
الجهاز); the section is checked in Arabic and right to left.

## Acceptance criteria

- **Given** a home with transcription on and machine N approved,
  **when** the admin sets transcription "Off on this machine" for N,
  **then** N's `component_overrides` row is `installed`, N's `cluster_node_stages` rows for
  `extract` and `transcribe` are `enabled = false`, and N stops claiming them within 2 s while
  the home keeps transcribing.
- **Given** N runs the previous release (no component runner),
  **when** the same override is set,
  **then** N stops claiming those stages (through `switched_off`).
- **Given** the admin flips only `transcribe` back on in N's stage table,
  **when** the Components section loads,
  **then** transcription on N shows "On, 1 stage off" naming `extract`.
- **Given** a machine being approved,
  **when** the admin unticks YouTube in the approve form,
  **then** the machine joins with a YouTube override `installed` and never claims a YouTube job.
- **Given** a `placement = home` component,
  **when** an override tries to enable it on N,
  **then** the answer is `409 home_only`.
- **Given** node N's credentials,
  **when** it calls `component_status_report` with node M's id,
  **then** the database refuses it.
- **Given** a viewer, no sign-in, or a cloud-linked play sign-in,
  **when** any route of §2 is called,
  **then** `401` (no sign-in) or `403`, and nothing changes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | override service, fake store | override `transcription` off on N | override row + two stage rows in one call |
| T02 | unit | same | clear the override | stage rows deleted; override `want` null |
| T03 | unit | `placement = home` component | enable on N | `409 home_only` |
| T04 | unit | home has `youtube` off | enable on N | allowed (`placement = any`); goes *Waiting* on N if `tool.yt-dlp` is missing there |
| T05 | role | every route of §2 | nil, viewer, play sign-in, machine cert | `401` for nil, `403` for the rest (`TestAdminOnly` pattern) |
| T06 | integration (Postgres) | scratch DB, a node row | `PUT` override, then `component_desired_for(N)` | `installed` for that id |
| T07 | integration (pipeline) | `StageSelector` on a node, previous-release code path | stage rows written by T01 | `switched_off` for both stages |
| T08 | integration (Postgres) | two pipeline runners (home, node) | override off on node | node stops claiming ≤ 2 s; home keeps claiming |
| T09 | unit | approve with `components` | approve | overrides for unticked components; none for ticked |
| T10 | vitest | Machines page, mixed state | render | "On, 1 stage off"; Arabic and RTL render; axe clean |
| T11 | vitest | approve form | untick YouTube, approve | request has the list without `youtube` |
| T12 | integration | node silent 2 min | `GET /api/components?machine=N` | last status with `stale: true` |
| T13 | CI only | `components-live` with a second machine | per-machine flip | N1 and N4 hold for the node |

## Edge cases

- **A machine is revoked or deleted**: its overrides go (`ON DELETE CASCADE`, 51.02) and so do
  its stage rows (existing cascade).
- **A machine without enough memory** for a component the home wants on: the component goes
  *Waiting* with `not_enough_memory` on that machine only. No override is needed.
- **Many machines**: the list route returns per-machine status for all machines only when asked
  (`?machines=all`); the default list carries a roll-up per component (51.02 `Rollup`).
- **A hold on one machine and on the home**: both must clear to run (the same rule as a stage
  pause and a component hold, §6.3).
- **A full node** (Epic 35) runs API loops too; its loop components follow the same overrides.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/components/overrides.go` (new) | Override service: rules of §1, the double write of §3, audit. |
| `api/internal/handlers/components/machines.go` (new) | Routes of §2. |
| `api/internal/handlers/components/machines_test.go` (new) | T01–T05, T09. |
| `api/internal/handlers/components/machines_pg_test.go` (new) | T06, T12. |
| `api/internal/handlers/cluster/cluster.go` | `ApproveRequest.Components`; call the override service on approve. |
| `api/internal/cluster/pgstore.go` | `SetStages` usable inside the override's transaction (takes a `*sql.Tx`). |
| `api/components_cli.go` | `--machine` flag. |
| `pipeline/tests/cluster/test_stages_overrides.py` (new) | T07. |
| `pipeline/tests/components/test_two_runners.py` (new) | T08. |
| `web/src/pages/Admin/Machines.tsx` | Components section, approve-form choices. |
| `web/src/pages/Admin/Machines.test.tsx`, `Machines.cases.test.tsx` | T10, T11. |
| `web/src/lib/clusterApi.ts`, `web/src/lib/componentsApi.ts` | `approve(..., components)`; override calls. |
| `web/src/i18n/en.json`, `ar.json` | Every string of §6. |

### Tests

- T01–T06, T09, T12 in the Go `unit` / `integration` jobs; T07, T08 in the pipeline tests
  (`uv run --extra dev pytest`, Postgres tier); T10, T11 in the web unit job.
- **CI only:** T13 in `components-live`.

### Rollout

Ships with Phase 2. Homes without machines see nothing new. Existing per-stage switches keep
working unchanged.

### Rollback

Clear all overrides (`DELETE FROM component_overrides`) and machines follow the home again;
the stage rows written through stay as plain stage switches, which is what Epic 31 had.

### Dependencies

51.08 (the service, roles, preview), 51.02 (`component_overrides`, `component_desired_for`,
`component_status_report`), 51.04–51.06 (runners on machines).
