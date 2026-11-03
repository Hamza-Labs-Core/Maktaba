# Story 51.14 — Health, metrics and the Queues page

> Epic 51 · Phase 2 · Depends on: 51.08 · Needed by: —

## Why

Once parts can be off, two kinds of "not running" must look different. A component that crashed
past its restart limit is a problem. A component the admin turned off is a choice. Today Epic
40's queue health has three tile states (`ok`, `waiting`, `failing`,
`api/internal/handlers/queues/queuehealth.go:18-23`), and jobs parked on anything count as
*waiting*, which the overview lists with the stages that need a look. With transcription off for
a month, its tile would sit amber and the overview would keep saying "1 stage waiting". This
story shows component health where admins look, gives monitoring the numbers, makes a parked
queue of an off component read as expected, and adds the bundle and enabled ids to the anonymous
usage report when the owner has opted in.

## Description

### 1. Health in `GET /api/components`

51.08 returns phase and conditions. This story fills the rest of README §4.13 from
`component_status.detail` (51.02), per machine and rolled up:

| Field | Source |
|---|---|
| `since` (uptime) | `component_status.since` when phase is `running` |
| `restarts_24h` | supervisor restart counter for `process` parts; loop runner's restart count for `loop` parts |
| `last_error_code`, `last_error_at` | the last `Failed` reason and when |
| `version` | `component_status.version` |
| `rss_mb` | `process` parts: the supervisor's per-process RSS (it already samples it for `/processes`); other kinds: none |
| `disk_mb` | assets and packs: size of `<data>/components/<id>/current` (51.16); models: the files' sizes from the catalog |
| `stale` | true when the row was not updated for 90 s |

The runners write these into `detail` when they report (≤ 4 KB, 51.02). Nothing new is
queried per request: the list route reads `component_status` once.

**System health page** (`/admin/health`, Epic 40): a new "Components" line: "All parts
running" or "1 failed: Transcription (crash loop)". Only `failed`, and `degraded` for more than
10 minutes, count as attention. `off`, `stopped` and `waiting` on a requirement the admin turned
off never do.

### 2. Supervisor `/metrics`

`cmd/maktaba-server/serve_metrics.go` adds, next to the update metrics, the components of
**this machine** (the supervisor reads its machine's `component_status` rows through the store,
once per scrape, cached 5 s):

```
# TYPE maktaba_component_up gauge
maktaba_component_up{id="transcription",machine="home",phase="running"} 1
# TYPE maktaba_component_restarts_total counter
maktaba_component_restarts_total{id="llm",machine="home"} 3
# TYPE maktaba_component_transition_seconds histogram
maktaba_component_transition_seconds_bucket{id="youtube",to="running",le="5"} 12
```

- `up` is 1 for `running` and `degraded`, 0 otherwise; the `phase` label lets a dashboard tell
  off from failed. Components with `want = absent` are not exported.
- `transition_seconds`: from the generation a runner acted on to the phase it reached, measured
  by the runner and written as `detail.last_transition = {to, seconds}`; buckets 0.5, 1, 2, 5,
  10, 15, 30, 60, 120 s (covers N2–N5).
- The API's `/metrics` already merges the supervisor's, so the home is one scrape target.
  Labels never carry user data; `machine` is `home` or the node id.

### 3. The Queues page: "waiting: transcription is off"

51.06 parks the queued jobs of an off component (README §4.5) with
`parked_on = 'component:<id>'`. Epic 40 already groups parked jobs by `parked_on` into
`StageHealth.Waiting` (`queuehealth.go:181`). This story:

- adds a fourth tile state **`off`** (`HealthOff`): a stage whose only waiting entries are
  `component:*` parks, or whose component is not enabled, with no failure signature. A
  `failing` signature still wins (a stage can fail and be off; that is shown as failing);
- adds `out.Off` beside `out.Waiting` and `out.Failing` in the overview, and leaves off stages
  out of `out.Waiting`, so "N stages waiting" and the attention counts don't include them;
- gives `WaitingOn` a `component` field (`"transcription"`) and the component's phase, so the
  tile can say **"Waiting: Transcription is off · 1,840 queued"** or **"Waiting: Transcription
  is stopped until you turn it on"**, with a link to `/settings/components`;
- shows a `component_catchup` job (51.06) on the Queues page with its progress after the
  component is turned back on, with the words "Catching up after Transcription was off".

The tile is grey (the `off` token), never amber or red. Words, English and Arabic together
(`web/src/i18n/en.json` / `ar.json`):

| Key | English | Arabic |
|---|---|---|
| `queue.state.off` | Off | متوقف |
| `queue.waiting.component_off` | Waiting: %s is off | في الانتظار: %s متوقف |
| `queue.waiting.component_held` | Waiting: %s is stopped until you turn it on | في الانتظار: %s متوقف حتى تشغّله |
| `queue.catchup` | Catching up after %s was off | استكمال ما فات بعد إيقاف %s |

The Remote apps' queue screens (Apple `QueueView.swift`, Android remote) read the same
`state` and words (51.11 adds them); they treat `off` as calm, not as attention.

### 4. Anonymous usage data, only with opt-in

The home's daily report (`api/internal/handlers/telemetry/report.go` `ServerReport`) is sent
only while the admin's switch `telemetry.share_server_data` is on (off by default, unchanged).
It gains two fields:

```json
{ "bundle": "full", "components": ["backups", "classify", "livetv", "transcription", …] }
```

- `bundle` is `component_meta.bundle` (51.02): `full`, `core`, `worker` or `custom`.
- `components` is the sorted list of catalog ids with `want = enabled` on the home. Ids of
  entry-point extensions (third-party, not in the catalog; Epic 54 plugins later) are left out: a
  name could identify a person. No counts, no machines, no media, no names.
- **The cloud first.** Maktaba Cloud's validator refuses unknown fields
  (`cloud/internal/telemetry/validate.go` `strict`, `DisallowUnknownFields`) and unknown
  feature keys. So the cloud ships first: `ReportServer` gains `bundle` (one of the four) and
  `components` (≤ 80 ids matching the catalog id pattern), stored in two new columns of
  `telemetry_server_reports`, and the usage page counts bundles and the share of homes per
  component. The home's sender, on a `400` naming either field, drops both and resends once,
  so a home on a new release never loses its report to an older cloud.
- Settings → Usage data (`web/src/pages/Settings/ServerUsageData.tsx`) lists the two new fields
  in "what is sent", in English and Arabic.

## Acceptance criteria

- **Given** `llm` crashed past its restart limit,
  **when** an admin opens System health,
  **then** the Components line says "1 failed: LLM (crash loop)" and counts as attention.
- **Given** transcription turned off by an admin with 1,840 jobs queued,
  **when** the Queues page loads,
  **then** the Transcribe tile is grey, says "Waiting: Transcription is off · 1,840 queued",
  links to Components, and the overview's waiting count does not include it.
- **Given** transcription is off and its stage also has a new failure signature over the
  threshold,
  **when** the Queues page loads,
  **then** the tile is red (failing wins).
- **Given** the supervisor's `/metrics`,
  **when** it is scraped,
  **then** it has `maktaba_component_up` for every non-absent component of that machine with a
  `phase` label, and `maktaba_component_transition_seconds` buckets.
- **Given** usage data is off,
  **when** the daily report time comes,
  **then** nothing is sent (unchanged).
- **Given** usage data is on,
  **when** the report is built,
  **then** it carries `bundle` and the enabled catalog ids, and no entry-point extension name.
- **Given** an older cloud that refuses `bundle`,
  **when** the home sends,
  **then** the home resends without the two fields and the report is accepted.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Go) | `stageHealth` with only `component:transcription` waiting | compute | `off` |
| T02 | unit (Go) | same plus failing signature | compute | `failing` |
| T03 | unit (Go) | overview with one off stage | compute | `Off` has it; `Waiting` doesn't |
| T04 | unit (Go) | status rows, one failed, one off | System health component line | attention for failed only |
| T05 | unit (Go) | supervisor metrics, fake store | scrape | `up`, `restarts_total`, histogram; no absent ids |
| T06 | unit (Go) | 100 scrapes in 1 s | scrape | one store read (5 s cache) |
| T07 | unit (Go) | report with entry-point `ext.someone` enabled | build | not in `components` |
| T08 | unit (Go) | sender, fake cloud answering 400 on `bundle` | send | second post without the fields; success |
| T09 | unit (cloud) | `validate.go` | report with `bundle: "core"`, 3 ids / bad id / 81 ids | ok / 400 / 400 |
| T10 | integration (cloud, Postgres) | store | save report | columns filled; usage page counts |
| T11 | vitest | Queue overview, off tile | render en and ar | grey tile, words, link; axe clean |
| T12 | vitest | Usage data settings | render | the two fields listed in en and ar |
| T13 | CI only | `components-live` | flip a stages component | parked count and `off` tile within 5 s; histogram has the transition |

## Edge cases

- **A stage shared by two concerns** doesn't exist: each stage has one component (51.01), so a
  tile maps to one component.
- **A component waiting on a requirement that failed** (not one turned off) is `waiting` with
  the requirement's failure shown, and the requirement's own `failed` is the attention item.
- **Nodes** export their own metrics; the home's System health shows every machine's failed
  components from `component_status`.
- **The report on a worker machine**: only the home sends a report (unchanged).
- **A cloud that is down**: the outbox retries as today (`telemetry_outbox`, a week).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `api/internal/components/health.go` (new) | Fill §1 fields from `detail`; staleness. |
| `api/internal/handlers/systemstatus/handler.go`, `types.go` | Components line and attention rule. |
| `cmd/maktaba-server/serve_metrics.go` | Component metrics beside the update metrics. |
| `cmd/maktaba-server/internal/supervisor/component_metrics.go` (new) | Read this machine's rows (cached), write the exposition. |
| `api/internal/handlers/queues/queuehealth.go` | `HealthOff`, `WaitingOn.Component`, `Off` in the overview. |
| `api/internal/handlers/queues/queuehealth_test.go` | T01–T03. |
| `api/internal/handlers/telemetry/report.go`, `sender.go` | `bundle`, `components`; drop-and-resend on a 400. |
| `api/internal/handlers/telemetry/sender_test.go` | T07, T08. |
| `cloud/internal/telemetry/validate.go`, `store.go`, `usage.go` | Accept, store and count the fields. |
| `cloud/migrations/00510001_telemetry_components.sql` (new) | Two columns on `telemetry_server_reports`. |
| `cloud/internal/telemetry/telemetry_test.go`, `store_integration_test.go` | T09, T10. |
| `web/src/pages/queue/QueueOverview.tsx`, `QueueDetail.tsx` | Off tile, words, catch-up line. |
| `web/src/pages/Admin/SystemHealth.tsx` | Components line. |
| `web/src/pages/Settings/ServerUsageData.tsx` | List the two fields. |
| `web/src/i18n/en.json`, `ar.json` | §3 and §4 words. |

### Tests

- T01–T08 in the Go `unit` job; T09, T10 in `relay.yml` (the cloud module's tests); T11, T12 in
  the web unit job.
- **CI only:** T13 in `components-live`.

### Rollout

The cloud change ships first (it is deployed on its own). The home part ships with Phase 2.

### Rollback

The `off` state is additive. The web ships in the same binary as the API, so the two never
mix outside an update's switch.
The two report fields are dropped by the sender's fallback, or by a rollback of the home.

### Dependencies

51.08 (the list route), 51.02 (`detail`, `bundle`), 51.06 (parking with
`parked_on = 'component:<id>'` and `component_catchup`), 51.04 (supervisor restart counts).
