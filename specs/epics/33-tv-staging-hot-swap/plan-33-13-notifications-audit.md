# Plan 33.13 — Notifications and audit — implementation

> Implementation plan for [story-33-13](story-33-13-notifications-audit.md).
> Migration: slot 0130 (`notifications`, `notification_reads`).

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | A general `notifications` table, TV as its first user. | No in-app store exists; the review flow and other features will need it (README decision 8). |
| D2 | Key + params, localised in the client. | Arabic and English without storing two texts. |
| D3 | Audit through the existing `audit_log`. | Append-only already; the Audit log page already lists it. |

## 1. Files

**Create**
- `shared/db/migrations/0130_notifications.sql` (+ sqlite)
- `api/internal/notify/` — `notify.go` (interface, subscribers, DB sink, rate limit), handlers `GET /api/notifications`, `POST /api/notifications/{id}/read`
- `web/src/components/NotificationsBell.tsx`
- tests

**Modify**
- `api/internal/tvchange/machine.go` — call `notify` and audit on transitions
- `web/src/i18n/en.json`, `ar.json`; header layout

## 2. Steps

1. Migration + sink + API.
2. Controller hooks.
3. Bell UI.

## 3. Tests

As in the story.

## 4. Rollout

Phase C.
