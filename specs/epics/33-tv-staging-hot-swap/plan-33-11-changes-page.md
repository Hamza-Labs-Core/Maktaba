# Plan 33.11 — Changes page — implementation

> Implementation plan for [story-33-11](story-33-11-changes-page.md). No migration.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | A Live TV sub-page, not a new top-level admin area. | Changes are TV's; the Live TV admin already holds channels, hosts, on air. |
| D2 | Actions driven by `allowed_actions` from the API. | The UI never re-implements the state machine. |
| D3 | Schedule diff computed server-side. | Generations are large; the API returns a per-channel summary and pages. |

## 1. Files

**Create**
- `web/src/pages/Admin/LiveTV/Changes.tsx`, `ChangeDetail.tsx`, `SlotBar.tsx`, `diff/LineupDiff.tsx`, `diff/ScheduleDiff.tsx`, `diff/SettingsDiff.tsx`, `ChecksTable.tsx`, `ApproveDialog.tsx`
- `web/src/lib/tvChanges.ts` — API client and types
- tests

**Modify**
- `web/src/pages/Admin/LiveTV.tsx` — tab/route "Changes", in-flight banner
- `web/src/pages/Admin/Channels.tsx` — draft notice
- `web/src/i18n/en.json`, `ar.json`
- `api/internal/handlers/tvchange/handlers.go` — `GET /api/tv/changes/{id}/schedule-diff?channel=&page=`

## 2. Steps

1. Client + types; list with slot bar.
2. Detail tabs; diffs.
3. Actions and dialogs.
4. Live updates.
5. i18n + RTL.

## 3. Tests

Vitest + Testing Library with fixtures from the API tests (shared JSON).

## 4. Rollout

Phase B (minimal: list, checks, actions), Phase C (full diffs, preview).
