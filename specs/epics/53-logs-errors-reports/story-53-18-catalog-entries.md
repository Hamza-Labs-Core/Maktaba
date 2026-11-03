# Story 53.18 — Epic 53's parts in the component catalog

> Epic 53 · Phase 1 · Depends on: Epic 51 (51.01 catalog, 51.04 reconciler, 51.05 Go loops;
> built), Epic 52.17 (rows for ids a later release adds) · Needed by: 53.03, 53.05, 53.09, 53.16

## Why

Epic 51 puts every supervised process and every background loop in one catalog,
[`shared/components/catalog.toml`](../../../shared/components/catalog.toml). CI keeps it that way:
the `catalog` job (`make catalog-check`), `cmd/maktaba-server/internal/supervisor/catalog_roles_test.go`
(every supervisor role/instance matches exactly one component) and the loop check
(`shared/components/runner/loopcheck`). This epic adds a process (the `logs` role, 53.09) and an
API loop (`api:reports_forwarder`, 53.16). Without catalog entries both fail CI, and neither can be
turned off, held or shown on the Components page.

Epic 51 also has no place for the status groups of `/status.json` (53.03): the catalog's loaders
(`shared/components/catalog.go`, `pipeline/src/maktaba_pipeline/components/catalog.py`) refuse an
unknown field. The groups stay in the supervisor, keyed by catalog id, and need a check of their
own so a new process component is never left out of them.

## Description

### 1. Two catalog entries

```toml
[[component]]
id        = "logs.index"
kind      = "process"
group     = "system"
title_key = "components.logs.index.title"
desc_key  = "components.logs.index.desc"
# the index, search, live tail, error groups and metric rollups (53.09, 53.10, 53.12);
# the segments themselves are written by the supervisor, which is not a component
processes = ["logs/"]
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
resources = { ram_mb = 120, disk_mb = 350, gpu = "none" }
bundles   = ["full", "core", "worker"]
since     = "<the release that ships 53.09>"

[[component]]
id        = "reports.share"
kind      = "loop"
group     = "system"
title_key = "components.reports.share.title"
desc_key  = "components.reports.share.desc"
# on as the admin's "send problem reports to Maktaba" setting says (53.16), like telemetry
loops     = ["api:reports_forwarder"]
requires  = ["cloud.link"]
placement = "home"
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
bundles   = []
since     = "<the release that ships 53.16>"
```

- `logs.index` is switchable and placed `any`: each full machine runs its own role (53.09 §4).
  It is in every bundle, so a Core home gets log search. Turning it off loses no line (the
  supervisor writes the segments); 53.09 says what the pages do meanwhile.
- `reports.share` is in no bundle and starts `installed` (off). The admin setting writes its
  desired state through `api/internal/components` (`component.enable` / `component.disable`
  audit rows); nothing else turns it on.
- No new reason code is needed: both use Epic 51's (`dependency_off`, `held`, `crash_loop`,
  `boot_flag`, …), which already have English and Arabic words.
- The `[logs]` keys of `server.toml` (53.02) stay settings of the always-on keeper. Neither entry
  lists them under `settings`.
- `make catalog-sync` copies the file to `pipeline/src/maktaba_pipeline/components/catalog.toml`,
  and `testdata/catalog.dump.json` is regenerated, as for any catalog change.

### 2. Rows on homes that are already seeded

Epic 51 writes desired rows once (`store.Seed`); a catalog id a later release adds has no row,
and no row reads as on. [52.17](../52-transcript-word-review/story-52-17-new-component-defaults.md)
adds the step that gives each new id a row as the home's bundle
says (`component.seed` audit row). With it, `logs.index` is `enabled` on every bundle and
`reports.share` is `installed`. This story ships after 52.17, or ships 52.17's step with it.

### 3. Words

| Key | English | Arabic |
|---|---|---|
| `components.logs.index.title` | Log search | البحث في السجلات |
| `components.logs.index.desc` | Searches the home's logs, groups its errors and keeps the dashboards' history. Logs are kept on disk even when it is off. | يبحث في سجلات الجهاز الرئيسي، ويجمع أخطاءه، ويحفظ سجل لوحات المعلومات. تبقى السجلات محفوظة على القرص حتى عند إيقافه. |
| `components.reports.share.title` | Problem reports to Maktaba | بلاغات المشكلات إلى مكتبة |
| `components.reports.share.desc` | Sends the problem reports people choose to share to Maktaba, through the cloud link. | يرسل إلى مكتبة بلاغات المشكلات التي يختار الأشخاص مشاركتها، عبر الربط السحابي. |

The `catalog` job fails when a `title_key` or `desc_key` has no English or Arabic words
(`tools/ci/catalog_words.mjs`).

### 4. The status groups, checked

`cmd/maktaba-server/internal/supervisor/statusmap.go` (53.03) maps catalog ids to status groups.
A new test, `statusmap_catalog_test.go`, beside `catalog_roles_test.go`:

- every component with `processes` is in exactly one group, or in an explicit "no group" list
  with a reason (`ollama/` of `llm`: it has no viewer feature of its own);
- every id the table names exists in the catalog;
- a component's `features` (Epic 51's capabilities words) appear in its group's `features_down`.

A later epic that adds a process component fails this test until it picks a group.

## Acceptance criteria

- **Given** this catalog and the `logs` role,
  **when** `make catalog-check` and the supervisor's tests run,
  **then** they pass, and `ByProcess("logs", "")` answers `logs.index`.
- **Given** `api/main.go` declares `reports_forwarder`,
  **when** the loop check runs,
  **then** it passes, and `ByLoop("api", "reports_forwarder")` answers `reports.share`.
- **Given** a Core home seeded on the previous release,
  **when** it updates,
  **then** `logs.index` is `enabled` and running, and `reports.share` is `installed` (off).
- **Given** an admin turns `logs.index` off (`PUT /api/components/logs.index`, `want = installed`),
  **then** the `logs` process stops within its stop grace, `/status.json` says `logs: off`, and segments keep growing.
- **Given** a new process component with no status group,
  **when** the supervisor's tests run,
  **then** `statusmap_catalog_test.go` fails and names it.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go, Python) | the catalog | load; `ByProcess`, `ByLoop` | the two ids; the Go and Python dumps equal |
| T02 | unit | `catalog_words.mjs` | the four keys | English and Arabic present |
| T03 | unit | `statusmap_catalog_test.go` | the catalog, and one with an extra process component | pass; fail naming it |
| T04 | integration | reconciler + a stand-in `logs` role | `want` enabled → installed → enabled | runs, stops, runs; status rows of part `supervisor` |
| T05 | update rehearsal | a Core and a Full fixture home on the previous release | update | rows as §2 says, one `component.seed` audit row |

## Edge cases

- **`[components] reconcile = "off"`:** the `logs` role runs as `server.toml` builds it, as every
  process does then; its status says `reconcile_off`.
- **A machine joined to the home** gets its own `logs` role; a per-machine override
  (`PUT /api/components/logs.index/machines/{node}`) can turn it off there only.
- **SQLite homes:** the same entries; desired state lives in the SQLite twins of the tables.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml`, `pipeline/src/maktaba_pipeline/components/catalog.toml` (via `make catalog-sync`) | §1. |
| `shared/components/testdata/catalog.dump.json` | Regenerated. |
| `web/src/i18n/en.json`, `ar.json` | §3. |
| `cmd/maktaba-server/internal/supervisor/statusmap_catalog_test.go` (new) | §4. |
| `cmd/maktaba-server/internal/componentseed/` | Only if 52.17 has not landed: its new-id seed. |

### Tests

T01–T04 in the Go, Python and web unit jobs; T05 in `update-rehearsal`.

### Rollout

Lands before or with 53.09 (the role) and 53.16 (the loop): the catalog entry and the code it
names must be in the same release, or the `catalog` job fails.

### Rollback

Revert with the role and the loop. A home that had rows for the two ids keeps them; the revert
must check that the runners and `GET /api/components` skip a row whose id the catalog no longer
has (51.18's rollback path is the place to prove it).

### Dependencies

Epic 51 (51.01, 51.04, 51.05; built). Epic 52.17 (rows for new ids). Needed by 53.03 (the groups'
check), 53.05 (`logs.index` runs in safe mode), 53.09, 53.16.
