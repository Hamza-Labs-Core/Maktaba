# Epic 51 — Every part of Maktaba can be turned on, off, added later or left out

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/51-configurable-components/`](../../../specs/epics/51-configurable-components/README.md).
> **Anchors:** [research](../../../specs/epics/51-configurable-components/research.md) · [Epic 31](../../../specs/epics/31-multi-instance/README.md) (machines) · [Epic 40](../../../specs/epics/40-queue-health/README.md) (queue health) · [Epic 42](../../../specs/epics/42-apps-player-remote-server/README.md) (editions) · [update rehearsal](../../update-rehearsal.md).

## Goal

The owner, 2026-11-03: *"currently all workers and features work right away, we need all to be
config, setting, whatever based, so that it can be stopped/started, enabled/disabled, even
server bundles, with and without components, ability to add later and so on."*

Every worker, background loop, feature, integration, runtime and model becomes a
**component** in one catalog. Admins say what they want; every machine reports what is true
and closes the gap live, without restarting the server and without losing a job. Setup offers
**Core**, **Full**, **Worker machine** or **Custom**. Anything left out can be downloaded later
as a signed pack. The apps hide features that are off from viewers and explain them to admins.
Existing homes keep running exactly what they run today.

## The model in one table

| | Values |
|---|---|
| Kinds | `process` (supervised OS process) · `stages` (pipeline stage group) · `loop` (goroutine / task) · `extension` · `asset` (model, runtime, tool) |
| Desired (admin) | `want` = absent / installed / enabled · `hold` = stopped until a time or until started |
| Actual (each machine) | phase: Not installed · Installing · Off · Stopped · Waiting · Starting · Running · Degraded · Draining · Failed, plus conditions with reason codes |
| Dependencies | `requires` (hard; dependents wait, keep their wish) · `wants` (soft; degraded) · `conflicts` |
| Stopping | stop claiming ≤ 2 s → drain (30 s; transcription 120 s) → force-pause what is left → keep the queue, don't feed; enable = resume + catch-up |
| Bundles | Full (today) · Core (no ML, no LLM; a smaller Mac dmg) · Worker machine · Custom |
| Packs | per-platform `.tar.zst` + signed `components-<version>.json`, same Sigstore identity as releases, lockstep with the server version; offline file import |

## Stories

| # | Story | Phase |
|---|---|---|
| 51.01 | [The component catalog](../../../specs/epics/51-configurable-components/story-51-01-catalog.md) | 1 |
| 51.02 | [Desired and actual state in the database](../../../specs/epics/51-configurable-components/story-51-02-state-tables.md) | 1 |
| 51.03 | [Existing installs keep running what they run](../../../specs/epics/51-configurable-components/story-51-03-seed-from-today.md) | 1 |
| 51.04 | [The supervisor reconciles process components](../../../specs/epics/51-configurable-components/story-51-04-supervisor-reconciler.md) | 1 |
| 51.05 | [Go loops start and stop from the catalog](../../../specs/epics/51-configurable-components/story-51-05-go-loop-runner.md) | 1 |
| 51.06 | [Pipeline stage groups stop without losing work](../../../specs/epics/51-configurable-components/story-51-06-pipeline-stage-groups.md) | 1 |
| 51.07 | [Extensions become components](../../../specs/epics/51-configurable-components/story-51-07-extensions.md) | 1 |
| 51.08 | [Admin API and CLI, with roles enforced](../../../specs/epics/51-configurable-components/story-51-08-admin-api-cli.md) | 2 |
| 51.09 | [Capabilities and the `component_off` answer](../../../specs/epics/51-configurable-components/story-51-09-capabilities.md) | 2 |
| 51.10 | [The Components page](../../../specs/epics/51-configurable-components/story-51-10-components-page.md) | 2 |
| 51.11 | [Apps hide or explain what is off](../../../specs/epics/51-configurable-components/story-51-11-clients.md) | 2 |
| 51.12 | [Per-machine components](../../../specs/epics/51-configurable-components/story-51-12-per-machine.md) | 2 |
| 51.13 | [Component settings: validated, versioned, kept](../../../specs/epics/51-configurable-components/story-51-13-settings.md) | 2 |
| 51.14 | [Health, metrics and the Queues page](../../../specs/epics/51-configurable-components/story-51-14-health.md) | 2 |
| 51.15 | [Packs and the signed component index](../../../specs/epics/51-configurable-components/story-51-15-packs-index.md) | 3 |
| 51.16 | [Install, remove and offline import](../../../specs/epics/51-configurable-components/story-51-16-install-remove.md) | 3 |
| 51.17 | [Bundles: the Core payload and the setup choice](../../../specs/epics/51-configurable-components/story-51-17-bundles.md) | 3 |
| 51.18 | [Updates and rollback carry packs](../../../specs/epics/51-configurable-components/story-51-18-update-rehearsal.md) | 3 |
| 51.19 | [Licence gating](../../../specs/epics/51-configurable-components/story-51-19-licence.md) | 3 |

## Key numbers

| Measure | Target |
|---|---|
| A change seen by every machine | p95 ≤ 2 s, ≤ 30 s worst case |
| Loop / stage component on | p95 ≤ 5 s; process component ≤ 15 s plus warm-up |
| Jobs lost, failed or cancelled by a disable | 0 (CI chaos job, 100 flips under load) |
| Core footprint after setup | ≤ 45 % of Full's and ≤ 400 MB; ≤ 700 MB idle RAM; 4 processes |
| Existing installs after the update | the same processes, stages, extensions and loops (rehearsal diff) |

## Hard calls (details in the README §9)

Desired state in Postgres and actual state per machine (not `server.toml`). Enable and Stop are
separate, and a Stop survives restarts. Dependents wait, they don't get disabled. Queues are
held and caught up, never cancelled. Packs move with the server version and are signed by
the release identity. Settings stay where they are and gain versions. Payload flavours only
where payloads differ (two Mac dmgs, two Linux packages, one Windows installer). No new paywalls.
