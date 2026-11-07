# Story 54.37 — The component catalog takes plugin entries

> Epic 54 · Phase 1 · Depends on: 54.01, Epic 51 (51.01, 51.02, 51.04, 51.05, 51.06, 51.08,
> 51.09) · Needed by: 54.07, 54.38, 54.17, 54.19

## Why

Epic 51's catalog is fixed per release. `shared/components/catalog.toml` is embedded in the Go
binaries (`shared/components/catalog.go`, `go:embed`) and copied byte for byte into the
pipeline (`pipeline/src/maktaba_pipeline/components/catalog.toml`, `make catalog-sync`). Both
loaders refuse an unknown field, a kind outside `process | stages | loop | extension | asset`
(or a `+` join of the first three), and the `catalog` CI job fails when the two copies, the
Go and Python dumps or the en/ar words drift. Nothing in Epic 51 adds a component at run time.

A third-party plugin is installed long after its server was built, so it cannot be in that
file. Every consumer of the catalog needs to see it all the same: the effect walk and
refusals of the admin API (`api/internal/components/effect.go`), `GET /api/components`, the
capabilities snapshot (`api/internal/components/features.go`), the Go loop runner's gates
(`shared/components/runner`, `runner.Gates`), the supervisor's reconciler
(`cmd/maktaba-server/internal/supervisor`), and the pipeline runner that gates stage groups
(`pipeline/src/maktaba_pipeline/components/runner.py`). Epic 51 does not provide this. This
story adds it, once, below 54.07's lifecycle.

## Description

### 1. A sixth kind: `plugin`

- `components.KindPlugin = "plugin"` (Go) and `KIND_PLUGIN` (Python). It never appears in
  `catalog.toml`: both loaders keep refusing it there, so a release cannot ship a static
  `plugin` entry by mistake.
- A plugin entry has id `plugin.<plugin id>` (`plugin.dev.example.silence-chapters`), which the
  existing id rule `^[a-z][a-z0-9_]*(\.[a-z0-9_*-]+)*$` accepts. The prefix `plugin.` is
  reserved: a static entry with it fails the `catalog` job.
- Its fields are derived from the manifest (54.01), never typed by hand:

| Catalog field | From |
|---|---|
| `kind` | `plugin` |
| `group` | `plugins` |
| `title_key`, `desc_key` | `plugin.<id>.name` and `.summary`, answered from the plugin's own `i18n/{en,ar}.json`, not from `web/src/i18n` |
| `stages` | the manifest's `[[stage]]` names as registered (`p_<hash>_<name>`, 54.06) |
| `requires` | `["plugins.host"]`, plus `llm` for a required `compute.llm` and `search.semantic` for a required `compute.embed` (54.21) |
| `wants` | the same two for optional ones |
| `settings` | `["plugins.<id>"]` (54.11) |
| `placement` | the manifest's: `home` (the default), `any` or `gpu` (README §4.13, story 54.42) |
| `platforms` | the manifest's, `any` meaning all four |
| `bundles` | `[]`: no bundle turns a plugin on |
| `licence` | empty: a paid plugin is gated by its entitlement (§3), never by a licence flag |
| `features` | `[]`: the nine capabilities features are fixed and a plugin serves none |
| `drain` | `{ grace_s = min(timeout_s, 120), checkpoint = true }` |
| `since` | the server version that installed it |

### 2. One catalog, two sources

- Go: `components.Default()` stays the static catalog. A new `components.With(static,
  entries []Component) *Catalog` returns a catalog that answers `Get`, `ByStage`, `Order`,
  `Requires`, `Dependents` and `Components` over both. Python gets `catalog.with_entries(...)`.
- The entries come from `plugin_installs` (54.07), read at start and on `NOTIFY
  plugin_installs_changed`. A changed set swaps the catalog pointer atomically; readers never
  see half a set.
- Every consumer named in Why takes the merged catalog: `Service.cat()` in the API, the
  capabilities snapshot, `runner.New(Options{Catalog})` in the API and streaming, the
  supervisor's reconciler and the pipeline runner. A process that has not read
  `plugin_installs` yet treats `plugin.*` desired rows as unknown and leaves them alone (it
  never reports them).
- The 51.01 check "every stage belongs to exactly one component" counts `p_*` stages as
  belonging to their plugin entry; a `p_*` stage with no entry is `not_configured`.

### 3. Gates and reason codes

`runner.Gates` (51.05) and its Python twin decide *Waiting* from desired state, requirements
and the licence. Plugin entries add five conditions, checked by the same function so every
runner agrees:

| Reason code | When | Phase |
|---|---|---|
| `incompatible` | the server or plugin API version is outside the manifest's range | waiting |
| `grants_needed` | the installed version asks for a capability not yet granted | waiting |
| `revoked` | a store `disable` revocation names this version (54.17) | waiting |
| `unreviewed_disallowed` | an unreviewed plugin while **Allow unreviewed plugins** is off (54.10) | waiting |
| `entitlement_missing` | a paid plugin with no valid store entitlement (54.19) | waiting |

Six more are status reasons the runners write: `plugin_crash_loop` (54.09),
`sandbox_partial` and `sandbox_unavailable` (54.03, the failed step in the condition's
`params`, not in the code), `runner_crash_loop` (54.23), `plugin_trusted_unsigned` and
`plugin_trusted_version` (54.22) and `kept_off` (54.07, on the `off` row of a kept-off
plugin). Each of the twelve is added to
`reasons` in `shared/components/catalog.toml` (an unknown reason is refused by
`store.ReportStatus`), with `components.reason.<code>` in `web/src/i18n/en.json` and
`ar.json`, so the `catalog` job checks the words.

The gates read their inputs (grants, revocations, entitlements, the unreviewed switch) from a
`plugin_gates` view the API keeps current. A change to any of them bumps
`component_meta.revision` through a write to the plugin's desired row (`generation` + 1, no
`want` change), so `NOTIFY components_changed` reaches every runner and the capabilities ETag
moves.

### 4. Processes that run on demand

Two first-party process components serve plugins: `plugins.host` (54.02) and
`core.plugin_runners` (54.23). Epic 51 runs a process while its component is enabled. These
two run only while at least one component they serve is enabled and its gates pass. The
supervisor's reconciler gets that rule as a catalog field, `on_demand = true` (both loaders
learn it), and reports the component `running` with `detail.idle = true` while no process is
needed. A home with no plugin enabled runs no `maktaba-plugind` (N14), without a derived
desired-state write that an admin's own switch would fight.

### 5. The API and the Components page

- `GET /api/components` lists plugin entries in the group `plugins`; `PUT
  /api/components/plugin.<id>`, `/stop`, `/start`, `DELETE` and `preview`/`apply` work on them
  as on any component (54.08 keeps `/api/plugins/*` in step).
- `POST /api/components/{id}/install` and `/api/components/import` refuse a `plugin.*` id
  (`409 plugin_install_route`, naming `POST /api/plugins/install`): packs carry first-party
  assets only (51.15, 51.16).
- A plugin entry is never in a bundle, so `POST /api/components/bundle` leaves it as it is.

## Acceptance criteria

- **Given** `catalog.toml` with an entry of `kind = "plugin"` or an id starting `plugin.`,
  **when** `make catalog-check` runs,
  **then** it fails, naming the entry.
- **Given** a plugin row in `plugin_installs`,
  **when** the API, streaming, the supervisor and the pipeline start,
  **then** each one's catalog has `plugin.<id>` with the fields of §1, and its stages are owned.
- **Given** an enabled plugin whose new version needs a grant,
  **when** the runners recompute their gates,
  **then** every runner reports `waiting` with `grants_needed`, and `GET /api/components`
  shows the same reason.
- **Given** no plugin enabled,
  **when** the supervisor reconciles,
  **then** no `plugins` process runs and `plugins.host` reads `running` with `idle: true`.
- **Given** `POST /api/components/plugin.x/install`,
  **then** `409 plugin_install_route`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go, py) | `catalog.toml` with a `plugin` entry | load | refused |
| T02 | unit (go, py) | static catalog + 2 plugin entries | `With` / `with_entries` | merged answers; Go and Python dumps equal |
| T03 | integration (PG, go) | `plugin_installs` change | NOTIFY | catalog swapped in API and supervisor |
| T04 | unit (go, py) | each gate input | `Gates` | the §3 reason, same in both languages |
| T05 | integration (go) | on-demand component, no plugin enabled | reconcile | no process; `idle` detail |
| T06 | unit | the twelve reason codes | `catalog` job | en + ar words present |
| T07 | role test (go) | `PUT /api/components/plugin.x` | no one, viewer, play sign-in, admin | 401, 403, 403, 200 |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.go`, `catalog_test.go` | `KindPlugin`, `With`, `on_demand`, the `plugin.` reserve. |
| `pipeline/src/maktaba_pipeline/components/catalog.py` | The same, in Python. |
| `shared/components/catalog.toml` | The twelve reason codes; `on_demand` on `plugins.host` and `core.plugin_runners`. |
| `shared/components/runner/runner.go`, `pipeline/src/maktaba_pipeline/components/runner.py` | Plugin gates. |
| `api/internal/components/service.go`, `features.go`, `effect.go` | The merged catalog; install refusal. |
| `cmd/maktaba-server/internal/supervisor/` | The merged catalog; the on-demand rule. |
| `web/src/i18n/en.json`, `ar.json` | `components.reason.*` words. |

### Tests

- Unit T01, T02, T04, T06; integration T03, T05; role test T07.

### Rollout

Dark: with no `plugin_installs` row the merged catalog equals the static one.

### Rollback

Release rollback. An older release reads `plugin.*` desired rows as unknown ids and ignores
them; `[components] reconcile = "off"` stops every runner acting.

### Dependencies

54.01 (manifest fields). Epic 51: 51.01 (catalog and its job), 51.02 (tables, NOTIFY),
51.04 (supervisor), 51.05 (`runner.Gates`), 51.06 (pipeline stage groups), 51.08 (admin API),
51.09 (capabilities).
