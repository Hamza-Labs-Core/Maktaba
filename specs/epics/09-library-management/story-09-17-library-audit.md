# Story 9.17 — Library audit log

Library lifecycle events are recorded in the canonical `audit_log`
table (schema in [README.md](README.md)) with `category='library'`.
Records lifecycle events: scan triggered, settings changed, video
purged, library deleted, speaker merged, file purge results,
duplicate detected, runtime root overlap.

> The previous design proposed a separate `library_audit` table. This
> has been unified into the single `audit_log` table per REVIEW.md
> §1.1.f. Rows for library events carry `category='library'`; security
> events (Epic 10 Story 10.16) carry `category='security'`.

**AC-1 — Append-only.**
- **Given** any audit event,
- **When** written,
- **Then** the row is INSERT-only; `BEFORE UPDATE/DELETE` triggers raise
  exceptions (the trigger is defined once for `audit_log` and applies to
  every category).

**AC-2 — Surfaced in API.**
- **Given** an admin,
- **When** `GET /api/libraries/{id}/audit?cursor=...` is called,
- **Then** the response is paginated audit entries (newest-first) for
  the given library, filtered to `category='library'`. The endpoint
  reuses Epic 7 Story 7.2's cursor primitive.

**AC-3 — Retention via partitioning.**
- **Given** the `audit_log` table is monthly-partitioned per the schema
  in [README.md](README.md),
- **When** the nightly trim runs,
- **Then** partitions older than `audit_retention_days` (default 365)
  are detached and copied to long-term storage; the live table only
  holds the last `retention_days` of rows.

**Test cases:**
- Integration: trying to UPDATE a row → exception; trying to DELETE → exception.
- Integration: nightly trim detaches partitions older than 365 days;
  the live table count drops.
- Integration: audit endpoint returns library scoped events newest-first
  and respects pagination.

**Edge cases:**
- Audit log unavailable (DB partial outage) — the actions still succeed
  (audit is best-effort, never blocking); a `audit_write_failed_total`
  metric tracks misses.
- An audit event that contains user-supplied content (e.g., the new
  collection name) — `payload_jsonb` is parameterized; no injection
  risk. Length capped at 8 KiB.
