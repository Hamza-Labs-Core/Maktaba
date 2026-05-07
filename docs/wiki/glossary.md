# Maktaba Glossary

> Project-specific terminology used across `specs/`, plans, mockups, and code.
> Curated from epic READMEs, plans, and stories. Each entry: **definition** + **where used**.

---

## A

**ACL** — Access Control List. Per-library role assignment (`admin` / `editor` / `viewer`) in `library_acl(user_id, library_id, role)`. Checked by API on every state-changing request and surfaced to streaming via the JWT `lib` claim. *Used in:* Epic 10.10, [Epic 19 §19.8](epics/epic-19-scalability.md), [Epic 23 §23.2](epics/epic-23-security.md).

**Advisory lock** — Postgres `pg_advisory_xact_lock(namespace, key)` for per-resource serialization across workers; released on transaction commit; reaper force-releases stale holders. *Used in:* [Epic 19 §19.4](epics/epic-19-scalability.md), [Epic 24 §24.4](epics/epic-24-data-integrity.md).

**ALPN** — Application-Layer Protocol Negotiation; TLS extension that negotiates `h2` (HTTP/2) vs `http/1.1` at handshake. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**Argon2id** — Password-hashing algorithm (RFC 9106) with parameters `memory=65536 KiB`, `iterations=3`, `parallelism=1`. *Used in:* Story 10.1, [Epic 23 §23.1](epics/epic-23-security.md).

**Atomic write** — `temp file → fsync → atomic rename → fsync_dir`; ensures no partial output is visible if the process crashes. Falls back to `(write, fsync, rename, fsync_dir)` on non-atomic FS. *Used in:* [Epic 24 §24.1](epics/epic-24-data-integrity.md).

**audio_track** — Individual audio stream within a media file; selected by disposition (main / alternate / commentary); `detected_language` and `detected_language_confidence` stored separately. *Used in:* Epic 2.

**Audit log** — Append-only `audit_log` table (monthly partitions); categories `auth | library | admin | data | config | keys`; trigger blocks UPDATE/DELETE; retention configurable per category. *Used in:* [Epic 21 §21.6](epics/epic-21-observability.md), [Epic 23](epics/epic-23-security.md).

**`aud` claim** — Standard JWT audience field; one of `streaming`, `streaming-direct`, or `streaming-static`. Streaming verifier rejects mismatches. *Used in:* [Epic 23 §23.1](epics/epic-23-security.md).

---

## B

**BLAKE3** — Cryptographic hash function used for content identity; sampled hash `blake3(first_4MiB ‖ size_le8 ‖ last_4MiB)` for files ≥8 MiB; whole-file hash otherwise. *Used in:* Epic 1.2, [Epic 19 §19.6](epics/epic-19-scalability.md), [Epic 24 §24.8](epics/epic-24-data-integrity.md).

**BM25** — Probabilistic relevance-ranking score combined with tsvector queries in FTS. *Used in:* Epic 5.2.

**Branch protection** — GitHub-enforced ruleset: required status checks (the `ci-success` rollup) + approvals before merge. IaC under `deploy/github/branch-protection.tf`. *Used in:* [Epic 22 §22.1](epics/epic-22-devops.md).

**Budget cap** — Per-library USD limit on API-backed transcription (e.g., OpenAI Whisper); enforced at job-claim time; over-budget jobs requeued for next month. In-progress jobs never preempted. *Used in:* [Epic 19 §19.7](epics/epic-19-scalability.md).

---

## C

**Cache hit rate** — `hits / (hits + misses)` after warm-up. Floors per cache: HLS segment ≥70 %, embedding ≥90 %, probe ≥99 %, JWKS ≥99 %. *Used in:* [Epic 18 §18.8](epics/epic-18-performance.md).

**Capacity floor** — Minimum single-host capacity guaranteed by v1: 50 k videos, 1 M segments, 8 direct-play or 4 transcoded sessions on Mac mini M2 16 GB / 30 TB SSD. *Used in:* [Epic 19 §19.1](epics/epic-19-scalability.md).

**Cardinality** — Bounded label dimensionality on Prometheus metrics. Forbidden labels: `video_id`, `user_id`, per-row identifiers. *Used in:* [Epic 21 §21.2](epics/epic-21-observability.md).

**Cert rotation overlap** — 7-day window during certificate renewal when both old and new TLS SPKI hashes are accepted. *Used in:* [Epic 15 §15.2](epics/epic-15-discovery.md).

**Chapter** — Logical segment of a video with `start_sec`, `end_sec`, `title`. `source` is `embedded` (extracted from container), `inferred` (ML-detected), or `manual` (user). *Used in:* Epic 5.7, Epic 9.18.

**Chroma / ChromaDB** — Vector database for semantic search embeddings; embedded mode (single-process). Single-writer enforced via `pg_try_advisory_lock('chroma:writer')`; second worker falls back to read-only. *Used in:* Epic 5.3, [Epic 19 §19.4](epics/epic-19-scalability.md), [Epic 24 §24.4](epics/epic-24-data-integrity.md).

**Chromatic** — Visual-regression service; diffs gate component-library merges. *Used in:* [Epic 17 §17.2](epics/epic-17-ux-design-system.md).

**CI gate** — Parallel quality check in CI (lint / unit / integration / e2e / perf-ci / build-artifacts). All feed into `ci-success`. *Used in:* [Epic 22 §22.1](epics/epic-22-devops.md).

**Cohort** — Named group of beta users (e.g., `preview-2026`); user opt-in controlled by feature-flag `UserOptIn` metadata. *Used in:* [Epic 16 §16.6/16.8](epics/epic-16-subscriptions.md).

**Cold path** — Cache miss path triggering fresh computation (cold transcode via FFmpeg, cold embed via Pipeline gRPC, cold probe via ffprobe); latency ~3–6× warm. *Used in:* [Epic 18](epics/epic-18-performance.md).

**Cold scan** — Initial directory walk over a 30 TB tree; bounded RSS ≤800 MiB; 8 concurrent scan workers with per-file 60 s timeout on SMB/NFS; skips files <30 s old. *Used in:* [Epic 19 §19.6](epics/epic-19-scalability.md).

**Compose for TV** — AndroidX Compose APIs optimized for large screens and D-pad input. *Used in:* [Epic 14 §14.2](epics/epic-14-tv-apps.md).

**Confirmation token** — Required field on destructive operations (delete library, delete user, rotate signing key) matching the resource name/ID. *Used in:* [Epic 23 §23.6](epics/epic-23-security.md).

**Content hash** — `content_hash`. BLAKE3 sampled hash uniquely identifying a media file independent of path. Renames/moves preserve identity (`UPDATE videos SET path = ...`); modify-in-place creates a new row with `superseded_by` linking to the old. *Used in:* Epic 1.2, [Epic 18](epics/epic-18-performance.md), [Epic 19](epics/epic-19-scalability.md), [Epic 24](epics/epic-24-data-integrity.md).

**Content type prediction** — Lecture / sermon / interview / film / other classification stored in `content_type_predictions(video_id, type, confidence, tags)`. *Used in:* Epic 9.10.

**Continue Watching row** — Home-screen first row populated from `playback_state` rows with `5–95 %` watched, sorted by recency; cross-device sync via WebSocket. *Used in:* [Epic 14 §14.5](epics/epic-14-tv-apps.md).

**Contract test** — Tests that service boundaries (GraphQL, REST, gRPC, WebSocket) match their schemas. Drift fails CI. *Used in:* [Epic 20 §20.6](epics/epic-20-testing.md).

**CSRF token** — Cross-site request-forgery defense; paired with session cookie; validated on every state-changing web request. *Used in:* Story 10.2, [Epic 23 §23.1](epics/epic-23-security.md).

**CVE scan gate** — `govulncheck` (Go) + `pip-audit` (Python) + `npm audit` (web); high-severity blocks merge unless suppressed under `security/suppressions/<cve-id>.md`. *Used in:* [Epic 23 §23.7](epics/epic-23-security.md).

---

## D

**D-pad first** — Every flow on TV apps must be completable with the remote D-pad alone. *Used in:* [Epic 14](epics/epic-14-tv-apps.md).

**Debounce** — Coalesce rapid successive writes (e.g., watch-progress) into fewer DB transactions; 1-second window per `(user, video)` pair. *Used in:* [Epic 24 §24.4](epics/epic-24-data-integrity.md).

**Design token** — Reusable design value (color, size, duration, spacing, elevation, breakpoint, font, motion easing) stored in `design/tokens/tokens.json` and regenerated per platform via Style Dictionary. *Used in:* [Epic 17 §17.1](epics/epic-17-ux-design-system.md).

**Device pseudonym** — 96-bit random per-device identifier (per opt-in) for telemetry aggregation; never linked to `user_id`. *Used in:* [Epic 16 §16.5](epics/epic-16-subscriptions.md).

**Diarization** — Speaker identification in audio; outputs speaker labels and timestamps; powered by `pyannote`. *Used in:* Epic 3.9.

**DLNA / UPnP MediaServer** — UPnP AV device exposing ContentDirectory (SOAP) and HTTP byte serving; opt-in; only direct-play files surfaced (no transcoded HLS). *Used in:* [Epic 15 §15.4](epics/epic-15-discovery.md).

**Doctor** — `maktaba-api migrate doctor`, `maktaba-pipeline doctor`. Pre-flight check that runs planned migrations in a temp DB seeded from production, estimates duration, warns on data loss. *Used in:* [Epic 22 §22.6](epics/epic-22-devops.md).

**Drain mode** — `/admin/drain` endpoint flips `/healthz` (or `/readyz`) to 503 while completing in-flight requests; streaming holds segments open; pipeline finishes claimed jobs. *Used in:* [Epic 19 §19.2](epics/epic-19-scalability.md), [Epic 22 §22.6](epics/epic-22-devops.md).

---

## E

**Ed25519** — Edwards-curve Digital Signature Algorithm. Used for federation handshakes ([Story 15.7](epics/epic-15-discovery.md)), Long-term server identity (Story 10.18), license-key signing ([Epic 16 §16.4](epics/epic-16-subscriptions.md)), feature-flag bundle signing ([Epic 16 §16.8](epics/epic-16-subscriptions.md)).

**Embedding** — Dense vector representation of a transcript chunk; cosine-distance similarity drives semantic search. Generated by sentence-transformers; stored in ChromaDB. *Used in:* Epic 5.3.

**Ephemeral keypair** — Single-use X25519 key generated during federation pairing; stored encrypted at rest; destroyed after SAS confirmation. *Used in:* [Epic 15 §15.7](epics/epic-15-discovery.md).

**Error category** — `auth | db | ffmpeg | network | ml | unknown`. Tagged on every emitted error for alerting and dashboards. *Used in:* [Epic 21 §21.5](epics/epic-21-observability.md).

**`error_id`** — UUIDv7 generated at first error emission and propagated across service boundaries via gRPC metadata header `x-error-id`. *Used in:* [Epic 21 §21.5](epics/epic-21-observability.md).

**`events` table** — Durable, append-only WS event log (`id BIGSERIAL`, `channel`, `payload`, ...). Clients reconnect via `last_event_id` cursor for cross-replica fan-out. 7-day retention. *Used in:* [Epic 19 §19.2](epics/epic-19-scalability.md).

---

## F

**Failed-login lockout** — `(user, ip)` tuple bucket; ≥5 failures within 15-min sliding window → 15-min lock. Locked users receive `423 Locked` even with the correct password. *Used in:* [Epic 23 §23.6](epics/epic-23-security.md).

**Federation** — Asymmetric library sharing between two Maktaba instances (A → B can read A's `Lectures`; B → A can read B's `Films`); per-library ACL scoping. *Used in:* [Epic 15 §15.3/15.7](epics/epic-15-discovery.md).

**Federation JWT** — Short-lived (≤15 min) token signed by the owning server's Ed25519 long-term key for streaming and GraphQL federation scopes. *Used in:* [Epic 15 §15.7](epics/epic-15-discovery.md).

**FFmpeg / FFprobe** — Multimedia framework: FFmpeg for transcoding, stream extraction, sprite generation; FFprobe for inspecting structure without decoding. Subprocess model with resource accounting. *Used in:* Epic 2, Epic 8.

**Feature flag** — Boolean / numeric / string value controlling UI visibility and server behavior. Resolved per user (defaults < tier < cohort < user override), cached 60 s, signed with Ed25519. *Used in:* [Epic 16 §16.6/16.8](epics/epic-16-subscriptions.md).

**Fixture** — Reproducible test data (media samples, seeded DB state) committed to repo (≤50 MiB) or downloaded with checksum verification. *Used in:* [Epic 20 §20.2](epics/epic-20-testing.md).

**Flake** — Test that fails intermittently. Auto-quarantine threshold: ≥3 occurrences in a 7-day window → P2 issue, 14-day SLA. *Used in:* [Epic 20 §20.8](epics/epic-20-testing.md).

**Focus engine** — Spatial-navigation system (tvOS / Android TV) for D-pad input; cards grow on focus; tab order explicit. *Used in:* [Epic 14 §14.3](epics/epic-14-tv-apps.md).

**Force-merge override** — `force-merge: <reason>` line in PR body; validated by CI; stored as audit trail. *Used in:* [Epic 22 §22.1](epics/epic-22-devops.md).

**Forward compatibility** — Old binary can read state produced by a newer (one-minor-bumped) version; artifact formats carry top-level `schema_version` and unknown fields are ignored on minor bumps. *Used in:* [Epic 24 §24.9](epics/epic-24-data-integrity.md).

**fsync** — Filesystem call forcing buffered data to persistent storage. `fsync_dir` syncs the parent directory inode after a rename. *Used in:* [Epic 24 §24.1](epics/epic-24-data-integrity.md).

**FTS** — Full-text search. Postgres `tsvector` with Arabic-aware language config, or SQLite FTS5 virtual table. *Used in:* Epic 5.2.

---

## G

**GHSA** — GitHub Security Advisory; published with CVE (if assigned), mitigations, and affected versions. *Used in:* [Epic 23 §23.8](epics/epic-23-security.md).

**Goose** — Migration runner; `shared/db/migrations/NNNN_<topic>.{sql,sqlite.sql}`; `goose_db_version` table tracks applied migrations. *Used in:* [Epic 22 §22.4](epics/epic-22-devops.md).

**Graceful degradation** — Search embed timeout → FTS-only with `degraded: true`. Transcode cap exceeded → direct-play downgrade. No silent retry; observable. *Used in:* [Epic 18 §18.2](epics/epic-18-performance.md), [Epic 19 §19.7](epics/epic-19-scalability.md).

---

## H

**HDR pass-through** — HLG and Dolby Vision preserved end-to-end where the device supports them; never up-/down-converted. tvOS sets `appliesPerFrameHDRDisplayMetadata`. *Used in:* [Epic 14](epics/epic-14-tv-apps.md).

**Head sampler** — Tracing decision made at root span creation; composite policy: 100 % of error / slow spans, 1 % of others. *Used in:* [Epic 21 §21.3](epics/epic-21-observability.md).

**Health probes** — `/healthz` (liveness; never blocks), `/readyz` (readiness; deps green ≤800 ms), `/api/system/health` (aggregator). *Used in:* [Epic 21 §21.4](epics/epic-21-observability.md).

**HLS** — HTTP Live Streaming; adaptive video format with `.m3u8` manifest and segment files. Primary streaming protocol. *Used in:* Epic 8.5.

**Hot path** — Code path serving high-throughput requests (manifest issue, segment serve, session open, WS event). Targets ≤100 ms p95. *Used in:* [Epic 18](epics/epic-18-performance.md).

**HSTS** — HTTP Strict-Transport-Security header; default `max-age=31536000; includeSubDomains`; opt-out via `MAKTABA_DISABLE_HSTS=true`. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

---

## I

**Idempotency key** — Tuple `(content_hash, stage, backend, model, config_hash)` stored in `processing_jobs.idempotency_key`; deduplicates retries and bulk re-enqueues. *Used in:* [Epic 24 §24.2](epics/epic-24-data-integrity.md).

**i18n table** — Locale-aware string catalog (`api/internal/i18n/locales/{en,ar}.toml`). All UI copy pulled from here, not hard-coded. *Used in:* [Epic 17](epics/epic-17-ux-design-system.md).

**Image-size guard** — CI lint enforcing per-service container image budgets (api ≤60 MiB, streaming ≤80 MiB, pipeline ≤1.2 GiB, web ≤30 MiB). *Used in:* [Epic 22 §22.3](epics/epic-22-devops.md).

**In-process CA** — Maktaba's private Certificate Authority (persisted in `signing_keys` table, encrypted private key); issues 24 h leaf certs for inter-service mTLS with SANs `localhost` + container name. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**Integration test** — Real Postgres / FFmpeg / ChromaDB / fixture media; ≤5 s per test soft cap; ≤8 m total runtime. *Used in:* [Epic 20 §20.4](epics/epic-20-testing.md).

**Integrity doctor** — Weekly verification: re-verify content_hash (sample / full mode), sidecar presence, FK referential, FTS / Chroma parity. Repair flag re-enqueues jobs or reindexes. *Used in:* [Epic 24 §24.7](epics/epic-24-data-integrity.md).

---

## J

**JWKS** — JSON Web Key Set; `GET /.well-known/jwks.json` publishes public keys; streaming caches with TTL ≤5 min, ETag-aware. *Used in:* Story 10.6, [Epic 23 §23.1](epics/epic-23-security.md).

**JWT** — JSON Web Token. Maktaba uses RS256 with a single active signing key; rotation 90 d active + 30 d overlap; access token TTL 15 min; claims include `lib` (library UUIDs) and `aud` (streaming variant). *Used in:* Story 10.3/10.4/10.6, [Epic 23 §23.1](epics/epic-23-security.md).

---

## K

**`kid`** — Key identifier embedded in JWT header pointing into the JWKS document. *Used in:* [Epic 23 §23.1](epics/epic-23-security.md).

---

## L

**Latency budget** — Per-endpoint p50 / p95 / p99 ceiling (ms) measured at fixture scale on reference hardware. Contract between user and implementation. *Used in:* [Epic 18 §18.1](epics/epic-18-performance.md).

**Leanback** — Android TV's legacy UI library (now superseded by androidx.tv but still supported for legacy devices). *Used in:* [Epic 14](epics/epic-14-tv-apps.md).

**`lib` claim** — JWT field carrying the array of library UUIDs the user has a role on. Streaming verifier uses this for offline authorization. *Used in:* [Epic 23 §23.1/23.2](epics/epic-23-security.md).

**Library** — Top-level collection of media roots (user-scoped or shared); metadata includes language defaults, ignore rules, scan intervals. *Used in:* Epic 1, Epic 9.

**`library_acl`** — Per-library role table (`user_id`, `library_id`, `role` ∈ admin/editor/viewer). *Used in:* Story 10.10, [Epic 19 §19.8](epics/epic-19-scalability.md), [Epic 23 §23.2](epics/epic-23-security.md).

**License key** — Ed25519-signed JSON `{license_id, tier, seats, issued_at, expires_at, signature}`. Validated locally; refreshed daily; 30-day offline grace. *Used in:* [Epic 16 §16.4](epics/epic-16-subscriptions.md).

**Liveness probe** — `/healthz`. Process-alive check; never blocks on dependencies; orchestrator restarts on failure. *Used in:* [Epic 21 §21.4](epics/epic-21-observability.md).

---

## M

**`maktaba_normalize`** — Custom Postgres text-search function for Arabic normalization (diacritics removal, Unicode normalization); applied before tokenization. *Used in:* Epic 5.2.

**mDNS / Bonjour** — Multicast DNS service discovery on the LAN. Maktaba advertises `_maktaba._tcp.local.` with TXT records `(version, name, tls, auth_required, mdns_id)`. *Used in:* [Epic 15 §15.1](epics/epic-15-discovery.md).

**`mdns_id`** — Per-server stable UUID published in mDNS TXT records; survives hostname changes; canonical server identity. *Used in:* [Epic 15](epics/epic-15-discovery.md).

**Migration slot** — Sequential integer (`0001`, `0002`, ...) identifying a DB schema migration; ordering enforced topologically by dependency. *Used in:* [Epic 22 §22.4](epics/epic-22-devops.md), [Migrations](migrations.md).

**MLX** — Apple Metal Performance Shaders backend; fast on-device transcription on Mac. One of three Whisper backends. *Used in:* Epic 3.2.

**mTLS** — Mutual TLS; both client and server present certificates. Maktaba uses it inter-service when not loopback. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**Mutation testing** — Injecting small code changes to detect if tests catch them; identifies weak coverage. *Used in:* [Epic 20 §20.3](epics/epic-20-testing.md).

---

## N

**Native histogram** — Exponential bucket histogram (Prometheus 2.40+ / OTel native format) with fixed-bucket fallback. *Used in:* [Epic 21 §21.2](epics/epic-21-observability.md).

**Nonce** — 32 random bytes embedded in the QR pairing URL; second factor against shoulder-surfing. *Used in:* [Epic 15 §15.5/15.6](epics/epic-15-discovery.md).

---

## O

**OCSP stapling** — TLS extension where the server staples a fresh OCSP response with its certificate; clients don't have to call the OCSP responder. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**Offline grace** — 30-day window after the last successful license refresh during which the free tier remains fully functional. *Used in:* [Epic 16 §16.4](epics/epic-16-subscriptions.md).

**OTel / OpenTelemetry** — Tracing standard used end-to-end (web → API → gRPC → pipeline/streaming → DB). *Used in:* [Epic 21 §21.3](epics/epic-21-observability.md).

---

## P

**Pairing code** — 6 alphanumeric chars (base32 minus `IL01`) bound to user + device kind + 5-min expiry; one-time use. *Used in:* [Epic 15 §15.6](epics/epic-15-discovery.md).

**Partial unique index** — Index enforcing uniqueness only on rows matching a WHERE condition (e.g., `UNIQUE(...) WHERE deleted_at IS NULL`). Allows soft-deleted resurrection. *Used in:* [Epic 24 §24.3](epics/epic-24-data-integrity.md).

**PAT** — Personal Access Token; long-lived non-interactive credential. *Used in:* Story 11.13.

**Path masking** — Filesystem paths under media root replaced with `<media>/<library>/<relative>` in logs and traces. *Used in:* [Epic 21 §21.8](epics/epic-21-observability.md).

**`paths.canonical_under_roots(p)`** — Universal filesystem-input gate. Resolves symlinks, rejects `..`, NUL bytes; asserts the resolved path is under a configured library root. *Used in:* [Epic 23 §23.5](epics/epic-23-security.md).

**Pipeline** — Python service running stages: scan, probe, extract audio, transcribe, subtitle gen, indexing, thumbnail. *Used in:* Epic 6 (job queue) and stage epics.

**`processing_jobs`** — Queue table (slot 0002). Owned by plan-06-01; consumed by all pipeline stages. *Used in:* Epic 6, [Epic 24 §24.2](epics/epic-24-data-integrity.md).

**`prefers-reduced-motion`** — CSS media query honored by all components — no animations when the user OS prefers reduced motion. *Used in:* [Epic 17 §17.3](epics/epic-17-ux-design-system.md).

---

## Q

**QR URL** — `https://{server}/pair?code={code}&mid={mdns_id}&spki={hash}&n={nonce}`. Encodes everything needed for mobile to claim without prior LAN discovery. *Used in:* [Epic 14 §14.1](epics/epic-14-tv-apps.md), [Epic 15 §15.5](epics/epic-15-discovery.md).

**Quarantine** — Auto-skip flaky test + file P2 issue; SLA 14 days to fix or delete. *Used in:* [Epic 20 §20.8](epics/epic-20-testing.md).

**QUIC tunnel** — Outbound long-lived connection from server to relay edge using QUIC (UDP-based, faster handshake than TLS). *Used in:* [Epic 15 §15.2](epics/epic-15-discovery.md).

---

## R

**Rate-limit bucket** — Per-IP or per-user counter; fixed-window or sliding-window tracking; returns `429 Retry-After` on breach. *Used in:* [Epic 23 §23.6](epics/epic-23-security.md).

**Recommendations Channel** — Android TV home-screen channel API; Maktaba publishes Continue Watching + Recommendations via WorkManager + PreviewChannel. *Used in:* [Epic 14 §14.2/14.5](epics/epic-14-tv-apps.md).

**Recommendations rail** — Up to 5 rows × ≤20 items: "Because you watched X" / "More from {speaker}" / "Newly added in {library}" / "Editor's picks". *Used in:* [Epic 14 §14.6](epics/epic-14-tv-apps.md).

**Redaction** — Field-level rewriting of known-sensitive keys to `***` plus high-entropy string detection. Canonical list: `shared/redact/list.yaml`. *Used in:* [Epic 21 §21.8](epics/epic-21-observability.md), [Epic 23 §23.4](epics/epic-23-security.md).

**Refresh token** — Opaque, long-lived (30 d) credential stored hashed in DB; rotation revokes previous; reuse triggers session-wide revocation. *Used in:* Story 10.4, [Epic 23 §23.1](epics/epic-23-security.md).

**Relay edge** — Ingress for client HTTPS; routes to `mdns_id`; passes TLS ciphertext through without decryption. *Used in:* [Epic 15 §15.2](epics/epic-15-discovery.md).

**Relay region** — Geographic routing hint (`us | eu | ap`); user selects at opt-in. *Used in:* [Epic 15 §15.2](epics/epic-15-discovery.md).

**Replica lag** — Time offset between primary writes and replica catch-up; alert at >60 s; routing falls back to primary. *Used in:* [Epic 19 §19.5](epics/epic-19-scalability.md).

**Replay tape** — Cassette-format recording of HTTP exchanges (e.g., OpenAI Whisper API) used as a deterministic test mock. *Used in:* [Epic 20 §20.4](epics/epic-20-testing.md).

**Reproducible build** — Byte-stable artifact given the same inputs (via `SOURCE_DATE_EPOCH`, `-trimpath`, locked deps, ko, signed checksums). *Used in:* [Epic 22 §22.2](epics/epic-22-devops.md).

**RPO** — Recovery Point Objective. Maximum tolerable data loss; Epic 24.5 targets ≤24 h (last daily backup). *Used in:* [Epic 24 §24.5/24.6](epics/epic-24-data-integrity.md).

**RRF** — Reciprocal Rank Fusion. Hybrid search ranking combining FTS + semantic scores without learned weights (`k=60`). *Used in:* Epic 5.4.

**RS256** — RSA Signature with SHA-256; asymmetric JWT signing; JWKS publishes public keys. *Used in:* Story 10.6, [Epic 23 §23.1](epics/epic-23-security.md).

**RTL baseline** — Arabic-first design philosophy; LTR is an adaptation, not the default. Components ship LTR + RTL Storybook snapshots. *Used in:* [Epic 17 §17.7](epics/epic-17-ux-design-system.md).

**RTO** — Recovery Time Objective. Maximum tolerable downtime; Epic 24.6 targets ≤30 min for DB-lost scenario. *Used in:* [Epic 24 §24.6](epics/epic-24-data-integrity.md).

---

## S

**SAS** — Short Authentication String. 4-word PGP word-list phrase derived from X25519 ECDH shared secret; compared out-of-band (phone call) to defeat MITM. *Used in:* [Epic 15 §15.7](epics/epic-15-discovery.md).

**SBOM** — Software Bill of Materials (cyclonedx); lists transitive dependencies with versions; published per release. *Used in:* [Epic 22 §22.2](epics/epic-22-devops.md), [Epic 23 §23.7](epics/epic-23-security.md).

**Scale axis** — Measurable dimension of capacity (video count, concurrent sessions, QPS). Each Maktaba service has an explicit one. *Used in:* [Epic 19](epics/epic-19-scalability.md).

**`schema_version`** — Top-level integer on JSON artifacts (segment JSON, VTT extras, sprite manifests); readers tolerate higher minor versions via ignore-unknown-fields. *Used in:* [Epic 24 §24.9](epics/epic-24-data-integrity.md).

**Seat cap** — Max user count per tier (1 free, 4 home, unlimited pro). Sentinel UUID excluded. *Used in:* [Epic 16 §16.2](epics/epic-16-subscriptions.md).

**Semantic token** — Token representing a use-case (`--color-bg`, `--color-accent`, `--color-error`) rather than a literal color value. *Used in:* [Epic 17 §17.1](epics/epic-17-ux-design-system.md).

**SemVer** — Semantic versioning `MAJOR.MINOR.PATCH`; pre-release `v1.2.0-rc.N` supported. Clients pin major version. *Used in:* [Epic 22 §22.5](epics/epic-22-devops.md).

**Sentinel user** — Single-user-mode UUID `00000000-0000-0000-0000-000000000001`. All requests resolve to it; admin token bypass also maps to it. *Used in:* Story 10.9, [Epic 19 §19.8](epics/epic-19-scalability.md), [Epic 23 §23.1](epics/epic-23-security.md).

**Sidecar** — Auxiliary file in `.maktaba/` directory alongside source media (subtitles VTT/SRT, sprites, posters, segment JSON). Atomically written. *Used in:* [Epic 24 §24.1](epics/epic-24-data-integrity.md).

**Signed URL** — Short-TTL URL with JWT-style `aud` and `sub=session_id` for direct streaming of poster, sprite, subtitle, segment. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**Single-flight** — One upstream operation services N concurrent identical requests; N-1 wait on the first's result. Go: `singleflight.Group`; Python: `asyncio.Lock`. *Used in:* [Epic 18 §18.3](epics/epic-18-performance.md).

**`SKIP LOCKED`** — Postgres `SELECT … FOR UPDATE SKIP LOCKED`. Workers across hosts coordinate exactly-once job claim. *Used in:* [Epic 19 §19.4](epics/epic-19-scalability.md).

**Smoke flow** — Golden user journey exercising end-to-end happy path (e.g., upload → search → play). Five flows in v1. *Used in:* [Epic 20 §20.5](epics/epic-20-testing.md).

**Soak test** — 24-hour steady-state workload run to detect memory leaks (slope <1 MiB/h) and goroutine leaks (delta ≤50 from baseline). *Used in:* [Epic 18 §18.5](epics/epic-18-performance.md).

**Soft delete** — Logical deletion via `deleted_at TIMESTAMPTZ NULL` rather than hard row removal; partial unique indexes `WHERE deleted_at IS NULL` allow resurrection. *Used in:* [Epic 24 §24.3](epics/epic-24-data-integrity.md).

**Span** — Unit of work within a distributed trace; tagged with attributes (service, route, duration, error, slow flag). Sampled head-based. *Used in:* [Epic 21 §21.3](epics/epic-21-observability.md).

**SPIFFE-style cert** — Service certificate with subject identity (CN = service name); issued by Maktaba's in-process CA. *Used in:* [Epic 23 §23.3](epics/epic-23-security.md).

**SPKI pinning** — Pin SHA-256 of the server's SubjectPublicKeyInfo DER encoding; immune to CA compromise. *Used in:* [Epic 15 §15.2](epics/epic-15-discovery.md), [Epic 23 §23.3](epics/epic-23-security.md).

**Stateless service** — API holds no in-memory session state durable across replica failure (beyond ~5-min ring buffer fast path); transparent rolling restarts. *Used in:* [Epic 19 §19.2](epics/epic-19-scalability.md).

**Sticky session** — Streaming replicas pinned per `session_id` via consistent-hash LB. Failover is clean reopen (`410 Gone`). *Used in:* [Epic 19 §19.3](epics/epic-19-scalability.md).

**Storybook** — Component library documentation + testing UI; every component ships LTR + RTL snapshots, light + dark, A11y AA audit. *Used in:* [Epic 17 §17.2](epics/epic-17-ux-design-system.md).

**Structured logging** — JSON logs in prod (Go slog, Python structlog); required base fields `ts`, `level`, `service`, `msg`; contextual `request_id`, `session_id`, `job_id`, `video_id`, `user_id`. *Used in:* [Epic 21 §21.1](epics/epic-21-observability.md).

**Style Dictionary** — Build tool transforming `tokens.json` into platform-specific outputs (CSS, Swift, Kotlin, JSON). *Used in:* [Epic 17 §17.1](epics/epic-17-ux-design-system.md).

**STT** — Speech-to-Text. Whisper backends: MLX (Mac), faster-whisper, OpenAI API. Resumable per-segment commits. *Used in:* Epic 3.

---

## T

**Tier** — `free` (1 seat), `home` (4 seats), `pro` (unlimited). All paid features are server-side gates. *Used in:* [Epic 16](epics/epic-16-subscriptions.md).

**Tier grace** — 30-day window after downgrade during which premium-only features become read-only; mutations blocked with `403 tier-grace-readonly`. *Used in:* [Epic 16 §16.2](epics/epic-16-subscriptions.md).

**TOFU pinning** — Trust On First Use. Client pins server's TLS SPKI hash on first authenticated connection; verified on all subsequent connections. *Used in:* [Epic 14](epics/epic-14-tv-apps.md), [Epic 15](epics/epic-15-discovery.md), [Epic 23](epics/epic-23-security.md).

**Top Shelf** — Apple TV's above-home-screen surface for app context (tvOS 12+); separate `MaktabaTopShelf` extension target reads shared App Group keychain. *Used in:* [Epic 14 §14.1/14.5](epics/epic-14-tv-apps.md).

**Trace propagation** — W3C `traceparent` header (no B3) carrying trace ID and parent span ID across service boundaries. *Used in:* [Epic 21 §21.3](epics/epic-21-observability.md).

**Transcript** — Complete STT output for a video; multiple variants per audio track / backend / model; history tracked via `is_active` flag. *Used in:* Epic 3, Epic 5.

**`transcript_segment`** — Individual sentence/clause from STT output; has `start_sec`, `end_sec`, `text`, optional `speaker`. Base unit for subtitle render and indexing. *Used in:* Epic 3.6.

**`transcript_unit`** — Chunk of transcript_segments combined for semantic search; configurable by min/max token count or temporal boundaries. *Used in:* Epic 5.1.

**`tsvector`** — Postgres data type representing tokenized, normalized text suitable for full-text search; generated from `transcript_units.text` via `maktaba_normalize`. *Used in:* Epic 5.2.

---

## U

**Unit test** — Pure code, no I/O, no DB, no FFmpeg, no network. ≤100 ms soft cap per test. *Used in:* [Epic 20 §20.1/20.3](epics/epic-20-testing.md).

---

## V

**`vector_index`** — Semantic search index mapping text chunks to embedding vectors; powered by ChromaDB; rebuilt on `reprocess --from-stage index`. *Used in:* Epic 5.3.

**Version-jump guard** — Enforces single-minor upgrade jumps; `v1.0 → v1.2` requires `v1.0 → v1.1 → v1.2`. *Used in:* [Epic 22 §22.6](epics/epic-22-devops.md).

---

## W

**Warm path** — Request hits a hot cache (embedding LRU, probe LRU, HLS segment cache, JWKS cache). *Used in:* [Epic 18](epics/epic-18-performance.md).

**Watch-progress** — User's playback position per `(user_id, video_id)`; last-writer-wins; server-side debounce 1 s. *Used in:* Epic 7.11, [Epic 24 §24.4](epics/epic-24-data-integrity.md).

**Web vitals** — Browser-side performance metrics (LCP, FID, CLS); opt-in only via `POST /api/telemetry/web-vitals`; rate-limited. *Used in:* [Epic 21 §21.8](epics/epic-21-observability.md), [Epic 16 §16.5](epics/epic-16-subscriptions.md).

**Whisper** — OpenAI's open-source multilingual STT model; Maktaba supports MLX, faster-whisper, and OpenAI API backends. *Used in:* Epic 3.

---

## X

**X25519 ECDH** — Elliptic Curve Diffie-Hellman key exchange using Curve25519; used in federation pairing to establish a shared secret. *Used in:* [Epic 15 §15.7](epics/epic-15-discovery.md).

---

## See also

- [Security architecture summary](security.md)
- [Migration catalog](migrations.md)
- [Epic wiki pages](epics/)
- [`specs/architecture.md`](../../specs/architecture.md) — canonical normative doc.
