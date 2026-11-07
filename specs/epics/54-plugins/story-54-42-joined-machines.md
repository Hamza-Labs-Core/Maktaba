# Story 54.42 — Third-party plugins on joined machines

> Epic 54 · Phase 1 · Depends on: 54.07, 54.09, 54.37, 54.38, 54.40, Epic 35, Epic 53 · Needed by: —

## Why

A home with a joined machine that has a GPU or more cores should be able to give plugin work
to it, as it gives core work. First-party plugins already run on any machine (README §15.4,
`placement:any`), because every machine is a full Maktaba with its own API. Third-party
plugins use the same model, with the home still the one place where they are installed and
approved (README §4.13, H12).

## Description

### 1. Placement

The manifest's `placement` (54.39 §2): `home` (the default), `any`, or `gpu` (a machine that
serves the `gpu` pool). The loader sets the plugin `StageDef`'s placement from it, the catalog
entry (54.37) carries it, and the claim SQL applies it as it does for core stages: a `home`
stage is claimed only by the home's worker, an `any` stage by any machine's, a `gpu` stage by
the workers of machines that serve the `gpu` pool.

### 2. Distribution

- At install the home signs the package's `files.json` with its cluster identity key
  (`home.sig`, beside `developer.sig` and `store.sig`).
- A joined machine's supervisor lists the enabled plugins whose placement fits it
  (`GET /internal/cluster/plugins`, over the cluster channel) and fetches each package it
  lacks (`GET /internal/cluster/plugins/{id}/{version}/package`).
- Before unpacking it verifies the store's signature against the embedded trust root when
  the package has one (Phase 2, 54.14), otherwise `home.sig` against the home's cluster
  identity it joined with; then every file's sha256 (54.01). A failure is reported to the
  home (`plugin_package_untrusted`) and nothing is unpacked.
- It unpacks with the same layout code (54.38) under its own `<state>/plugins/`, and removes
  versions the home no longer lists.

### 3. Running

- Each machine runs `plugins.host` on demand (54.37 §4) when a plugin it can run is enabled:
  its own `maktaba-plugind`, the same OS sandbox (§5.3).
- Each machine's Go API serves the plugin API on its own `<data>/run/plugin-api.sock` and
  mints tokens for its own jobs (`POST /internal/plugins/invocations`, machine-local auth),
  from the home's `plugin_installs` and `plugin_grants` as its database copy has them; it
  refuses to mint while its copy's grants generation is older than the job's
  (`plugin_held: grants_pending`, the job waits).
- Media handles open local paths through the share-path mapping (Epic 35); writes go to the
  home's database exactly as the machine's other writes do.

### 4. Health, logs and off

- The machine's host writes each plugin's lines to Epic 53's segments with
  `service = plugin.<id>` and the machine name, shipped to the home through Epic 53's
  channels; each machine reports `component_status` for `plugin.<id>` (Epic 51 per machine),
  and the Plugins page shows each machine's state.
- Disable, hold, a revocation `disable`, keep-off and uninstall change the home's rows; every
  machine's runner sees them through `NOTIFY components_changed` on its copy, stops claiming
  within 2 s, ends its tokens and drains as the home does; uninstall removes the files.

## Acceptance criteria

- **Given** a home and one joined machine, plugin A (`home`) emitting `x.done` and plugin B
  (`any`, subscribed to A, granted),
  **when** a video is added,
  **then** A runs on the home, B's job is claimed and run on the joined machine, and B's
  logs and health show on the home's Plugins page under the machine's name.
- **Given** B revoked or turned off on the home,
  **when** the machine next claims,
  **then** it claims no B job within 2 s and its in-flight B job is drained.
- **Given** a package on the machine whose bytes differ from `files.json`,
  **when** it verifies,
  **then** it refuses to unpack and reports `plugin_package_untrusted`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (py) | `placement` home / any / gpu | load | `StageDef.placement` and pool claims |
| T02 | unit (go) | package with a good and a bad `home.sig` | verify | ok / `plugin_package_untrusted` |
| T03 | integration (PG, go) | machine-local mint with an older grants generation | mint | waits |
| T04 | e2e (`plugins-e2e`, two machines on one runner) | A on the home, B on the machine | add a video | B ran on the machine |
| T05 | e2e | B revoked on the home | claim | none on the machine ≤ 2 s |

## Edge cases

- **A machine offline at install**: it fetches on reconnect; jobs that only it could run
  wait (Epic 35).
- **A machine on an older release** that cannot read the manifest: it reports
  `incompatible` for that plugin and does not claim its jobs.
- **A `gpu` plugin and no GPU machine**: its jobs wait, and the page says no machine fits.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/plugins/loader.py` | placement |
| `api/internal/plugins/lifecycle.go` | `home.sig` at install; the cluster routes |
| `cmd/maktaba-server/internal/plugins/` | fetch, verify, unpack per machine |
| `api/internal/plugins/tokens.go` | the grants-generation check per machine |
| `tests/components/` | the two-machine e2e |

### Tests

T01–T05.

### Rollout

Dark until a manifest says `any` or `gpu`.

### Rollback

A release without it runs every plugin stage on the home (placement read as `home`).

### Dependencies

54.07, 54.09, 54.37, 54.38, 54.40, Epic 35 (cluster channel), Epic 53 (log shipping).
