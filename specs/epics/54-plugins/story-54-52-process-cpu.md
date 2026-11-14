# Story 54.52 — CPU of each process

> Epic 54 · Phase 5 · Depends on: 54.44, Epic 51 (51.14) · Not built

## Why

The Plugins page shows the memory of each process that runs a plugin and says that processor use is not measured. The supervisor samples memory only. An admin deciding which plugin to turn off on a slow machine needs both.

## Description

- The supervisor samples each running process's CPU time with its memory, every five minutes, on macOS, Linux and Windows, and reports the share of one core used since the last sample as `cpu_pct` beside `rss_mb`, and per process in `cpu_parts` beside `rss_parts`. The same hysteresis keeps an idle machine from rewriting rows.
- `GET /api/plugins` carries it on each process of `resources.processes`; the page shows it beside memory and drops the sentence that says it is not measured.
- A shared process's CPU is the whole process's, shown with the plugins that share it, as memory is.

## Tests

- The reconciler's rows carry `cpu_pct` and `cpu_parts` from a fake sampler, and an idle process rewrites nothing.
- The page shows a shared group's CPU with who shares it.
