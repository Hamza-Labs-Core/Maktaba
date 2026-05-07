# Maktaba — Independent Spec Review

> Critical audit of [`specs/architecture.md`](architecture.md) and the four epic
> documents under [`specs/epics/`](epics/). Findings are grouped by severity
> and category. Where two documents disagree, the resolution is left to the
> spec owners but a recommendation is offered.
>
> **Documents reviewed:**
> - [`specs/architecture.md`](architecture.md) — system design (2,292 lines)
> - [`specs/epics/01-pipeline.md`](epics/01-pipeline.md) — Pipeline (Epics 1–6)
> - [`specs/epics/02-api-streaming.md`](epics/02-api-streaming.md) — API/Streaming/Library/Auth (Epics 7–10)
> - [`specs/epics/03-clients-discovery.md`](epics/03-clients-discovery.md) — Clients/Discovery/Subscriptions/UX (Epics 11–17)
> - [`specs/epics/04-nonfunctional.md`](epics/04-nonfunctional.md) — NFRs (Epics 18–24)

---

## Severity Legend

- **🔴 Blocker** — Must fix before implementation; will cause runtime breakage or schema corruption.
- **🟠 Major** — Conflicting requirements that will be settled the wrong way unless explicitly resolved.
- **🟡 Minor** — Inconsistency or gap that should be tightened but won't break a build.
- **🔵 Info** — Observation that does not require action but the team should be aware of.

---

## 1. Cross-Document Conflicts

### 1.1 Schema conflicts

#### 🔴 1.1.a — `videos.content_hash` uniqueness scope is contradicted three ways

- [`architecture.md` §8.1, line 1307](architecture.md): `content_hash TEXT NOT NULL UNIQUE` — **global** uniqueness.
- [`epics/01-pipeline.md` §1.5](epics/01-pipeline.md): "`videos.content_hash` is `UNIQUE` per library, not globally" — explicitly proposes the change with a migration `000X_videos_unique_per_library.sql`.
- [`epics/01-pipeline.md` §1.2 AC](epics/01-pipeline.md): "two files with identical bytes (and therefore identical hashes), when both are scanned, only the first creates a row" — this implies **global** uniqueness, not per-library.
- [`epics/02-api-streaming.md` Story 9.4 AC-2](epics/02-api-streaming.md): "two files with the same hash … only the first inserts a `videos` row" — also implies global uniqueness.
- [`epics/04-nonfunctional.md` Story 24.8 AC-3](epics/04-nonfunctional.md): "Copy creates a new path → same hash → already-ready row served immediately (no re-process)" — implies global uniqueness so transcripts are reused across libraries.

The same epic file (01-pipeline) contradicts itself between Story 1.2 and Story 1.5; epics 02 and 04 also contradict Story 1.5. **Resolution required:** decide once, propagate to all four documents, and update `architecture.md` §8.1 if per-library is chosen.

#### 🔴 1.1.b — `transcripts` UNIQUE constraint blocks the documented re-processing flow

- [`architecture.md` §8.1, line 1365](architecture.md): `UNIQUE (video_id, audio_track_id, backend, model)`.
- [`epics/01-pipeline.md` Story 3.5 AC](epics/01-pipeline.md): "re-running a transcribe with a different backend creates a **new** transcript row; the old one is preserved … and tagged with `transcripts.is_active = false`."

The unique constraint as written makes re-processing with the **same** `(backend, model)` impossible (UNIQUE violation), and `is_active` is not part of the key. Story 3.5 also references `transcripts.is_active` and a related column on `transcript_segments_v` (Story 4.5), but **`is_active` is not in the architecture schema at all**.

**Resolution:** add an `is_active BOOLEAN` column to `transcripts`, change the UNIQUE to `UNIQUE (video_id, audio_track_id, backend, model) WHERE is_active = true`, document.

#### 🔴 1.1.c — `subtitle_files.is_embedded` column referenced but not defined

- [`architecture.md` §8.1, line 1392–1401](architecture.md): `subtitle_files` columns are `(id, video_id, transcript_id, format, language, path, is_external, created_at)`.
- [`epics/01-pipeline.md` Story 4.4 AC](epics/01-pipeline.md): "`is_external = false`, `is_embedded = true` (new column)".

The epic describes `is_embedded` as "new" but no migration story owns it. Add to architecture schema.

#### 🔴 1.1.d — `transcripts_fts` is built on the wrong table

- [`architecture.md` §8.3](architecture.md): SQLite FTS5 virtual table indexes columns `(text, video_id, segment_id, language)` — implies it tracks `transcript_segments`.
- [`epics/01-pipeline.md` Story 5.1](epics/01-pipeline.md): introduces a new `transcript_units` table at the indexer level; segments are re-chunked into units before embedding.
- [`epics/01-pipeline.md` Story 5.2](epics/01-pipeline.md): "for SQLite, the schema in architecture §8.3 is created. For Postgres, an equivalent layer is created using a `tsvector` column on `transcript_units`".

The Postgres path indexes `transcript_units`; the SQLite path (per architecture) indexes `transcript_segments`. These are **different sources of truth** with different chunking. Either the SQLite path also needs a `transcript_units`-backed FTS, or the Postgres path must reuse `transcript_segments`. **Resolution:** unify on `transcript_units` for both engines and update §8.3.

#### 🟠 1.1.e — `tags` schema disagrees on column set

- [`architecture.md` §8.2, line 1418–1421](architecture.md): `tags(id, name)`, `name UNIQUE`.
- [`epics/02-api-streaming.md` Story 9.12 AC-1](epics/02-api-streaming.md): introduces `display_name` (preserve casing) and `normalized_name` (uniqueness key, NFC + casefold).

Migration not specified. Story 7.14 AC-3 also relies on the new shape. Update arch §8.2 schema.

#### 🟠 1.1.f — Multiple audit-log tables proposed by different epics

Three different audit tables are described:
- `library_audit` — [`epics/02-api-streaming.md` Story 9.17](epics/02-api-streaming.md): library lifecycle events.
- `security_audit` — [`epics/02-api-streaming.md` Story 10.16](epics/02-api-streaming.md): auth events.
- `audit_log` — [`epics/04-nonfunctional.md` Story 21.6](epics/04-nonfunctional.md): "all sensitive actions" with a stronger trigger guarantee.

Story 21.6's `audit_log` includes most of the events from `library_audit` and `security_audit`. **Resolution:** pick one canonical table (likely `audit_log` with a `category` column) and unify the three stories. Otherwise the system has three append-only tables with overlapping purpose and three retention policies.

#### 🟡 1.1.g — `streaming_sessions` table referenced but never defined

- [`architecture.md` §4.2](architecture.md): "Sessions live in `streaming_sessions` (Postgres)" but §8 contains no schema for the table.
- [`epics/02-api-streaming.md` Story 8.9 AC-1](epics/02-api-streaming.md): defines columns `{id, video_id, user_id, client_profile, mode, format, host, pid, started_at, last_segment_at, closed_at, closed_reason}`.
- [`epics/02-api-streaming.md` Story 9.15 AC-1](epics/02-api-streaming.md): expects FK cascade from `libraries` → `streaming_sessions`, but the schema in 8.9 doesn't include `library_id`.

Add the schema to architecture.md §8 and reconcile the cascade story (probably via `videos.library_id`, not directly).

#### 🟡 1.1.h — Tables referenced but absent from architecture.md §8

The following tables are **read or written by stories** but never defined in the architecture schema. Each needs a migration story owner and a §8 schema entry:

| Table | Introduced by | Referenced by |
|------|---------------|---------------|
| `web_sessions` | [02 Story 10.2](epics/02-api-streaming.md) | 10.5 |
| `refresh_tokens` | [02 Story 10.3](epics/02-api-streaming.md) | 10.4, 10.5 |
| `transcript_units` | [01 Story 5.1](epics/01-pipeline.md) | 5.2, 5.4 |
| `library_topics`, `video_topics` | [02 Story 9.9](epics/02-api-streaming.md) | 9.9 |
| `media_features` | [02 Story 9.10](epics/02-api-streaming.md) | 9.10 |
| `library_acl` | [04 Story 19.8](epics/04-nonfunctional.md) | 23.2 |
| `library_sweeps` | [02 Story 9.3](epics/02-api-streaming.md) | 9.3 |
| `library_audit` (or `audit_log`) | [02 Story 9.17](epics/02-api-streaming.md), [04 Story 21.6](epics/04-nonfunctional.md) | 9.15, 9.17, 10.16 |
| `security_audit` | [02 Story 10.16](epics/02-api-streaming.md) | 10.16, 21.6 |
| `app_settings` | [02 Story 7.15](epics/02-api-streaming.md) | 8.2 (`LISTEN profiles_changed`) |
| `streaming_sessions` | architecture §4.2 / [02 Story 8.9](epics/02-api-streaming.md) | 7.10, 8.9, 9.15 |
| `events` (WS replay buffer) | [04 Story 19.2 AC-3](epics/04-nonfunctional.md) | 19.2 |
| `devices` (push tokens) | [03 Story 12.4](epics/03-clients-discovery.md) | 12.4 |
| `federation` | [03 Story 15.3](epics/03-clients-discovery.md) | 15.3 |
| `library_audit_archive` | [02 Story 9.17 AC-3](epics/02-api-streaming.md) | 9.17 |

#### 🟡 1.1.i — `transcripts.is_active` referenced widely; absent from schema

Added by Story 3.5; consumed by Stories 4.3, 4.5, 5.1, 5.5. Not in architecture §8.1.

### 1.2 API endpoint conflicts

#### 🔴 1.2.a — Duplicate job/queue REST surfaces

Two parallel naming schemes for the same resource:

| Resource | Epic 7 (canonical) | Epic 21.7 (newly introduced) |
|----------|--------------------|------------------------------|
| Queue stats | `GET /api/queue/stats` (Story 7.13) | `GET /api/processing/status` (Story 21.7 AC-1) |
| Per-job detail | `GET /api/jobs/{id}` ([arch §9.5](architecture.md)) | `GET /api/processing/jobs/{id}` (Story 21.7 AC-2) |
| Per-job WS | `WS /ws/jobs` (Story 7.16) | `WS /ws/processing/{id}` (Story 21.7 AC-3) |

The architecture document has only `/api/jobs` and `/api/queue/stats` ([§9.5](architecture.md)). Story 21.7 invents a parallel `/api/processing/*` namespace. **Resolution:** delete or rename the duplicates; pick one.

#### 🔴 1.2.b — `GET /api/stream/capabilities` requires a Pipeline gRPC method that does not exist

- [`architecture.md` §9.4 + §9.9](architecture.md): the `Streaming` gRPC service exposes `OpenSession`, `CloseSession`, `EvictHashCache`, `HealthCheck` only.
- [`epics/02-api-streaming.md` Story 7.10 AC-4](epics/02-api-streaming.md): `GET /api/stream/capabilities` is "fetched live from Streaming over gRPC and cached for 60 s."
- [`epics/02-api-streaming.md` Story 8.8 AC-4](epics/02-api-streaming.md): `HealthCheck` is repurposed to return capabilities-shaped data (`ffmpeg_available, hwaccel, transcode_slots, cache_used_gib`).

The two are reconcilable (HealthCheck doubles as Capabilities), but the architecture's gRPC schema omits this. **Resolution:** add `Streaming.GetCapabilities` as a distinct RPC, or document that the API derives capabilities from `HealthCheck`.

#### 🟠 1.2.c — `Pipeline.ExtractEmbeddedSubtitle` RPC missing from architecture

- [`epics/01-pipeline.md` Story 4.4 AC-2](epics/01-pipeline.md) defines `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)` as gRPC.
- [`architecture.md` §9.9](architecture.md) Pipeline service: only `Embed`, `Transcribe`, `ListBackends`, `HealthCheck`.

Add to §9.9.

#### 🟠 1.2.d — JWT claim `library_ids` referenced only by the security epic

- [`epics/02-api-streaming.md` Story 23.2 AC-3](epics/02-api-streaming.md): "Streaming validates JWT signature and checks library membership against the JWT's `library_ids` claim."
- [`epics/02-api-streaming.md` Story 10.3 AC-2](epics/02-api-streaming.md): JWT shape includes `iss, aud, sub, iat, exp, jti, is_admin, kid` — **no `library_ids`**.
- [`epics/02-api-streaming.md` Story 10.8 AC-1](epics/02-api-streaming.md): manifest URL JWT carries `aud, sub, exp` only.

If 23.2 is enforced, every signed URL must carry `library_ids` — but the minter (10.8) doesn't include them, and Streaming has no other channel to learn library membership offline. **Resolution:** either add `library_ids` to the JWT claims (and to the minter and to the streaming verifier), or downgrade 23.2 AC-3 to "Streaming trusts the API's session-creation decision."

#### 🟡 1.2.e — Signed-URL audiences underspecified

- [`epics/02-api-streaming.md` Story 8.1 AC-1](epics/02-api-streaming.md) lists JWT audiences `streaming` and `streaming-direct`.
- [`epics/02-api-streaming.md` Story 10.8 AC-3](epics/02-api-streaming.md) introduces a third audience `streaming-static` for posters/sprites/subtitles — but no Streaming endpoint specifies that it accepts this audience, and Story 8.13 (poster serving) does not document JWT validation at all.

**Resolution:** decide whether posters/sprites are signed (recommended) or public; if signed, document that the middleware in 8.1 honors `streaming-static` for the `/stream/posters/*`, `/stream/sprites/*`, `/stream/{session_id}/subs/*` paths.

### 1.3 State machine conflicts

#### 🟠 1.3.a — Video state machine has more states than `architecture.md` lists

- [`architecture.md` §3](architecture.md): `DISCOVERED → PROBED → AUDIO_EXTRACTED → TRANSCRIBED → INDEXED → THUMBNAILED → READY` (plus `FAILED`).
- States introduced by epics, not in the FSM:
  - `MISSING` ([01 Story 1.5](epics/01-pipeline.md))
  - `READY_NO_AUDIO` ([01 Story 2.1 AC](epics/01-pipeline.md))
  - `SUPERSEDED` ([02 Story 9.7 AC-1](epics/02-api-streaming.md), [02 Story 9.6 AC-2](epics/02-api-streaming.md))
  - `CORRUPTED` ([04 Story 24.6 AC-1.3](epics/04-nonfunctional.md))

These are referenced in stats endpoints, library deletion, scan rehash, and DR. The architecture's `videos.state` is a `TEXT` so the DB allows them, but the FSM diagram is now wrong. **Resolution:** redraw the FSM in §3 with all six states and explicit transition arrows.

#### 🟠 1.3.b — `subtitle_gen` stage is missing from the state machine

- [`architecture.md` §3](architecture.md) state machine omits `subtitle_gen`.
- [`epics/01-pipeline.md` Story 4.1](epics/01-pipeline.md): `subtitle_gen` runs after `state = TRANSCRIBED` and "advances toward `INDEXED`".
- [`architecture.md` §7.1](architecture.md) `processing_jobs.stage` enumerates `scan|probe|extract|transcribe|index|thumb` — **no `subtitle_gen`**.

Either subtitle_gen is a side-effect of `transcribe` completion or it's a real stage; the documents disagree. **Resolution:** add a `subtitle_gen` state and stage entry, or fold subtitle generation into the `transcribe` finalization.

#### 🟡 1.3.c — Stage name `thumb` vs `thumbnail`

- [`architecture.md` §7.1](architecture.md) comment: "`stage TEXT NOT NULL,           -- scan|probe|extract|transcribe|index|thumb`".
- [`architecture.md` §7.4](architecture.md) table: stage name "thumbnail".
- [`epics/01-pipeline.md` Epic 6.7 AC-1](epics/01-pipeline.md): "`thumbnail=2`".

Pick one; settle on `thumbnail`.

### 1.4 Configuration conflicts

#### 🔴 1.4.a — `auth_rate_per_min` differs by 3×

- [`epics/02-api-streaming.md` Story 7.19 AC-4](epics/02-api-streaming.md): `auth_rate_per_min` default **30**.
- [`epics/04-nonfunctional.md` Story 23.6 AC-1](epics/04-nonfunctional.md): "`/api/auth/login 10/min per IP`".

The implementation will land at whichever value is read at code-time. **Resolution:** pick one (lower wins for security; recommend 10 for `/login`, 30 for the broader `/api/auth/*` surface, distinguishing the two limits).

#### 🟠 1.4.b — Failed-login lockout window 5 min vs 15 min

- [`epics/02-api-streaming.md` Story 10.11 AC-1](epics/02-api-streaming.md): "5 failed logins per username within `failed_login_window_sec` (default **900 s**)".
- [`epics/04-nonfunctional.md` Story 23.6 AC-3](epics/04-nonfunctional.md): "≥ 5 failures in **5 minutes** locks the user for 15 minutes".

Both use 5 attempts and 15 min lockout, but disagree on the *measurement* window (15 min vs 5 min). The 5-minute window is more sensitive; the 15-minute window catches slow attacks. Pick one.

#### 🟠 1.4.c — Heartbeat interval 5s vs 30s referenced inconsistently

- [`architecture.md` §7.1](architecture.md): `last_heartbeat_at` "updated every 5 s while running".
- [`architecture.md` §7.6](architecture.md): heartbeat interval also "every 5 s" implied by progress commit cadence.
- [`epics/01-pipeline.md` Story 6.3 AC-1](epics/01-pipeline.md): `heartbeat_sec` default 5 s.
- [`epics/01-pipeline.md` Story 6.6 AC-1](epics/01-pipeline.md): "`stale_claim_sec = 90` (3× the **30 s heartbeat**)".

Story 6.6 says 30 s, every other reference says 5 s. **Resolution:** correct the Story 6.6 comment; `90 = 18× the 5 s heartbeat`, not 3×. (If the team prefers a 30s heartbeat, the comment is right but every other story is wrong.)

#### 🟠 1.4.d — Search latency budget mismatch

- [`epics/02-api-streaming.md` Story 7.8 AC-?](epics/02-api-streaming.md) (implied by §9.3): hybrid search performance target "p95 search latency under 250 ms on the 100,000-segment fixture" (Story 7.8 test cases).
- [`epics/01-pipeline.md` Story 5.4 AC-?](epics/01-pipeline.md): "P95 latency target of 200 ms for `limit ≤ 50` on a 15,000 h library".
- [`epics/04-nonfunctional.md` Story 18.1 AC-2](epics/04-nonfunctional.md): "`POST /api/search` (FTS+vector fusion, top 20) — p50 ≤ 250 ms, p95 ≤ **500 ms**, p99 ≤ 800 ms".
- [`epics/04-nonfunctional.md` Story 18.2 AC-1](epics/04-nonfunctional.md): "Hybrid search at 100k segments returns the top-20 fused result set in p95 ≤ **500 ms**".

The strictest budget is 200 ms (5.4); the most generous is 800 ms (18.1). At 100 k segments, 200 ms p95 is aggressive for FTS+embed+RRF. **Resolution:** pick one number per scale tier and align all four stories.

#### 🟡 1.4.e — `transcode.max_concurrent` default 4 vs `(num_cores / 4)`

- [`architecture.md` §11.3](architecture.md) `streaming.toml` example: `max_concurrent = 4`.
- [`architecture.md` §10.4](architecture.md): "per-host max concurrent transcodes defaults to `(num_cores / 4)`".
- [`epics/04-nonfunctional.md` Story 19.7 AC-1](epics/04-nonfunctional.md): repeats `(num_cores / 4)`.

The example file shows a hard `4`; the rest of the doc derives from cores. **Resolution:** replace the example with a comment that the value is auto-derived unless set.

### 1.5 Behavioral conflicts

#### 🔴 1.5.a — Watch-progress monotonicity is contradicted

- [`epics/02-api-streaming.md` Story 7.11 Test cases / EC](epics/02-api-streaming.md): "a stale POST with `position_sec` lower than the current stored position is **still accepted** (user manually rewound) — no monotonicity check."
- [`epics/04-nonfunctional.md` Story 24.4 AC-2](epics/04-nonfunctional.md): "Watch-progress writes are last-writer-wins per (user, video) with **monotonic guarantee** (server rejects updates with `position_sec` lower than current unless a `seek=true` flag is set)."

The two stories require opposite behavior. The 7.11 design is correct for resume-everywhere UX (a user dragging the scrubber back is a normal action); 24.4's "seek=true flag" is invented in 24.4 alone and never appears in the player API. **Resolution:** delete 24.4 AC-2's monotonicity rule, OR add `seek=true` to 7.11 and update both.

#### 🟠 1.5.b — Player progress debounce contradicts upsert semantics

- [`epics/02-api-streaming.md` Story 7.11 AC-1](epics/02-api-streaming.md): "`playback_state` is upserted with the new position".
- [`epics/02-api-streaming.md` Story 7.11 AC-3](epics/02-api-streaming.md): "more than 1 POST per second is received per session, the additional POSTs are accepted with 200 OK but only the last per second is persisted (debounced server-side)".

Either every POST upserts (AC-1) or only one per second persists (AC-3); the latter is correct. AC-1 should say "debounced upsert."

#### 🟡 1.5.c — Logout invalidation behavior differs across stories

- [`epics/02-api-streaming.md` Story 10.5 AC-2](epics/02-api-streaming.md): "the access token is **not** invalidated server-side (it expires within 15 min naturally)".
- [`epics/02-api-streaming.md` Story 23.2](epics/02-api-streaming.md): authorization checks happen on every API call (and need ACL data which the JWT doesn't carry — see 1.2.d).

The trade-off is acceptable but Story 23.2 cannot enforce instantaneous revocation without a JWT denylist. Document the 15-min revocation lag explicitly in 23.2.

---

## 2. Missing Integration Points

### 2.1 gRPC contracts

#### 🔴 2.1.a — `Pipeline.ExtractEmbeddedSubtitle` is referenced but missing

See §1.2.c. The Streaming Service (Epic 8.11 AC-3) extracts embedded subs by spawning ffmpeg directly — but Story 4.4 says the Pipeline owns the extraction. Either:
- Add the RPC to architecture §9.9 and have Streaming call Pipeline (cleanest), OR
- Document that Streaming extracts subs locally and remove Story 4.4's RPC requirement.

#### 🟠 2.1.b — `Streaming.OpenSession` request/response shape unspecified

[`architecture.md` §9.9](architecture.md) declares `OpenSession(OpenSessionRequest) returns (Session)` but neither message is defined; the field list in [02 Story 7.10 AC-1](epics/02-api-streaming.md) and [02 Story 8.8 AC-1](epics/02-api-streaming.md) overlap but don't match perfectly:

- 7.10 calls it with `{video_id, client_profile, audio_track?, subtitle_track?, start_sec?, max_bitrate_kbps?}`.
- 8.8 mentions `format?` as additional, plus `force_software`, `force_transcode`, `burn_subs`, `accept_queue` in 8.7/8.10/8.11.

**Resolution:** publish the actual `.proto` content under [`architecture.md` §9.9](architecture.md) or in `shared/proto/streaming.proto`.

#### 🟡 2.1.c — `Pipeline.Transcribe` ad-hoc RPC vs job-queue path

[`architecture.md` §9.9](architecture.md) declares `rpc Transcribe(TranscribeRequest) returns (stream TranscribeEvent)`. [§1.4](architecture.md) explains: "ad-hoc operations like 'transcribe this clip with backend X right now.'" No epic story owns this RPC's API or use case. Either delete from the proto or add a story in Epic 7 that exposes it (e.g., `POST /api/transcribe/once`).

### 2.2 DB schema referenced but undefined

See §1.1.h. Each missing table needs a migration story owner.

### 2.3 WebSocket events / pub-sub channels

#### 🟠 2.3.a — `LISTEN` channels are partially documented

Channels mentioned across documents:

| Channel | Producer | Consumer | Documented? |
|---------|----------|----------|-------------|
| `videos.new` | scanner ([01 1.1](epics/01-pipeline.md)) | API ([02 7.16](epics/02-api-streaming.md)) | ✓ |
| `videos.state_changed` | reprocess ([02 7.5](epics/02-api-streaming.md)) | API | ✓ |
| `jobs.new` | enqueue ([02 7.3 AC-5](epics/02-api-streaming.md)) | claim loop | ✓ |
| `jobs.flag_set` | API ([02 7.12 AC-1](epics/02-api-streaming.md)) | worker | ✓ |
| `job.pending` | enqueue ([01 6.2 AC](epics/01-pipeline.md)) | claim loop | partial — same as `jobs.new`? |
| `job.progress` | worker ([01 6.3 AC](epics/01-pipeline.md), [arch §7.10](architecture.md)) | API → WS | ✓ |
| `job.heartbeat` | worker ([01 6.3 AC](epics/01-pipeline.md)) | reaper | ✓ |
| `job.reaped` | reaper ([01 6.6 AC](epics/01-pipeline.md)) | API → WS | ✓ |
| `job.force_pause` | API ([01 6.4 AC-2](epics/01-pipeline.md)) | worker | ✓ |
| `segments.committed` | transcribe ([01 5.5 AC](epics/01-pipeline.md)) | live indexer | ✓ |
| `library.settings_changed` | API ([02 9.1 AC-3](epics/02-api-streaming.md)) | orchestrator | ✓ |
| `profiles_changed` | API ([02 8.2 EC](epics/02-api-streaming.md)) | streaming | ✓ |
| `settings_changed` | API ([02 7.15 AC-2](epics/02-api-streaming.md)) | API replicas | ✓ |
| `jwks_changed` | API ([02 10.7 TC-2](epics/02-api-streaming.md)) | streaming | optional optimization |

Issue: `job.pending` and `jobs.new` look like the same channel under two names. **Resolution:** standardize naming (`jobs.*` namespace recommended) in one place.

#### 🟡 2.3.b — `events` table for WS replay vs ring buffer

[02 Story 7.16 AC-4](epics/02-api-streaming.md) uses an in-memory "ring buffer (last 60 s)" for slow-consumer reconnects.
[04 Story 19.2 AC-3](epics/04-nonfunctional.md) requires a persistent `events` table to replay missed events across replicas.

Two different mechanisms; pick one. The `events` table is more durable (and survives restarts), but adds write amplification on the hot path.

### 2.4 Auth tokens

#### 🟠 2.4.a — Streaming JWT validation uses different audience names than minter

See §1.2.e. Reconcile.

#### 🟡 2.4.b — Single-user mode bypasses what exactly?

- [`architecture.md` §9.8](architecture.md): "an env-configured admin token bypasses the user table entirely; the UI stores it after first boot."
- [`epics/02-api-streaming.md` Story 10.9](epics/02-api-streaming.md): admin token bypass with synthetic user.
- [`epics/04-nonfunctional.md` Story 19.8 AC-2](epics/04-nonfunctional.md): "single-user mode treats all requests as authorized as the sentinel user."

If the admin-token path produces a synthetic admin (10.9) but [04 19.8 AC-1](epics/04-nonfunctional.md) defines a **sentinel** UUID `00000000-0000-0000-0000-000000000001` for user-scoped rows, the synthetic admin's `user_id` must equal the sentinel — that linkage isn't documented anywhere. **Resolution:** explicitly state that the admin-token bypass user_id = the sentinel UUID.

### 2.5 Search

#### 🟠 2.5.a — Search uses `transcript_units`, but architecture matches segments

See §1.1.d. Compose-level: the indexing epic (Story 5) writes to `transcript_units`; the API search response shape ([architecture.md §9.3](architecture.md)) returns `matches[].segment_id, start_sec, end_sec` — i.e., consumers expect segment-level hits. Story 5.1 maps unit→segment via `segment_ids JSONB`, so this works, but the architecture should explicitly document "search results are unit-derived but expressed in segment coordinates."

### 2.6 Streaming cache vs probe metadata

#### 🟡 2.6.a — `EvictHashCache` invalidates probe cache too

- [`epics/02-api-streaming.md` Story 8.8 AC-3](epics/02-api-streaming.md): `EvictHashCache` deletes "remux, posters, sprites, thumbs."
- [`epics/02-api-streaming.md` Story 8.15 EC](epics/02-api-streaming.md): the in-memory probe cache is "invalidated by the Pipeline calling Streaming's `EvictHashCache`".

But the documented semantics in 8.8 don't include probe-cache invalidation. **Resolution:** add probe-cache to the AC-3 list.

### 2.7 Chapters

#### 🟠 2.7.a — Chapter inference has no Pipeline story

- [`architecture.md` §4.6](architecture.md): "Inferred chapters from transcript-level topic shifts (cosine drop between adjacent segment embeddings > threshold)".
- [`epics/02-api-streaming.md` Story 8.12](epics/02-api-streaming.md) covers chapter **delivery** (HLS DATERANGE + JSON) but not generation.
- No story in [`epics/01-pipeline.md`](epics/01-pipeline.md) covers chapter inference.

This is a **missing pipeline stage**. Either Story 9.10 (auto-categorization) needs to grow, or a new Epic 1 story (`chapter_infer`) is needed.

### 2.8 GraphQL ↔ REST

#### 🟡 2.8.a — Story 7.17 AC-2 promises parity but admin/system endpoints are not enumerated

[`epics/02-api-streaming.md` Story 7.17 AC-2](epics/02-api-streaming.md): "every REST endpoint listed in §9.1–9.7 has a corresponding `Query` field … or `Mutation` field." Several endpoints are added by other stories (see §3 below) and would need GraphQL counterparts; the parity claim is silently broken by every new REST endpoint.

---

## 3. Missing Stories

### 3.1 Capabilities mentioned in `architecture.md` with no story

- 🔴 **Chapter inference from transcripts** — see §2.7.a.
- 🟡 **`Pipeline.Transcribe` ad-hoc RPC** ([§9.9](architecture.md)) — no story owner.
- 🟡 **DASH manifests for live profiles** — [04 Story 8.6](epics/02-api-streaming.md) covers; [arch §4.3](architecture.md) says "DASH is opt-in per session" but the live↔static profile transition (Story 8.6 EC) is undertested.
- 🟡 **Subtitle "burn-in" mode** — [arch §4.5](architecture.md): "Clients that don't support sidecar subtitles (rare) can request burned-in mode per session, which forces a transcode." Mentioned only as an EC in [02 Story 8.11](epics/02-api-streaming.md); not a real story.
- 🟡 **Chapter delivery via HLS DATERANGE** — covered by [02 Story 8.12](epics/02-api-streaming.md) but the architecture's `chapters.json` resource (§4.6) is not separately tested.

### 3.2 Endpoints referenced by stories but with no story owner

The following REST endpoints are referenced in tests, ECs, or other stories' ACs but are **not the subject of any AC-bearing story**:

| Endpoint | Referenced by | Status |
|----------|---------------|--------|
| `POST /api/users` (admin user create) | [02 Story 10.1 AC-3](epics/02-api-streaming.md) | story exists in 10.1 but parts of CRUD missing |
| `PATCH /api/users/{id}` | 10.1 AC-3 | ditto |
| `DELETE /api/users/{id}` | 10.1 AC-3 | ditto |
| `DELETE /api/users/{id}/sessions/{session_id}` | [02 Story 10.5 AC-4](epics/02-api-streaming.md) | no AC owner |
| `POST /api/users/{id}/unlock` | [02 Story 10.11 EC](epics/02-api-streaming.md) | no AC owner |
| `POST /api/devices/register` | [03 Story 12.4 AC](epics/03-clients-discovery.md) | no AC owner on the API side |
| `GET /api/recommendations` | [03 Story 14.6](epics/03-clients-discovery.md) | no AC owner |
| `GET /api/me/flags` | [03 Story 16.6](epics/03-clients-discovery.md) | no AC owner |
| `POST /api/auth/pair` | [03 Story 15.5](epics/03-clients-discovery.md) | no AC owner |
| `DELETE /api/federation/{partner_id}` | [03 Story 15.3 EC](epics/03-clients-discovery.md) | no AC owner |
| `POST /api/federation/...` (token gen) | [03 Story 15.3 AC](epics/03-clients-discovery.md) | not detailed |
| `PATCH /api/libraries/{id}/topics/{topic_id}` | [02 Story 9.9 AC-2](epics/02-api-streaming.md) | no AC owner |
| `GET /api/libraries/{id}/audit` | [02 Story 9.17 AC-2](epics/02-api-streaming.md) | brief only |
| `GET /api/security/audit` | [02 Story 10.16 AC-3](epics/02-api-streaming.md) | brief only |
| `POST /api/telemetry`, `POST /api/telemetry/web-vitals` | [03 Story 16.5](epics/03-clients-discovery.md), [04 Story 21.2 EC-3](epics/04-nonfunctional.md) | no API story |
| `PATCH /api/videos/{id}/subtitles/{id}` | [01 Story 4.4 EC](epics/01-pipeline.md) | listed as deferred |
| `POST /api/auth/login`, `POST /api/auth/refresh`, `POST /api/auth/logout`, `POST /api/auth/logout-all` | [02 Stories 10.2–10.5](epics/02-api-streaming.md) | covered ✓ |
| `GET /api/.well-known/jwks.json` | [02 Story 10.6 AC-3](epics/02-api-streaming.md) | covered ✓ |

🔴 **High-impact gaps:**
- `GET /api/recommendations` is the entire Epic 14.6 backend, **with no implementation story**.
- `POST /api/devices/register` blocks Epic 12.4 (push notifications) **with no implementation story**.
- `POST /api/auth/pair` blocks Epic 15.5 (QR pairing).

### 3.3 Stories that depend on stories that don't exist

- 🟠 [03 Story 14.6](epics/03-clients-discovery.md) (TV recommendations) depends on `GET /api/recommendations` — no implementation story.
- 🟠 [03 Story 12.4](epics/03-clients-discovery.md) (push) depends on `/api/devices/register` and an APNs/FCM bridge — no API story.
- 🟠 [03 Story 15.5](epics/03-clients-discovery.md) (QR pairing) depends on `POST /api/auth/pair` — no API story.
- 🟡 [02 Story 9.7](epics/02-api-streaming.md) (library stats) requires "cached aggregates updated on insert/delete" for sub-50ms perf — no story defines or maintains the aggregate table.
- 🟡 [04 Story 21.7](epics/04-nonfunctional.md) introduces `/api/processing/*` endpoints conflicting with Epic 7 (see §1.2.a).
- 🟡 [02 Story 10.11 EC](epics/02-api-streaming.md) references admin endpoint `POST /api/users/{id}/unlock` — no AC.

### 3.4 Client features without API counterparts

- 🟡 [03 Story 11.6](epics/03-clients-discovery.md) Settings page → "Personal Access Token (PAT) management for clients" — no API story for PAT issuance.
- 🟡 [03 Story 11.6](epics/03-clients-discovery.md) → "list active sessions (with revoke)" — only partially covered by 10.5 AC-4.
- 🟡 [03 Story 11.10](epics/03-clients-discovery.md) (offline replay queue) → "save search" replay against API — depends on idempotency keys not defined in 7.9.
- 🟡 [03 Story 12.6](epics/03-clients-discovery.md) (offline downloads) → "marking a video downloaded sets a server-side flag" — no `POST /api/videos/{id}/downloaded` endpoint.

---

## 4. Ambiguities & Under-specified Areas

### 4.1 Direct play vs sessions handshake

[`epics/02-api-streaming.md` Story 8.3 AC-4](epics/02-api-streaming.md): direct-play endpoint returns 409 with `manifest_url` if the video isn't direct-playable. But the manifest_url requires a session that's only minted by `POST /api/stream/sessions`. So:

- A native player calls `GET /stream/direct/{video_id}` directly (with the direct JWT minted via [02 Story 10.8 AC-2](epics/02-api-streaming.md))?
- A web player **must** call `POST /api/stream/sessions` first regardless (because direct-play JWT minting is not exposed to the web player)?

Story 7.10 AC-1 doesn't enumerate the "this is direct-playable" return mode; the response only lists `{session_id, manifest_url, expires_at, ladder, current_rendition}`. **Resolution:** pick a single client flow (recommend "always call POST /sessions"; the response carries `mode ∈ {direct, remux, transcode}` and a `direct_url` if mode=direct).

### 4.2 Search snippet rendering against unit→segment mapping

[`epics/01-pipeline.md` Story 5.4 AC-3](epics/01-pipeline.md) returns `matches[].segment_id` per [`architecture.md` §9.3](architecture.md). But hits are scored at the **unit** level (Story 5.1). If a unit spans 2 segments and matches, which segment is reported? `segment_ids[0]`? Document explicitly.

### 4.3 Subtitle URL paths

[`architecture.md` §4.5](architecture.md) and [§9.4](architecture.md) examples are inconsistent:
- §9.4 lists `GET /stream/{session_id}/subs/{lang}.vtt` (a single VTT file).
- §4.4 transcode pipeline writes `subs/{lang}/seg-N.vtt` (segmented).
- [02 Story 8.11 AC-1](epics/02-api-streaming.md) uses `/stream/{session_id}/subs/auto.vtt` (single file).
- [02 Story 8.11 AC-4](epics/02-api-streaming.md) introduces `subs/{lang}.m3u8` (sub-playlist wrapping a single VTT).

**Resolution:** decide on monolithic VTT (8.11) or segmented VTT (§4.4) for HLS subs. WebVTT supports both; monolithic is simpler.

### 4.4 Web client offline strategy gaps

[`epics/03-clients-discovery.md` Story 11.10 AC](epics/03-clients-discovery.md): "actions that require the network (start a session, save a search) are queued with `bgsync` and replayed on reconnect." But:
- "Start a session" is **not idempotent** without a client-supplied idempotency key (Epic 7 doesn't define one).
- Replay of a "start a session" action that succeeded but the response was lost would mint a duplicate session.
**Resolution:** add an Idempotency-Key header convention in Story 7.1.

### 4.5 ChromaDB single-writer guarantees

[`epics/04-nonfunctional.md` Story 24.4 AC-3](epics/04-nonfunctional.md): "ChromaDB upserts use a documented single-writer rule (one Pipeline process at a time)." But [`architecture.md` §10.3](architecture.md) plans "horizontal scale-out" with N pipeline workers. **Resolution:** explicitly state "scale-out is bounded by the single-writer constraint until ChromaDB server is adopted (deferred)."

### 4.6 Test cases that don't test the stated AC

- 🟡 [02 Story 7.20 TC-2](epics/02-api-streaming.md): "kill Postgres → /health returns 503 down" — but AC-1 says HTTP status reflects worst component, with `200` for ok/degraded and `503` for down. Killing Postgres entirely makes the API itself fail — testing 503 against an API that can't accept connections is fragile.
- 🟡 [01 Story 5.4 TC-3](epics/01-pipeline.md): "snippet highlight grapheme-aware" tests — only one fixture; combining marks have many corner cases not enumerated.
- 🟡 [02 Story 7.11 TC-3](epics/02-api-streaming.md): "stale POST with `position_sec` lower than the current stored position is **still accepted**" — directly contradicts [04 Story 24.4 AC-2](epics/04-nonfunctional.md). See §1.5.a.
- 🟡 [04 Story 18.1 TC-3](epics/04-nonfunctional.md): "artificially slow `pgx` to add 200 ms to every query; the harness must fail" — slows every query but doesn't test the per-endpoint budget identification.

### 4.7 Edge cases mentioned but not test-covered

- 🟡 Watch progress through closed session (Epic 7.11 EC) — accepted, but no test asserts that the closed-session row in `streaming_sessions` is reused vs ignored.
- 🟡 Library deletion during in-flight scan ([02 Story 7.3 EC](epics/02-api-streaming.md)) — covered by an integration test that "deletes the library while the scan worker is paused mid-loop." But scans aren't pausable in the same way as transcribes ([01 Story 1.4](epics/01-pipeline.md) only supports `DELETE /scan` cancellation, not pause). Test scenario is unclear.
- 🟡 Two libraries with overlapping roots after a `mount` change ([02 Story 9.16 EC](epics/02-api-streaming.md)): "out of scope; not detected at runtime" — fine, but should at minimum log a periodic warning.
- 🟡 [01 Story 5.5](epics/01-pipeline.md) live indexer: "transcribe paused mid-video → live indexing pauses naturally" — no test verifies that the live indexer doesn't continue chunking partial unit text after pause.

---

## 5. Security Gaps

### 5.1 Streaming-side authorization is too thin

- 🔴 [02 Story 8.1](epics/02-api-streaming.md): Streaming verifies JWT signature + audience + sub + exp. But:
  - The `sub` is just `session_id` — Streaming has no way to verify *which user* opened that session.
  - There's no `library_ids` claim in the JWT (see §1.2.d), so 23.2's library-membership check has no input.
  - If an attacker steals a manifest URL, they can stream until expiry, even if their access has been revoked.
  
  **Resolution:** include `usr=user_id` and `lib=[library_id, ...]` in the manifest JWT; Streaming can then re-check `lib` against a small authorization-policy cache (refreshed via gRPC).

- 🟠 The poster/sprite/subtitle endpoints ([02 Story 8.13](epics/02-api-streaming.md)) show no JWT validation in the AC, even though Story 8.1 says "every Streaming endpoint runs through one middleware that validates a signed JWT." If posters are unsigned, they leak across libraries.

### 5.2 Input validation gaps

- 🟠 [`epics/02-api-streaming.md` Story 9.4 AC-1](epics/02-api-streaming.md) BLAKE3 hashing reads ranges of files on disk. The hash function does not validate that the file path is inside a registered library root before reading. Combined with [04 Story 23.5 AC-2](epics/04-nonfunctional.md) ("paths from clients are forbidden"), this is OK for the *user-facing* input, but if an internal worker is induced to operate on a path supplied via the API (e.g., library root), the canonicalization/sandbox check needs to be its own helper used everywhere.
- 🟠 [01 Story 4.4](epics/01-pipeline.md) `Pipeline.ExtractEmbeddedSubtitle(video_id, stream_index)`: `stream_index` is an integer from the API — but the Pipeline must ensure it picks an existing subtitle stream, not an arbitrary stream. The Story doesn't enumerate the validation.
- 🟡 [02 Story 7.6 EC](epics/02-api-streaming.md) `?from=-5&to=99999` clamps. But missing in the AC: what about `?from=NaN`?

### 5.3 Untrusted content rendering

- 🟡 [04 Story 23.5 AC-5](epics/04-nonfunctional.md) says subtitle files are "sanitized for HTML/script injection before rendering." But [01 Story 4.1](epics/01-pipeline.md) writes VTT cues from transcript text without any escaping. A transcript that contains `<script>` (vanishingly rare from STT but possible from external sidecar SRT) would land in a VTT cue verbatim. **Resolution:** add explicit cue-text escaping to Story 4.1/4.2.

### 5.4 Federation pairing

- 🟡 [03 Story 15.3](epics/03-clients-discovery.md) federation token exchange: the AC doesn't specify how the token is delivered (HTTP? out-of-band?), what cryptographic primitives bind it, or what stops a man-in-the-middle from substituting public keys during pairing.

### 5.5 Cloud relay

- 🟡 [03 Story 15.2](epics/03-clients-discovery.md): "End-to-end encrypted: the server holds the TLS cert, relay sees only ciphertext." This is true if and only if clients pin the server's cert via TOFU; the AC doesn't describe pinning. Document the TOFU flow.

### 5.6 Approval-required actions

- 🟡 No story explicitly requires approval before destructive admin actions (`DELETE /api/libraries/{id}?purge=true`, `DELETE /api/users/{id}`, `keys rotate --immediate`). [02 Story 7.3 AC-4](epics/02-api-streaming.md) and [02 Story 10.6 EC](epics/02-api-streaming.md) describe these but don't require a confirmation token. **Resolution:** for `purge=true`, require a `confirm` query parameter equal to the resource name.

---

## 6. Scalability Concerns

### 6.1 Operations that may not scale to 30 TB / 50k videos

- 🟠 [02 Story 7.13](epics/02-api-streaming.md) `GET /api/queue/stats` requires `done_24h` count. The architecture's [§7.1](architecture.md) `processing_jobs` indexes don't include `(state, finished_at)`, so this filter requires a scan of the failed/done partition. Add an index `(state, finished_at) WHERE state IN ('done','failed')` or compute incrementally.
- 🟠 [02 Story 9.7 AC-2](epics/02-api-streaming.md) library stats <50ms via "cached aggregates updated on insert/delete" — no story owns the aggregate table, no migration, no triggers.
- 🟠 [04 Story 21.7 AC-1](epics/04-nonfunctional.md) `/api/processing/status` requires `oldest pending job age` — needs `MIN(created_at) WHERE state='pending'`. No covering index.
- 🟡 [02 Story 7.4 AC-1](epics/02-api-streaming.md) video list filtering by `tag, speaker, content_type, language` simultaneously: each filter requires a join. No composite index strategy is described.
- 🟡 [01 Story 5.1](epics/01-pipeline.md) chunking is O(transcript size) per pipeline run, and chunks are stored in `transcript_units`. A 4-hour transcript at ~200 chars/unit = ~7,200 units — fine. But [arch §10.1](architecture.md) projects 150,000 segments → ~300,000 units per library; cross-library search needs query plans that don't scan all units.

### 6.2 Unbounded growth

- 🟠 [04 Story 19.2 AC-3](epics/04-nonfunctional.md) `events` table for WS replay grows unboundedly. No retention specified.
- 🟠 `library_audit`, `security_audit`, `audit_log` — each has its own retention rule (see §1.1.f). Without partitioning ([10.16 EC2](epics/02-api-streaming.md) mentions "audit table partitioned by month" but only for `security_audit`), these grow large.
- 🟡 [01 Story 6.5 AC-1](epics/01-pipeline.md) `processing_jobs.error JSONB` accumulates traceback strings; old failed jobs are kept forever. No retention rule.

### 6.3 Query patterns missing indexes

- 🟠 `transcript_units(language)` for filter pushdown ([01 Story 5.4 AC-3](epics/01-pipeline.md)) — not specified.
- 🟠 `videos(detected_language)` — covered ([arch §8.1](architecture.md) `CREATE INDEX ON videos (detected_language)`).
- 🟠 `videos(content_type)` — required by [02 Story 7.4 AC-1](epics/02-api-streaming.md) filter, but not in arch.
- 🟠 `processing_jobs(state, finished_at)` — for stats, see §6.1.
- 🟡 `playback_state(updated_at)` — required for "Continue Watching" ([03 Story 14.5 AC-2](epics/03-clients-discovery.md) "started on phone shows up on TV within 5 s").
- 🟡 `streaming_sessions(last_segment_at)` — for the reaper ([02 Story 8.9](epics/02-api-streaming.md)).

### 6.4 Single-writer bottlenecks

- 🟡 ChromaDB ([24.4 AC-3](epics/04-nonfunctional.md)) — documented as single-writer; OK.
- 🟡 The transcribe stage is single-GPU per worker. Horizontal pipeline scale-out requires separate GPUs per host. Acceptable.
- 🟡 Postgres `LISTEN/NOTIFY` queue overflow ([04 Story 19.2 EC-1](epics/04-nonfunctional.md)) — fallback exists; but the threshold for "burst" isn't specified.

### 6.5 Realistic performance budgets

- 🟡 [04 Story 18.2 AC-1](epics/04-nonfunctional.md) hybrid search at 100 k segments p95 ≤ 500 ms. With FTS5 + `multilingual-e5-large` query embedding (~200-400 ms) + RRF — feasible only with a warm embedding cache and a fast vector store. Cold embedding p95 will exceed budget. **Resolution:** distinguish warm vs cold p95 explicitly in Story 18.2.

---

## 7. Dependency Sequencing

### 7.1 Recommended build order

The four epic documents disagree on sequencing. Below is a single dependency-respecting order across all 24 epics, derived from cross-epic reads:

**Phase 0 — Foundations (must finish before Phase 1):**
1. Epic 22 (DevOps) Story 22.4 (migrations infrastructure), 22.1 (CI gates).
2. Epic 24 Story 24.3 (DB constraints) — schema-level invariants.
3. Epic 6 Stories 6.1–6.3 (job queue, claim loop, heartbeat) — every other pipeline story rides on this.
4. Epic 10 Stories 10.1, 10.6 (users, JWT keys).

**Phase 1 — Core API + Auth:**
5. Epic 7 Story 7.1 (HTTP skeleton) — every other API story depends.
6. Epic 7 Story 7.19 (validation, rate limiting) — middleware all stories assume.
7. Epic 7 Story 7.2 (cursor pagination) — every list endpoint.
8. Epic 10 Stories 10.2, 10.3, 10.4, 10.5, 10.10, 10.15 (auth flows + transport).
9. Epic 7 Story 7.18 (gRPC clients) — needed by 7.10, 7.8.

**Phase 2 — Library + Pipeline core:**
10. Epic 9 Story 9.1 (library config schema), 9.16 (overlap), 9.5 (ignore rules).
11. Epic 7 Story 7.3 (library CRUD).
12. Epic 1 Stories 1.1–1.5 (scanner) + Epic 9 Stories 9.2, 9.3, 9.4, 9.6.
13. Epic 1 Stories 2.1–2.4 (probe + extract).
14. Epic 1 Stories 6.4–6.10 (queue control: pause/resume/cancel/retry/reaper/concurrency).

**Phase 3 — Transcription + Subtitles + Search:**
15. Epic 1 Stories 3.1–3.5 (STT backend protocol + implementations + registry).
16. Epic 1 Stories 3.6, 3.7, 3.8 (durable per-segment commit, pause/resume, crash recovery) — **the critical-path stories**.
17. Epic 1 Stories 4.1–4.5 (subtitles).
18. Epic 1 Stories 5.1–5.6 (search + indexing).
19. Epic 7 Stories 7.6 (transcript window), 7.7 (subtitles read), 7.8 (search), 7.9 (saved searches).
20. Epic 7 Stories 7.4 (videos CRUD), 7.5 (process control), 7.12 (job control), 7.13 (queue stats).

**Phase 4 — Streaming:**
21. Epic 8 Story 8.1 (server skeleton, signed-URL middleware), 8.15 (probe cache), 10.7 (offline JWT verify), 10.8 (signed-URL minter).
22. Epic 8 Story 8.2 (capability matrix).
23. Epic 8 Stories 8.3 (direct play), 8.4 (remux), 8.5 (HLS), 8.7 (hwaccel), 8.10 (concurrency caps).
24. Epic 8 Story 8.6 (DASH).
25. Epic 8 Story 8.8 (gRPC server: OpenSession/Close/Evict), 8.9 (session store/reaper).
26. Epic 7 Story 7.10 (session lifecycle), 7.11 (watch progress).
27. Epic 8 Stories 8.11 (live subtitle), 8.12 (chapters), 8.13 (posters/sprites), 8.14 (cache GC).

**Phase 5 — WebSocket / GraphQL:**
28. Epic 7 Story 7.16 (WebSocket fan-out), 7.17 (GraphQL).

**Phase 6 — Library long-tail:**
29. Epic 9 Stories 9.7 (stats), 9.8/9.9/9.10 (categorization), 9.11 (speakers), 9.12 (tags), 9.13/9.14 (collections), 9.15 (deletion), 9.17 (audit).

**Phase 7 — Observability + Hardening:**
30. Epic 21 Stories 21.1–21.8 (logs, metrics, traces, health, errors, audit, job visibility, privacy).
31. Epic 7 Stories 7.14, 7.15, 7.20 (collections/tags/speakers REST, settings, health).
32. Epic 23 Stories 23.1–23.8 (security hardening on top of 10).

**Phase 8 — Clients (parallel between web and TV):**
33. Epic 17 Stories 17.1, 17.2 (design tokens, components) — block all UI work.
34. Epic 11 Stories 11.1–11.12 (web).
35. Epic 12 Stories 12.1–12.9 (mobile).
36. Epic 13 Stories 13.1–13.8 (desktop).
37. Epic 17 Stories 17.3–17.11 (motion, loading, errors, onboarding, RTL).
38. Epic 14 Stories 14.1–14.6 (TV apps).
39. Epic 15 Stories 15.1–15.5 (discovery + pairing).

**Phase 9 — NFR coverage and integrity:**
40. Epic 18 Stories 18.1–18.8 (perf budgets, cache hit rates).
41. Epic 19 Stories 19.1–19.8 (capacity, scale-out, multi-tenant readiness).
42. Epic 20 Stories 20.1–20.8 (test pyramid).
43. Epic 24 Stories 24.1, 24.2, 24.5, 24.6, 24.7, 24.8, 24.9 (atomic writes, idempotent jobs, backup, DR, integrity, identity, fwd/back compat).

**Phase 10 — Optional:**
44. Epic 16 Stories 16.1–16.6 (subscriptions/feature flags) — only if monetization is in v1.
45. Epic 22 Stories 22.2, 22.3, 22.5–22.8 (reproducible builds, packaging).

### 7.2 Critical-path stories

The following stories block the largest fan-outs and must be prioritized:

| Story | Blocks | Reason |
|-------|--------|--------|
| [01 6.1](epics/01-pipeline.md) (job schema + indexes) | every Pipeline epic, every job-control endpoint | DB schema is foundational |
| [01 3.6](epics/01-pipeline.md) (real-time per-segment commit) | 3.7, 3.8, 5.5, 6.10, 24.2 | THE correctness keystone |
| [02 7.1](epics/02-api-streaming.md) (HTTP skeleton) | every Epic 7 story, every Epic 9 endpoint, every client story | foundational |
| [02 7.18](epics/02-api-streaming.md) (gRPC clients) | 7.8, 7.10, 7.15 | inter-service plumbing |
| [02 8.1](epics/02-api-streaming.md) (signed-URL middleware) | every Streaming endpoint | perimeter |
| [02 10.6](epics/02-api-streaming.md) (RS256 keys + JWKS) | 10.7, 10.8, every authenticated endpoint, every signed URL | crypto foundation |
| [04 22.4](epics/04-nonfunctional.md) (migrations infra) | every schema change | tooling |
| [03 17.1](epics/03-clients-discovery.md) (design tokens) | every UI epic | tokens |

### 7.3 Parallelizable work

Once Phase 1 is in place, the following can run concurrently:

- Pipeline (Epics 1) and API/Streaming (Epics 7, 8) are largely independent — the gRPC contract is the shared interface.
- Web (Epic 11), Mobile (Epic 12), Desktop (Epic 13) all wrap the same web bundle and can be built sequentially or in parallel.
- TV (Epic 14) shares no code with web; can be built in parallel by a separate person.
- NFR epics (18–24) accrete tests and infrastructure on top of feature work and can run in parallel with feature epics.
- Discovery (15), Subscriptions (16), Design system (17) are largely orthogonal to backend work.

---

## 8. Summary of Recommendations

### Must fix before implementation (🔴):

1. Resolve `videos.content_hash` uniqueness scope (§1.1.a).
2. Fix `transcripts` UNIQUE constraint to allow `is_active`-tagged history (§1.1.b).
3. Add `subtitle_files.is_embedded` to architecture schema (§1.1.c).
4. Unify `transcripts_fts` source (segments vs units) (§1.1.d).
5. Eliminate duplicate job/queue REST surfaces (§1.2.a).
6. Define `Streaming.GetCapabilities` or document HealthCheck-as-Capabilities (§1.2.b).
7. Reconcile `auth_rate_per_min` 30 vs 10 (§1.4.a).
8. Reconcile watch-progress monotonicity (§1.5.a).
9. Add chapter inference story to Epic 1 (§2.7.a).
10. Add `library_ids` to streaming JWT claims, or scope down 23.2 AC-3 (§5.1).
11. Add API stories for `/api/recommendations`, `/api/devices/register`, `/api/auth/pair` (§3.2).

### Major resolutions (🟠):

12. Migration stories for ~14 tables referenced but undefined (§1.1.h).
13. Reconcile failed-login window (§1.4.b).
14. Fix Story 6.6 heartbeat-interval comment (§1.4.c).
15. Single search-latency budget across all four epics (§1.4.d).
16. Define `subtitle_gen` stage in the FSM (§1.3.b).
17. Add new video states (`MISSING`, `READY_NO_AUDIO`, `SUPERSEDED`, `CORRUPTED`) to FSM (§1.3.a).
18. Pick one audit-table design (§1.1.f).
19. Standardize NOTIFY channel naming (§2.3.a).
20. Pick WS replay mechanism (in-memory ring vs `events` table) (§2.3.b).
21. Document streaming poster/sprite/subtitle JWT validation (§5.1).
22. Add covering indexes for stats queries (§6.1).
23. Settle direct-play vs session handshake (§4.1).

### Minor cleanups (🟡):

24. Stage-name standardization (`thumb` vs `thumbnail`) (§1.3.c).
25. Streaming session schema in arch §8 (§1.1.g).
26. Add idempotency-key convention to Story 7.1 (§4.4).
27. Distinguish warm vs cold search latency budgets (§6.5).
28. Add retention/partitioning for `events` and audit tables (§6.2).
29. Sub-URL paths (monolithic vs segmented VTT) (§4.3).

---

## Appendix — Issues by Epic

| Epic | Blocker | Major | Minor | Total |
|------|---------|-------|-------|-------|
| arch | 4 | 5 | 7 | 16 |
| 01 (pipeline) | 3 | 4 | 4 | 11 |
| 02 (API/streaming) | 6 | 8 | 9 | 23 |
| 03 (clients/discovery) | 3 | 2 | 5 | 10 |
| 04 (NFR) | 2 | 5 | 6 | 13 |

Cross-document issues (counted once above) where the conflict spans two or more documents are reflected in the architecture row plus the originating epic.
