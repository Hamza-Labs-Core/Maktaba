# Maktaba — End-to-End Test Plan

> Companion to [`epics/20-testing/`](epics/20-testing/) (the test pyramid),
> [`docs/testing.md`](../docs/testing.md) (tier definitions and budgets), and
> [`shared/testtier/`](../shared/testtier/) (the cross-language tier helpers).
> Story [20.5 — End-to-end smoke flows](epics/20-testing/story-20-05-e2e-smoke-flows.md)
> implements **a subset** of this plan as the merge-gate smoke pack; this
> document is the broader **system-level** plan that drives the full E2E
> matrix (smoke + nightly + release-candidate).
>
> **Source documents:**
> - [`architecture.md`](architecture.md) — service split, FSM, gRPC, schema, capability matrix, cloud relay.
> - [`IMPLEMENTATION_ORDER.md`](IMPLEMENTATION_ORDER.md) — topologically-sorted phase plan; this E2E plan walks the same critical paths it identifies.
> - [`docs/testing.md`](../docs/testing.md) + [`shared/testtier/`](../shared/testtier/) — tier soft/hard caps the e2e suite must respect.
> - [Story 22.1 — CI pipeline](epics/22-devops/story-22-01-ci-pipeline.md) + [its plan](epics/22-devops/plan-22-01-ci-pipeline.md) — where the `e2e` gate slots into the merge gate.
>
> **Scope boundary.** "E2E" here means **full-stack, browser-driven Playwright
> flows against a `docker compose` stack** (the four backend services + DB +
> ChromaDB + web). Per-platform native suites (XCUITest for tvOS, Espresso
> for Android TV, Appium / Maestro for Capacitor mobile) are referenced but
> live outside the `make e2e` target — they have their own per-platform
> sections at the end.

---

## 0. Executive summary

| | |
|---|---|
| Tier | `e2e` (per [`shared/testtier/go/tier.go`](../shared/testtier/go/tier.go) — `TierE2E`) |
| Per-test soft cap | **30 s** (warn) · 90 s (fail) |
| Suite wall-clock budget | **5 min** total (`E2ETotalBudget` in [`shared/testtier/go/tier.go`](../shared/testtier/go/tier.go)) |
| Runner | Playwright Test, Chromium + WebKit projects |
| Stack | `deploy/compose/test.yml` — postgres-16, chromadb, api, streaming, pipeline, web |
| Local entry point | `make e2e` |
| CI gate | [`.github/workflows/_e2e.yml`](epics/22-devops/plan-22-01-ci-pipeline.md#section-26) — required check on PRs to `main` |
| Total e2e flows in this plan | **34 user-journey flows** across 6 epics, grouped into 4 packs (smoke / extended / cloud / admin) |

**The plan in one paragraph.** The smoke pack (5 flows, ≤ 5 min) is the
merge-gate suite from Story 20.5 — it has to stay tight. The extended pack
adds 12 flows that cover the rest of ingest + streaming + auth and runs
nightly on `main`. The cloud pack (8 flows) lives in its own compose
overlay because it needs a relay stub and tunnel client. The admin pack
(9 flows) exercises multi-user, ACL, and operational surfaces. The matrix
covers two platforms (`linux/amd64`, `darwin/arm64`), two modes
(single-user, multi-user), two database choices (Postgres, SQLite), and
three streaming modes (direct play, remux, HLS transcode). Acceptance
criteria from 41 stories across Epics 1–25 map back to specific flows
in §5.

---

## 1. Test scenarios — user journeys

Each scenario lists the **flow steps**, the **stories it exercises**, the
**fixtures it consumes**, and the **observable assertions** that mark
pass / fail. Flow IDs are stable (referenced by §5 acceptance mapping).

### 1.1 Ingest pack

#### F-I-01 — Library creation through `ready` state

**Pack:** smoke (already in Story 20.5 plan §6).
**Stories:** 1.1, 1.3, 1.4, 1.5, 1.6, 1.2, 2.1, 2.3, 3.1, 3.5, 3.6,
4.1, 4.3, 5.1, 5.2, 5.3, 5.5, 7.3, 7.4, 7.16, 9.1, 9.2.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `POST /api/libraries` with `{name: "Library", roots: ["/media/maktaba/library"]}` | `201`; row in `libraries`; `library_scan_state = idle`. |
| 2 | `docker exec api cp fixtures/arabic-lecture-60s.mp4 /media/maktaba/library/` | filesystem watcher fires (Story 1.3); `videos` row inserted within 5 s; `state = DISCOVERED`. |
| 3 | (no action — pipeline drains) | FSM advances `DISCOVERED → PROBED → AUDIO_EXTRACTED → TRANSCRIBED → SUBTITLED → INDEXED → READY` within 90 s on fixture-sized media (Story 1.6, Appendix A). |
| 4 | `GET /api/videos/{id}` | `state = "ready"`, `media_info.duration_sec ≈ 60`, `audio_tracks` populated, at least one `transcripts.is_active = true`, `subtitle_files` row pointing at `.vtt` sidecar, poster present in `cache/posters/`. |
| 5 | `GET /api/libraries/{id}/videos` | The new video appears in the grid response, `processed_at` set. |
| 6 | Browser: `WS /ws/library/{id}` watcher | A `video.ready` event arrives within 2 s of `state → READY` (Story 7.16). |

**Pass:** all 6 assertions green within 90 s wall-clock.
**Fail-on:** any FSM state regressed (e.g. `READY` → `TRANSCRIBED`), or duplicate `videos` rows (idempotency under retry — Story 1.3 EC).

#### F-I-02 — Drop video grid badge: `processing → ready`

**Pack:** smoke (Flow 02 in Story 20.5 plan §6).
**Stories:** 1.1, 11.1, 7.4, 7.16, 17.2.
**Assertion:** Playwright sees `data-testid="badge-processing"` first, then `badge-ready` within 90 s.

#### F-I-03 — Soft delete on file removal

**Pack:** extended.
**Stories:** 1.3, 1.5 (slot 0006/0007 `deleted_at`), 9.15.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | After F-I-01 lands `ready`, `docker exec api rm /media/maktaba/library/arabic-lecture-60s.mp4` | Watcher fires within 5 s; `videos.deleted_at` set; row remains in DB. |
| 2 | `GET /api/libraries/{id}/videos` | Row hidden by default; `?include=deleted` surfaces it. |
| 3 | Re-add the same file | `videos.deleted_at` cleared (resurrection per same `content_hash`). |

#### F-I-04 — Multi-track MKV: track selection

**Pack:** extended.
**Stories:** 2.1, 2.2, 3.1, 3.5, 4.4.
**Fixture:** `multi-track-2ad-2subs.mkv` (Story 20.2 AC1).

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Drop fixture into library | `audio_tracks` rows = 2; `subtitle_files` rows = 2 with `is_embedded = true`. |
| 2 | `PATCH /api/videos/{id}` `{audio_track_selection: 1}` | Transcribe job re-runs on track 1. |
| 3 | Wait for `state = READY` | Active transcript references the second audio track. |

#### F-I-05 — Audio-less video skips cleanly

**Pack:** extended.
**Stories:** 1.6, 2.1, 3.6 (Story 20.2 EC1).
**Fixture:** `silent-clip.mp4` (no audio stream).
**Assertion:** FSM lands `state = SKIPPED_NO_AUDIO`; UI shows "no audio" placeholder; no `transcript_segments` rows.

#### F-I-06 — Corrupted moov atom — classified failure

**Pack:** extended.
**Stories:** 2.1 (Story 20.2 EC2), 1.6 error states.
**Fixture:** `corrupt-moov.mp4` (deliberately mangled).
**Assertion:** FSM lands `state = FAILED_PROBE`; `videos.error` JSONB contains a classified error code (`probe.moov_invalid`), no panic in logs.

#### F-I-07 — Pause / resume — exact-second invariant

**Pack:** smoke (Flow 04 in Story 20.5 plan §8).
**Stories:** 3.6, 3.7, 6.4, 6.5, 6.10.
**Assertion:** `processing_jobs.last_segment_end_sec` after resume equals pre-pause value; no duplicate `transcript_segments.id`; final segment count is monotone.

#### F-I-08 — Crash recovery resume

**Pack:** extended.
**Stories:** 3.8, 6.6, 6.8.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | While transcribe is mid-flight, `docker kill pipeline` | Reaper marks job `paused` after stale-heartbeat sweep (10 s). |
| 2 | `docker compose up pipeline` | Same job resumes; `last_segment_end_sec` unchanged at restart point. |
| 3 | Wait for `state = READY` | Total segments == fixture's golden segment count. |

#### F-I-09 — RTL filename round-trip

**Pack:** extended.
**Stories:** Story 20.2 EC3, 1.1, 5.2, 7.8.
**Fixture:** `محاضرة-عربية.mp4`.
**Assertion:** filename round-trips through scan → probe → index → `GET /api/search?q=محاضرة` returns the row; no Unicode normalization drift.

### 1.2 Search pack

#### F-S-01 — Hybrid search returns results with timestamps

**Pack:** smoke (Flow 03 in Story 20.5 plan §7).
**Stories:** 5.1, 5.2, 5.3, 5.4, 5.6, 7.8, 7.6, 8.5, 8.11, 11.4.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | After F-I-01, `GET /api/search?q=بسم الله&mode=hybrid` | ≥ 1 result; each has `video_id`, `start_sec`, `end_sec`, `score`, `score_breakdown` (FTS + semantic weights — Story 5.4 RRF). |
| 2 | Click first result | Web navigates to `/watch/{id}?t={start_sec}`. |
| 3 | Player `currentTime` after first `loadeddata` event | Within 1 s of `start_sec` (Story 18.6 TTFF measure). |
| 4 | Subtitle track visible | VTT cue at `start_sec` matches the FTS snippet. |

#### F-S-02 — FTS5 / `tsvector` Arabic prefix match

**Pack:** smoke.
**Stories:** 5.2 (Arabic FTS config — slot 0019), 7.8.
**Assertion:** `?q=بسم&mode=fts` matches `بسم الله` via Arabic stemming; English mode does not match.

#### F-S-03 — Semantic-only search

**Pack:** extended.
**Stories:** 5.3, 5.4, 7.8, Pipeline `Embed` gRPC.
**Assertion:** Query "morning prayer" returns Arabic segment about `صلاة الفجر` even though no English text appears in the transcript; rank > 0.5.

#### F-S-04 — Saved searches & smart collection

**Pack:** extended.
**Stories:** 5.6, 7.9, 9.14.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `POST /api/saved-searches` with `{q: "tafsir", mode: "hybrid"}` | row in `saved_searches`. |
| 2 | `POST /api/collections` `{kind: "smart", saved_search_id: …}` | row in `collections`; populated asynchronously. |
| 3 | After 5 s, `GET /api/collections/{id}/videos` | matches the saved search live result set. |

#### F-S-05 — Search suggestions

**Pack:** extended.
**Stories:** 5.6, 7.8.
**Assertion:** `GET /api/search/suggest?q=بس` returns suggestions sorted by frequency; LTR mode returns English suggestions.

### 1.3 Streaming pack

#### F-T-01 — Direct play (range serve)

**Pack:** smoke.
**Stories:** 8.1, 8.2, 8.3, 8.15, 10.7, 10.8.
**Fixture:** `h264-aac-faststart.mp4` (Story 20.2; direct-playable on every client per Story 8.2 capability matrix).

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `POST /api/stream/sessions` with `client_profile: "chrome-desktop"` | API returns `{session_id, manifest_url, expires_at}`; mode in session log = `direct`. |
| 2 | Player GETs manifest URL with `Range: bytes=0-1023` | `206 Partial Content`, `Content-Range: bytes 0-1023/{file_size}`, no FFmpeg subprocess spawned for this session. |
| 3 | Player seeks to 30 s, GETs `Range: bytes=N-M` | `206`; player resumes within Story 18.6 TTFF budget. |

#### F-T-02 — Remux only (direct stream)

**Pack:** extended.
**Stories:** 8.4, 8.15, 8.2.
**Fixture:** `h264-aac.mkv` (right codecs, wrong container).

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Open stream session with `client_profile: "safari-desktop"` | `mode = direct_stream_remux`. |
| 2 | First request | FFmpeg launches with `-c copy -f mp4 -movflags +frag_keyframe+empty_moov`; CPU < 5 % over the stream. |

#### F-T-03 — HLS adaptive transcode

**Pack:** smoke.
**Stories:** 8.5, 8.6, 8.7, 8.9, 8.10, 8.11, 8.12, 8.14.
**Fixture:** `hevc-source.mkv` (Story 8.2 forces transcode for browsers).

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Open session, `client_profile: "chrome-desktop"` | API returns `manifest_url`; mode = `hls_transcode`; `streaming_sessions` row pinned. |
| 2 | GET manifest | M3U8 lists 3 renditions (1080p / 720p / 480p) and ≥ 1 audio + ≥ 1 subtitle group per §4.3. |
| 3 | Player requests segments out of order (ABR switch) | Each `seg-N.ts` served from the same FFmpeg session; monotonic segment numbers; no `404`. |
| 4 | Auto-discovered hardware accel | When run on `darwin/arm64`, FFmpeg command line includes `h264_videotoolbox`; on `linux/amd64` falls back to `libx264`. |
| 5 | `EvictHashCache` gRPC | Streaming forgets the probe cache entry for the session's video; next probe re-reads. |

#### F-T-04 — Subtitle live-render (auto-generated)

**Pack:** smoke (extends Flow 03 / F-S-01).
**Stories:** 4.5, 5.2, 8.11.
**Assertion:** Subtitle VTT pulled from `transcript_segments_v` view, not from a `.vtt` file; cues align with audio within ±200 ms.

#### F-T-05 — Subtitle sidecar SRT

**Pack:** extended.
**Stories:** 4.3, 4.4, 8.11.
**Fixture:** `lecture-with-sidecar.mp4` + `lecture-with-sidecar.ar.srt`.
**Assertion:** Pipeline scan discovers sidecar; Streaming converts SRT → VTT on first request; player offers both auto-gen and sidecar tracks.

#### F-T-06 — Chapter markers in HLS

**Pack:** extended.
**Stories:** 8.12, 5.7 / 9.18 (chapter inference — IMPLEMENTATION_ORDER §4.2 resolution: 9.18 canonical).
**Assertion:** Manifest contains `#EXT-X-DATERANGE:CLASS="chapter"` for each chapter row; chapter posters served from `cache/thumbs/`.

#### F-T-07 — Session reap on client disappearance

**Pack:** extended.
**Stories:** 8.9.
**Step:** Player opens session, page closes without `CloseSession`. After 90 s of no segment fetches, `streaming_sessions.state = reaped`, FFmpeg subprocess gone, HLS segments GC'd.

#### F-T-08 — Watch progress sync across devices

**Pack:** extended.
**Stories:** 7.11, 7.16 (events table), 8.5, 11.3.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Browser A plays to 23 s, sends progress | `playback_state.position_sec` ≈ 23. |
| 2 | Browser B opens video detail | UI shows "Resume at 0:23". |
| 3 | WS `/ws/playback/{video_id}` | Fires `playback.update` events across both sessions. |

#### F-T-09 — Transcode concurrency cap & backpressure

**Pack:** extended.
**Stories:** 8.10, 6.7.
**Assertion:** With the per-host cap set to 1, the 2nd `OpenSession` for transcode returns `429` (or queues, depending on the resolved API contract) and `streaming.transcode_queue_depth` metric reflects the wait.

### 1.4 Auth pack

#### F-A-01 — Web login & cookie session

**Pack:** smoke.
**Stories:** 10.1, 10.2, 10.5, 10.6, 10.10, 10.15.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `POST /api/auth/register` | `201`; argon2id hash in `users.password_hash`. |
| 2 | `POST /api/auth/login` | `200`; `Set-Cookie: maktaba_session=…; HttpOnly; Secure; SameSite=Lax`; CSRF token returned. |
| 3 | Authenticated request without CSRF | `403`. |
| 4 | `POST /api/auth/logout` | Session row revoked; subsequent calls `401`. |

#### F-A-02 — Native JWT login + refresh rotation

**Pack:** smoke.
**Stories:** 10.3, 10.4, 10.6.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `POST /api/auth/login` with `Accept: application/json` | RS256 JWT + refresh token; `kid` matches JWKS. |
| 2 | After 15 min, `POST /api/auth/refresh` | New access JWT; old refresh token marked rotated; reuse detection arms. |
| 3 | Replay rotated refresh token | `401`; security audit event logged (Story 10.16 / 21.6). |

#### F-A-03 — Streaming-side offline JWT verify

**Pack:** smoke.
**Stories:** 8.1, 10.6, 10.7, 10.8.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Take a signed manifest URL and mangle the signature | Streaming returns `401`, never asks API. |
| 2 | Rotate JWKS on the API side | Within the JWKS refresh window (Story 10.6 — 10 min), Streaming validates new-`kid` URLs and rejects old-`kid` URLs. |

#### F-A-04 — Brute-force lockout

**Pack:** extended.
**Stories:** 10.11, 10.12.
**Assertion:** 11 wrong-password attempts in 60 s → `429` + `Retry-After`; per-family limiter dimension applies (per-IP + per-username).

#### F-A-05 — Session listing & revoke

**Pack:** extended.
**Stories:** 11.14, 10.5.
**Assertion:** `GET /api/me/sessions` lists active sessions; `DELETE /api/me/sessions/{id}` invalidates the JWT immediately (next request `401`).

#### F-A-06 — Single-user-mode admin bypass

**Pack:** extended.
**Stories:** 10.9, 19.8.
**Assertion:** With `MAKTABA_SINGLE_USER=true` and an `Authorization: Bearer ${MAKTABA_ADMIN_TOKEN}` header, all writes succeed and audit log records the sentinel user `0000…0001`.

#### F-A-07 — Personal Access Token

**Pack:** extended.
**Stories:** 11.13, 10.1.
**Assertion:** Create PAT; use it on `GET /api/me`; revoke; subsequent use returns `401`.

### 1.5 Cloud pack

**Stack overlay:** `deploy/compose/cloud.yml` (overrides `test.yml`) adds:
- `relay` — `maktaba-cloud --role=relay` stub bound to `*.relay.test` (TLS via self-signed CA injected into the browser project).
- `cloud-api` — `--role=api` stub backed by Postgres `cloud_test`.
- A local CCNS proxy so `{user}.maktaba.app` resolves to the relay container.

These flows mock external dependencies (Stripe, APNs/FCM, Postmark) via
local fakes; see `tests/e2e/cloud/fakes/` (created by Epic 25 plans).

#### F-C-01 — Claim server through cloud

**Pack:** cloud (nightly only).
**Stories:** 13.7 (architecture), 25.6, 25.26, 10.18 (BLOCKED until 10.18 lands per IMPLEMENTATION_ORDER §4.1).

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Self-hosted CLI: `maktaba cloud init` | Server generates 96-bit claim token; posts `{token_hash, server_pubkey}` to `cloud-api`. |
| 2 | Cloud UI: enter `K3F9-MZ7P` claim code while signed in | `POST /api/servers/claim`; receives `{server_id, server_token, cloud_endpoint, entitlement}`. |
| 3 | Server stores `server_token` (encrypted with local data key from 10.14) | Server opens WSS tunnel to relay; tunnel registry shows the server within 5 s. |

#### F-C-02 — Tunnel establishment & PING/PONG

**Pack:** cloud.
**Stories:** 13.4, 25.7, 25.8.
**Assertion:** WSS frames flow per architecture §13.4 framing; PINGs at 25 s; tunnel survives a relay-pod simulated restart (server reconnects to a new pod within 10 s).

#### F-C-03 — Remote access via relay (HTTP-over-WSS)

**Pack:** cloud.
**Stories:** 25.9, 25.10, 25.11.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Browser hits `https://alice.maktaba.app/api/libraries` | Cloud edge → relay → tunnel → local API → tunnel → cloud edge → browser. Round trip < 200 ms with relay running on same host. |
| 2 | Response correctness | Body identical to the LAN-direct response. |
| 3 | Range request through relay | Backpressure works: per-stream `WINDOW_UPDATE` frames flow; throughput throttles correctly. |

#### F-C-04 — LAN-first probe wins

**Pack:** cloud.
**Stories:** 13.5, 25.10, 15.1.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Client launches with both LAN candidate and relay URL | 1-second LAN probe wins; subsequent calls go LAN-direct for the next 5 min. |
| 2 | Block LAN (firewall rule) | Within 5 min, client falls back to relay path; `relay_calls_total` counter increments. |

#### F-C-05 — Bandwidth metering

**Pack:** cloud.
**Stories:** 25.13, 25.14, 25.15.
**Assertion:** Streaming through relay updates `bandwidth_daily` table; daily quota cap returns `402 Payment Required` once exceeded; LAN-direct path remains unmetered.

#### F-C-06 — Push fan-out

**Pack:** cloud.
**Stories:** 25.17, 25.18, 25.19, 12.4, 12.10.
**Assertion:** Self-hosted server POSTs `{user_id, payload}` to cloud; fake APNs / FCM endpoints record the push; mobile fake clients receive the message envelope.

#### F-C-07 — Entitlement push & feature gate

**Pack:** cloud.
**Stories:** 16.6, 16.8 (BLOCKED on 10.18), 25.14.
**Assertion:** Cloud worker pushes `ENT_REFRESH` frame; self-hosted server's feature flags update without restart; gated UI elements toggle on next render.

#### F-C-08 — Server revocation (REVOKE frame)

**Pack:** cloud.
**Stories:** 25.7 frame `0x20 REVOKE`, 25.16.
**Assertion:** Admin revokes a server in the cloud; relay sends `REVOKE`; tunnel closes; subsequent client `{user}.maktaba.app` calls return `404` from cloud edge.

### 1.6 Admin pack

#### F-AD-01 — Admin creates additional user

**Pack:** admin.
**Stories:** 10.1, 10.13 / 23.2 (three-role: admin / editor / viewer), 19.8.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Admin `POST /api/admin/users` `{email, role: "viewer"}` | User row created, role = viewer. |
| 2 | New user logs in, attempts `POST /api/libraries` | `403` (writes are admin/editor only). |
| 3 | Audit log row (Story 21.6) | `action = user.create`, `actor = <admin>`, `target = <new user>`. |

#### F-AD-02 — Library ACL assignment

**Pack:** admin.
**Stories:** 10.13, 23.2, 19.8.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Admin attaches viewer to library A only | `library_acl` row inserted. |
| 2 | Viewer lists libraries | Sees A; cannot see B. |
| 3 | Viewer attempts read on B's video | `403`; admin attempt succeeds. |

#### F-AD-03 — Settings flip with `If-Match`

**Pack:** admin.
**Stories:** 7.15, 11.6.
**Assertion:** `PATCH /api/settings` with stale `If-Match` ETag returns `412`; with current ETag succeeds; settings change broadcast to all connected admin clients via WS.

#### F-AD-04 — Manual scan trigger

**Pack:** admin.
**Stories:** 9.6, 7.3.
**Assertion:** `POST /api/libraries/{id}/scan` returns `202` + scan job id; `GET /api/libraries/{id}/scan/{job_id}` streams progress until done; new files detected and queued.

#### F-AD-05 — Queue dashboard live updates

**Pack:** admin.
**Stories:** 11.5, 7.12, 7.13, 6.9, 7.16, 6.4, 6.5.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | Drop 5 fixtures rapidly | Queue dashboard shows 5 rows in `pending`; depth metric matches. |
| 2 | Bulk-pause via UI button | `POST /api/jobs:bulk-pause` returns 200; all 5 rows → `paused` within 2 s; WS pushes per-row state. |
| 3 | Set priority on row 3 | When resumed, row 3 claimed first. |
| 4 | Force-pause a running job | Job's worker observes `pause_requested` and stops at the next segment commit. |

#### F-AD-06 — Job control: cancel + retry

**Pack:** admin.
**Stories:** 6.4, 6.5, 7.12.
**Assertion:** Cancel mid-flight → job state `cancelled`; new identical work returns the cached `READY` result (idempotency from Story 24.2); manual retry resets to `pending` with `attempt += 1` and backoff respected.

#### F-AD-07 — Library deletion: catalog only vs file purge

**Pack:** admin.
**Stories:** 9.15.
**Assertion:** Catalog-only delete removes rows but leaves files on disk; file-purge removes both. Re-scan after catalog-only delete re-imports everything from the same `content_hash`.

#### F-AD-08 — Audit log surface

**Pack:** admin.
**Stories:** 21.6, 9.17 (deprecated by 21.6 per IMPLEMENTATION_ORDER §4.2), 10.16.
**Assertion:** All sensitive actions in F-A-* and F-AD-* show up in `GET /api/admin/audit?actor=…` with `action`, `target_type`, `target_id`, `created_at`, `dedupe_key` populated.

#### F-AD-09 — Backup & restore roundtrip

**Pack:** admin (release-candidate only — slow).
**Stories:** 24.5, 24.6, 24.7.

| Step | Action | Assertion |
|------|--------|-----------|
| 1 | `make backup` against a populated stack | Backup tarball written; checksums verified. |
| 2 | Tear down stack; recreate empty | Empty DB. |
| 3 | `make restore TARBALL=…` | Catalog, transcripts, segments, jobs all return; F-I-01 read assertions still pass against restored DB. |

---

## 2. Test infrastructure

### 2.1 Docker compose stack

**File:** [`deploy/compose/test.yml`](epics/20-testing/plan-20-05-e2e-smoke-flows.md#section-3) (smoke) and `deploy/compose/cloud.yml` (overlay for the cloud pack).

| Service | Image / build | Why |
|---|---|---|
| `postgres` | `postgres:16.4-alpine3.20` | Authoritative state; matches prod default per architecture §1.2. |
| `chromadb` | `chromadb/chroma:0.5.4` | Vector store; embedded mode for v1 per Story 19.4. |
| `api` | `build api/Dockerfile` (ko-built per Story 22.2) | API service binary. |
| `streaming` | `build streaming/Dockerfile` | Streaming service binary. |
| `pipeline` | `build pipeline/Dockerfile` (uv-frozen per 22.2) | Python worker pool. |
| `web` | `build web/Dockerfile.test` | Serves the static React bundle + Vite preview. |
| `relay` *(cloud overlay)* | `build cloud/Dockerfile` w/ `--role=relay` | WSS tunnel endpoint (architecture §13.4). |
| `cloud-api` *(cloud overlay)* | `build cloud/Dockerfile` w/ `--role=api` | Identity, claim, entitlement. |
| `fake-apns`, `fake-fcm`, `fake-stripe`, `fake-postmark` *(cloud overlay)* | Local fakes from `tests/e2e/cloud/fakes/` | External-dep test doubles. |

**Healthcheck contract.** Each service exposes `/healthz` (liveness) and
`/readyz` (readiness — depends on DB connection + migrations applied) per
Story 21.4. Compose `condition: service_healthy` on every `depends_on`
edge guarantees the stack is fully drained before Playwright connects.

**Volumes.**
- `/media/maktaba/library` (rw) — Pipeline + API read; tests write fixtures here via `docker exec`.
- `/var/maktaba/cache` (rw) — Streaming cache; tests inspect via `docker exec` for poster / HLS artifacts.
- `/etc/maktaba` (ro) — Bind-mounted config; tests render the right `*.toml` per matrix cell (§3).

**Ports.** Random host ports allocated by Compose; the stack helper
([`web/e2e/helpers/stack.ts`](epics/20-testing/plan-20-05-e2e-smoke-flows.md#section-4))
reads the port mapping after `up` and writes `baseURL` into the
Playwright `globalSetup` via environment.

### 2.2 Test fixtures

Authoritative spec: [Story 20.2](epics/20-testing/story-20-02-fixtures-seed-data.md).

| Fixture | Purpose | Used by |
|---|---|---|
| `arabic-lecture-60s.mp4` (h264+aac, faststart) | Smoke ingest, direct-play, search | F-I-01, F-I-02, F-S-01, F-T-01 |
| `english-clip-60s.mp4` | English-only transcribe smoke | F-A-* warm-up |
| `mixed-language-60s.mp4` | Language tag (Story 9.8) | F-I-04 variant |
| `multi-track-2ad-2subs.mkv` (HEVC, 2× audio, 2× embedded subs) | Track selection + embedded sub extraction | F-I-04, F-T-05 |
| `silent-clip.mp4` | EC1: no-audio skip | F-I-05 |
| `corrupt-moov.mp4` | EC2: classified failure | F-I-06 |
| `محاضرة-عربية.mp4` | EC3: RTL filename | F-I-09 |
| `h264-aac-faststart.mp4` | Direct-play path | F-T-01 |
| `h264-aac.mkv` | Remux path | F-T-02 |
| `hevc-source.mkv` | HLS transcode path | F-T-03 |
| `lecture-with-sidecar.mp4` + `.ar.srt` | Sidecar SRT discovery | F-T-05 |
| `4k-hdr-sample.mp4` (download-on-demand) | Capacity probe | release-candidate only |

**Golden files.** Each fixture is paired with:
- `*.probe.json` — expected `ffprobe` output (byte-for-byte stable per Story 20.2 TC1).
- `*.transcript.json` — expected segment count, first-token text, total duration sum (text comparison uses `≥ 0.95` token-level F1 to tolerate STT non-determinism — see §2.4 stability tactics).
- `*.content_hash` — BLAKE3 hex of the file bytes.

All fixtures live under `shared/fixtures/samples/`; size guard is enforced
by `make fixtures-check` (Story 20.2 TC2).

**Seeded DB.** `shared/fixtures/seeded_db/maktaba_test.dump` — Postgres
custom-format dump with 1 k videos / 10 k segments for performance tests
in F-T-09 and the perf-ci tier; loads in ≤ 5 s per AC3.

### 2.3 Test harness

#### 2.3.1 Driving the API

`web/e2e/helpers/api.ts`:

```ts
export class ApiClient {
  constructor(private baseURL: string, private adminToken: string) {}

  // Authenticated convenience wrappers — return parsed JSON, throw on non-2xx.
  async createLibrary(req: NewLibraryRequest): Promise<Library> { … }
  async waitForState(videoId: string, state: VideoState, timeoutMs = 90_000): Promise<void> { … }
  async createSession(videoId: string, profile: ClientProfile): Promise<StreamSession> { … }
  async listAuditEvents(filter: AuditFilter): Promise<AuditEvent[]> { … }
}
```

`waitForState` polls every 500 ms with WS-event short-circuit when
`/ws/library/{id}` fires — keeps per-test latency ≤ 1 s once the FSM
transitions, which keeps the suite inside the 30 s soft cap.

#### 2.3.2 Verifying streaming

`web/e2e/helpers/streaming.ts`:

```ts
export async function expectDirectPlay(page: Page, url: string) {
  const response = await page.request.get(url, { headers: { Range: 'bytes=0-1023' } });
  expect(response.status()).toBe(206);
  expect(response.headers()['content-range']).toMatch(/^bytes 0-1023\/\d+$/);
}

export async function expectHlsManifestRenditions(page: Page, manifestUrl: string, expected: number) {
  const body = await page.request.get(manifestUrl).then(r => r.text());
  expect(body.match(/#EXT-X-STREAM-INF:/g)).toHaveLength(expected);
}

export async function expectTtffWithin(page: Page, ms: number) {
  // Per PLAN_REVIEW for Story 18.6: use the `loadeddata` event, not `timeupdate`.
  const ttff = await page.evaluate(() => new Promise<number>(res => {
    const v = document.querySelector('video')!;
    const t0 = performance.now();
    v.addEventListener('loadeddata', () => res(performance.now() - t0), { once: true });
  }));
  expect(ttff).toBeLessThan(ms);
}
```

#### 2.3.3 Verifying search

`web/e2e/helpers/search.ts`:

```ts
export async function expectSearchHit(api: ApiClient, q: string, expectedVideoId: string) {
  const { results } = await api.search({ q, mode: 'hybrid' });
  const hit = results.find(r => r.video_id === expectedVideoId);
  expect(hit, `expected hit for "${q}" on ${expectedVideoId}`).toBeDefined();
  expect(hit!.start_sec).toBeGreaterThanOrEqual(0);
  expect(hit!.score_breakdown.fts + hit!.score_breakdown.semantic).toBeGreaterThan(0);
}
```

#### 2.3.4 Stable visual snapshots

Reuses `stableScreenshot` from Story 20.5 plan §9 — mask dynamic regions
(timestamps, queue depth badges), wait for `document.fonts.ready`, disable
animations. RTL diff threshold: 0.5 % max pixel ratio.

#### 2.3.5 Cloud-pack helpers

`tests/e2e/cloud/helpers/tunnel.ts` exposes:

```ts
export async function killRelayPod(): Promise<void>;
export async function expectTunnelReconnect(serverId: string, withinMs: number): Promise<void>;
export async function injectLatency(ms: number): Promise<void>;   // tc-netem inside relay container
```

### 2.4 Determinism & stability tactics

E2E flakes are the single most expensive class of bug in a merge gate
(Story 20.8). The plan inherits:

| Source of non-determinism | Tactic |
|---|---|
| STT segment boundaries vary ±200 ms across runs | Compare against golden with token-F1 ≥ 0.95 + duration sum within ±2 s. |
| Embedding vector ordering varies across Chroma writes | Search-result assertions check **set membership** and **score rank**, not exact float values. |
| Compose port allocation | `globalSetup` pins ports after `compose up`. |
| Browser font loading races | `await page.evaluate(() => document.fonts.ready)` before every screenshot. |
| Background scan timing | Use Story 7.16 WS events; never `sleep`. |
| Time-of-day in audit-log assertions | Mask `created_at`, assert with `expect.toBeWithin(now - 30s, now + 5s)`. |

The 30 s per-test soft cap (Story 20.1 AC4) is enforced by Playwright's
`test.slow()` budget; a test that breaches the cap warns once; >3× the
cap (90 s) **fails the build** per
[`shared/testtier/ts/playwright.config.ts`](../shared/testtier/ts/playwright.config.ts).

---

## 3. Test matrix

Each cell in the matrix is **a separate CI job** so failures surface per
combination instead of being smeared across a single matrix run.

### 3.1 Platform × matrix

| Platform | Runner image | Smoke pack | Extended pack | Cloud pack | Admin pack |
|---|---|:-:|:-:|:-:|:-:|
| `linux/amd64` | `ubuntu-22.04` (GitHub Actions) | **PR** | nightly | nightly | nightly |
| `darwin/arm64` | `macos-14` (Apple Silicon) | nightly (smoke spot-check) | nightly | release-candidate | release-candidate |
| `linux/arm64` (Ampere) | `ubuntu-22.04-arm` | nightly | release-candidate | — | — |

Per Story 22.1 AC3: build artifacts cover all three OS/arch combos; test
gates run primarily on `linux/amd64` with `darwin/arm64` spot-checks for
darwin-only paths (VideoToolbox hwaccel — F-T-03 step 4).

### 3.2 User-mode matrix

| Mode | How configured | Validates |
|---|---|---|
| **single-user** | `MAKTABA_SINGLE_USER=true`, admin-token bypass per Story 10.9 | F-AD-01..06 use sentinel UUID; auth ACLs collapse to admin-or-deny |
| **multi-user** | Default. Users created, roles assigned via Story 23.2 | F-A-* + F-AD-01..04 cover three-role transitions |

Smoke pack runs single-user (matches Story 20.5 globalSetup that seeds
the admin token). Extended pack runs both. Admin pack runs multi-user.

### 3.3 Storage matrix

| Storage | Config | Validates |
|---|---|---|
| **Postgres 16** | `database.url = postgresql://…` | Default / primary path for all 34 flows. |
| **SQLite (single-file)** | `database.url = sqlite:///var/maktaba/maktaba.db` | F-I-01, F-I-07, F-S-01, F-S-02, F-T-01 — confirms the FTS5 variant from architecture §8.3 and Story 19.5's portability claims; **excluded from cloud pack** (relay assumes Postgres on cloud side). |

Per architecture Appendix C #3, SQLite is "single user, low write load"
— the SQLite cell of the matrix uses single-user mode and the smoke pack
only. Postgres is the default everywhere else.

### 3.4 Streaming-mode matrix

| Streaming mode | Fixture cell | Capability profile | Validates |
|---|---|---|---|
| **Direct play** | `h264-aac-faststart.mp4` | `chrome-desktop`, `ios-native` | F-T-01 |
| **Direct stream (remux)** | `h264-aac.mkv` | `safari-desktop` | F-T-02 |
| **HLS transcode (libx264)** | `hevc-source.mkv` | `chrome-desktop` on `linux/amd64` | F-T-03 |
| **HLS transcode (VideoToolbox)** | `hevc-source.mkv` | `chrome-desktop` on `darwin/arm64` | F-T-03 step 4 |
| **DASH** | same as HLS | DASH-opt-in session | F-T-03 variant; nightly only |

### 3.5 Locale matrix (RTL/LTR)

Every smoke flow runs twice: `?lang=en` (LTR) and `?lang=ar` (RTL).
Visual diff with ≤ 0.5 % pixel ratio threshold per Story 20.5 AC1.5 / TC3.

### 3.6 Browser matrix

| Project | Smoke | Extended | Notes |
|---|:-:|:-:|---|
| `chromium` | ✓ | ✓ | Default; HLS via Vidstack JS player. |
| `webkit` | F-T-* only | F-T-*, F-S-01 | Native HLS path (Story 20.5 EC1). |
| `firefox` | — | F-A-* only | Cookie + CSRF differences; deferred from smoke. |

---

## 4. CI integration

This plan slots into the CI pipeline from
[Story 22.1](epics/22-devops/story-22-01-ci-pipeline.md) and its
[plan](epics/22-devops/plan-22-01-ci-pipeline.md).

### 4.1 Where the gates live

```
PR push ─► trigger ─► fan out:
                    ├─ lint
                    ├─ unit
                    ├─ integration
                    ├─ e2e                ◄── this plan (smoke pack)
                    ├─ perf-ci
                    └─ build-artifacts
                                   │
                                   ▼
                            ci-success (single required check)
                                   │
                                   ▼  (merge to main)
                            nightly workflow ─► extended + cloud + admin packs
                                   │
                                   ▼  (release tag)
                            release-rc workflow ─► full matrix + F-AD-09 backup roundtrip
```

### 4.2 Per-workflow contract

| Gate | Workflow file | Packs run | Wall-clock budget | Where defined |
|---|---|---|---|---|
| `e2e` (PR) | `.github/workflows/_e2e.yml` | smoke (5 flows) | ≤ 5 min (`E2ETotalBudget`) | Story 22.1 plan §2.6 |
| `e2e-nightly` | `.github/workflows/nightly.yml` | smoke + extended + admin (~26 flows) | ≤ 25 min | this plan |
| `e2e-cloud-nightly` | `.github/workflows/cloud-nightly.yml` | cloud overlay (8 flows) | ≤ 15 min | this plan + Epic 25 |
| `e2e-release-rc` | `.github/workflows/release-rc.yml` | full matrix incl. F-AD-09 | ≤ 60 min | this plan |

### 4.3 Inputs and outputs

**Inputs (from earlier gates):**
- The `build-artifacts` gate produces every container image; the e2e gate
  consumes them via `docker compose --pull never` instead of rebuilding
  (saves ~3 min per run).
- The `integration` gate populates the seeded DB tarball as a cached
  artifact; e2e reuses it for flows that need 1 k videos.

**Outputs (always uploaded on failure, retained 14 days):**
- `web/playwright-report/` — HTML report.
- `web/test-results/**/trace.zip` — Playwright traces (DOM, network, console, screenshots).
- `docker-compose-logs.txt` — `docker compose logs` from every service.
- `pg-dump.sql.gz` — DB snapshot at failure moment (single-user, redacted via Story 21.8).
- `prometheus-snapshot.tar` — service metrics dump for the test window.

### 4.4 Flake handling

Per Story 20.8:
- E2E tests are allowed exactly **one automatic retry** (Playwright
  `retries: 1`) on PRs and **zero retries** on nightly.
- Three retries on the same test inside a 7-day window → auto-quarantined
  via the `flake-bot` (Story 20.8), excluded from `ci-success`, and
  filed as a ticket. **Quarantining is a 7-day stopgap, not a fix.**

### 4.5 Branch protection

Branch protection requires `ci-success` (the aggregate job, not
individual gates). The `e2e` gate is one of its `needs:` dependencies,
so a red e2e blocks merge. Force-merge override is the
`force-merge: <reason>` PR-body section validated by
`_pr-body-check.yml`.

### 4.6 Fork safety

Per Story 22.1 EC2: PRs from forks **skip** `e2e` and `perf-ci` because
secrets (cloud-fake credentials, signed-URL keys) aren't available. The
gate reports `skipped` with a "needs maintainer rerun" comment instead
of failing.

---

## 5. Acceptance-criteria mapping

For each story whose ACs are validated by an e2e flow, the table below
names the flow ID and the specific AC bullet. A story can be covered by
multiple flows; an AC can be split across flows. ACs that need
**non-e2e** validation (e.g., type drift, library-level unit invariants,
performance budgets verified only by perf-ci) are not listed here and
remain owned by the unit / integration / perf-ci tiers.

### 5.1 Epic 1 — Scanner & state machine

| Story | AC | Flow | How verified |
|---|---|---|---|
| 1.1 | walks roots, inserts videos | F-I-01 step 2 | row appears within 5 s of drop |
| 1.1 | NOTIFY trigger fires | F-I-01 step 2; F-I-02 | WS event observed |
| 1.2 | content_hash unique per `(library_id, hash)` | F-I-01 step 4; F-I-03 step 3 | duplicate insert is no-op |
| 1.3 | debounced rename-as-update | F-I-03 step 1 | single videos row updated, no extra insert |
| 1.3 | soft-delete on disappearance | F-I-03 step 1 | `videos.deleted_at` set |
| 1.4 | manual scan CLI / API | F-AD-04 | `/api/libraries/{id}/scan` returns 202 |
| 1.5 | libraries + videos schema | F-I-01 step 1 | rows insertable, FKs valid |
| 1.6 | 12-state FSM, 7 stages | F-I-01 step 3 | every transition observed in order |

### 5.2 Epic 2 — Audio extraction

| Story | AC | Flow |
|---|---|---|
| 2.1 | ffprobe → media_info + audio_tracks | F-I-01 step 4 |
| 2.2 | track selection persists | F-I-04 step 2 |
| 2.3 | extract no intermediate WAV | F-I-01 step 3 (memory-bounded; observed via metrics in failure dump) |
| 2.4 | resource accounting | F-T-09 |

### 5.3 Epic 3 — Transcription

| Story | AC | Flow |
|---|---|---|
| 3.1 | STT backend protocol | F-I-01 step 3 (any backend wired) |
| 3.5 | transcript history (`is_active`) | F-I-04 step 3 — re-transcribe creates new active row |
| 3.6 | per-segment durable commit | F-I-07 |
| 3.7 | pause/resume exact-second | F-I-07 |
| 3.8 | crash recovery | F-I-08 |

### 5.4 Epic 4 — Subtitles

| Story | AC | Flow |
|---|---|---|
| 4.1 | SRT + VTT generated | F-I-01 step 4 (`subtitle_files` row) |
| 4.3 | sidecar discovery | F-T-05 |
| 4.4 | embedded extraction | F-I-04, F-T-05 variant |
| 4.5 | live VTT view | F-T-04 |

### 5.5 Epic 5 — Search indexing

| Story | AC | Flow |
|---|---|---|
| 5.1 | search-units schema | F-S-01 step 1 |
| 5.2 | FTS5 / tsvector Arabic | F-S-02 |
| 5.3 | Chroma vector index | F-S-03 |
| 5.4 | hybrid RRF | F-S-01 step 1 (score_breakdown present) |
| 5.5 | incremental indexing | F-I-01 step 3 (search hits while transcribe still running) |
| 5.6 | suggestions | F-S-05 |
| 5.7 / 9.18 | chapters | F-T-06 |

### 5.6 Epic 6 — Job queue

| Story | AC | Flow |
|---|---|---|
| 6.1–6.3 | claim, heartbeat | F-I-01 step 3 |
| 6.4 | pause/resume/cancel | F-I-07, F-AD-05, F-AD-06 |
| 6.5 | backoff & retry | F-AD-06 |
| 6.6 | reaper | F-I-08 |
| 6.7 | concurrency caps | F-T-09 |
| 6.8 | graceful shutdown | F-I-08 |
| 6.9 | queue stats | F-AD-05 step 1 |
| 6.10 | last_segment_end_sec invariant | F-I-07 (no dup ids) |

### 5.7 Epic 7 — API

| Story | AC | Flow |
|---|---|---|
| 7.3 | library CRUD | F-I-01 step 1 |
| 7.4 | video list / detail / patch / delete | F-I-01 step 4; F-AD-07 |
| 7.5 | processing control | F-AD-05, F-AD-06 |
| 7.6 | transcript window | F-S-01 step 1 (response shape) |
| 7.7 | subtitles + chapters read | F-T-04, F-T-06 |
| 7.8 | search API | F-S-01..F-S-05 |
| 7.9 | saved searches | F-S-04 |
| 7.10 | stream session lifecycle | F-T-01..F-T-03 step 1 |
| 7.11 | watch progress sync | F-T-08 |
| 7.12 | job control | F-AD-05, F-AD-06 |
| 7.13 | queue stats | F-AD-05 |
| 7.14 | collections + tags + speakers | F-S-04 |
| 7.15 | settings | F-AD-03 |
| 7.16 | WS fan-out | F-I-01 step 6; F-T-08 step 3 |
| 7.18 | gRPC clients | F-T-01..F-T-03 (cross-service path) |
| 7.19 | validation + rate limiting | F-A-04 |
| 7.20 | health + version | implicit in every flow (`/healthz` precondition) |

### 5.8 Epic 8 — Streaming

| Story | AC | Flow |
|---|---|---|
| 8.1 | signed-URL middleware | F-T-01 step 1; F-A-03 |
| 8.2 | capability matrix | F-T-01..F-T-03 (mode selected per profile) |
| 8.3 | direct play 206 | F-T-01 |
| 8.4 | remux | F-T-02 |
| 8.5 | HLS transcode | F-T-03 |
| 8.6 | DASH | F-T-03 variant (nightly) |
| 8.7 | hwaccel auto-detect | F-T-03 step 4 |
| 8.9 | session store + sticky transcoder + reaper | F-T-03 step 3; F-T-07 |
| 8.10 | concurrency caps | F-T-09 |
| 8.11 | live VTT | F-T-04 |
| 8.12 | chapter markers | F-T-06 |
| 8.13 | posters / sprites | F-I-01 step 4 (poster in cache) |
| 8.14 | LRU cache | F-T-07 (segments GC'd) |
| 8.15 | probe cache | F-T-03 step 5 |

### 5.9 Epic 9 — Library management

| Story | AC | Flow |
|---|---|---|
| 9.1, 9.2 | config + watcher | F-I-01 step 2 |
| 9.6 | manual scan | F-AD-04 |
| 9.7 | library stats | F-AD-04 (response includes counts) |
| 9.14 | smart collections | F-S-04 step 2 |
| 9.15 | catalog vs purge deletion | F-AD-07 |
| 9.17 | audit log (deprecated by 21.6) | F-AD-08 |

### 5.10 Epic 10 — Auth

| Story | AC | Flow |
|---|---|---|
| 10.1 | argon2id users | F-A-01 step 1 |
| 10.2 | web login + CSRF | F-A-01 |
| 10.3 / 10.4 | JWT + refresh rotation | F-A-02 |
| 10.5 | logout / revoke | F-A-01 step 4; F-A-05 |
| 10.6 | RS256 + JWKS | F-A-02 step 1; F-A-03 |
| 10.7 | streaming offline verify | F-A-03 |
| 10.8 | signed URL | F-T-01 step 1 |
| 10.9 | single-user bypass | F-A-06 |
| 10.10 | CSRF | F-A-01 step 3 |
| 10.11 | brute-force | F-A-04 |
| 10.12 | auth rate limits | F-A-04 |
| 10.13 / 23.2 | role ACL | F-AD-01, F-AD-02 |
| 10.14 | secrets loading | implicit (cloud-pack server-token at-rest in F-C-01) |
| 10.15 | TLS / HSTS / secure cookies | F-A-01 step 2 (cookie flags asserted) |
| 10.16 | security audit log | F-A-02 step 3 |
| 10.17 / 15.6 | device pairing | covered by Capacitor / Maestro spot-check (§6) |
| 10.18 (MISSING — IMPLEMENTATION_ORDER §4.1) | Ed25519 identity | **blocks F-C-01, F-C-07** until authored |
| 11.13 | PAT | F-A-07 |
| 11.14 | session listing | F-A-05 |

### 5.11 Epic 11 — Web UI

| Story | AC | Flow |
|---|---|---|
| 11.1 | library browser | F-I-02 |
| 11.2 | video detail | F-S-01 step 2 |
| 11.3 | player | F-S-01 step 3; F-T-01..F-T-03 |
| 11.4 | search UI | F-S-01..F-S-05 |
| 11.5 | queue dashboard | F-AD-05 |
| 11.6 | settings UI | F-AD-03 |
| 11.7 | responsive | smoke pack run at 360×640 + 1440×900 viewports |
| 11.8 | dark / light theme | smoke pack runs both themes for visual diff |
| 11.11 | a11y | per-flow `@axe-core/playwright` audit (zero serious/critical) |
| 11.12 | i18n RTL | every flow runs `?lang=ar` |

### 5.12 Epics 13–14 — Desktop & TV

Desktop e2e uses the Tauri test runner separately (Story 13.1); TV e2e is
explicitly **out of scope** for `make e2e` per Story 20.5 EC3 and lives
in per-platform XCUITest / Espresso suites (a gap flagged by
IMPLEMENTATION_ORDER §4.5).

### 5.13 Epic 16 — Subscriptions

| Story | AC | Flow |
|---|---|---|
| 16.1 | free tier | implicit baseline |
| 16.4 / 16.6 / 16.8 (BLOCKED on 10.18) | entitlement push | F-C-07 |
| 16.5 / 16.7 | telemetry | covered by integration tier; not e2e |

### 5.14 Epic 21 — Observability

| Story | AC | Flow |
|---|---|---|
| 21.1 | structured logging | every flow's failure dump contains JSON logs |
| 21.4 | healthz / readyz | implicit (compose `service_healthy` precondition) |
| 21.6 | audit log | F-AD-08 |

### 5.15 Epic 22 — DevOps

| Story | AC | Flow |
|---|---|---|
| 22.1 | CI pipeline | this plan **is** the e2e gate |
| 22.3 | compose stack | every flow |
| 22.6 | upgrade / rollback | F-AD-09 (RC only) |

### 5.16 Epic 24 — Data integrity

| Story | AC | Flow |
|---|---|---|
| 24.1 | atomic sidecar writes | F-I-01 step 4 (no partial `.vtt` ever observed) |
| 24.2 | idempotent jobs | F-AD-06 |
| 24.5 / 24.6 / 24.7 | backup, restore, verify | F-AD-09 (RC only) |
| 24.8 | identity stability | F-I-03 step 3 |

### 5.17 Epic 25 — Cloud relay

| Story | AC | Flow |
|---|---|---|
| 25.6 / 25.26 | claim flow | F-C-01 |
| 25.7 / 25.8 | tunnel frames + PING | F-C-02 |
| 25.9 / 25.10 / 25.11 | relay request path + LAN-first | F-C-03, F-C-04 |
| 25.13–25.15 | bandwidth + billing | F-C-05 |
| 25.16 | revoke | F-C-08 |
| 25.17–25.19 | push fan-out | F-C-06 |

---

## 6. Out-of-band native suites

These exist to complete the test pyramid but are **not** part of the
Playwright `make e2e` target. They are listed here so the coverage story
is whole; each row has its own owning epic.

| Surface | Tool | Trigger | Notes |
|---|---|---|---|
| Capacitor iOS / Android | Maestro (preferred) or Appium spot-check | nightly per Story 20.5 EC2 | One flow: `library_open.yaml`. Native player path covered by Story 12.3 integration tests. |
| tvOS | XCUITest | per-PR on `apps/tvos/**` changes | Story 14.1 acceptance. **Currently unowned per IMPLEMENTATION_ORDER §4.5** — add a story to Epic 20 before TV ships. |
| Android TV | JUnit5 + Compose-test (Espresso) | per-PR on `apps/androidtv/**` changes | Story 14.2 acceptance. Same gap as tvOS. |

---

## 7. Concrete first-iteration deliverables

To stand this plan up incrementally without blocking on every prerequisite:

1. **Phase A** — ship the 5 smoke flows from Story 20.5 plan as-is. They
   already pass with the P0–P7 critical path landed. (F-I-01 = Flow 02,
   F-I-07 = Flow 04, F-S-01 = Flow 03, F-A-01 = Flow 01 setup, RTL diff
   = Flow 05.)
2. **Phase B** — add F-T-01, F-T-02, F-T-03 once Epic 8 (streaming) lands
   the three modes.
3. **Phase C** — add F-AD-01..F-AD-06 once Epics 9, 21.6, 23.2 ship.
4. **Phase D** — add F-I-03..F-I-09, F-S-02..F-S-05 (nightly only).
5. **Phase E** — stand up cloud overlay + F-C-01..F-C-08 once Story 10.18
   lands and Epic 25 plans are authored.
6. **Phase F** — wire darwin/arm64 + linux/arm64 matrix cells.
7. **Phase G** — wire F-AD-09 backup-roundtrip into the release-rc
   workflow.

---

## 8. Open items

These items must be resolved (story-side) before the matching flows can
be authored:

| Item | Blocker | Where to fix |
|---|---|---|
| **Story 10.18** Ed25519 identity keys missing | F-C-01, F-C-07 blocked | author story + plan in `epics/10-auth-security/` per IMPLEMENTATION_ORDER §4.1 |
| **Cloud-fake images** for APNs/FCM/Stripe/Postmark | F-C-05, F-C-06 blocked | Epic 25 plans (35 stories still without plans per IMPLEMENTATION_ORDER §5) |
| **TV e2e surface** (XCUITest + Compose-test) | TV coverage in matrix | extend Story 20.1 or add a new story to Epic 20 |
| **`Pipeline.Enqueue*` gRPC** undefined | F-AD-04 implementation ambiguous | architectural decision per IMPLEMENTATION_ORDER §4.5 |
| **Endpoint canonicalization** (`/api/me/playback-state` vs `/api/stream/sessions/{id}/progress`) | F-T-08 endpoint choice | architectural decision per IMPLEMENTATION_ORDER §4.5 |

---

## 9. References

- [`architecture.md`](architecture.md) — service split, FSM, gRPC, schema, capability matrix, cloud relay protocol.
- [`IMPLEMENTATION_ORDER.md`](IMPLEMENTATION_ORDER.md) — phases, dependencies, conflicts, missing stories.
- [`docs/testing.md`](../docs/testing.md) — tier definitions, soft / hard caps, budget enforcer.
- [`shared/testtier/`](../shared/testtier/) — Go / Python / TS tier constants and netguards.
- [Story 20.1 — Test pyramid](epics/20-testing/story-20-01-test-pyramid.md) + [plan](epics/20-testing/plan-20-01-test-pyramid.md).
- [Story 20.2 — Fixtures & seed data](epics/20-testing/story-20-02-fixtures-seed-data.md) + [plan](epics/20-testing/plan-20-02-fixtures-seed-data.md).
- [Story 20.4 — Integration tests](epics/20-testing/story-20-04-integration-tests.md) — the tier directly below this one.
- [Story 20.5 — E2E smoke flows](epics/20-testing/story-20-05-e2e-smoke-flows.md) + [plan](epics/20-testing/plan-20-05-e2e-smoke-flows.md) — the merge-gate subset of this plan.
- [Story 20.6 — Contract tests](epics/20-testing/story-20-06-contract-tests.md) — schema drift gates that complement the e2e behavioral assertions.
- [Story 20.7 — Perf regression CI](epics/20-testing/story-20-07-perf-regression-ci.md) — sibling gate; not e2e but adjacent.
- [Story 20.8 — Flaky-test policy](epics/20-testing/story-20-08-flaky-test-policy.md) — retry + quarantine policy that this plan inherits.
- [Story 22.1 — CI pipeline](epics/22-devops/story-22-01-ci-pipeline.md) + [plan](epics/22-devops/plan-22-01-ci-pipeline.md) — host workflow for the e2e gate.
