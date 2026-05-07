# Story 21.6 — Audit log for sensitive actions

A non-rotated, append-only log of security-relevant events. Distinct
from the operational log.

This story owns the canonical audit table for the platform. The
`library_audit` and `security_audit` tables described in earlier drafts
of Epic 9 / Epic 10 are unified into `audit_log` with a `category`
column; the consuming endpoints (`GET /api/libraries/{id}/audit` and
`GET /api/security/audit`) read filtered views over this single table.
Migrations are owned here.

## Acceptance criteria

- AC1. Audit events are written to a dedicated `audit_log` Postgres
  table (and a mirrored file `/var/maktaba/audit/audit.log` for
  out-of-DB recovery). The table schema is:
  ```sql
  CREATE TABLE audit_log (
    id           BIGSERIAL PRIMARY KEY,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    category     TEXT NOT NULL CHECK (category IN
                   ('auth','library','admin','data','config','keys')),
    event        TEXT NOT NULL,
    actor_user   UUID NULL,
    actor_ip     INET NULL,
    actor_ua     TEXT NULL,
    target_kind  TEXT NULL,
    target_id    TEXT NULL,
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    clock_source TEXT NOT NULL DEFAULT 'db'
  ) PARTITION BY RANGE (occurred_at);
  ```
  Monthly partitions; an indexed view `audit_log_security` and view
  `audit_log_library` provide the filtered surfaces for the existing
  REST endpoints.
- AC2. Events recorded (categories in parentheses):
  - `auth`: login success, login failure, refresh-token reuse,
    admin-token use, account lockout (with `user_id`, `ip`, `ua`).
  - `keys`: JWT signing-key rotation (old/new `kid`).
  - `config`: settings change (key + old/new value hashes, never
    plaintext for secrets).
  - `library`: library lifecycle (create, root added/removed, delete,
    purge).
  - `data`: bulk export / bulk delete / `purge=true` confirmation
    (paired with the confirmation token referenced in
    [Story 23.6](../23-security/story-23-06-rate-limiting.md)).
  - `admin`: user create/update/disable, ACL grant/revoke, admin unlock.
- AC3. Audit rows are append-only enforced by a `BEFORE UPDATE OR
  DELETE` trigger raising an exception. Partition drops for retention
  are routed through a documented admin command that itself emits a
  `data` audit row.
- AC4. Audit retention default 1 year; configurable per-category
  (`auth` may be shorter, `keys` longer). Retention is implemented by
  detaching and dropping monthly partitions; explicit immediate delete
  requires the admin CLI, which itself emits an audit row.

## Test cases

- TC1. Append-only: an attempted `UPDATE audit_log SET ...` fails with
  the trigger's exception.
- TC2. Login flow: failed login attempts are logged with the offered
  username and the source IP, not the offered password (never).
- TC3. Retention: a partition older than the retention window is
  detached and dropped by the scheduled task; the deletion itself is
  audited.
- TC4. View parity: `GET /api/libraries/{id}/audit` returns rows from
  `audit_log` filtered to `category='library' AND target_id =
  library_id`; `GET /api/security/audit` filters to
  `category IN ('auth','admin','keys')`. Schemas in earlier docs that
  named `library_audit` or `security_audit` as separate tables are
  superseded; their migration stories cite this AC for the table.

## Edge cases

- EC1. DB unreachable during a sensitive action — the file mirror
  captures the event; on next DB connection, the mirror is replayed
  into the table.
- EC2. Clock skew on file mirror — events use `now()` from the DB when
  available, falling back to wall-clock with `clock_source='wall'`.
- EC3. Audit for the audit reader — every `SELECT` against `audit_log`
  through the API is itself audited (read-audit), bounded by the
  audit reader role.
