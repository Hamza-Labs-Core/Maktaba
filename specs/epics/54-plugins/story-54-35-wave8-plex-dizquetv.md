# Story 54.35 — Wave 8: Plex and dizqueTV through the service door

> Epic 54 · Phase 4 · Depends on: 54.34 · Needed by: 54.36

## Why

The Plex and dizqueTV extensions hold the home's most sensitive settings (the Plex token) and
talk to other products. As first-party plugins they must keep working without ever holding the
secret: they call through the home's service door (story 31.R), which adds it.

## Description

### 1. The stages

| Stage | Plugin | Pool | Placement | Trusted needs |
|---|---|---|---|---|
| `plex_index` | `tv.maktaba.plex` | `network_plex` | home, or any with `relay` | `service:plex` |
| `plex_collection` | `tv.maktaba.plex` | `network_plex` | same | `service:plex` |
| `plex_groups` | `tv.maktaba.plex` | `network_plex` | same | `service:plex` |
| `plex_livetv` | `tv.maktaba.plex` | `network_plex` | same | `service:plex` |
| `dizquetv_show` | `tv.maktaba.dizquetv` | `network_tv` | home | `service:dizquetv` |
| `dizquetv_channels` | `tv.maktaba.dizquetv` | `network_tv` | home | `service:dizquetv` |
| `dizquetv_audit` | `tv.maktaba.dizquetv` | `audits` | home | `service:dizquetv` |

Runner group `service`: **no** outbound network at all; every request to Plex or dizqueTV is a
plugin API call `service.request(name, method, path, body)`.

### 2. The service door

The API forwards `service.request` through the existing service door (31.R): on the home
directly, on a node through the home's relay (`StageDef.relay`, `rules.relay`), adding the
token from `home_secrets` on the home. The plugin sees responses, never the token or Plex's
address; the API refuses paths outside each service's allow-list (the Plex and dizqueTV API
paths today's extension code calls, listed in the manifest's `service` table and reviewed).

### 3. Extensions become plugins

The `extensions` rows (enabled, configured, settings) stay; the component (`ext.plex`,
`ext.dizquetv`) keeps its id and its kind, `extension`, and gains `plugin = "tv.maktaba.plex"`
/ `"tv.maktaba.dizquetv"` (54.27 §3). Epic 51.07's write-through (`extensions.enabled` kept in
step with the desired row, `api/internal/components/writethrough.go`) is unchanged. `is_configured()` reads
the same settings through `settings.read`. Cloudflare Stream has no stage and stays an
extension component (README §15.2).

### 4. Parity

Plex and dizqueTV are HTTP fakes behind the service door in the parity run; parity compares
the requests sent (method, path, body), the rows written, the results and fingerprints.

## Acceptance criteria

- **Given** the parity fixture with the Plex and dizqueTV fakes,
  **when** `plugins-parity` runs the seven stages,
  **then** the requests, rows, results and fingerprints are identical, and N16 holds.
- **Given** the `service` runner,
  **when** a Plex stage tries to open a socket or read `home_secrets`,
  **then** both are refused.
- **Given** a node with the relay on,
  **when** it runs `plex_index`,
  **then** the request reaches Plex through the home, with the token added there.
- **Given** a `service.request` to a path not in the allow-list,
  **when** it reaches the API,
  **then** `403 plugin_service_path`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | HTTP fakes | seven stages | identical requests and rows; N16 |
| T02 | integration (CI only) | `service` runner sandbox | socket, secrets path | refused |
| T03 | integration (go) | node relay | `service.request` | token added on the home only |
| T04 | integration (go) | path outside the allow-list | call | 403 |
| T05 | update rehearsal scenario 16.8 | Plex configured | update and rollback | extensions keep working; settings unchanged |
| T06 | unit (`fp-plex`, `fp-dizquetv`) | moved tests | run | green |

## Edge cases

- **Plex down**: the same cause as today (`dependency:plex`), because the API passes the
  service door's error through unchanged.
- **The token rotated**: nothing to do in the plugin; the door reads the new one.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/plex/`, `plugins/first-party/dizquetv/` (new) | §1, §3; `extensions/plex/`, `extensions/dizquetv/` stage code moves. |
| `api/internal/plugins/pluginapi/routes_service.go` (new) | §2 forwarding through the service door. |
| `pipeline/src/maktaba_pipeline/extensions/` | Plex and dizqueTV registration removed after one release. |
| `shared/db/migrations/` | Roles; `impl = plugin` for the seven. |
| `tests/update_rehearsal/scenario_16_8_wave8.*` | T05. |

### Tests

- Parity T01; integration T02–T04; rehearsal T05; unit T06.

### Rollout

The release flips the seven at update.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.34; Epic 31 story 31.R (the service door).
