# Implementation Plan — Story 30.2 GDPR compliance layer

> Companion to [story-30-02-gdpr-compliance.md](story-30-02-gdpr-compliance.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `cloud/internal/privacy/` (hashing, country, retention, deletion, policy). |
| Public endpoint | `GET /privacy` mounted on the relay role (before the proxy catch-all). |
| Operator endpoint | `GET /v1/admin/privacy/processing-records` on the api role (admin-gated). |
| DPA | `cloud/docs/dpa-template.md`. |

## 1. privacy.go — primitives (unit-tested, no DB)

```go
const RetentionDays = 90
const RawRetentionHours = 24
const DefaultCountryHeader = "CF-IPCountry"

// HashServerID = first 16 hex of SHA-256(salt | "|" | id).
func HashServerID(salt, id string) string

// NormalizeCountry upper-cases a 2-letter A–Z code; "", "XX", "T1",
// and malformed input → "".
func NormalizeCountry(raw string) string

// CountryFromRequest reads the edge country header and normalises it.
// The IP is never read or returned.
func CountryFromRequest(r *http.Request, header string) string
```

## 2. retention + deletion (store-backed)

- `PurgeHourly(ctx, db, now)` — `DELETE FROM relay_metrics_hourly WHERE
  hour < now-90d`. Called by the metrics Runner's daily tick.
- `DataSubjectService{DB}` with `Delete(ctx, userID) (Report, error)`:
  `DELETE FROM push_dispatch_log WHERE user_id=$1`; `Report{PushRows int;
  MetricsRows int /*always 0, aggregate-only*/; Note string}`.

## 3. policy.go — policy + Article 30 + handler

- `Policy()` returns a struct (controller, purpose, data categories,
  retention, lawful basis, rights, contact) → served as JSON by
  `PolicyHandler` at `GET /privacy`.
- `ProcessingRecords()` returns `[]ProcessingActivity` (Article 30:
  name, purpose, categories, recipients, retention, safeguards) → served
  by an admin endpoint.

## 4. DPA template

`cloud/docs/dpa-template.md` — parties, subject-matter, duration, nature
& purpose, data categories, sub-processors table, security measures,
sub-processor list, deletion/return, audit. Marked TEMPLATE.

## 5. Tests

`privacy_test.go`: `HashServerID` determinism + salt sensitivity + no
raw id leak; `NormalizeCountry` table; `CountryFromRequest` header
handling incl. `XX`/absent → `""`. `Policy`/`ProcessingRecords` shape
asserted (non-empty required fields). Deletion SQL follows the no-live-DB
convention.
