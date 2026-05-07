# Epic 24 — Data Integrity

**Goal.** A user's media library and the platform's derived state survive
crashes, power loss, partial writes, concurrent jobs, and operator
mistakes. The library is the durable truth; derived data (transcripts,
indexes, caches) is recoverable from it. Recovery is documented and
tested.

This epic covers atomicity, idempotency, consistency between authoritative
state and derived state, backup / restore, durability of in-flight work,
and integrity verification.

## Stories

- [Story 24.1 — Atomic writes for sidecar artifacts](story-24-01-atomic-writes.md)
- [Story 24.2 — Idempotent and resumable jobs](story-24-02-idempotent-jobs.md)
- [Story 24.3 — Database consistency and constraints](story-24-03-database-constraints.md)
- [Story 24.4 — Concurrency and locking](story-24-04-concurrency-locking.md)
- [Story 24.5 — Backup and restore](story-24-05-backup-restore.md)
- [Story 24.6 — Disaster recovery](story-24-06-disaster-recovery.md)
- [Story 24.7 — Integrity verification](story-24-07-integrity-verification.md)
- [Story 24.8 — Identity stability across operations](story-24-08-identity-stability.md)
- [Story 24.9 — Forward and backward compatibility](story-24-09-forward-back-compat.md)
