# Plan 35.15 — Observability, docs and runbook — implementation

> Implementation plan for [story-35-15-observability-docs](story-35-15-observability-docs.md). Design: [design.md](design.md) §14, §16.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Update metrics live in the supervisor (run, steps, targets) and the API (checks, catalog). | Each process reports what it owns; the supervisor survives role restarts. |
| D2 | The supervisor's metrics are served on its loopback admin and scraped by the API's `/metrics` aggregation. | One scrape target per machine, as today for the other roles. |
| D3 | One updates doc for users, one runbook for operators. | Users need "what happens"; operators need "what to do". |
| D4 | The release notes print the exact identity. | The loose regexp in today's notes would accept a fork; humans should verify what the updater verifies. |

## 1. Files

**Create**
- `cmd/maktaba-server/internal/updater/metrics.go` — run/step/target metrics, reload from journal.
- `api/internal/cluster/updates/metrics.go` — check and catalog metrics; node delegate/proxy metrics in
  `api/internal/cluster/homeproxy/metrics.go`.
- `web/src/components/cluster/updates/ControlCentreUpdateCard.tsx` (+ test).
- `docs/updates.md`.
- `docs/cluster/runbook-updates.md`.

**Modify**
- `cmd/maktaba-server/internal/supervisor/eventlog.go` — update events in `events.jsonl`.
- `api/internal/system/aggregator.go` — `update.state`, `update.machines_behind` health samples;
  supervisor metrics aggregation.
- `docs/auto-update.md` — keep desktop/mobile; link to `docs/updates.md` for the server and cluster.
- `docs/cluster/README.md`, `docs/cluster/join-a-machine.md`, `docs/cluster/troubleshooting.md`.
- `deploy/packaging/README.md`, `deploy/packaging/upgrade.md`, `deploy/packaging/node/README.md`.
- `.github/workflows/release.yml` — release notes: exact SAN `https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/<tag>`, issuer, and the Sigstore bundle command.
- `docs/WIKI_SYNC.md` — new pages in the wiki sync list.

## 2. Steps

1. Metrics and events in the supervisor and the API; health samples.
2. Control-centre card.
3. `docs/updates.md` from the README's settings and decisions (same names and defaults).
4. Cluster docs for full nodes and conversion.
5. Runbook: one entry per design §14 row, then the manual procedures, then known issues from 35.14.
6. Packaging docs and release notes identity.
7. Link check in CI.

## 3. Tests

Story TC1–TC6.

## 4. Rollout

Metrics and events land with each story that emits them (phase B on); the docs and runbook close phase
E and are reviewed against a real 03:00 run on the owner's cluster before the epic is marked done.
