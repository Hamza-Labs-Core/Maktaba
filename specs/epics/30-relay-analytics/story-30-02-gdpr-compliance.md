# Story 30.2 — GDPR compliance layer

> Epic 30 · Cloud Relay Anonymous Analytics · Phase 2 (privacy)

## Description

The privacy layer makes the relay's analytics defensible under GDPR by
construction. It is implemented in `cloud/internal/privacy/`.

Controls:

- **Server-id hashing.** Any place that must reference a server in a
  retained artifact uses `privacy.HashServerID(salt, id)` — a salted
  SHA-256 truncated to 16 hex chars — never the raw UUID. (The aggregate
  metrics tables reference no server at all; the helper exists for logs
  and any future per-server breakdown.)
- **No IP storage.** Country is derived from the edge header
  (`CF-IPCountry`) via `privacy.CountryFromRequest`, normalised to an
  ISO-3166 alpha-2 code (or `''` for unknown/anonymised `XX`/`T1`). The
  raw IP is read for nothing else and never persisted or logged.
- **90-day retention with auto-purge.** `relay_metrics_hourly` rows older
  than `privacy.RetentionDays` (90) are deleted by the purge goroutine;
  raw rows are deleted at 24 h.
- **Deletion on account delete.** `privacy.DataSubjectService.Delete`
  removes any user-linked rows for a deleted account
  (`push_dispatch_log`) and returns a report. The aggregate metrics hold
  no user data, so they need no per-user redaction — that is the point of
  the aggregate-only design (README D1), and the deletion report states
  it explicitly.
- **Privacy policy endpoint.** `GET /privacy` (public, no auth) returns
  the relay's processing summary as JSON.
- **Article 30 records.** A structured record of processing activities
  (`privacy.ProcessingRecords`) is served to operators and documents
  purpose, categories, recipients, retention, and safeguards.
- **DPA template.** `cloud/docs/dpa-template.md` — a fill-in Data
  Processing Agreement for operators who need one with sub-processors.

## Acceptance criteria

- **Given** a request with `CF-IPCountry: DE`,
  **when** `CountryFromRequest` runs,
  **then** it returns `"DE"` and the function reads the IP for nothing;
  **and given** `CF-IPCountry: XX` (or absent), it returns `""`.

- **Given** the same server id and salt,
  **when** `HashServerID` is called twice,
  **then** it returns the same 16-char hex digest, and a different salt
  yields a different digest, and the raw id never appears in the output.

- **Given** hourly rows older than 90 days,
  **when** the retention purge runs,
  **then** they are deleted and rows within 90 days remain.

- **Given** a deleted account with rows in `push_dispatch_log`,
  **when** `DataSubjectService.Delete` runs,
  **then** those rows are removed and the report records the count and
  confirms the aggregate metrics required no change.

- **Given** any client,
  **when** it requests `GET /privacy`,
  **then** it receives the policy JSON with `200` and no authentication
  is required.

## Notes

- The country header name is configurable but defaults to Cloudflare's
  `CF-IPCountry`; the normalisation is the testable core.
- Hashing uses the same construction as the on-server `watch.hashIP`
  (Epic 29 D4) for consistency across the codebase.
