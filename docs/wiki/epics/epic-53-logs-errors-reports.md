# Epic 53 — Logs, errors and problem reports, and a UI that never goes dark

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/53-logs-errors-reports/`](../../../specs/epics/53-logs-errors-reports/README.md).
> **Anchors:** [Epic 21](epic-21-observability.md) (the loggers and probes it builds on) · [Epic 50](epic-50-cloud-no-downtime.md) (cloud monitoring, status page, incidents) · [Epic 51](epic-51-configurable-components.md) (the component catalog and its runners, built) · [research](../../../specs/epics/53-logs-errors-reports/research.md) · [`docs/troubleshooting-logs.md`](../../troubleshooting-logs.md) · [`docs/audits/errors-logging-crashes.md`](../../audits/errors-logging-crashes.md).

## Goal

Every part of the home, the apps and the cloud must be reportable, and no crash in one part may
take the UI down with it.

Today the admin's tools for finding out what went wrong (Logs, System health) need the API and
Postgres, the parts most likely to have failed. All processes share one 50 MB `server.log`.
Crashes stay on the devices, and nobody can report a problem. This epic covers four things:

- every part logs the same way, to disk;
- the UI and a recovery page survive any one crash;
- people can report problems from every app;
- admins get search, error groups and dashboards inside Maktaba.

## Targets

| | |
|---|---|
| UI with any one part down (pipeline, a worker, streaming, Postgres, API, log index) | the web shell loads in 100 % of CI fault runs, ≤ 1 s first byte on the LAN |
| Banner after a part goes down | ≤ 15 s, in English and Arabic, in every app |
| Recovery console with Postgres or the API down | signed in ≤ 10 s |
| Log ingest | 500 lines/s sustained; 5,000/s burst for 60 s with 0 lines lost to disk |
| Search | filters p95 ≤ 300 ms; full text over 7 days / 15 M lines p95 ≤ 1.5 s; live tail ≤ 2 s |
| Retention | logs 7 days (debug 24 h), errors 30 days, reports 90 days; at most 1 GB in all; an admin can change each |
| Report size | app ≤ 512 KB (+ a screenshot ≤ 1 MB only if ticked), home ≤ 5 MB, to Maktaba ≤ 8 MB |
| Cost on the home Mac | ≤ 5 % of one core, ≤ 200 MB RAM, ≤ 1 GB disk |

## Key decisions

- **Logs go to files first.** The supervisor captures each child's output into per-service
  hourly segments, capped and rotated. Nothing else has to be running. Each line names its
  Epic 51 component (`component`, a catalog id).
- **The search index is a separate SQLite file** (FTS5) owned by a crash-isolated `logs` role.
  It is not in the library Postgres, so search works when Postgres is down, and logs stay out of
  backups and the cluster copy. Rejected:
  - Postgres partitions: they fail with the database.
  - Loki/Prometheus/Grafana: about 1.5–4 GB.
  - Self-hosted Sentry: 16 GB.
- **The recovery console is served by the supervisor**, with no SPA, API or Postgres.
  - Sign-in: a 7-day recovery pass the API gives admins while it is up, checked against cached
    JWKS keys, or a one-time link from the Mac's tray.
  - Actions: restart, safe mode, roll back, restore, download diagnostics.
- **Safe mode** (Home Assistant's idea) runs `core.postgres`, `core.api`, `core.updates`, the
  web and `logs.index` for one start. Every other component reports `off` with Epic 51's
  `boot_flag` reason; desired state is never written.
  Crash-loop cut-offs now cover the supervisor itself: the launcher shows a "can't start" page.
- **One status strip in every app**, fed by the home's `/status.json` and the cloud's
  `summary.json` (Epic 50.11).
- **Off is not down.** `/status.json`'s groups are sets of Epic 51 catalog ids. A component an
  admin turned off or held is `off`: no banner, no repair item. Switched-off features are
  Epic 51's capabilities to show (51.11), not the strip's.
- **Crash and report uploads use the Sentry envelope format** with our own senders:
  - no Google SDKs;
  - PLCrashReporter on tvOS, because MetricKit doesn't cover it;
  - `ApplicationExitInfo` for Android ANRs.
- **The home is the privacy boundary.** User ids are hashed in logs. Reports go to Maktaba only
  when the admin enabled it and the sender ticked the box for that report.
- **Crash reports to the home are off by default.** Each app asks during its initial setup and
  keeps a switch in Settings → Privacy. Anything that leaves the home stays opt-in.
- **Screenshots in reports are offered, off by default**, and shown in the preview before
  sending.

## Decisions (2026-11-03)

The open questions, answered:

1. Crash reports to the home: **off by default**, asked during onboarding, a Settings switch
   afterwards (53.14).
2. Retention: **shorter**. Logs 7 days, errors 30, reports 90, at most 1 GB in all, adjustable
   by an admin (53.02, 53.09–53.13, 53.15).
3. Screenshots in reports: **offered, off by default, shown in the preview** (53.15, 53.16).

Also: **a fault rehearsal in CI** kills each part and checks the UI, the banner and the
console. It gates releases like the update rehearsal.

## Phases

1. **Never in the dark:** 53.01–53.07, and 53.18 (the catalog entries).
2. **See everything:** 53.08–53.13 (request ids, index, error groups, admin pages, dashboards,
   performance).
3. **People can tell us:** 53.14–53.15 (crash reports, Report a problem).
4. **Beyond the home:** 53.16–53.17 (reports to Maktaba; the cloud on the same model, feeding
   Epic 50).

## Stories

| # | Story | Phase |
|---|---|---|
| 53.01 | [One log line for every part](../../../specs/epics/53-logs-errors-reports/story-53-01-log-schema.md) | 1 |
| 53.02 | [The log keeper: every component's log on disk](../../../specs/epics/53-logs-errors-reports/story-53-02-log-keeper.md) | 1 |
| 53.03 | [The home's status, without the API or the database](../../../specs/epics/53-logs-errors-reports/story-53-03-status-endpoint.md) | 1 |
| 53.04 | [The recovery console](../../../specs/epics/53-logs-errors-reports/story-53-04-recovery-console.md) | 1 |
| 53.05 | [Safe mode and crash-loop cut-offs](../../../specs/epics/53-logs-errors-reports/story-53-05-safe-mode.md) | 1 |
| 53.06 | [A status strip in every app](../../../specs/epics/53-logs-errors-reports/story-53-06-status-strip.md) | 1 |
| 53.07 | [The fault rehearsal in CI](../../../specs/epics/53-logs-errors-reports/story-53-07-fault-rehearsal.md) | 1 |
| 53.08 | [One request id from the app to the last process](../../../specs/epics/53-logs-errors-reports/story-53-08-request-ids.md) | 2 |
| 53.09 | [Log index, search and live tail](../../../specs/epics/53-logs-errors-reports/story-53-09-log-index.md) | 2 |
| 53.10 | [Error groups](../../../specs/epics/53-logs-errors-reports/story-53-10-error-groups.md) | 2 |
| 53.11 | [Admin Logs and Errors pages](../../../specs/epics/53-logs-errors-reports/story-53-11-admin-pages.md) | 2 |
| 53.12 | [Dashboards and home analytics](../../../specs/epics/53-logs-errors-reports/story-53-12-dashboards.md) | 2 |
| 53.13 | [Performance budget and load test](../../../specs/epics/53-logs-errors-reports/story-53-13-performance.md) | 2 |
| 53.14 | [Crash reports from every app](../../../specs/epics/53-logs-errors-reports/story-53-14-crash-reports.md) | 3 |
| 53.15 | [Report a problem](../../../specs/epics/53-logs-errors-reports/story-53-15-report-a-problem.md) | 3 |
| 53.16 | [Reports and errors to Maktaba](../../../specs/epics/53-logs-errors-reports/story-53-16-reports-to-maktaba.md) | 4 |
| 53.17 | [The cloud on the same model, feeding Epic 50](../../../specs/epics/53-logs-errors-reports/story-53-17-cloud-observability.md) | 4 |
| 53.18 | [Epic 53's parts in the component catalog](../../../specs/epics/53-logs-errors-reports/story-53-18-catalog-entries.md) | 1 |

The dependency graph is in the [epic README](../../../specs/epics/53-logs-errors-reports/README.md#8-stories).
53.01 and 53.18 can start at once.

## With Epic 51

Epic 51 is built. This epic uses its catalog ids, the supervisor's reconciler and its
desired-state cache (no Postgres needed), its reason codes, its health and metrics, and its
capabilities. What 51 does not provide is added here:

- **53.18** adds `logs.index` (the `logs` role, kind `process`) and `reports.share` (the
  forwarder loop, kind `loop`, requires `cloud.link`, off by default) to the catalog, and a test
  that every process component maps to one status group.
- **53.05** adds a boot-wide forced-off set to the reconciler for safe mode. 51 forces only the
  supervisor's own loops, and it always runs `core.*`.
- The console's **Restart** is the supervisor's own, for this boot only. 51 has no restart
  route and no timed hold.

## With Epic 50

Epic 50 keeps the infrastructure side:

- Prometheus, Alertmanager, Grafana;
- probes, the canary and paging;
- the status page and incidents.

Epic 53 owns what the processes log and report, at home and in the cloud. Story 53.17 gives the
cloud the shared schema, logs on each node forwarded to the witness, error groups, and the
application metrics that 50.10 alerts on (`cloud_error_spike`). 53.06's strip draws 50.11's
banner. Edges: 53.17 → 50.10, 53.06 → 50.11.

## Compared with Plex

Plex only lets you download logs, its alerts last one browser session, and people need Tautulli
for history. It uploads crashes on the next start. Maktaba:

- shows logs live and searchable;
- groups errors with counts;
- has dashboards over 30 days;
- sends crashes to the home only after the user opts in, and to Maktaba only with consent;
- keeps a recovery page that works when the database is broken (Home Assistant's model).
