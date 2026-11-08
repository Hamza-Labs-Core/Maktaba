# Story 54.35 — Wave 8: Plex and dizqueTV through the service door

> Epic 54 · Phase 4 · Depends on: 54.34 · Needed by: 54.36

**The Plex stages moved first; the dizqueTV stages followed** once the door built programmes
on the home (issue #827): dizqueTV stores the programme bodies its stages write, which carry
the Plex token, so the plugin never writes or reads one.

## Why

The Plex and dizqueTV extensions hold the home's most sensitive settings (the Plex token) and
talk to other products. As first-party plugins they must keep working without ever holding the
secret: they call through the home's service door (story 31.R), which adds it.

The Plex stages move first. The dizqueTV stages need more: their programmes carry the Plex
token in the request body, and dizqueTV stores the programmes, so a plugin could read the
token back through the same paths it writes them with. A door that filled the token into a
body would have to scrub every dizqueTV answer, transformed or partial copies included. That
is a larger promise than adding a header. So they move once the door builds a programme on the
home from the plugin's Plex item ids, and answers every read with a projection that holds no
secret: the token crosses the plugin boundary in neither direction (issue #827).

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

## Built (2026-11-07)

Wave 8 moved `plex_index`, `plex_collections_import`, `plex_collection`, `plex_groups` and
`plex_livetv` into `tv.maktaba.plex`, in the `service` runner, which has no network (roles in
the wave's `NNNN_fp_roles` migration). `plex_collections_import` (Epic 56) moves with them:
`ext.plex` maps to one plugin, and its stages are the manifest's. Every Plex call goes through
the home's service door: on the home the plugin API's `trusted/service`, on a joined machine
the worker's channel to the home. The door adds the token and scrubs it out of every answer;
the plugin holds neither the token nor Plex's address. The door's Plex allow-list gains the
Live TV calls `plex_livetv` makes, for a caller on the home only. The dizqueTV stages followed
(issue #827, below). Parity (T01) runs a library through index, collection import,
collection and groups, and Live TV's registration and channel map, against a fake Plex behind
HTTP on each side: the calls Plex receives, with the token, are identical, and N16 holds. The
sandbox test (T02) checks the `service` runner has no network.

## Built: the dizqueTV stages (2026-11-07, issue #827)

`dizquetv_show`, `dizquetv_channels` and `dizquetv_audit` run in `tv.maktaba.dizquetv`, in the
`service` runner (`service = ["dizquetv", "plex"]`, roles in `0214_fp_roles`). dizqueTV is
home-only in the door: a joined machine never relays it. The plugin never writes a raw
programme. The door's own operations (`/maktaba/...`) take a token-free record of each Plex
item (`program_spec`), check every field, and build the programme on the home with Plex's
address, the server's name and the token. A channel's programmes are written as references:
an index into the stored channel, or a show id and a rating key, which the door fills in from
what dizqueTV holds. Every read is a projection: a whitelist of fields, and any field whose
value holds a secret is left out, then merged back from the stored document on a write. A
final check refuses any answer that names the token, dizqueTV's own Plex tokens or Plex's
address, raw, URL-encoded or in part. If dizqueTV's Plex servers cannot be read, the door
refuses the call, and so does an unreadable Plex upstream. Raw writes that could carry a
programme are refused. A channel's pictures (logo, watermark, offline picture and soundtrack),
which dizqueTV and its clients fetch, are written only as the home's own: none, the stored
value, a file directly under `dz_art_url`, or one uploaded to dizqueTV (at its address or at
`dz_public_url`), with the file's version as the only query. A pass-through programme keeps
only its duration, `isOffline`, and a redirect's target. The agent channel's door carries Plex
only. The hosted-logo check
is a HEAD of a file under the home's own `dz_art_url` only. Its redirects are followed only
within that host. Tests: the door's allow-list, refusals and token-leak tests run against a
fake dizqueTV that stores and echoes its bodies. The plugin's parity test runs a season's
show, a planned channel and the audit on each side. Its leak test plants the token in the Plex
settings and in the fake's plex-servers, and checks that no answer or settings the plugin got
carry it.
