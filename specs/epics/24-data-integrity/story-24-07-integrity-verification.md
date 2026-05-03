# Story 24.7 — Integrity verification

Periodic and on-demand integrity checks across the canonical surfaces.

## Acceptance criteria

- AC1. A `maktaba-pipeline doctor --integrity` runs:
  - `content_hash` re-verification on a configurable sample (or
    full library, opt-in).
  - Sidecar presence check (every `READY` video has expected
    artifacts).
  - DB referential integrity (no dangling FKs, no soft-deleted
    children with live parents).
  - FTS / Chroma row-count parity with `segments` row count.
- AC2. Integrity reports are written to `audit_log`
  ([Story 21.6](../21-observability/story-21-06-audit-log.md))
  and visible in the admin panel.
- AC3. Auto-remediation is opt-in: `--repair` re-enqueues missing
  sidecars and re-indexes missing segments. Without `--repair`,
  doctor only reports.
- AC4. The doctor runs as a scheduled task once a week by default;
  configurable cadence; off in single-user mode unless opted in.

## Test cases

- TC1. Detection: corrupt a transcript file; doctor reports
  mismatch; with `--repair`, sidecar is regenerated.
- TC2. FTS parity: delete one row directly from the FTS table;
  doctor reports drift; `--repair` reindexes.
- TC3. Sample mode: with a 50 k-video library, sampled integrity
  check completes in ≤ 5 minutes; full-mode is allowed but
  documented as overnight.

## Edge cases

- EC1. Hash recomputation reading 30 TB — the sample mode is the
  default; full mode requires explicit opt-in and a confirmation
  prompt.
- EC2. Drift caused by a known background job — the doctor
  cross-references in-flight jobs and excludes their outputs from
  the parity check.
- EC3. False positive from a clock skew (mtime regressions) —
  doctor uses content_hash, not mtime, as the source of truth.
