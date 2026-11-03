# Story 53.03 — The home's status, without the API or the database

> Epic 53 · Phase 1 · Depends on: 53.18, Epic 51 (51.04 reconciler, built) · Needed by: 53.04, 53.05, 53.06, 53.07

## Why

System health (`/admin/health`) is the right page, but it is behind the API, which is behind
Postgres. When either fails, nobody can see what failed, and every app just says "can't load".
The supervisor already knows each child's state (`/processes` on its loopback admin port). That
knowledge needs a public, minimal, always-on form.

## Description

### 1. `GET /status.json` on the web port

Served by the supervisor itself (not proxied), no sign-in, `Cache-Control: no-store`, answered
from memory in ≤ 10 ms:

```jsonc
{
  "v": 1,
  "at": "2026-11-03T10:15:00Z",
  "mode": "normal",                    // normal | safe | recovery
  "overall": "degraded",               // ok | degraded | down
  "components": {
    "database":  "ok",
    "api":       "ok",
    "streaming": "down",
    "processing":"ok",
    "search":    "ok",
    "logs":      "ok"
  },
  "features_down": ["playback", "live_tv"],
  "banner": "streaming_down"
}
```

- Component names are **user-level**, not process names (`processing` covers every pipeline
  worker; a single worker down is `degraded`, all of them `down`).
- No versions, hostnames, paths, counts or error text: anyone who can reach the web port
  (including through the relay) may read it. Details are for admins (§3).
- States: `ok`, `starting`, `degraded` (alive but a probe or a dependency fails), `restarting`
  (in backoff), `down` (should run, but crash-looped and cut off, 53.05), `off` (turned off,
  held or not installed in Epic 51's desired state, §2a; e.g. no TV).

### 2. The mapping

The keys of `components` in `/status.json` are **status groups**, coarser than Epic 51's
components (a viewer needs "Playback", not `livetv.slots`). Each group is a set of Epic 51
catalog ids ([`shared/components/catalog.toml`](../../../shared/components/catalog.toml)). The
catalog has no status field and its loaders refuse unknown fields, so the table lives in the
supervisor (`statusmap.go`), keyed by catalog id; 53.18 adds the test that keeps it complete.

| Group | Epic 51 components (processes they own) | Down means `features_down` | Banner key |
|---|---|---|---|
| `database` | `core.postgres` (`postgres/`; on SQLite homes the file check) | everything except the recovery console | `database_down` |
| `api` | `core.api` (`api/`) | everything except the recovery console | `api_down` |
| `streaming` | `core.streaming` (`streaming/`), `livetv.slots` (`tv-slot/*`) | `playback`, `live_tv` | `streaming_down` |
| `processing` | `core.pipeline` (`pipeline/`, `pipeline/chroma-copy`), `transcription` (`pipeline/stt-*`), `search.semantic`'s `pipeline/index` | `new_media`, `transcripts`, `thumbnails` | `processing_down` (admins only) |
| `search` | `search.semantic`, read through the API's search readiness (pgvector index) | `semantic_search` | `search_down` |
| `logs` | `logs.index` (`logs/`, 53.09, catalog entry from 53.18) | `log_search` | none for viewers |

- A process maps to its component with `ByProcess(role, instance)`
  (`shared/components/catalog.go`), the same lookup `catalog_roles_test.go` checks.
- `live_tv`, `transcripts` and `semantic_search` are the words of Epic 51's capabilities
  (`features` in the catalog). `playback`, `new_media`, `thumbnails` and `log_search` are status
  words only: they have no switch of their own.
- `overall` is `down` when `database` or `api` is `down`, else `degraded` when anything is not
  `ok` or `off`, else `ok`.

### 2a. Off is not down

A group, or a component inside it, that an admin turned off is `off`, never `down`. The
supervisor knows without Postgres: its reconciler (story 51.04,
`cmd/maktaba-server/internal/supervisor/reconcile.go`) holds the machine's desired state and
keeps the last one it saw in `<state>/state/components.json` (`compcache.go`).

| Desired state (Epic 51) | Reconciler phase | In `/status.json` |
|---|---|---|
| `want = enabled`, no hold, requirements met | `starting`, `running`, `degraded`, `failed` | as the checks say (`starting`, `ok`, `degraded`, `restarting`, `down`) |
| `want = installed` or `absent` | `off`, `not_installed` | `off` |
| a hold (`POST /api/components/{id}/stop`) | `stopped` (reason `held`) | `off` |
| a requirement off or the licence missing | `waiting` (`dependency_off`, `licence_missing`) | `off` |
| kept off for this boot (safe mode, 53.05) | `off` (condition `Reconciled` false, reason `boot_flag`) | `off`, `mode: "safe"` |

A group is `off` only when every component in it is off (no TV: `livetv.slots` off leaves
`streaming` as `core.streaming` says). Only `ok`, `degraded`, `restarting` and `down` raise a
banner (53.06).

### 3. Admin detail

`GET /api/system/processes` (exists) stays the admin view through the API. For when the API is
down, the recovery console (53.04) reads the same detail (restarts, last exit, crash loop, the
last error line) straight from the supervisor.

### 4. How it is computed

From what the supervisor already has: child state, liveness probes every 10 s, crash-loop
marking, and the reconciler's view of each component (§2a; the same phases it reports as
`component_status` rows of part `supervisor`). It reads no `component_status` row and no
Postgres table. Added: one readiness check per component every 10 s (`/readyz` where it exists; for
`database`, a `SELECT 1` over a dedicated connection with a 2 s timeout, which also catches a
Postgres that runs but refuses connections). The state changes only after **2** checks agree
(20 s), so a blip doesn't flash a banner; `down` from a crash is immediate.

### 5. Events

Each change is written as a supervisor event (`eventlog.go`, exists) and a 53.01 line
`status_group_changed` (`group`, `from`, `to`, `reason`, and `component` when one catalog id
caused it), so the index and the timeline in 53.11 show it. The name differs from Epic 51's
`components_changed` NOTIFY, which is about desired state, not health.

## Acceptance criteria

- **Given** every part is healthy,
  **when** anyone gets `/status.json`,
  **then** `overall` is `ok` and no field names a host, path or version.
- **Given** the API process is killed,
  **when** `/status.json` is read within 1 s,
  **then** `api` is `restarting` or `down` and `overall` is `down`, answered by the supervisor while the API is gone.
- **Given** Postgres runs but refuses connections,
  **when** two checks fail,
  **then** `database` is `degraded` and the banner key is `database_down`.
- **Given** one of three pipeline workers is down,
  **then** `processing` is `degraded`, not `down`.
- **Given** the home has no TV configured (`livetv` and `livetv.slots` at `want = installed`),
  **then** the TV slots don't make `streaming` anything but `core.streaming`'s state, and Live TV is reported `off`.
- **Given** an admin stops `transcription` (`POST /api/components/transcription/stop`),
  **when** its workers exit,
  **then** `processing` stays `ok`, no banner shows, and nothing is reported `down`.
- **Given** Postgres is down and `transcription` was turned off before,
  **when** `/status.json` is read,
  **then** `transcription` still counts as `off` (from `<state>/state/components.json`).
- **Given** a request through the cloud relay,
  **when** it asks for `/status.json`,
  **then** it gets the same document.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fake children states | compute | the table's mapping, `overall` rule |
| T02 | unit | one failed check then OK | compute | no change (needs 2) |
| T03 | unit | JSON output | scan | no hostnames, paths, versions, digits beyond `v` |
| T04 | integration | supervisor + stand-ins | kill the API | `/status.json` answers 200 in ≤ 10 ms, `api` not `ok` |
| T05 | integration | Postgres that refuses connections (`pg_hba` reject) | wait 25 s | `database: degraded` |
| T06 | unit | reconciler phases `off`, `stopped`+`held`, `waiting`+`dependency_off`, `off`+`boot_flag` | compute | `off`, no banner; `failed`+`crash_loop` → `down` |
| T07 | unit | the catalog and `statusmap.go` | every `process` component | maps to exactly one group (53.18's check) |

## Edge cases

- **The supervisor is itself starting:** `/status.json` answers `mode: "normal"`, every
  component `starting`, until the first round of checks.
- **SQLite homes** (no Postgres role): `database` checks the library file opens and answers.
- **Joined machines** answer their own `/status.json`; the home's says nothing about them (the
  Machines page does).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/supervisor/status.go`, `statusmap.go` (new) | §1, §2, §4; groups keyed by Epic 51 catalog id. |
| `cmd/maktaba-server/internal/supervisor/reconcile.go` | Expose each component's phase and reason to `status.go` (read-only, from memory). |
| `cmd/maktaba-server/cmd_serve.go` | Route `/status.json` on the web port before the `/api/` proxy. |
| `cmd/maktaba-server/internal/webui/serve.go` | Keep `/status.json` out of the SPA fallback. |
| `shared/go/statusdoc/` (new) | The document type, shared with the API (53.06) and the apps' fixtures. |
| `docs/api/status.md` (new) | The contract. |

### Tests

T01–T05 in the `cmd/maktaba-server` unit and integration jobs.

### Rollout

Touches the supervisor: `update-rehearsal` on the exact commit. Nothing reads it until 53.06.

### Rollback

Revert; nothing depends on it yet.

### Dependencies

None. Needed by 53.04, 53.05, 53.06, 53.07.
