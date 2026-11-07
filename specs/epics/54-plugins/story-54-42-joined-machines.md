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

- The home keeps each installed version's files (`files.json`, the signatures, the files it
  lists) and serves them to its machines only: `GET /internal/cluster/plugins/files/{id}/
  {folder}/{path}`, for a request a machine signed (delegation), and only the files
  `files.json` lists.
- A joined machine's plugins runner fetches the folder `plugin_installs` names for each
  plugin its placement fits, with the signed client it fetches release files with (over
  its cluster link first).
- Before anything is placed it checks that `files.json`'s sha256 is the digest the home
  recorded in `plugin_installs` (the home's own word: only the home writes that table, read
  over the machine's verified database link), that every file matches `files.json`, and,
  when the package is signed, the chain of trust of 54.14 against the embedded trust root.
  A failure places nothing and gates the plugin on that machine with
  `plugin_package_untrusted`.
- It places the copy with the same layout code (54.38), points `current` at it, and removes
  versions and plugins the home no longer keeps. It writes no plugin table: an uninstall is
  the home's.

### 3. Running

- Each machine runs `plugins.host` on demand (54.37 §4; its catalog placement is `any`)
  while a plugin it may run is enabled: its own `maktaba-plugind`, the same OS sandbox
  (§5.3). A plugin whose placement does not fit is gated there (`plugin_home_only`,
  `plugin_no_gpu`).
- The machine's API serves the plugin API on its own socket for its own jobs. The home
  mints each token (`POST /internal/cluster/plugins/invocations`, signed by the machine; it
  writes the invocation row and checks the plugin is on and placed for machines) and the
  machine adopts it; ending it is sent to the home too.
- Reads and media are the machine's: library, transcripts, its plugin data and exports
  through its database role; media through its own mounts (the share-path mapping).
- **Every write goes to the home**: data, tags, fields, chapters, subtitles, artwork,
  suggestions, notices, requests, events and asks of the home's models (`compute`, story
  54.21) (`POST /internal/cluster/plugin-api/v1/…`, the
  job's token, signed by the machine). The home answers them as calls on its own socket,
  with the same capability, scope, quota and lineage checks, under its own role, so the
  plugin tables' guard stays and one place enforces quotas and lineage.
- The machine's pipeline names its own copy to its host (`MAKTABA_PLUGINS_ROOT`).

### 4. Health, logs and off

- Each job's outcome goes to the home (`POST /internal/plugins/outcome` on the machine's
  API, sent on to `/internal/cluster/plugins/outcome`), which records it in the plugin's
  breaker with the dispatcher's own statements (a test holds the two texts equal) and holds
  the plugin when it trips.
- The machine's host writes each plugin's lines to its Epic 53 segments with
  `service = plugin.<id>`, which travel with the machine's other logs; each machine reports
  `component_status` for `plugin.<id>` (Epic 51 per machine).
- Disable, hold, a revocation `disable`, keep-off and uninstall change the home's rows; a
  machine checks the plugin is on at the home on every call of a job's token (so the next
  call after off is refused), the home mints no new token, and the machine's runner stops
  claiming through its catalog's gates; an uninstall removes the machine's copies.

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
| T02 | unit (go) | a copy from the home; a tampered file; a `files.json` the home did not install | fetch | placed, current set / `plugin_package_untrusted`, nothing placed |
| T03 | integration (PG, go) | a machine's mint, read, forwarded write, ungranted write, end | call | the home's invocation row; the read here; the write applied by the home; `plugin_capability`; ended at the home |
| T04 | e2e (`api/twomachine_e2e_test.go`, a home and a full node on one runner) | A on the home emits; B, placed any, subscribed | B's job on the machine | B's token from the home, its read on the machine, its write applied by the home with a lineage of 2 |
| T05 | e2e | B turned off on the home | the machine's next call, a new mint | refused within 2 s; no token |
| T06 | integration (PG, go) | five traps on a machine | outcomes | the home's breaker trips and holds the plugin |

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
| `api/internal/plugins/remote.go`, `breaker.go`; `api/internal/handlers/plugins/cluster.go` | the home's routes for its machines; the machine's forwarding plugin API and adopted tokens |
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
