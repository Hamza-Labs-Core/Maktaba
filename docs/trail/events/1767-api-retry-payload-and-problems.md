---
schema: 1
kind: pr
pr: 1767
title: Preserve retry job payloads and align API problem statuses
date: 2026-11-26
session: 4419ee9d-62d2-421a-9d13-992d2c6e9711
agent: antigravity
---

## What happened
- Fixed #1763 across the Go API's retry paths: failures-page retries (`failurelist.go`, `failureactions.go`), queue bulk retries (`failures.go`, `control.go`), and single-job retries (`jobs.go`) now preserve a failed job's JSON `payload` and `force_cascade` while stripping one-shot `run_now` marks via `queues.RetryPayload`, covered by unit tests and real-PostgreSQL integration tests (`retry_payload_pg_test.go`).
- Fixed all six items of #1172: aligned six 401 responses from `forbidden` to `unauthorized`; registered `pairing-code-expired` (410) and `cloud-link-refused` (422) in `shared/api/problems.json`, `registry_gen.json`, and `dist-manifest.json`; mapped missing edits in batch undo (`review.go`) to 404 instead of 409; wired 401 `token-expired` on expired access JWTs in both home (`middleware.go`) and joined full nodes (`nodeauth.go`); and mapped `*plugins.Problem` refusals in `/api/components` and the components CLI instead of answering 500.
- Merged as `73884609e` with all 130 checks green.

## What we learned
- Carrying a failed job's `payload` into `enqueueTargets` made `p.Payload["apply"] == true` reachable from `Retry` (not only `Apply`), where missing or null `plan_digests` would marshal as `"null"` and produce `[null]` in `mergeApprovalSQL`. Normalizing `plan_digests` via `approvalDigestsJSON` and skipping the merge on `OutcomeInserted` closed that gap.
- Translating an exported `*plugins.Problem` directly into `httperror.Write` needs defensive validation: clamping statuses outside `400..599` to `500`, guarding against typed-nil pointers, and skipping reserved RFC 9457 keys in `Params` so a plugin cannot panic `WriteHeader` or overwrite envelope fields.
- Adding `TypeTokenExpired` in `api/internal/httperror/types.go` tripped `affected-tests` until `registry_test.go` asserted the new constant and status mappings; checking `tools/affected-tests` locally before pushing catches gate misses immediately.
- Full nodes verify access JWTs in `nodeauth.Resolver.Middleware` rather than `middleware.JWTBearer`, so auth-gate signals like `WithTokenExpired` must be wired in both places.

## Keep doing
- Verify JWT signature strictly before checking audience and expiry so forged or wrong-audience tokens never reveal `token-expired`.
- Run `tools/affected-tests` gate locally before pushing and test `NULL` payloads alongside populated payloads in PostgreSQL integration tests.

## Open
- None for #1763 or #1172 (both closed).
