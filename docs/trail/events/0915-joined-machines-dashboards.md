---
schema: 1
kind: pr
pr: 915
title: Epic 53 story 53.12 — the home's dashboards carry every joined machine
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [905]
---

## What happened
- The home's dashboards showed only the home, because each machine's log index scrapes only its own endpoints. The home's API admin `/metrics` now asks every joined machine's supervisor agent for its metrics. It uses the existing cluster channel (a new `metrics` command), which also reaches machines behind NAT. It asks all machines at once, waits at most 2 s, and caches one round for 10 s.
- From a machine it takes only the `maktaba_component_*` and `maktaba_host_*` series, with their `machine` label set to the machine's node id. A machine that is slow, old or offline adds nothing. The hub's event log skips the timed `metrics` command.
- The component series' label cap now counts per machine (200 each, at most 50 machines). Board series with many lines come last, so each machine's memory, disk and load fit in a board's 50 lines.
- **Security review, fixed before merge:** at first the home took any `maktaba_*` family from a machine, and kept a series the machine sent twice. That let one machine repeat a family or series the home has, and fail the home's whole scrape. Now only the two per-machine prefixes are taken, a label set that repeats after the rewrite is kept once, and `/metrics` serves what it can when one source fails (`ContinueOnError`).

## What we learned
- To reach a joined machine, use its supervisor's agent on the cluster channel (`MachineAgents` plus `CommandWithin`): no new route or port, and NAT is already handled.
- A command asked on a timer has to stay out of the hub's event log.
- `prometheus.Gatherers` asks its sources one after another, so two 2 s sources can take 4 s. Fetch them side by side and merge.
- Treat another machine's metrics as untrusted input: allow only the families you need, remove repeated series after rewriting labels, and don't let one bad family fail the scrape.

## Open
- Spec 53.12 still describes the component cap as the catalog's size. It is now that size per machine; changing the spec means updating its Arabic too.
- With several machines, the Overview's 50 lines leave out component lines. A per-phase rollup may be needed.
