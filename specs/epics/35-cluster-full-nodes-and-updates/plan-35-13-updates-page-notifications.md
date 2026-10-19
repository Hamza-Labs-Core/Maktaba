# Plan 35.13 — Updates page, notifications and history — implementation

> Implementation plan for [story-35-13-updates-page-notifications](story-35-13-updates-page-notifications.md). Design: [design.md](design.md) §4, §13, §16.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | One page for the cluster (not per machine). | Updates are a cluster operation; machines are rows. |
| D2 | Live view through the existing hub browser WebSocket, topic `updates`. | No second socket; works through a node's proxy (`home` class). |
| D3 | Notifications through Epic 33.13's `notify(kind, payload)`; audit-only fallback. | One notification centre; 33.13 may land after this story. |
| D4 | Restore is a documented command on the page, not a button. | A restore loses writes since the backup (design §6.4). |
| D5 | Settings stored as `rules.updates` in `app_settings` with `schema.json` entries. | Rules as settings; the generic settings page and this card share one schema. |

## 1. Files

**Create**
- `api/internal/cluster/updates/handlers.go` — `GET /api/cluster/updates`, settings, run, skip,
  defer, runs list/detail/log (SSE), cancel, resume, target retry/skip; tests.
- `api/internal/cluster/updates/notify.go` — kinds, rate limit, fallback.
- `web/src/pages/Admin/Updates.tsx`, `Updates.test.tsx`, `UpdatesRunLive.test.tsx`,
  `UpdatesSettings.test.tsx`; components under `web/src/components/cluster/updates/`
  (`MachinesVersions.tsx`, `RunTimeline.tsx`, `RunLog.tsx`, `UpdatePolicyCard.tsx`).

**Modify**
- `api/internal/router/p28.go` — `POST /api/admin/system/update` delegates to Update now.
- `api/internal/router/p31.go` — mount the updates routes (admin, `home` class).
- `api/internal/cluster/agent/hub.go` — `updates` topic fed by supervisor run events.
- `web/src/App.tsx`, `web/src/components/AppShell.tsx` — route, nav entry, run banner.
- `web/src/pages/Admin/Machines.tsx` — version chip, behind badge.
- `web/src/pages/Settings.tsx` (Epic 28.6 section) — link to the page.
- `web/src/i18n/en.json`, `ar.json` — strings and notification keys.
- `api/internal/handlers/rules/schema.json` — `rules.updates`.

## 2. Steps

1. Handlers on top of S1 tables (story 35.3/35.4) and the supervisor loopback (`/update/status`).
2. Hub topic `updates` from the supervisor's event stream.
3. Notify with kinds and fallback; audit rows.
4. Page skeleton: header, machines table, settings card.
5. Live run timeline and log; history and detail.
6. Banner and Machines page chip; Epic 28.6 link.
7. Arabic strings, RTL check.

## 3. Tests

Story TC1–TC10; Playwright smoke in `web/src/test` opening the page against a mocked API in both
languages.

## 4. Rollout

Phase D. The read-only parts (versions, history) can ship in phase B with the home orchestrator; the
machines table fills in once 35.12 reports node versions.
