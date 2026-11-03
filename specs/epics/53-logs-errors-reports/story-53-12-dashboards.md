# Story 53.12 — Dashboards and home analytics

> Epic 53 · Phase 2 · Depends on: 53.09, Epic 51 (51.14 component metrics, built) · Needed by: 53.13

## Why

The services export Prometheus metrics (`/metrics` on the API admin port with the supervisor's
update metrics; the pipeline's own exporter), but nothing keeps their history. The queue tiles
(Epic 40) show now, not last week. Plex puts bandwidth, resources and history behind Plex Pass and
people still install Tautulli. Maktaba needs its dashboards built in.

## Description

### 1. Rollups (README D9)

The `logs` role scrapes every local `/metrics` endpoint every **15 s** (API, streaming, pipeline
workers, supervisor; the port registry lists them) and the host's CPU, memory, disk and network
(the supervisor's `hostinfo`). It keeps:

```sql
CREATE TABLE metric_1m (name TEXT, labels TEXT, minute INTEGER, value REAL, PRIMARY KEY (name, labels, minute));
CREATE TABLE metric_1h (name TEXT, labels TEXT, hour INTEGER, value REAL, min REAL, max REAL, PRIMARY KEY (name, labels, hour));
```

- Counters become rates per minute; gauges keep the last value (1 min) and min/max/avg (1 h).
- **Only an allow-list** of series (`shared/metrics/dashboard-series.json`, about 200 series),
  so a new high-cardinality metric can't grow the store. Label values per series are capped at 50.
- 7 days at 1 minute, **30 days** at 1 hour (shortened by the retention decision of
  2026-11-03, to match the errors' 30 days). About 200 × 1,440 rows a day: a few MB, inside
  `logs.db`'s share of the 1 GB cap.
- Error counts per component come from 53.10's hours, not from metrics.
- The allow-list includes Epic 51's component series, which the supervisor's `/metrics` already
  serves (merged into the API's; story 51.14): `maktaba_component_up{id,machine,phase}`,
  `maktaba_component_restarts_total{id,machine}` and
  `maktaba_component_transition_seconds{id,to}`. `id` is a catalog id, so its label values are
  bounded by the catalog (45 ids today); the per-series cap of 50 does not apply to `id`, whose
  cap is the catalog's own size.

### 2. The dashboards (`/admin/dashboards`)

| Board | Panels |
|---|---|
| **Overview** | the 53.03 states now; errors per hour by component (14 d); components by phase over time and restarts per component (from `maktaba_component_*`), with turn-off and hold times marked from the `components` audit rows (`component.disable`, `component.hold`, …); requests per minute and p95 latency (API); CPU, memory, disk free of the home and each machine |
| **Queue** | jobs done and failed per hour per stage; queue depth per stage; time in queue p50/p95; per machine throughput (from `job_events` and the queue metrics) |
| **Processing** | transcription hours of audio per hour and real-time factor; thumbnails per hour; MP4 conversions and dedup (Epic 47); failures by class (Epic 40) |
| **Streaming** | sessions now and per hour, direct play vs transcode, transcode real-time factor, bandwidth out (LAN, relay), TV channels on air and their restarts |
| **Storage** | library size by library, cache and transcode folders, backups' size and age, log segments and index size, free space per disk, growth per week |
| **Use** | plays per day by app and by library, active people per day (counts, from Epic 29/44's `watch_sessions` and the per-app API counts), top videos and series this week, sign-ins by app version |

- Each panel: hover values, the time range picker shared across the board (1 h, 24 h, 7 d,
  30 d, 90 d), "Open in Logs" for the same window and component.
- Charts are SVG with the design system's tokens (dark and light), numbers in the admin's locale,
  right to left in Arabic with time still running left to right on the x axis (standard for
  Arabic charts), and every panel has a text summary for screen readers.
- **Use** shows counts and names only to admins; it never shows one person's history (the
  existing activity page and Profile do that, under their own rules).

### 3. API

Admin only: `GET /api/admin/dashboards/{board}?range=` returns the board's series in one call
(≤ 50 series, ≤ 2,000 points each, downsampled by the role). Role tests.

### 4. On the Remote apps

Overview and Storage on the Remote apps (Apple, Android), read-only.

### 5. Strings

| Key | English | Arabic |
|---|---|---|
| `dash.title` | Dashboards | لوحات المتابعة |
| `dash.overview` | Overview | نظرة عامة |
| `dash.queue` | Queue | قائمة المهام |
| `dash.processing` | Processing | المعالجة |
| `dash.streaming` | Streaming | البث |
| `dash.storage` | Storage | التخزين |
| `dash.use` | Use | الاستخدام |
| `dash.openLogs` | Open in Logs | فتح في السجلات |
| `dash.noData` | No data for this time yet | لا توجد بيانات لهذه الفترة بعد |

## Acceptance criteria

- **Given** a week of running,
  **when** the admin opens Queue with 7 d,
  **then** each stage's done/failed per hour shows, and the numbers match `job_events` counts within 1 %.
- **Given** a new metric with 10,000 label values,
  **then** it isn't stored (not in the allow-list), and the store's size doesn't change.
- **Given** the board API,
  **then** it answers in ≤ 500 ms p95 for 90 d.
- **Given** a viewer,
  **then** every dashboard route answers 403.
- **Given** Arabic,
  **then** labels and summaries are Arabic, the layout right to left, the time axis left to right.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fake `/metrics` text over time | scrape | rates, gauges, rollups right |
| T02 | unit | series not allowed / too many labels | scrape | dropped, counted in `metric_dropped_total` |
| T03 | unit | retention | sweep | 7 d / 30 d |
| T04 | unit | board API | each board | ≤ 50 series, downsampled |
| T05 | role test | nobody, viewer, admin | | 401, 403, 200 |
| T06 | unit (vitest) | board fixtures | render en/ar | text summaries, RTL |
| T07 | UI (**CI only**) | dark theme screenshots | | |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/logindex/metrics.go` (new) | Scraper, rollups, retention. |
| `shared/metrics/dashboard-series.json` (new) | The allow-list. |
| `api/internal/handlers/dashboards/` (new) | §3 (Use reads `watch_sessions` and the API counts from Postgres; the rest from the role). |
| `web/src/pages/Admin/Dashboards/` (new), `web/src/components/charts/` (new, SVG) | §2. |
| `apps/apple/Remote/`, `apps/android/remote/` | §4. |
| `web/src/help/content/{en,ar}/dashboards.md` | Help. |

### Tests

T01–T06 in unit jobs, T07 in CI UI jobs.

### Rollout

The scraper starts with the release; boards fill as data arrives ("No data for this time yet").

### Rollback

Revert the pages and the scraper; the tables stay in `logs.db`.

### Dependencies

53.09 (the role that hosts it); Epic 51 story 51.14 (the `maktaba_component_*` series). Needed by 53.13.
