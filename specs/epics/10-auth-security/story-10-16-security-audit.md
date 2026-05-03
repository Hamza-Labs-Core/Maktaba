# Story 10.16 — Audit log for security-sensitive actions

Reuses the canonical `audit_log` table (schema in
[../09-library-management/README.md](../09-library-management/README.md)).
Security events are stored with `category='security'`. The previously
proposed separate `security_audit` table has been unified per
REVIEW.md §1.1.f.

**AC-1 — Event vocabulary.**
- **Given** any action below,
- **When** persisted,
- **Then** the row has `category='security'` and `event` ∈
  `{login.success, login.failed, logout, logout-all,
  lockout-username, lockout-ip, refresh.replay-detected,
  password.changed, key.rotated, admin-token.used,
  permission.denied, streaming.direct.access, pair.code-issued,
  pair.code-claimed}`. `payload_jsonb` carries event-specific detail.

**AC-2 — Append-only.**
- The `audit_log` table's `BEFORE UPDATE/DELETE` triggers (defined in
  Epic 9 Story 9.17 AC-1) cover this story too — same trigger, same
  guarantee.

**AC-3 — Surfaced in API.**
- **Given** an admin,
- **When** `GET /api/security/audit?cursor=...` is called,
- **Then** entries with `category='security'` are returned newest-first;
  non-admins receive 403.

**AC-4 — Retention via partitioning.**
- Same as Epic 9 Story 9.17 AC-3: monthly partitions detached after
  `audit_retention_days` (default 365). The partitioning is shared
  across categories.

**Test cases:**
- Integration: a failed login writes one `category='security',
  event='login.failed'` row; a successful login writes
  `event='login.success'` and resets the username's failure counter.
- Integration: a non-admin trying to read `/api/security/audit` → 403.

**Edge cases:**
- High-volume admin-token use (single-user mode) — sample `admin-token.used`
  at 1/min so the audit log doesn't fill. The first use per IP per day
  is always logged.
- Audit table partitioned by month for fast retention pruning;
  documented in operations.
