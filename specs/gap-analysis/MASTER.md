# Master Gap Analysis — All 25 Epics (behavioral)

> Synthesis of the 25 per-epic reports in this directory. Unlike
> `specs/FULL_IMPLEMENTATION_AUDIT.md` (structural: "does the package
> exist and is it mounted"), this pass scored every acceptance
> criterion **behaviorally**: does the wired code actually run on a
> real boot/request path and satisfy the AC. One read-only agent per
> epic, evidence cited to `file:line` in each `epic-NN-*.md`.

## Headline

| Bar | Complete-and-wired |
|---|---|
| Prior structural audit (272 story-ACs) | ~172 (~63%) |
| **This behavioral pass (~1,410 leaf ACs)** | **~243 (~17%)** |

The 46-point drop is not contradiction — it's the difference between
"code exists" and "code runs." Four systemic wiring failures cascade
through almost every epic.

---

## The 4 systemic root causes

### R1 — The pipeline executes nothing (kills Epics 01–06, 09)

`pipeline/src/maktaba_pipeline/pipeline/runtime.py:218-235` binds
**every** stage (`scan`, `probe`, `extract`, `transcribe`,
`subtitle_gen`, `index`, topic/chapter) to `_placeholder_handler`,
which logs and marks the job `done`. No production caller passes
`dispatch_overrides`; `__main__.py` even drops `scan` from default
stages. The scanner, FFmpeg probe/extract, all 3 STT backends,
subtitle generators, dedup/language/topic/chapter classifiers are
well-unit-tested **pure library code with zero runtime callers**. A
deployed Maktaba walks no directories, extracts no audio, produces no
transcript or subtitle row — jobs transition `discovered → done`
having done nothing.

> **Corrects the prior audit (§A.2):** the daemon is *not* a
> sleep-loop stub. `__main__.py → runtime.run()` genuinely starts
> `ClaimLoop` + `Reaper`. The defect is the no-op dispatch table, not
> a missing entry point. (epic-06 agent)

### R2 — No gRPC transport exists (kills Epics 05, 07, 08)

There is no `shared/proto/`. `api/internal/grpcclients/{pipeline,
streaming}/realclient.go` return `ErrNotImplemented` for every RPC
after a bare TCP dial. `streaming/main.go` instantiates no gRPC
server; the pipeline exposes none. Consequence: semantic/hybrid
search silently collapses to FTS-only, `OpenSession` always 503s
(streaming session bookkeeping is permanently stale), STT-backend
listing returns empty. The retry/circuit-breaker code is dead.

### R3 — API authn/authz is bypassed (kills Epics 10, 23; endangers all)

`api/.../auth_bootstrap.go:99 applySecurity` installs only
`JWTBearer` + `AdminToken`, which **attach** a principal if present
but never **require** one. No `RequireAuth` gate is mounted;
`Handler.CookieAuth` is orphaned (its return value discarded in
`main.go`); `GET /api/auth/me` is registered nowhere. Net: every
Epic-07 business route is reachable **unauthenticated**, and web
cookie clients can never restore a session. Compounding it, every
minted native JWT carries `Lib: []string{}` (`auth.go:201,322`), so
streaming's library-coverage authz can never pass for a real
cross-tenant user.

### R4 — Client↔API contract drift (kills Epic 11 flagship flows)

`web/src/pages/Search.tsx:42` calls `GET /api/search`; the server
mounts only `POST /api/search` → every search 404/405s.
`VideoPlayer.tsx:29` calls unmounted `GET /api/videos/{id}/stream`
and never performs the `POST /api/stream/sessions` handshake →
playback cannot start. Mobile `GET /api/devices` returns plaintext
`push_token` (live security defect, violates Story 12.10).

---

## Standalone high-severity defects (independent of R1–R4)

| Epic | Defect | Evidence |
|---|---|---|
| 24 Data Integrity | `verify.py:42` hashes sha256 of first 16 MiB, but identity is BLAKE3(head4‖tail4‖size) — corruption detection **can never match** `videos.content_hash` and silently never fires | epic-24 |
| 21 Observability | `audit_log` is a flat, freely-mutable table — no append-only trigger, no partitioning — an "append-only security log" that can be silently `UPDATE`/`DELETE`d | epic-21 |
| 16 Subscriptions | Zero premium-gate call sites anywhere (`.Allows(` grep empty); license store in-memory only (lost on restart); tier model `free/premium` contradicts spec `free/home/pro` | epic-16 |
| 25 Cloud Relay | The on-prem **cloudlink client does not exist** — no `cmd/maktaba-cloudlink`, no `internal/cloudlink`. No self-hosted server can claim, tunnel, or relay to the cloud. Cloud side accepts tunnels nobody dials | epic-25 |
| 20 Testing | `make test-e2e` / `perf-ci` are no-op stubs that exit 0 — the CI **e2e and perf merge gates report green while asserting nothing**; web has 0 tests | epic-20 |
| 22 DevOps | No `release.yml`, no artifact signing — a self-hoster cannot obtain a versioned, signed release | epic-22 |
| 19 Scalability | No cross-replica event bus — `ws.go` is single-process in-memory; no `events` table / `LISTEN/NOTIFY`; a 2nd API replica cannot fan out | epic-19 |
| 11 Web UI | All 8 present pages are Phase-10 placeholder-level; 11.7/11.9/11.13 absent; 11.14 unwired | epic-11 |
| 17 Design System | 2 of 26 primitives exist (`Button`, `ThemeProvider`); README says 17.1/17.2 block Epics 11–14 | epic-17 |
| 13 Desktop | Native menu built but **no `on_menu_event` handler** — every desktop menu item & shortcut is a visual no-op; zero `@tauri-apps` use in web/ | epic-13 |
| 14 TV Apps | Both apps non-functional scaffolds (~515 LOC, canned data); GraphQL backbone returns HTTP 501 unconditionally | epic-14 |
| 15 Discovery | `POST /api/pairing/exchange` mints no token (dead-ends); pairing store in-memory; mDNS/federation/DLNA absent | epic-15 |
| 12 Mobile | No `ios/`/`android/`/`plugins/`; `native-shell.ts` never invoked (its importer doesn't exist); ~62/75 ACs missing | epic-12 |

---

## Corrections to `FULL_IMPLEMENTATION_AUDIT.md`

1. **§A.2 wrong**: pipeline is not a sleep-loop stub; it runs the
   claim loop. Real defect = no-op dispatch table (R1).
2. **§A.1 / D.4 misdiagnosed**: migrations 0036 vs 0054 `audit_log`
   are *not* an incompatible duplicate. 0054 is a well-formed additive
   `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` applied after 0036, not
   skipped. (epic-24)
3. **`P0_CERTIFICATION.md` is stale**: its Story 22.8 FAIL and "T201
   in `__main__.py`" claims are contradicted by current code.
   (epic-22)

---

## Recommended fix sequence (unblocks the most ACs per unit work)

1. **R1 — real pipeline dispatch table.** Wire
   `stage → handler` in `runtime.py`/`__main__.py` (handlers all
   exist). Single highest-leverage change: revives Epics 01–06, 09.
2. **R3 — install a `RequireAuth` gate** + mount `GET /api/auth/me` +
   stop discarding `CookieAuth` + populate JWT `lib[]`. Closes the
   auth bypass and the web session-restore gap in one stroke.
3. **R2 — define `shared/proto/` + stand up the streaming & pipeline
   gRPC servers**, dial them from `api/main.go`. Revives semantic
   search + streaming sessions + STT listing.
4. **R4 — reconcile the web↔API contract** (search verb, stream
   handshake) and fix the `GET /api/devices` token leak.
5. Standalone correctness: `verify.py` hash algorithm; `audit_log`
   append-only trigger; real `make test-e2e`/`perf-ci`.
6. Reach features: cloudlink client (25), event bus (19), component
   library (17), release pipeline (22).

After 1–4 the system is end-to-end functional for the core
self-hosted use case. 5–6 are correctness/scale hardening and product
surface.

---

## Per-epic AC tallies (behavioral)

| Epic | ~ACs | Complete | Partial | Missing | Unwired/Stub |
|---|---|---|---|---|---|
| 01 Scanner | 21 | 8 | 4 | 4 | 5 |
| 02 Audio Extraction | 28 | 5 | 13 | 9 | — |
| 03 Transcription | 56 | 9 | 31 | 14 | 2 |
| 04 Subtitles | 38 | 4 | 12 | 20 | 2 |
| 05 Search & Indexing | 40 | 3 | 11 | 18 | 7 |
| 06 Job Queue | 46 | 26 | 8 | 3 | 9 |
| 07 API Server | 91 | 31 | 28 | 14 | 18 |
| 08 Streaming | 50 | 7 | 22 | 14 | 7 |
| 09 Library Mgmt | 62 | 11 | 22 | 14 | 15 |
| 10 Auth & Security | 85 | 22 | 20 | 28 | 8 |
| 11 Web UI | 95 | 5 | 14 | 46 | 30 |
| 12 Mobile | 75 | 2 | 3 | 62 | 8 |
| 13 Desktop | 38 | 0 | 9 | 24 | 7 |
| 14 TV Apps | 74 | 3 | 14 | 50 | 7 |
| 15 Discovery | 44 | 0 | 5 | 36 | 3 |
| 16 Subscriptions | 68 | 1 | 6 | 56 | 5 |
| 17 UX Design System | 70 | 6 | 7 | 54 | 3 |
| 18 Performance | 32 | 2 | 8 | 18 | 4 |
| 19 Scalability | 33 | 8 | 13 | 11 | 1 |
| 20 Testing | 36 | 8 | 5 | 21 | 2 |
| 21 Observability | 35 | 9 | 10 | 13 | 3 |
| 22 DevOps | 33 | 11 | 12 | 9 | 1 |
| 23 Security | 38 | 3 | 19 | 16 | — |
| 24 Data Integrity | 38 | 1 | 9 | 22 | 6 |
| 25 Cloud Relay | 190 | 58 | 46 | 52 | 34 |

Counts are approximate (agents used slightly different leaf-AC
granularity). See each `epic-NN-*.md` for the authoritative per-AC
table with `file:line` evidence.
