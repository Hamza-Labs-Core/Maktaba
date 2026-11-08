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
- **Only an allow-list** of series (`shared/logkeeper/go/logindex/dashboard-series.json`, about
  200 series), so a new high-cardinality metric can't grow the store. Label sets per series are
  capped at 50 (`label_cap`); a sample past a cap is not stored and is counted in
  `maktaba_metric_dropped_total`.
- 7 days at 1 minute, **30 days** at 1 hour (shortened by the retention decision of
  2026-11-03, to match the errors' 30 days). About 200 × 1,440 rows a day: a few MB, inside
  `logs.db`'s share of the 1 GB cap.
- Error counts per component come from 53.10's hours, not from metrics.
- The allow-list includes Epic 51's component series, which the supervisor's `/metrics` already
  serves (merged into the API's; story 51.14): `maktaba_component_up{id,machine,phase}`,
  `maktaba_component_restarts_total{id,machine}` and
  `maktaba_component_transition_seconds{id,to}`. `id` is a catalog id, bounded by the catalog
  (45 ids today). The home's `/metrics` carries every joined machine's component and host series
  too, each labelled with its `machine`, so an `id` series is capped per machine: up to 200 label
  sets for each machine (`id_label_cap`), for up to 50 machines (the `label_cap`); a 51st machine's
  series are dropped and counted like any other.

### 2. The dashboards (`/admin/dashboards`)

| Board | Panels |
|---|---|
| **Overview** | the 53.03 states now; errors per hour by component (14 d); components by phase over time and restarts per component (from `maktaba_component_*`), with turn-off and hold times marked from the `components` audit rows (`component.disable`, `component.hold`, …); requests per minute and p95 latency (API); CPU, memory, disk free of the home and each machine (many machines: below) |
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

#### The Overview with many machines

Each joined machine adds its load, memory, disk free and its components (labelled
`machine=<node id>`), so past a few machines a line per machine does not fit a board's 50
lines. The Overview of all machines draws instead:

- totals across the machines (with two or more): components up and down, the least disk free
  and memory free of any machine, the highest load, restarts per minute;
- the **five machines most in need of attention now** (a joined machine that is offline
  first, then most components down, then least disk free, then the highest load per CPU, by
  their latest values): their components down, load, disk free and memory free;
- "Showing 5 of 37 machines, those most in need of attention first", the offline machines
  named ("Offline: …", and marked in the picker), and a machine picker: one machine on its own
  shows its components up and down, load, CPUs, memory and disk, and a line for each component
  that was not up all the time or that restarted (an offline one says so: its lines end at its
  last values).

Which machines are offline is the cluster's to say, by the Machines page's own rule: an
approved machine is offline when neither it nor any of its workers was seen within the last
60 s. The API asks the cluster and passes the list to the log index (`offline=`); a caller's
own `offline=` never reaches it. The store alone cannot say it: a machine that stopped sending
keeps its last values there (by them it ranked last), and a machine removed from the cluster
looks the same as one that is offline. An offline machine the store has no lines of is still
counted, named and shown, with no lines.

Each machine is shown by its name, as the Machines page shows it (a joined machine's name,
the home's own name for `home`), in the picker, the Offline line and the chart legends; the
picker keeps the node id as a hint ("Attic NAS (n07)"), and the node id stays the key
(`machine=`). The API names them from the cluster; the log index knows only ids. A machine
with no name, or every machine when the cluster can't be read, is shown by its node id.

The log index sums each machine's components up, its components and its restarts, and the
totals across machines, as it writes a scrape (`maktaba_machine_*{machine}`,
`maktaba_machines_*`), so the board reads at most a line per machine, and the full lines only
of the machines it shows. Summing every component's rows when the board is asked took seconds
for 50 machines of 45 components over 30 days; read this way the Overview and one machine's
view answer in tens of milliseconds (`dashboard_30d_machines` in the performance run, budget
500 ms p95).

### 3. API

Admin only: `GET /api/admin/dashboards/{board}?range=` returns the board's series in one call
(≤ 50 series, ≤ 2,000 points each, downsampled by the role). On the Overview,
`machine=<node id>` answers that machine's lines on their own; the Overview's answer names the
machines (`machines`: `total`, `shown`, `all`, `offline`), `names` (`{<node id>: <name>}`,
only the ids the answer mentions; a name is the user's text, shown and nothing else: control
and bidirectional override characters dropped, at most 64 characters), and `omitted` counts
the lines the 50 left out. Role tests (the `machine=` query included).

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
| `dash.machine` | Machine | الجهاز |
| `dash.machine.all` | All machines | كل الأجهزة |
| `dash.machines.shown` | Showing {shown} of {total} machines, those most in need of attention first. Pick a machine to see its own. | عرض {shown} من {total} أجهزة، الأحوج إلى الانتباه أولاً. اختر جهازاً لترى بياناته وحده. |
| `dash.omitted` | {count} more lines did not fit on this board. | {count} خطوط أخرى لم تتسع لها هذه اللوحة. |
| `dash.machines.offline` | Offline: {machines}. Offline machines come first; their lines end at their last values. | غير متصلة: {machines}. تأتي الأجهزة غير المتصلة أولاً، وتنتهي خطوطها عند آخر قيمها. |
| `dash.machine.offline` | This machine is offline. Its lines end at its last values. | هذا الجهاز غير متصل. تنتهي خطوطه عند آخر قيمه. |
| `dash.machine.offlineOption` | {machine} (offline) | {machine} (غير متصل) |
| `dash.machine.option` | {name} ({id}) | {name} ({id}) |

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
| T08 | unit | 60 machines, a few in trouble | Overview; `machine=` | totals, the five most in need of attention in order, ≤ 50 lines, the machine count; one machine's own lines |
| T09 | unit (vitest, Apple, Android) | an Overview of 37 machines | render; pick one | "Showing 5 of 37", the picker asks `machine=`, en/ar |
| T10 | unit | the 60 machines, one well but offline and one offline with no lines | Overview with `offline=` | both shown first, counted and named in `offline`; bounds kept (≤ 1,000 ids, each ≤ 128 bytes) |
| T11 | unit (API, cluster) | approved machines seen now, 5 min ago, never; pending and revoked ones | Overview | the role is asked `offline=` with the two not seen, a caller's own dropped; admins only |
| T12 | unit (vitest, Apple, Android) | an Overview with offline machines | render; pick one | "Offline: …", "(offline)" in the picker, the one machine's offline line, en/ar |
| T13 | unit (API, cluster, vitest, Apple, Android) | machines with names, one without, a 200-character name, the cluster down | Overview | `names` for the ids mentioned only, the name bounded, the id when there is none; the picker, Offline line and legends show names, the picker the id as a hint, en/ar |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/logindex/metrics.go` (new) | Scraper, rollups, retention. |
| `shared/logkeeper/go/logindex/dashboard-series.json` (new) | The allow-list, with its `label_cap` and `id_label_cap`. |
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
