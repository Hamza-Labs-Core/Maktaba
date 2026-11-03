# Story 53.03 — The home's status, without the API or the database

> Epic 53 · Phase 1 · Depends on: — · Needed by: 53.04, 53.05, 53.06, 53.07

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
  (in backoff), `down` (crash-looped and cut off, 53.05, or stopped), `off` (not configured on
  this home, e.g. no TV).

### 2. The mapping

A table in the supervisor (`statusmap.go`), one row per component, until Epic 51 moves it into
the component descriptor (README §10):

| Component | From roles | Down means `features_down` | Banner key |
|---|---|---|---|
| `database` | `postgres` (or the SQLite file check) | everything except the recovery console | `database_down` |
| `api` | `api` | everything except the recovery console | `api_down` |
| `streaming` | `streaming`, TV slots | `playback`, `live_tv` | `streaming_down` |
| `processing` | pipeline workers | `new_media`, `transcripts`, `thumbnails` | `processing_down` (admins only) |
| `search` | the API's search readiness (pgvector index) | `search` | `search_down` |
| `logs` | `logs` (53.09) | `log_search` | none for viewers |

`overall` is `down` when `database` or `api` is `down`, else `degraded` when anything is not `ok`
or `off`, else `ok`.

### 3. Admin detail

`GET /api/system/processes` (exists) stays the admin view through the API. For when the API is
down, the recovery console (53.04) reads the same detail (restarts, last exit, crash loop, the
last error line) straight from the supervisor.

### 4. How it is computed

From what the supervisor already has: child state, liveness probes every 10 s, crash-loop
marking. Added: one readiness check per component every 10 s (`/readyz` where it exists; for
`database`, a `SELECT 1` over a dedicated connection with a 2 s timeout, which also catches a
Postgres that runs but refuses connections). The state changes only after **2** checks agree
(20 s), so a blip doesn't flash a banner; `down` from a crash is immediate.

### 5. Events

Each change is written as a supervisor event (`eventlog.go`, exists) and a 53.01 line
`component_state_changed` (`from`, `to`, `reason`), so the index and the timeline in 53.11 show
it.

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
- **Given** the home has no TV configured,
  **then** streaming's TV slots don't make `streaming` anything but its own state, and Live TV is reported `off`.
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
| `cmd/maktaba-server/internal/supervisor/status.go`, `statusmap.go` (new) | §1, §2, §4. |
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
