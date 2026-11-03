# Story 50.28 — Viewer analytics and broadcast monitoring

> Epic 50 · Phase B · Depends on: 50.10, 50.21, Epic 53.17 · Needed by: the broadcasting gate

## Why

Publishers want to know who watches: how many, when, from where and on what. Maktaba needs to
meter viewer-hours, check its costs, and know before publishers do when broadcasting breaks.
Epic 53 owns logs and application metrics (53.01, 53.12, 53.17), and 50.10 owns probes and
alerts. This story connects the edge to both, keeps viewer data aggregate, and adds a canary
broadcast.

## Description

### 1. Events

The edge writes Workers Analytics Engine events to the dataset `broadcast_events`:

| Event | Blobs (strings) | Doubles |
|---|---|---|
| `play` | publisher, channel, country, device class (web, iOS, tvOS, Android, Android TV, desktop), access (invite or open), mode | startup ms |
| `beat` | publisher, channel, country, device class, rung | minutes = 1, rebuffer ms, bitrate kbps |
| `switch` | publisher, from, to | switch ms |
| `error` | publisher, channel, code | — |
| `blocked` | publisher, channel, reason (geo, full, ip, revoked) | — |
| `ingest` | publisher, channel, rung | segment bytes, upload-to-visible ms, glass-to-glass ms (canary only) |

The session is a salted hash used only for distinct counts. There is no IP, no pass id and no
invite label.

### 2. Rollups and metering

- The cloud worker queries the Analytics Engine SQL API every hour, writes
  `broadcast_usage_hourly (publisher, channel, hour, viewer_minutes, sessions, peak_concurrent,
  countries jsonb, devices jsonb, rebuffer_ratio, errors)`, and keeps it 13 months.
- **Metering truth** is `PublisherControl`'s count (50.20 §3). The hourly rollup is a
  cross-check, and a difference over 2 % in a day raises a SEV3.
- **Folding small counts:** any country or device with fewer than 5 sessions in the shown period
  goes into "other" (`broadcast.analytics.k = 5`).

### 3. What the publisher sees

On the Broadcast page (home) and the cloud web app:

- live viewers now;
- peak concurrent viewers;
- viewer-hours (today, 7 days, this month, against the allowance);
- average watch time;
- countries (folded) and devices;
- quality (the share of time per rung) and the rebuffer ratio;
- errors by code;
- blocked attempts by reason.

Times are in the publisher's time zone. Export as CSV.

### 4. Logs and metrics (Epic 53)

- The edge logs in the 53.01 schema (`service = broadcast-edge`, hashed ids, no IPs). Workers
  Logs are forwarded by Logpush to the witness W, kept 14 days (53.17's model).
- The cloud API emits, on `/metrics` (53.17 defines them; 50.10 scrapes them and owns the
  alerts):
  - `maktaba_broadcast_channels_live`;
  - `maktaba_broadcast_viewers`;
  - `maktaba_broadcast_viewer_minutes_total`;
  - `maktaba_broadcast_ingest_lag_seconds` (a histogram);
  - `maktaba_broadcast_revocation_list_age_seconds`;
  - `maktaba_broadcast_edge_cost_estimate_usd` (from the cost model in README §14.4, checked
    against the Cloudflare bill monthly).
- **Alerts in 50.10:**
  - `broadcast_canary_down` (2 failed probes in a row, pages);
  - `broadcast_ingest_lag_high` (p95 > 3 s for 10 min);
  - `broadcast_revocation_list_stale` (> 10 min);
  - `broadcast_cost_80pct` (the month's estimate passes 80 % of `broadcast.monthly_budget_usd`).

### 5. The canary broadcast

- The canary home (50.10) publishes the `canary` publisher's channel, invite-only and always-on,
  from a test pattern with a burned-in UTC clock and frame counter.
- An outside probe on the two networks 50.10 already uses plays it every 60 s:
  1. redeem the canary pass;
  2. fetch the master and the rung playlists;
  3. fetch the last 2 segments;
  4. decode one frame and read the burned clock (OCR of a fixed-font clock, or the
     `PROGRAM-DATE-TIME` as a fallback).
- It records: success, startup ms, glass-to-glass seconds, and segment age.
- A minute counts as down when both networks fail. That feeds the **broadcast availability SLO**
  (README §4.2, 99.9 %).
- The status page (50.11) gains a **Broadcasting** component driven by it.

## Acceptance criteria

- **Given** 12 sessions from Egypt and 2 from Germany in a day,
  **when** the publisher opens analytics for that day,
  **then** the page shows Egypt 12 and Other 2, and Germany isn't named.
- **Given** the hourly rollup and `PublisherControl` differ by 3 % in a day,
  **when** the daily check runs,
  **then** a SEV3 is raised.
- **Given** the canary edge path fails from both networks twice in a row,
  **when** the alert evaluates,
  **then** the owner is paged within 90 s, and the status page shows Broadcasting degraded.
- **Given** the canary plays normally,
  **when** the probe reads the burned clock,
  **then** glass-to-glass is recorded, and its p95 over a day is ≤ 20 s.
- **Given** a user who isn't a home admin,
  **when** they ask for broadcast analytics,
  **then** the API answers `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (edge) | event writer | each event | the fields in the table; no IP, pass id or label |
| T02 | unit (Go) | rollup with a fake AE SQL API | an hour of events | rows right; folding at k = 5 |
| T03 | unit | cross-check | 1 % and 3 % differences | quiet / SEV3 |
| T04 | unit | probe | good, stale segment, wrong clock, network fail | success / fail / latency recorded / fail |
| T05 | unit | alert rules (promtool) | synthetic series | each alert fires as specified |
| T06 | integration | cloud API + fake edge | metrics endpoint | the `maktaba_broadcast_*` series present |
| T07 | role test | home and cloud APIs | non-admin | `403` |
| T08 | Arabic | analytics strings | check | present |
| T09 | rehearsal (CI, 50.29 `broadcast-observe`) | home + edge + probe | break ingest, stale the list | the alerts fire; the status component changes |

## Edge cases

- **The Analytics Engine SQL API is down.** The rollup retries the hour later. Metering isn't
  affected, because its truth is in `PublisherControl`.
- **AE sampling at high volume.** AE may sample, so counts use `_sample_interval` weighting.
  Metering still comes from the DO.
- **A publisher with one viewer.** Everything is "other" except totals. Totals are shown.
- **The canary home is down for maintenance.** It follows 50.10's planned-work silence.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.analytics.title` | Viewers | المشاهدون |
| `broadcast.analytics.live` | Watching now | يشاهدون الآن |
| `broadcast.analytics.peak` | Most at once | الأعلى في وقت واحد |
| `broadcast.analytics.hours` | Viewer-hours | ساعات المشاهدة |
| `broadcast.analytics.avgWatch` | Average watch time | متوسط مدة المشاهدة |
| `broadcast.analytics.countries` | Countries | البلدان |
| `broadcast.analytics.other` | Other | أخرى |
| `broadcast.analytics.quality` | Quality watched | الجودة المشاهَدة |
| `broadcast.analytics.privacy` | Places and devices with fewer than 5 viewers are grouped as "Other". We don't keep viewers' addresses or identities. | تُجمع الأماكن والأجهزة التي يقل مشاهدوها عن 5 تحت "أخرى". لا نحتفظ بعناوين المشاهدين أو هوياتهم. |
| `status.component.broadcasting` | Broadcasting | البث |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/packages/broadcast-edge/src/events.ts` | AE events. |
| `cloud/internal/broadcast/rollup.go` (new), `metrics.go` | Rollups, cross-check, metrics (with 53.17's names). |
| `cloud/migrations/` (new) | `broadcast_usage_hourly`. |
| `deploy/cloud-relay/monitoring/` (50.10's Prometheus and Alertmanager rules) | Broadcast alerts. |
| `deploy/cloud-relay/probes/broadcast_probe` (new, 50.10's probe set) | The canary viewer probe. |
| 50.11's status component config | Broadcasting. |
| `web/src/pages/LiveTv/BroadcastAnalytics.tsx`, `web/cloud/src/pages/BroadcastAnalytics.tsx`, i18n | The publisher's analytics. |

### Tests

- T01–T08 in the edge and cloud unit jobs and the monitoring rules check.
- **CI only:** rehearsal `broadcast-observe` (50.29).

### Rollout

Events and rollups from 50.21's first staging deploy. The canary broadcast before the
broadcasting gate (14 days at the SLO is gate item 7).

### Rollback

Analytics are read-only. Turning off the rollup job leaves metering untouched.

### Dependencies

50.10 (probes, alerts, the canary home), 50.21 (the edge), 53.17 (log and metric conventions).
