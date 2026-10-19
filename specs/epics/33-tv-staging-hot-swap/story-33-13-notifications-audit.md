# Story 33.13 — Notifications and audit

## Description

*As an admin, I want to be told when a change needs me, when it went live, when it failed or rolled back,
and to see later who approved what.*

Design: [design.md](design.md) §11.1, README R12.

## Acceptance criteria

- **AC1** In-app notifications (slot 0126 `notifications`, audience admins) for: change ready (needs
  approval), verification failed, auto-promote scheduled (only when an admin switched a rule on), swapped, failover to the hot standby, rolled back (manual /
  automatic with cause), staging slot crash loop, rollback point expiring in 1 h, a `ready` change waiting
  more than a day.
- **AC2** A bell in the web header shows unread counts; each notification links to the change; marking read
  is per user.
- **AC3** Every admin action and every automatic transition writes `audit_log` (category `tv_change`,
  `action`, `actor_user_id` or `actor_source = system`, `target_kind = tv_change`, `target_id`, payload with
  from/to state) — append-only (slot 0059).
- **AC4** `notify(kind, payload)` is one Go function behind an interface with subscribers; the general
  review/notification system (future) and mobile push (0065 categories, optional `tv_changes` category)
  subscribe without changes to the controller.
- **AC5** Notifications and audit texts are localised (en, ar) by key + params.

## Test cases

- **TC1** `TestNotifyOnTransitions` — table of transitions → notifications.
- **TC2** `TestAuditRowsAppendOnly`.
- **TC3** `NotificationsBell.test.tsx`.
- **TC4** `TestNotifySubscriberInterface`.

## Edge cases

- **EC1** Repeated failures of the same change notify once per hour at most.
- **EC2** No admin users with the web open: notifications persist and show at next sign-in.

## Dependencies

33.10.
