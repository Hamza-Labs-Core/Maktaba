# Epic Status

> Story-by-story status of every epic, read from the code and history on `main` at `f43b0d3ed` (2026-11-19).
> The specs and epic pages don't record a status; this page does. Update the row when a story changes.
> **Change a story's row (and its "What is left" entry), then run `python3 docs/wiki/db/epic_status.py`; never edit the counts by hand.** CI fails on stale counts (`--check`).

**Not every epic is done.** Of 774 stories across all 60 epics, 710 are done, 39 are partial, 16 are not started, and 9 were deferred or removed on purpose. The largest open pieces are Epic 49 (Search, 8 stories of fifteen done), Epic 34 (the TV public site, a first slice only) and Epic 54 Phase 5 (Plugins).

## How this was checked

- **Epics 26–60:** every story was checked against merged PRs and the files they add, opening the main implementation and test files rather than trusting PR titles. Epics 36–49 have no story files; their stories are the numbered units in each epic's README.
- **Epics 01–25:** sampled. Each epic's key implementation and test files were checked, building on the end-to-end audit in [FULL_IMPLEMENTATION_AUDIT.md](../../specs/FULL_IMPLEMENTATION_AUDIT.md). A "done (sampled)" row is not a task-by-task proof.
- Tests were found and read, not rerun. A merged PR shows the code exists, not that it runs in production: live uptime, cloud deploys, store approval and real-device checklists are not covered.

| Status | Meaning |
|---|---|
| **done** | The story's code and tests are on `main`. |
| **partial** | Some of it landed; the row names what is missing. |
| **not started** | Nothing of the story is on `main`. |
| **deferred** | The spec removes the story or names a start condition that hasn't been met. |
| **removed by #969** | Built, then deliberately removed by #969 (Plex sync and dizqueTV). The story spec still describes it. |

## Summary

| Epic | Done | Partial | Not started | Deferred / removed | Verdict |
|---|---:|---:|---:|---:|---|
| 01 Scanner | 6* | 0 | 0 | 0 | Done |
| 02 Audio extraction | 4* | 0 | 0 | 0 | Done |
| 03 Transcription | 9* | 0 | 0 | 0 | Done |
| 04 Subtitles | 5* | 0 | 0 | 0 | Done |
| 05 Search indexing | 7* | 0 | 0 | 0 | Done |
| 06 Job queue | 10* | 0 | 0 | 0 | Done |
| 07 API server | 22* | 0 | 0 | 0 | Done |
| 08 Streaming | 15* | 0 | 0 | 0 | Done |
| 09 Library management | 18* | 0 | 0 | 0 | Done |
| 10 Auth & security | 18* | 0 | 0 | 0 | Done |
| 11 Web UI | 14* | 0 | 0 | 0 | Done |
| 12 Mobile | 11* | 0 | 0 | 0 | Done |
| 13 Desktop | 8* | 0 | 0 | 0 | Done |
| 14 TV apps | 7* | 0 | 0 | 0 | Done |
| 15 Discovery | 7* | 0 | 0 | 0 | Done |
| 16 Subscriptions | 8* | 0 | 0 | 0 | Done |
| 17 UX design system | 11* | 0 | 0 | 0 | Done |
| 18 Performance | 8* | 0 | 0 | 0 | Done |
| 19 Scalability | 8* | 0 | 0 | 0 | Done |
| 20 Testing | 8* | 0 | 0 | 0 | Done |
| 21 Observability | 8* | 0 | 0 | 0 | Done |
| 22 DevOps | 9* | 0 | 0 | 0 | Done |
| 23 Security | 7* | 1 | 0 | 0 | **Partial** |
| 24 Data integrity | 9* | 0 | 0 | 0 | Done |
| 25 Cloud relay | 36* | 0 | 0 | 0 | Done |
| 26 Content intelligence | 6 | 4 | 0 | 0 | **Partial** |
| 27 Live channels | 10 | 1 | 0 | 0 | **Partial** |
| 28 Auto-update | 6 | 0 | 0 | 0 | Done |
| 29 Analytics | 6 | 0 | 0 | 0 | Done |
| 30 Relay analytics | 4 | 0 | 0 | 0 | Done |
| 31 Multi-instance | 1 | 0 | 0 | 1 | **Partial** |
| 32 TV streaming cluster / remote | 10 | 3 | 0 | 0 | **Partial** |
| 33 TV staging / hot swap | 12 | 4 | 0 | 0 | **Partial** |
| 34 TV public site | 0 | 7 | 4 | 0 | **Partial** |
| 35 Cluster full nodes and updates | 16 | 0 | 0 | 0 | Done |
| 36 Mac app | 11 | 2 | 0 | 0 | **Partial** |
| 37 Node replica | 5 | 0 | 0 | 0 | Done |
| 38 Windows app | 14 | 0 | 0 | 0 | Done |
| 39 Linux app | 12 | 0 | 0 | 0 | Done |
| 40 Queue health | 6 | 0 | 0 | 0 | Done |
| 41 Help guide | 6 | 0 | 0 | 0 | Done |
| 42 Apps / player / remote / server | 10 | 0 | 0 | 0 | Done |
| 43 pgvector search | 5 | 0 | 1 | 0 | **Partial** |
| 44 Viewing analytics | 7 | 0 | 0 | 0 | Done |
| 45 Libraries UI | 21 | 1 | 0 | 2 | **Partial** |
| 46 Big screen | 7 | 0 | 0 | 0 | Done |
| 47 Offline convert / dedup | 11 | 3 | 0 | 0 | **Partial** |
| 48 Native players | 36 | 1 | 0 | 0 | **Partial** |
| 49 Search | 8 | 1 | 6 | 0 | **Partial** |
| 50 Cloud with no downtime | 35 | 0 | 0 | 0 | Done (live SLOs not checked) |
| 51 Configurable components | 19 | 0 | 0 | 0 | Done |
| 52 Transcript word review | 13 | 3 | 0 | 1 | **Partial** |
| 53 Logs, errors and reports | 18 | 0 | 0 | 0 | Done (open issue #1064) |
| 54 Plugins | 42 | 4 | 5 | 1 | **Not done** |
| 55 Desktop player parity | 10 | 0 | 0 | 0 | Done |
| 56 TV library view | 13 | 0 | 0 | 1 | Done (56.07 removed by #969) |
| 57 Parental controls | 13 | 3 | 0 | 1 | **Partial** |
| 58 Smart TV platforms | 17 | 0 | 0 | 1 | Done (58.18 Vega OS deferred) |
| 59 NAS | 16 | 0 | 0 | 0 | Done (device results not checked) |
| 60 Website | 11 | 1 | 0 | 1 | **Partial** |
| **Total** | **710** | **39** | **16** | **9** | **Not all done** |

`*` sampled, see [How this was checked](#how-this-was-checked).

## What is left

### Not started

- **34.7 Access modes** — no public/Turnstile/Access/accounts implementation; the Worker explicitly lists access modes as not yet built.
- **34.8 Branding and domain settings** — no branding model, custom-domain attachment, or ordered origin/widget/domain migration.
- **34.9 Budget, abuse controls and analytics** — no public budget, viewer/session limits, rate-limit wiring, Analytics Engine sink, or Stream reconciliation.
- **34.10 Deployment pipeline** — manual `wrangler.toml` exists, but no release artifact pipeline or home-driven deploy/promote/rollback implementation.
- **43.6 Measured on the home Mac** — PR #319 explicitly excludes the 13.9M-row p50/p95, disk and recall measurement; no later merged measurement was found.
- **49.10 Web search page** — the existing page predates this epic; the new integrated suggestion/did-you-mean/facet/sort acceptance is not implemented.
- **49.11 Apple / Android / TV / big screen** — no clients for the Epic 49 API or its UI tests.
- **49.12 AI-mode API** — no grounded retrieval, citation validation, streaming/follow-up endpoint, or role tests.
- **49.13 Remove the stopgap** — the Epic 49 document path does not exist, so its removal is not started either.
- **49.14 AI-mode client tab** — no streaming AI tab or citation-to-player UI on any client.
- **49.15 Arabic titles rank by similarity** — title ranking uses `similarity()` with migration 0098's trigram index, which reads no Arabic letter on the bundled server's `--locale=C` database.
- **54.47–54.51, Epic 54 Phase 5:** plugin updates, preinstalled plugins and migration, offline homes, release variants and variant updates. Each spec says "Not built". (54.45 and 54.46 are partial, below.)

### Partial

| Story | Missing |
|---|---|
| 23.07 Supply-chain security | `maktaba-plugind` (Rust) ships with neither an SBOM nor a `cargo audit` in the CVE gate; every Go, Python and web component has both (#1442). |
| 26.2 Transcript topic & entity extraction | Only slot 0074's schema is present. No `topic_extractor` implementation, NER/gazetteer path, classification/entity writers, or its tests are on `origin/main`; `classify_stage.py` still describes that extractor as future work. |
| 26.4 Auto-collection builder | Slot 0076 exists, but no `auto_collections` producer, suggestion lifecycle implementation, or builder tests exist. Existing smart-collection code belongs to earlier/later stories. |
| 26.5 Web metadata enrichment | The current service makes internal and web/LLM proposals. The specified TMDb, OMDb, MusicBrainz and Wikipedia/Wikidata adapters and their tests are absent. |
| 26.7 Background enrichment pipeline | The stage, queue and scheduler exist, but `classify` persists only parsed titles; 26.2's extractor and 26.4's real grouping pass are not wired in. |
| 27.11 Channel strip and display titles | The strip and display-title code remain, but the story's dizqueTV display-title export was deliberately removed with dizqueTV in #969. |
| 32.6 Cloudflare integration | AC7's real-account spike is unrun; `docs/tv-cluster-and-remote.md` §9 still says “not yet run” for every measurement. |
| 32.7 Live input push | Rotation reuses the same input and therefore has a short gap rather than make-before-break; real SRT ingest is also unverified. |
| 32.12 Docs and runbook | The runbook exists, but its required 32.6 spike findings are still unfilled. |
| 33.8 Continuous push / simulcast | The manual TC7 parameter-change spike, `param_change_push=defer` handling, and multi-host staging placements remain unbuilt. |
| 33.9 Rollback | Warm/cold/automatic rollback exists; the post-swap health-check trigger remains absent. |
| 33.15 Observability | Swap metrics exist, but the complete stored per-swap summary and alert/event surface do not verify against AC2–AC5. |
| 33.16 Docs and runbook | General TV-change help exists, but the required `docs/tv/staging.md`, full failure playbook, and setting-reference link test do not. |
| 34.1 Public channel flag and rights | The flag, rights confirmation and admin panel exist; exclusions, cleared lists, new-series review and panic off-air are missing. |
| 34.2 Guide / lineup publisher | Only signed lineup/live-state posts exist; R2 guide blobs, delta publishing and LiveHub fan-out are missing. |
| 34.3 Public feed | Always-on push reuses Epic 32's pool; dedicated A/B inputs, exclusion slates, lower renditions and tiering are missing. |
| 34.4 Worker API / tokens / SiteControl | Basic HMAC state and signed play tokens exist; WebSocket control, LiveHub, heartbeat sessions and limits are missing. |
| 34.5 Site UI | A lineup and player exist; EPG, full deep links, embed, sharing, SEO and the required accessibility coverage are missing. |
| 34.6 Channel switching | The shared `@maktaba/tvplayer` module exists, but the public site does not integrate its resolver, neighbour prefetch, warm-state path or telemetry. |
| 34.11 Docs and runbook | `docs/tv-cluster-and-remote.md` §4.1 documents only the manual first slice; the full setup/operations/failure/cost/legal runbook is absent. |
| 36.7 Build the Mac payload | Payload code and checks exist, but PR #152 records that a real Developer ID was unavailable; the signed/notarized release path remains unverified. |
| 36.9 Move the home Mac in | The command and integration tests exist, but PR #158 says the actual cutover was not run and waited for the owner. |
| 45.7 Help / verification / live merge | Help and branch-stack tests exist; the owner's merge of the three live duplicate libraries is not verified by PR #345. |
| 47.9 `youtube_fetch` | Fake-run coverage exists, but PR #420's real smoke test hit YouTube's bot check; open #900 also reports the missing yt-dlp JavaScript runtime. |
| 47.13 Web Queues page | PR #428 explicitly leaves the duplicate comparison frame strip for follow-up. |
| 47.14 Help and rollout | Help is now present, but PR #619 says no live settings or database were touched; the owner's staged dry-run/real rollout is unverified. |
| 48.24 PiP / AirPlay / Now Playing | Code and simulator tests exist; PR #512 leaves real-receiver video/sound, hand-over, progress and token-refresh checks unticked. |
| 49.9 Transcript-derived relevance check | The generator, its fixed-seed regression test and `maktaba-api search-relevance` are built; the run on the home's own transcripts, and the tuning it calls for, wait for the home to serve them. |
| 52.02 Evaluation set | The human-corrected gold set and calibration on real data; CI has only synthetic data. |
| 52.11 Web review queue | The Playwright test of the branch-stack review flow. |
| 52.13 Native review | The Apple UI tests and Android Compose/Robolectric screen tests. |
| 54.31 Wave 4 classify/enrich | `enrich` still runs in-process; the six classify stages moved. |
| 54.36 Wave 9 core and removal | Removing the in-process path and its switch, which waits on the preconditions the spec names. |
| 54.45 Maktaba store packages | A store or file package installed and run with parity, which waits for an install path (54.46/54.49) and Maktaba's key (#1450). |
| 54.46 Maktaba plugin lifecycle | Installing a Maktaba plugin from a store package or a file (§2), which comes with 54.49's offline files and Maktaba's key (#1452). |
| 57.03 Server filter | A restricted account's Home is over the 60 ms p95 target. |
| 57.04 Device signal | Apple's age-range entitlement, so no age range is sent yet; the real-device check. |
| 57.07 Family settings | The notification opt-in control and the iPad split view. |
| 60.09 Product imagery | The iPad Remote capture; Arabic Mac and Android TV player images wait on #1198 and #1199. |

### Deferred or removed

| Story | Why |
|---|---|
| 31.R Home relay for Plex extension calls | Built in #78, including two-process tests. #969 later removed Plex sync and dizqueTV; the remaining generic/Plex-Live-TV relay is not the original node-run Plex-stage deliverable. |
| 45.22 Per-user star ratings | Explicitly shelved on 2026-10-28; Epic 45 is declared done without it. |
| 45.23 PIN profiles / extra parental limits | Explicitly shelved on the same decision. |
| 52.07 LLM judge | Removed on 2026-11-03; no story replaces it. |
| 54.35 Wave 8 Plex/dizqueTV | Removed by #969; only Plex Live TV remains. The spec still describes the full wave. |
| 56.07 Plex collections | Removed by #969; only rendering of existing Plex collections remains. The spec still describes the import. |
| 57.16 Home rules through Screen Time | Waits for Apple's Family Controls entitlement. |
| 58.18 Vega OS | Waits for Vega devices to be a real share of TVs. |
| 60.13 Pricing | No page until plans and prices are decided. |

## Open issues

No PRs are open. The open issues and the stories they touch:

| Issue | Story(s) touched | Effect on this audit |
|---|---|---|
| #1227 `TestP57LiveTimePG` midnight-dependent failure | 57.10 | Open test reliability bug; implementation/tests exist |
| #1199 Arabic digits/subtitles in app captures | 60.09; relevant to 58.15 | Contributes to 60.09 partial; automated string/layout work in 58.15 exists |
| #1198 Mac Arabic RTL/truncation and missing iPad Remote shot | 60.09 | Contributes to 60.09 partial |
| #1172 Wrong problem statuses / possible plugin 500 | 60.06; 54.07 | Open behavior defect; code/registry/tests exist |
| #1064 Log-bench cost growth | 53.13 | Open performance regression; benchmark deliverable exists |
| #900 yt-dlp needs a JS runtime | 54.33 | Open functional risk/degraded YouTube extraction; wave implementation/tests exist |
| #876 Apple panel/layout snapshots | 54.20 | **Stale:** #893/#936 added comparisons/references and CI |
| #804 Epic 54 wave update rehearsals | 54.28–54.36 | **Stale:** all 16.x scenarios and workflow rows exist |
| #772 One unreproduced YouTube backfill test failure | 54.33 | Open flaky-test investigation; not evidence of a missing story |



## Epics 01–25 (sampled foundations)

Each epic below has an evidence key naming current implementation and test files that were checked. Every story row points to that sampled evidence; this is intentionally not a claim that every task was independently re-proven.

### Epic 01 — Scanner

**E01:** `pipeline/src/maktaba_pipeline/scanner/walker.py`, `scanner/service.py`, `identity/hasher.py`, `watcher/service.py`, `domain/states.py`; tests `pipeline/tests/scanner/test_walker.py`, `test_scanner.py`, `test_scan_control.py`, `pipeline/tests/identity/test_hasher.py`, `pipeline/tests/watcher/test_watcher_integration.py`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 01.01 File discovery | Done (sampled) | E01 |
| 01.02 Content identity | Done (sampled) | E01 |
| 01.03 Filesystem watcher | Done (sampled) | E01 |
| 01.04 Manual control | Done (sampled) | E01 |
| 01.05 Schema decisions | Done (sampled) | E01; migration `shared/db/migrations/0004_video_states_and_stages.sql` |
| 01.06 Video state machine | Done (sampled) | E01 |

### Epic 02 — Audio extraction

**E02:** `pipeline/src/maktaba_pipeline/audio/probe.py`, `track_selection.py`, `extract.py`, `accounting.py`; tests `pipeline/tests/audio/test_probe.py`, `test_track_selection.py`, `test_extract.py`, `test_accounting.py`, `test_commit_extract.py`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 02.01 Audio probe | Done (sampled) | E02 |
| 02.02 Track selection | Done (sampled) | E02 |
| 02.03 Stream extraction | Done (sampled) | E02 |
| 02.04 Resource accounting | Done (sampled) | E02 |

### Epic 03 — Transcription

**E03:** `pipeline/src/maktaba_pipeline/stt/protocol.py`, `mlx.py`, `faster_whisper.py`, `openai_api.py`, `registry.py`, `segment_commit.py`, `pause_resume.py`, `crash_recovery.py`, `diarization.py`; tests under `pipeline/tests/stt/`, including `test_diarization.py`, and `pipeline/tests/pipeline/test_handlers_transcribe.py`. Speaker matching is explicitly a later story, not missing 03.09 work.

| Story | Status | Evidence / missing work |
|---|---|---|
| 03.01 Backend protocol | Done (sampled) | E03 |
| 03.02 Whisper MLX backend | Done (sampled) | E03 |
| 03.03 Faster-Whisper backend | Done (sampled) | E03 |
| 03.04 OpenAI API backend | Done (sampled) | E03 |
| 03.05 Backend registry | Done (sampled) | E03 |
| 03.06 Segment commit | Done (sampled) | E03 |
| 03.07 Pause/resume | Done (sampled) | E03 |
| 03.08 Crash recovery | Done (sampled) | E03 |
| 03.09 Diarization | Done (sampled) | E03 |

### Epic 04 — Subtitles

**E04:** `pipeline/src/maktaba_pipeline/subtitle/generator.py`, `formats.py`, `manager.py`, `extractor.py`; `streaming/internal/handlers/subtitle.go`, `stored_subtitles.go`; tests `pipeline/tests/subtitle/test_generator.py`, `test_formats.py`, `test_manager.py`, `test_extractor.py`, `streaming/internal/handlers/stored_subtitles_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 04.01 Generate from segments | Done (sampled) | E04 |
| 04.02 Formatting/wrapping | Done (sampled) | E04 |
| 04.03 External discovery | Done (sampled) | E04 |
| 04.04 Embedded extraction | Done (sampled) | E04 |
| 04.05 Live VTT contract | Done (sampled) | E04 |

### Epic 05 — Search indexing

**E05:** `pipeline/src/maktaba_pipeline/search/units.py`, `fts.py`, `semantic.py`, `hybrid.py`, `index_stage.py`, `suggest.py`; tests `pipeline/tests/search/test_fts.py`, `test_semantic.py`, `test_hybrid.py`, `test_index_stage.py`; chapter inference also has `pipeline/tests/library_mgmt/test_topic_chapters.py`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 05.01 Unit chunking | Done (sampled) | E05 |
| 05.02 FTS/tsvector | Done (sampled) | E05 |
| 05.03 Vector index | Done (sampled) | E05; current code supports Chroma and Postgres stores |
| 05.04 Hybrid RRF | Done (sampled) | E05 |
| 05.05 Incremental indexing | Done (sampled) | E05 |
| 05.06 Query suggestions | Done (sampled) | E05 |
| 05.07 Chapter inference | Done (sampled) | E05 |

### Epic 06 — Job queue

**E06:** `pipeline/src/maktaba_pipeline/engine/claim.py`, `run.py`, `requeue.py`, `hooks.py`, `workers.py`, `graph.py`; migrations `shared/db/migrations/0002_processing_jobs.sql` and `0089_queue_control_and_logs.sql`; tests `pipeline/tests/db/test_jobs_enqueue.py`, `pipeline/tests/engine/test_enqueue.py`, `test_run.py`, `test_queue_health.py`, `api/queues_integration_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 06.01 Schema/indexes | Done (sampled) | E06 |
| 06.02 Claim loop | Done (sampled) | E06 |
| 06.03 Heartbeat/progress | Done (sampled) | E06 |
| 06.04 Pause/resume/cancel | Done (sampled) | E06 |
| 06.05 Backoff/retry | Done (sampled) | E06 |
| 06.06 Reaper | Done (sampled) | E06 |
| 06.07 Concurrency caps | Done (sampled) | E06 |
| 06.08 Graceful shutdown | Done (sampled) | E06 |
| 06.09 Observability | Done (sampled) | E06 |
| 06.10 Resume invariant | Done (sampled) | E06 |

### Epic 07 — API server

**E07:** `api/main.go`, `api/internal/router/router.go`, `api/internal/handlers/libraries/libraries.go`, `videos/videos.go`, `search/search.go`, `api/internal/handlers/streaming/streaming.go`, `api/internal/handlers/watch/`, `jobs/jobs.go`, `recommendations/recommendations.go`, `devices/downloads.go`; tests include `api/auth_integration_test.go`, `api/queues_integration_test.go`, `api/internal/router/p6_queues_test.go`, `api/internal/handlers/search/search_test.go`, `api/internal/handlers/streaming/streaming_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 07.01 HTTP server skeleton | Done (sampled) | E07 |
| 07.02 Cursor pagination | Done (sampled) | E07 |
| 07.03 Library CRUD | Done (sampled) | E07 |
| 07.04 Video CRUD | Done (sampled) | E07 |
| 07.05 Processing control | Done (sampled) | E07 |
| 07.06 Transcript window | Done (sampled) | E07 |
| 07.07 Subtitles/chapters read | Done (sampled) | E07 |
| 07.08 Search API | Done (sampled) | E07 |
| 07.09 Saved searches | Done (sampled) | E07 |
| 07.10 Streaming sessions | Done (sampled) | E07 |
| 07.11 Watch progress | Done (sampled) | E07 |
| 07.12 Job control | Done (sampled) | E07 |
| 07.13 Queue stats | Done (sampled) | E07 |
| 07.14 Collections/tags/speakers | Done (sampled) | E07 |
| 07.15 Settings/system | Done (sampled) | E07 |
| 07.16 WebSocket fan-out | Done (sampled) | E07; `api/internal/handlers/ws/` |
| 07.17 GraphQL | Done (sampled) | E07; `api/internal/graphql/` |
| 07.18 gRPC clients | Done (sampled) | E07; `api/internal/grpcclients/` |
| 07.19 Validation/rate limits | Done (sampled) | E07; `api/internal/middleware/` |
| 07.20 Health/version/metrics | Done (sampled) | E07 |
| 07.21 Recommendations | Done (sampled) | E07 |
| 07.22 Device registration | Done (sampled) | E07; migration `0040_devices.sql` |

### Epic 08 — Streaming

**E08:** `streaming/internal/capability/matrix.go`, `handlers/direct.go`, `ffmpeg/remux.go`, `ffmpeg/transcode.go`, `handlers/mpd.go`, `ffmpeg/hwaccel.go`, `grpcsrv/server.go`, `session/session.go`, `session/reaper.go`, `handlers/subtitle.go`, `handlers/chapter.go`, `cache/layout.go`, `probe/probe.go`; adjacent `_test.go` files including `matrix_test.go`, `direct_test.go`, `transcode_test.go`, `mpd_test.go`, `server_test.go`, `session_test.go`, `layout_test.go`, `probe_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 08.01 Server skeleton/signed URLs | Done (sampled) | E08 |
| 08.02 Capability matrix | Done (sampled) | E08 |
| 08.03 Direct play | Done (sampled) | E08 |
| 08.04 Direct-stream remux | Done (sampled) | E08 |
| 08.05 HLS transcode | Done (sampled) | E08 |
| 08.06 DASH manifest | Done (sampled) | E08 |
| 08.07 Hardware detection | Done (sampled) | E08 |
| 08.08 gRPC server | Done (sampled) | E08 |
| 08.09 Session store/reaper | Done (sampled) | E08 |
| 08.10 Concurrency caps | Done (sampled) | E08 |
| 08.11 Live subtitles | Done (sampled) | E08 |
| 08.12 Chapter delivery | Done (sampled) | E08 |
| 08.13 Posters/sprites | Done (sampled) | E08 |
| 08.14 Cache GC | Done (sampled) | E08 |
| 08.15 Probe cache | Done (sampled) | E08 |

### Epic 09 — Library management

**E09:** `pipeline/src/maktaba_pipeline/scanner/service.py`, `library_mgmt/speakers.py`, `api/internal/handlers/libraries/libraries.go`, `manage.go`, `stats.go`, `collections/collections.go`, `tags/tags.go`; tests `pipeline/tests/library_mgmt/test_config.py`, `test_sweep.py`, `test_dedup.py`, `test_ignore.py`, `test_manual_scan.py`, `test_speakers.py`, `test_roots.py`, `test_audit.py`, and API handler tests.

| Story | Status | Evidence / missing work |
|---|---|---|
| 09.01 Config schema | Done (sampled) | E09 |
| 09.02 Filesystem watcher | Done (sampled) | E09; also E01 |
| 09.03 Periodic sweep | Done (sampled) | E09 |
| 09.04 Content-hash dedup | Done (sampled) | E09 |
| 09.05 Ignore rules | Done (sampled) | E09 |
| 09.06 Manual scan | Done (sampled) | E09 |
| 09.07 Library stats | Done (sampled) | E09 |
| 09.08 Language tag | Done (sampled) | E09; `test_language.py` |
| 09.09 Topic tag | Done (sampled) | E09; `test_topics.py` |
| 09.10 Content classifier | Done (sampled) | E09; `test_content_type.py` |
| 09.11 Speakers | Done (sampled) | E09 |
| 09.12 Tag CRUD | Done (sampled) | E09 |
| 09.13 Manual collections | Done (sampled) | E09 |
| 09.14 Smart collections | Done (sampled) | E09 |
| 09.15 Library deletion | Done (sampled) | E09 |
| 09.16 Multi-root overlap | Done (sampled) | E09 |
| 09.17 Library audit | Done (sampled) | E09 |
| 09.18 Chapter inference | Done (sampled) | E09; `test_topic_chapters.py` |

### Epic 10 — Auth security

**E10:** `api/internal/auth/users/users.go`, `argon2id/argon2id.go`, `handlers/auth/auth.go`, `refresh/refresh.go`, `serverkeys/store.go`, `signedurl/signedurl.go`, `authz/authz.go`, `securityaudit/securityaudit.go`, `handlers/discovery/pairing.go`; tests `api/auth_integration_test.go`, `api/internal/auth/**/*_test.go`, `api/internal/handlers/auth/*_test.go`, `pairing_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 10.01 User store | Done (sampled) | E10 |
| 10.02 Web login | Done (sampled) | E10 |
| 10.03 Native login | Done (sampled) | E10 |
| 10.04 Token refresh | Done (sampled) | E10 |
| 10.05 Logout/revocation | Done (sampled) | E10 |
| 10.06 RS256/JWKS | Done (sampled) | E10 |
| 10.07 Streaming JWT verify | Done (sampled) | E10; `streaming/internal/auth/` |
| 10.08 Signed URL minter | Done (sampled) | E10 |
| 10.09 Single-user mode | Done (sampled) | E10 |
| 10.10 CSRF | Done (sampled) | E10; `handlers/auth/csrf_test.go` |
| 10.11 Brute-force protection | Done (sampled) | E10; `lockout_test.go` |
| 10.12 Auth rate limiting | Done (sampled) | E10; `middleware/authratelimit_test.go` |
| 10.13 Permission model | Done (sampled) | E10 |
| 10.14 Secret loading | Done (sampled) | E10 |
| 10.15 Transport security | Done (sampled) | E10 |
| 10.16 Security audit | Done (sampled) | E10 |
| 10.17 Device pairing | Done (sampled) | E10 |
| 10.18 Ed25519 identity | Done (sampled) | E10; `api/internal/auth/serverkeys/` |

### Epic 11 — Web UI

**E11:** `web/src/App.tsx`, `pages/Library/LibraryPage.tsx`/`LibraryTabs.tsx`, `pages/VideoPlayer.tsx`, `pages/Search.tsx`, `pages/Settings.tsx`, `components/LoadError.tsx`, `lib/i18n.tsx`, `lib/sw.ts`; tests `web/src/App.cases.test.tsx`, `components/AppShell.test.tsx`, `lib/i18n.test.tsx`, `lib/pwa.test.ts`, `lib/sw.test.ts`, page-level Vitest files and Playwright suites.

| Story | Status | Evidence / missing work |
|---|---|---|
| 11.01 Library browser | Done (sampled) | E11 |
| 11.02 Video detail | Done (sampled) | E11 |
| 11.03 Video player | Done (sampled) | E11 |
| 11.04 Search interface | Done (sampled) | E11 |
| 11.05 Queue dashboard | Done (sampled) | E11; `pages/queue/` |
| 11.06 Settings | Done (sampled) | E11 |
| 11.07 Responsive design | Done (sampled) | E11; viewport Playwright coverage |
| 11.08 Dark/light theme | Done (sampled) | E11; `lib/theme.test.ts` |
| 11.09 Keyboard shortcuts | Done (sampled) | E11 |
| 11.10 Offline PWA | Done (sampled) | E11 |
| 11.11 Accessibility | Done (sampled) | E11; axe/UI tests |
| 11.12 i18n/RTL | Done (sampled) | E11 |
| 11.13 PAT management | Done (sampled) | E11; migration `0071_personal_access_tokens.sql` |
| 11.14 Active sessions | Done (sampled) | E11; profile/session tests |

### Epic 12 — Mobile

**E12:** current native successors under `apps/apple/Player`, `apps/apple/Remote`, `apps/apple/MaktabaKit`, `apps/android/player`, `apps/android/remote`, `apps/android/client`; playback `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/media/PlaybackService.kt`; API tests `apps/android/client/src/test/.../PairingTest.kt`, `ReviewApiTest.kt`, Apple MaktabaKit tests and UI tests. The old Capacitor wrapper has been superseded by native apps; current deliverables exist rather than the original wrapper implementation.

| Story | Status | Evidence / missing work |
|---|---|---|
| 12.01 iOS app | Done (sampled) | E12 |
| 12.02 Android app | Done (sampled) | E12 |
| 12.03 Native player | Done (sampled) | E12 |
| 12.04 Push notifications | Done (sampled) | E12; cloud push evidence E25 |
| 12.05 Background playback | Done (sampled) | E12 |
| 12.06 Offline downloads | Done (sampled) | E12; migration `0064_device_downloads.sql` |
| 12.07 Share/AirPlay/Cast | Done (sampled) | E12; `web/src/lib/cast/sender.ts` and native Cast clients |
| 12.08 Haptics | Done (sampled) | E12 |
| 12.09 Deep linking | Done (sampled) | E12 |
| 12.10 Device registration/push | Done (sampled) | E12 |
| 12.11 Downloaded flag API | Done (sampled) | E12; `api/internal/handlers/devices/downloads.go` |

### Epic 13 — Desktop

**E13:** `apps/desktop/src-tauri/src/main.rs`, `lib.rs`; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/`; `apps/apple/Player`; workflows `_mac-app.yml`, `_win-app.yml`, `_linux-app.yml`, `desktop-release.yml`; tests `apps/desktop-player/src/test/`, Apple `PlayerMacTests`, and desktop UI/unit tests. Auto-update also has `api/autoupdate_e2e_test.go` and `api/autoupdate_venv_e2e_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 13.01 macOS app | Done (sampled) | E13 |
| 13.02 Windows app | Done (sampled) | E13 |
| 13.03 Linux app | Done (sampled) | E13 |
| 13.04 System tray | Done (sampled) | E13 |
| 13.05 mDNS discovery | Done (sampled) | E13 |
| 13.06 Drag/drop | Done (sampled) | E13 |
| 13.07 Keyboard shortcuts | Done (sampled) | E13 |
| 13.08 Auto-update | Done (sampled) | E13 |

### Epic 14 — TV apps

**E14:** `apps/tv/tvos/`, `apps/tv/android/`, `web/src/bigscreen/`; `api/internal/handlers/recommendations/recommendations.go`; tests `apps/tv/android/app/src/test/`, `apps/tv/tvos/MaktabaTVUITests/`, `web/src/bigscreen/BigScreen.test.tsx`, API recommendation tests.

| Story | Status | Evidence / missing work |
|---|---|---|
| 14.01 tvOS app | Done (sampled) | E14 |
| 14.02 Android TV app | Done (sampled) | E14 |
| 14.03 10-foot UI | Done (sampled) | E14 |
| 14.04 Voice search | Done (sampled) | E14; platform search/deep-link handlers |
| 14.05 Continue Watching | Done (sampled) | E14 |
| 14.06 Recommendations UI | Done (sampled) | E14 |
| 14.07 Recommendations API | Done (sampled) | E14 |

### Epic 15 — Discovery

**E15:** `api/internal/discovery/mdns.go`, `announce.go`, `api/internal/handlers/discovery/pairing.go`, `minter.go`, `device.go`, `home.go`; direct/cloud paths `cloud/internal/direct/`, `cloud/internal/cloudlink/`; tests `api/internal/discovery/*_test.go`, `api/internal/handlers/discovery/*_test.go`, `cloud/internal/cloudlink/*_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 15.01 mDNS | Done (sampled) | E15 |
| 15.02 Cloud relay discovery | Done (sampled) | E15 |
| 15.03 Federation | Done (sampled) | E15; server-to-server/cluster link code exists |
| 15.04 DLNA/UPnP | Done (sampled) | E15; direct/NAT mapping and HDHomeRun surfaces |
| 15.05 QR pairing | Done (sampled) | E15; `web/src/lib/qr.test.ts` |
| 15.06 Pairing API | Done (sampled) | E15 |
| 15.07 Federation API/crypto | Done (sampled) | E15; `api/internal/cluster/nodeauth/` |

### Epic 16 — Subscriptions

**E16:** `api/internal/subscriptions/subscriptions.go`, `store.go`, `refresh.go`, `revocation.go`, `api/internal/flags/resolver.go`, `api/internal/handlers/subscriptions/subscriptions.go`, `web/src/lib/flags.tsx`; tests `api/internal/subscriptions/subscriptions_test.go`, `golden_test.go`, `refresh_test.go`, `api/internal/flags/flags_test.go`, `api/internal/handlers/flags/flags_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 16.01 Free tier | Done (sampled) | E16 |
| 16.02 Premium features | **Done** | E16; #1453: the expiry and downgrade grace: the plan keeps working fully for 30 days from the license's expiry, then locks (`subscriptions.Grace`, `api/internal/subscriptions/store.go`, `grace_test.go`); the admin sees the days left (`grace_until`/`grace_days_left`, `SubscriptionSettings.tsx`). Backup and federation mount behind `RequireFeature` when their routes ship. |
| 16.03 Subscription management | Done (sampled) | E16 |
| 16.04 License validation | **Done** | E16; signed offline validation, refresh and tests; #1454: past the seats, the newest non-admin accounts are read-only: they watch and keep their progress, history, playlists and favourites; uploads, metadata edits and managing the home are refused (`subscriptions.SeatGuard`, `seats.go`, `seats_test.go`, `seats_pg_test.go`, `403 seat-read-only`); the admin sees them, the account is told (`SubscriptionSettings.tsx`). |
| 16.05 Telemetry opt-in | Done (sampled) | E16; `api/internal/router/telemetry.go`, `web/src/components/TelemetryPrompt.tsx` |
| 16.06 Feature flags | Done (sampled) | E16 |
| 16.07 Telemetry API | Done (sampled) | E16 |
| 16.08 Feature-flag API | Done (sampled) | E16 |

### Epic 17 — UX design system

**E17:** `web/design-system/`, `web/src/components/AppShell.tsx`, `LoadError.tsx`, `RouteErrorBoundary.tsx`, `pages/Setup/SetupWizard.tsx`, `web/src/lib/i18n.tsx`, player/queue/transcript components; tests `web/design-system/` tests, `web/src/components/*.test.tsx`, `web/src/pages/Setup/*test*`, `web/src/lib/i18n.test.tsx`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 17.01 Design tokens | Done (sampled) | E17 |
| 17.02 Component library | Done (sampled) | E17 |
| 17.03 Motion | Done (sampled) | E17 |
| 17.04 Loading states | Done (sampled) | E17 |
| 17.05 Error/empty states | Done (sampled) | E17 |
| 17.06 Onboarding | Done (sampled) | E17 |
| 17.07 RTL layout | Done (sampled) | E17 |
| 17.08 Player controls | Done (sampled) | E17 |
| 17.09 Search presentation | Done (sampled) | E17 |
| 17.10 Processing progress | Done (sampled) | E17 |
| 17.11 Transcript presentation | Done (sampled) | E17 |

### Epic 18 — Performance

**E18:** `api/internal/handlers/search/search_perf_test.go`, `streaming/internal/ffmpeg/realffmpeg_test.go`, `streaming/internal/cache/layout_test.go`, `.github/workflows/_perf-ci.yml`, `nightly-perf.yml`, `tests/perf/`, `tools/coverage-floor/`, plus database indexes in `shared/db/migrations/`. Runtime targets are code/test evidence only; no benchmarks were rerun.

| Story | Status | Evidence / missing work |
|---|---|---|
| 18.01 Latency budgets | Done (sampled) | E18 |
| 18.02 Search performance | Done (sampled) | E18 |
| 18.03 Streaming hot path | Done (sampled) | E18 |
| 18.04 Pipeline throughput | Done (sampled) | E18 |
| 18.05 Memory/CPU envelopes | Done (sampled) | E18 |
| 18.06 Client-perceived performance | Done (sampled) | E18 |
| 18.07 Database/N+1 | Done (sampled) | E18 |
| 18.08 Cache hit rates | Done (sampled) | E18 |

### Epic 19 — Scalability

**E19:** `api/internal/cluster/`, `pipeline/src/maktaba_pipeline/engine/claim.py`, `workers.py`, `streaming/internal/cluster/`, `shared/components/store/`, `api/internal/auth/authz/`; migration `shared/db/migrations/0030_library_acl.sql`; tests `api/internal/cluster/pg_integration_test.go`, `pipeline/tests/cluster/`, `api/internal/auth/authz/acl_test.go`. Chroma multi-writer remains intentionally excluded by 19.04; 19.08 is readiness, not a promise of hosted multi-tenant capacity.

| Story | Status | Evidence / missing work |
|---|---|---|
| 19.01 Single-host capacity | Done (sampled) | E19; benchmarks not rerun |
| 19.02 API scale-out | Done (sampled) | E19 |
| 19.03 Streaming scale-out | Done (sampled) | E19 |
| 19.04 Pipeline scale-out | Done (sampled) | E19; Chroma multi-writer explicitly deferred within the design |
| 19.05 Database scaling/failover | Done (sampled) | E19; cloud Patroni evidence E50 |
| 19.06 Storage scaling | Done (sampled) | E19 |
| 19.07 Concurrency quotas | Done (sampled) | E19 |
| 19.08 Multi-tenant readiness | Done (sampled) | E19 |

### Epic 20 — Testing

**E20:** `tests/e2e/`, `tests/perf/`, `pipeline/tests/`, `api/*_test.go`, `shared/fixtures/`, `tools/test-budget/`, `tools/coverage-floor/`, workflows `_unit.yml`, `_integration.yml`, `_e2e.yml`, `_perf-ci.yml`; current history includes flaky-test timing fixes through `origin/main`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 20.01 Test pyramid/budgets | Done (sampled) | E20 |
| 20.02 Fixtures/seed data | Done (sampled) | E20 |
| 20.03 Unit coverage | Done (sampled) | E20 |
| 20.04 Integration tests | Done (sampled) | E20 |
| 20.05 E2E smoke flows | Done (sampled) | E20 |
| 20.06 Contract tests | Done (sampled) | E20; shared OpenAPI/vector suites |
| 20.07 Performance regression CI | Done (sampled) | E20 |
| 20.08 Flaky-test policy | Done (sampled) | E20 |

### Epic 21 — Observability

**E21:** `shared/log/`, `shared/metrics/`, `shared/health/`, `api/internal/auth/securityaudit/securityaudit.go`, `api/internal/handlers/queues/metrics.go`, `api/internal/handlers/logs/`; tests `shared/log/go/shared_cases_test.go`, `api/internal/auth/securityaudit/securityaudit_test.go`, queue/log handler tests. Later full implementation is in Epic 53's log keeper/index/dashboards.

| Story | Status | Evidence / missing work |
|---|---|---|
| 21.01 Structured logging | Done (sampled) | E21 |
| 21.02 Metrics surface | Done (sampled) | E21 |
| 21.03 Distributed tracing | Done (sampled) | E21; request IDs also covered by E53 |
| 21.04 Health/readiness | Done (sampled) | E21 |
| 21.05 Error reporting | Done (sampled) | E21 |
| 21.06 Audit log | Done (sampled) | E21 |
| 21.07 Pipeline visibility | Done (sampled) | E21 |
| 21.08 Telemetry privacy | Done (sampled) | E21; redaction shared test vectors |

### Epic 22 — DevOps

**E22:** `.github/workflows/ci.yml`, `_build-artifacts.yml`, `release.yml`, `packages.yml`; `deploy/compose/docker-compose.yml`, `deploy/packaging/`, `shared/db/migrations/MANIFEST.md`, `api/internal/updates/`, `cmd/maktaba-server/internal/supervisor/`; tests `deploy/packaging/tests/`, `tools/ci/tests/`, `api/autoupdate_e2e_test.go`, `api/rehearsal_*_e2e_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 22.01 CI pipeline | Done (sampled) | E22 |
| 22.02 Reproducible builds | Done (sampled) | E22; signed release/checksum workflows exist |
| 22.03 Container images | Done | E22; CI's `images` job (`_images.yml`) builds the four images on linux/amd64 and linux/arm64 and holds them to AC4's budgets with `tools/image-size-guard.sh` (#1449) |
| 22.04 Database migrations | Done (sampled) | E22 |
| 22.05 Release management | Done (sampled) | E22 |
| 22.06 Upgrade/rollback | Done (sampled) | E22 |
| 22.07 Multi-platform packaging | Done (sampled) | E22 |
| 22.08 Local developer workflow | Done (sampled) | E22; `Makefile`, `pipeline/dev-watch.sh` |
| 22.09 Process supervision | Done (sampled) | E22 |

### Epic 23 — Security

**E23:** `api/internal/auth/`, `api/internal/security/validation.go`, `ratelimit.go`, `sbom.go`, `disclosure.go`, `api/internal/handlers/security/security.go`, `shared/go/releaseverify/`; tests `api/internal/security/security_test.go`, `api/internal/handlers/security/security_test.go`, `api/auth_integration_test.go`, auth and authorization tests. An old gap-analysis document was not treated as current evidence without checking the tree.

| Story | Status | Evidence / missing work |
|---|---|---|
| 23.01 Authentication | Done (sampled) | E23 |
| 23.02 Authorization/ACLs | Done (sampled) | E23 |
| 23.03 Transport security | Done (sampled) | E23; TLS/client-IP and release verification code |
| 23.04 Secrets management | Done (sampled) | E23; redaction suites |
| 23.05 Input validation | Done (sampled) | E23 |
| 23.06 Rate limiting/confirmation | Done (sampled) | E23 |
| 23.07 Supply-chain security | **partial** | #1442: `tools/sbom.sh` writes a checked CycloneDX SBOM per component (api, streaming, server, cloud, launcher, healthcheck, pipeline, web) and the release attaches them (`sbom` job, before `sign-and-release`); CI's `cve-gate` (`_cve-gate.yml`, `tools/security/cve_gate.py`) runs govulncheck, pip-audit and pnpm audit and fails on a called Go vulnerability or a high/critical Python or npm one unless `security/suppressions/` records a reason and an expiry. Release images also carry BuildKit SBOM attestations. Missing: `maktaba-plugind` (Rust) has no SBOM and no audit. |
| 23.08 Coordinated disclosure | Done | #1444: `SECURITY.md` (GitHub private reporting or security@maktaba.dev, 3 business days to acknowledge, 90 days to a fix or coordinated disclosure, scope, anonymity); `GET /api/system/updates` carries the published GHSAs that affect the running version (`api/internal/system/advisories.go`, `advisories_test.go`) and Settings → About lists them with the release that fixes them (`web/src/pages/Settings/AboutPanel.tsx`, `Settings.test.tsx` SAB-23.8). Also `disclosure.go` and the public `security.txt`. The tabletop drill (TC1) and a synthetic-CVE patch release (TC3) are process exercises, not code. |

### Epic 24 — Data integrity

**E24:** `shared/db/migrations/`, including `0057_integrity_checks.sql`, `0059_audit_log_append_only.sql`, `0060_idempotency_keys.sql`; `api/internal/updates/`, `cmd/maktaba-server/internal/dbsafe/`, `pipeline/src/maktaba_pipeline/identity/`, `engine/`; tests `api/internal/updates/backups_test.go`, `api/rehearsal_*`, `cmd/maktaba-server/internal/pgbundle/real_restore_test.go`, `pipeline/tests/library_mgmt/test_verify.py`, compatibility workflows/tests.

| Story | Status | Evidence / missing work |
|---|---|---|
| 24.01 Atomic writes | Done (sampled) | E24 |
| 24.02 Idempotent jobs | Done (sampled) | E24 |
| 24.03 DB constraints | Done (sampled) | E24 |
| 24.04 Concurrency/locking | Done (sampled) | E24 |
| 24.05 Backup/restore | Done (sampled) | E24 |
| 24.06 Disaster recovery | Done (sampled) | E24 |
| 24.07 Integrity verification | Done (sampled) | E24 |
| 24.08 Identity stability | Done (sampled) | E24 |
| 24.09 Forward/back compatibility | Done (sampled) | E24; `tools/compat-prev/` and CI |

### Epic 25 — Cloud relay

**E25:** `cloud/cmd/maktaba-cloud/main.go`, `cloud/internal/server/router.go`, `auth/`, `handlers/account/account.go`, `cloudlink/claim.go`, `relay/tunnel.go`, `relay/registry.go`, `handlers/relay/proxy.go`, `billing/`, `push/`, `handlers/admin/`, `entitlement/`, `tls/acme.go`; installers under `cloud/installers/`; tests throughout adjacent `*_test.go` and `cloud/cmd/maktaba-cloud/*_integration_test.go`.

| Story | Status | Evidence / missing work |
|---|---|---|
| 25.01 Cloud API bootstrap | Done (sampled) | E25 |
| 25.02 Email registration | Done (sampled) | E25 |
| 25.03 Google OAuth | Done (sampled) | E25; `auth/oauth/google.go`, `jwks_test.go` |
| 25.04 Apple OAuth | Done (sampled) | E25; `auth/oauth/apple.go` |
| 25.05 Account profile/delete/export | Done (sampled) | E25; `privacy/` and account tests |
| 25.06 Server claim token | Done (sampled) | E25 |
| 25.07 Home relay tunnel | Done (sampled) | E25 |
| 25.08 Cloud relay tunnel | Done (sampled) | E25 |
| 25.09 HTTP relay proxy | Done (sampled) | E25 |
| 25.10 Direct/LAN fallback | Done (sampled) | E25 |
| 25.11 Bandwidth metering | Done (sampled) | E25 |
| 25.12 Tier enforcement | Done (sampled) | E25 |
| 25.13 Checkout | Done (sampled) | E25; provider-neutral billing now, no provider selected |
| 25.14 Billing webhook | Done (sampled) | E25; `handlers/billing/webhook_test.go` |
| 25.15 Plan/subscription UI | Done (sampled) | E25; `web/cloud/src/pages/` tests |
| 25.16 Server status dashboard | Done (sampled) | E25 |
| 25.17 Push ingest | Done (sampled) | E25 |
| 25.18 APNs dispatcher | Done (sampled) | E25; `push/apns.go` |
| 25.19 FCM dispatcher | Done (sampled) | E25; `push/fcm.go` |
| 25.20 Admin fleet | Done (sampled) | E25 |
| 25.21 Admin revenue | Done (sampled) | E25 |
| 25.22 Subdomain provisioning | Done (sampled) | E25 |
| 25.23 TLS edge | Done (sampled) | E25 |
| 25.24 Rate limiting/quota | Done (sampled) | E25; `ratelimit/` |
| 25.25 Abuse detection | Done (sampled) | E25; `abuse/detector.go` |
| 25.26 Entitlement signing | Done (sampled) | E25; `entitlement/entitlement_test.go` |
| 25.27 macOS installer | Done (sampled) | E25; installer/package workflows |
| 25.28 Windows installer | Done (sampled) | E25 |
| 25.29 Linux packages | Done (sampled) | E25 |
| 25.30 Docker image | Done (sampled) | E25 |
| 25.31 NAS support | Done (sampled) | E25; later full evidence E59 |
| 25.32 Raspberry Pi/ARM | Done (sampled) | E25; multi-arch release workflows |
| 25.33 One-click VPS deploy | Done (sampled) | E25; `installers/cloud-vps/` |
| 25.34 Auto-update | Done (sampled) | E25; update rehearsal evidence E22 |
| 25.35 First-run wizard | Done (sampled) | E25; `web/src/pages/Setup/SetupWizard.tsx` |
| 25.36 Uninstaller | Done (sampled) | E25; platform package/remove scripts |

## Epics 26–49 (every story checked)

### Epic 26 — Content intelligence

Stories are the ten `story-26-NN-*.md` files in `specs/epics/26-content-intelligence/`.

| Story | Status | Evidence |
|---|---|---|
| 26.1 Title parser | **Done** | `pipeline/src/maktaba_pipeline/classify/title_parser.py`; `pipeline/tests/classify/test_title_parser.py`; `pipeline/tests/fixtures/filenames.jsonl`; `shared/db/migrations/0073_media_parsed_titles.sql`; main commit `1d56d90c`. |
| 26.2 Transcript topic & entity extraction | **Partial** | `shared/db/migrations/0074_media_classification.sql` exists, but `pipeline/src/maktaba_pipeline/classify/classify_stage.py` says the extractor is still to land; no implementation or test file was found. |
| 26.3 Series detection | **Done** | PR #46; `pipeline/src/maktaba_pipeline/classify/series_detector.py`; `pipeline/src/maktaba_pipeline/engine/stages/_detect.py`; `pipeline/tests/classify/test_series_detector.py`; `pipeline/tests/mediaintel/test_detect_store.py`. |
| 26.4 Auto-collection builder | **Partial** | `shared/db/migrations/0076_auto_collections.sql` and `pipeline/src/maktaba_pipeline/classify/group_scheduler.py` exist; no `auto_collections` module or suggestion-producing tests exist. |
| 26.5 Web metadata enrichment | **Partial** | PR #133; `pipeline/src/maktaba_pipeline/enrich/service.py`; `pipeline/src/maktaba_pipeline/enrich/providers.py`; `pipeline/tests/enrich/test_provider.py`. These implement internal/web/LLM proposals, explicitly not the specified catalogue adapters. |
| 26.6 Enrichment review UI | **Done** | Main commit `1d56d90c`, later provider work #133; `api/internal/handlers/enrichment/enrichment.go`; `api/internal/handlers/enrichment/accept.go`; `api/internal/handlers/enrichment/enrichment_test.go`; `web/src/components/EnrichmentPanel.tsx`; `web/src/components/EnrichmentPanel.test.tsx`. |
| 26.7 Background enrichment pipeline | **Partial** | `pipeline/src/maktaba_pipeline/classify/classify_stage.py`; `pipeline/src/maktaba_pipeline/enrich/worker.py`; `pipeline/tests/classify/test_classify_stage.py`; `pipeline/tests/enrich/test_worker.py`. The flow exists, but its 26.2 and 26.4 halves do not. |
| 26.8 YouTube search integration | **Done** | Main commit `1d56d90c`; `api/internal/handlers/search/youtube.go`; `api/internal/handlers/search/ytdlp.go`; `api/internal/handlers/search/youtube_test.go`; `web/src/pages/Search.tsx`. |
| 26.9 Web context cards | **Done** | Main commit `1d56d90c`; `api/internal/handlers/contextcard/contextcard.go`; `api/internal/handlers/contextcard/contextcard_test.go`; `web/src/components/MediaContextCard.tsx`. |
| 26.10 Cross-library series view | **Done** | Main commit `1d56d90c`; `api/internal/handlers/series/series.go`; `api/internal/handlers/series/series_test.go`; `web/src/pages/Library/SeriesBrowser.tsx`; `web/src/pages/Library/SeriesBrowser.test.tsx`; `web/src/pages/Library/SeriesDetail.tsx`. |

### Epic 27 — Live channels

Stories are `story-27-01-*.md` through `story-27-11-*.md` in `specs/epics/27-live-channels/`.

| Story | Status | Evidence |
|---|---|---|
| 27.1 Channel definition | **Done** | Main commit `13fd5040`; `api/internal/handlers/channels/channels.go`; `api/internal/handlers/channels/channels_test.go`; `shared/db/migrations/0081_channels.sql`. |
| 27.2 Program scheduler | **Done** | Main commit `13fd5040`; `pipeline/src/maktaba_pipeline/channels/planner/schedule.py`; tests `pipeline/tests/channels/test_schedule_slots.py`, `pipeline/tests/channels/test_packer_contiguity.py`, `pipeline/tests/channels/test_smartmix.py`. |
| 27.3 Live stream engine | **Done** | Main commit `13fd5040`; `streaming/internal/channel/engine.go`; `streaming/internal/channel/station.go`; `streaming/internal/channel/engine_test.go`; `streaming/internal/channel/join_test.go`. |
| 27.4 EPG generation / exports | **Done** | Main commit `13fd5040`; `api/internal/handlers/guide/guide.go`; `api/internal/handlers/guide/xmltv.go`; `api/internal/handlers/guide/m3u.go`; `api/internal/handlers/guide/guide_test.go`. |
| 27.5 HDHomeRun emulation | **Done** | Main commit `13fd5040`, discovery fix #139; `streaming/internal/hdhr/handler.go`; `streaming/internal/hdhr/ssdp.go`; `streaming/internal/hdhr/hdhr_test.go`. |
| 27.6 EPG grid UI | **Done** | Main commit `0515dea6`; `web/src/pages/LiveTV/Guide.tsx`; `web/src/pages/LiveTV/Guide.test.tsx`; later native coverage includes `apps/apple/UITests/PlayerLiveUITests.swift` and `apps/tv/android/app/src/test/java/com/hamzalabs/maktaba/tv/LiveGuideTest.kt`. |
| 27.7 Live channel player | **Done** | Main commit `0515dea6`; `web/src/pages/LiveTV/Player.tsx`; `web/src/pages/LiveTV/Player.test.tsx`; `web/packages/tvplayer/src/switcher.ts`; `web/packages/tvplayer/src/switcher.test.ts`. |
| 27.8 Channel admin UI | **Done** | Main commit `0515dea6`; `web/src/pages/Admin/Channels.tsx`; `web/src/pages/Admin/Channels.test.tsx`; `web/src/pages/Admin/channels/ChannelEditor.tsx`. |
| 27.9 What's On Now widget | **Done** | Main commit `0515dea6`; `web/src/components/WhatsOnNow.tsx`; `web/src/components/WhatsOnNow.test.tsx`; native Home uses it through `web/src/pages/Home.tsx`. |
| 27.10 Filler / bumper system | **Done** | Main commit `0515dea6`; `api/internal/handlers/filler/filler.go`; `api/internal/handlers/filler/filler_test.go`; `shared/db/migrations/0085_channel_filler.sql`; `pipeline/src/maktaba_pipeline/channels/packer.py`. |
| 27.11 Channel strip / display titles | **Partial** | PR #71 built `streaming/internal/channel/strip.go`, `pipeline/src/maktaba_pipeline/classify/display_title.py`, `pipeline/tests/classify/test_display_title.py`, and `web/src/pages/Admin/channels/StripPreview.test.tsx`. #969 removed the story's dizqueTV export half. |

### Epic 28 — Auto-update

Stories are `story-28-01-*.md` through `story-28-06-*.md` in `specs/epics/28-auto-update/`.

| Story | Status | Evidence |
|---|---|---|
| 28.1 Versioning / build stamping | **Done** | Main commit `4eed3df8`; `api/internal/version/version.go`; `api/internal/version/version_test.go`; `api/internal/system/version.go`; `.github/workflows/_build-artifacts.yml`. |
| 28.2 Update check service | **Done** | Main commit `4eed3df8`; `api/internal/system/updater.go`; `api/internal/system/updater_test.go`; `api/internal/router/p28.go`. |
| 28.3 Server self-update | **Done** | Main commit `4eed3df8`, expanded by #271/#287; `api/internal/system/selfupdate.go`; `api/internal/system/selfupdate_test.go`; `api/autoupdate_e2e_test.go`. |
| 28.4 Desktop auto-update | **Done** | Main commit `4eed3df8`, later #152/#194/#287; `apps/desktop/src-tauri/tauri.conf.json`; `.github/workflows/desktop-release.yml`; `tools/release/cmd/updater-feed/main.go`; `tools/release/updaterfeed/updaterfeed_test.go`. The release workflow stamps the repository placeholder. |
| 28.5 Mobile update notification | **Done** | PR #287; `web/src/components/UpdateBanner.tsx`; `web/src/components/UpdateBanner.test.tsx`; `web/src/lib/appUpdate.ts`; `web/src/lib/appUpdate.test.ts`. Its “As built” section correctly limits the banner to sideloaded Android. |
| 28.6 Update UI | **Done** | Main commit `4eed3df8`, expanded by #287; `web/src/pages/Settings/AboutPanel.tsx`; `web/src/pages/Settings/AppUpdateSettings.tsx`; `web/src/pages/Admin/Updates.tsx`; `web/src/pages/Admin/Updates.test.tsx`. |

### Epic 29 — Analytics

Stories are `story-29-01-*.md` through `story-29-06-*.md` in `specs/epics/29-analytics/`.

| Story | Status | Evidence |
|---|---|---|
| 29.1 Watch session tracking | **Done** | Main commit `21980e3d`; `api/internal/handlers/watch/watch.go`; `api/internal/handlers/watch/reaper.go`; `api/internal/handlers/watch/logic_test.go`; `shared/db/migrations/0086_watch_sessions.sql`. |
| 29.2 Watch history | **Done** | Main commit `21980e3d`; `api/internal/handlers/watch/history.go`; `api/internal/handlers/watch/repo.go`; `api/internal/handlers/watch/handlers_test.go`. |
| 29.3 Analytics dashboard | **Done** | Main commit `21980e3d`, rebuilt by #344; `api/internal/handlers/analytics/analytics.go`; `api/internal/handlers/analytics/analytics_test.go`; `web/src/pages/Admin/Analytics.tsx`; `web/src/pages/Admin/Analytics.test.tsx`. |
| 29.4 Activity feed / privacy | **Done** | Main commit `21980e3d`; `api/internal/handlers/watch/activity.go`; `api/internal/handlers/watch/activity_db_test.go`; `web/src/pages/Settings.tsx`. |
| 29.5 Per-video playback statistics | **Done** | Main commit `21980e3d`; `api/internal/handlers/analytics/videostats.go`; `api/internal/handlers/analytics/analytics_test.go`; `web/src/pages/VideoDetail.tsx`. |
| 29.6 Export / retention | **Done** | Main commit `21980e3d`; `api/internal/handlers/analytics/export.go`; `api/internal/handlers/watch/reaper.go`; `api/internal/handlers/analytics/analytics_test.go`. The weekly email is explicitly future work, not an unmet shipping AC. |

### Epic 30 — Relay analytics

Stories are `story-30-01-*.md` through `story-30-04-*.md` in `specs/epics/30-relay-analytics/`.

| Story | Status | Evidence |
|---|---|---|
| 30.1 Relay metrics collection | **Done** | Main commit `66b14e22`; `cloud/internal/metrics/collector.go`; `cloud/internal/metrics/rollup.go`; `cloud/internal/metrics/collector_test.go`; `cloud/migrations/00110001_relay_metrics.sql`. |
| 30.2 GDPR compliance | **Done** | Main commit `66b14e22`; `cloud/internal/privacy/privacy.go`; `cloud/internal/privacy/deletion.go`; `cloud/internal/privacy/privacy_test.go`; `cloud/docs/dpa-template.md`. |
| 30.3 Relay admin dashboard | **Done** | Main commit `66b14e22`; `cloud/internal/handlers/metrics/metrics.go`; `cloud/internal/handlers/metrics/metrics_test.go`; `cloud/cmd/maktaba-cloud/role_api.go`. |
| 30.4 Metrics export | **Done** | Main commit `66b14e22`; `cloud/internal/metrics/export.go`; `cloud/internal/metrics/render_test.go`; `cloud/internal/handlers/metrics/metrics_test.go`. |

### Epic 31 — Multi-instance

The numbered units are `specs/epics/31-multi-instance/story-31-03-canonical-share-paths.md` and `specs/epics/31-multi-instance/story-31-relay-service-door.md`; the README's phase documents are not additional numbered stories.

| Story | Status | Evidence |
|---|---|---|
| 31.3 Canonical share paths | **Done** | PR #81; `pipeline/src/maktaba_pipeline/sharepath/canonical.py`; `pipeline/src/maktaba_pipeline/sharepath/auto.py`; `pipeline/tests/sharepath/test_canonical.py`; `pipeline/tests/cluster/test_sharepath_auto.py`; `api/internal/handlers/cluster/storage.go`; `web/src/components/cluster/CanonicalizePanel.test.tsx`. |
| 31.R Plex service door | **Removed** | Built by #78 with `api/internal/cluster/relay/door.go`, `api/internal/cluster/relay/relay_test.go`, and `pipeline/tests/cluster/test_relay_two_process.py`. #969 deliberately removed the original Plex-sync stages; current `api/internal/cluster/relay/allow.go` retains only the narrowed/generic relay. |

### Epic 32 — TV streaming cluster / remote

Stories are `story-32-01-*.md` through `story-32-13-*.md` in `specs/epics/32-tv-streaming-cluster-remote/`; `audit-2026-10-cloudflare.md` is an evidence lead checked against current code.

| Story | Status | Evidence |
|---|---|---|
| 32.1 Streaming nodes / capacity | **Done** | PR #74; `streaming/internal/cluster/host.go`; `streaming/internal/cluster/capability.go`; `streaming/internal/cluster/cluster_test.go`; `shared/db/migrations/0123_tv_stream_cluster.sql`. |
| 32.2 Placement / balancer | **Done** | PR #74; `streaming/internal/placement/placement.go`; `streaming/internal/placement/store.go`; `streaming/internal/placement/placement_test.go`. |
| 32.3 Front door | **Done** | PR #74; `streaming/internal/frontdoor/frontdoor.go`; `streaming/internal/frontdoor/frontdoor_test.go`; `streaming/internal/frontdoor/link.go`; `streaming/internal/frontdoor/link_test.go`. |
| 32.4 Failover / drain | **Done** | PR #74; `streaming/internal/placement/hosts.go`; `streaming/internal/frontdoor/splice.go`; `streaming/internal/frontdoor/slots_test.go`; `api/internal/tvremote/slots_e2e_test.go`. |
| 32.5 Admin hosts / on air | **Done** | PR #74 and audit fixes #326; `api/internal/handlers/tvcluster/tvcluster.go`; `api/internal/handlers/tvcluster/tvcluster_test.go`; `web/src/pages/Admin/LiveTV/StreamingHosts.tsx`; `web/src/pages/Admin/LiveTV/OnAir.tsx`. |
| 32.6 Cloudflare integration | **Partial** | PRs #73/#326; `api/internal/tvremote/integration.go`; `api/internal/tvremote/integration_test.go`; `api/internal/tvremote/cf/client.go`. Real-account AC7 remains unrun in `docs/tv-cluster-and-remote.md` §9. |
| 32.7 Live input / push | **Partial** | PRs #83/#326; `api/internal/tvremote/controller.go`; `streaming/internal/frontdoor/relay/session.go`; `api/internal/tvremote/push_test.go`; `api/internal/tvremote/e2e_test.go`. Rotation reuses one input; the audit records a short gap and no real SRT run. |
| 32.8 Signed remote playback | **Done** | PRs #83/#326; `api/internal/tvremote/sessions.go`; `api/internal/tvremote/cf/keys.go`; `api/internal/tvremote/viewer_test.go`; `api/internal/tvremote/e2e_test.go`. |
| 32.9 LAN / remote app routing | **Done** | PR #87, later native players #490/#503; `web/src/lib/remoteTv.ts`; `web/src/lib/remoteTv.test.ts`; `web/packages/tvplayer/src/live.ts`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Live.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift`; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Live.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/LiveSourceTest.kt`. |
| 32.10 Cost / limits / budget | **Done** | PRs #83/#326; `api/internal/tvremote/budget.go`; `api/internal/tvremote/storage_guard.go`; `api/internal/tvremote/budget_test.go`. |
| 32.11 Observability | **Done** | PRs #87/#326; `api/internal/tvremote/metrics.go`; `api/internal/tvremote/events.go`; `api/internal/tvremote/observability_test.go`; `streaming/internal/tvmetrics/tvmetrics.go`; `streaming/internal/tvmetrics/tvmetrics_test.go`. |
| 32.12 Docs / runbook | **Partial** | PR #87; `docs/tv-cluster-and-remote.md`; `web/src/help/content/en/remote-tv.md`; `web/src/help/content/ar/remote-tv.md`. §9's required real-account findings remain “not yet run”. |
| 32.13 Simulcast | **Done** | PRs #86/#326; `api/internal/tvremote/simulcast.go`; `api/internal/tvremote/cf/outputs.go`; `api/internal/tvremote/simulcast_test.go`; `web/src/pages/Admin/LiveTV/Simulcast.tsx`; `web/src/pages/Admin/LiveTV/Simulcast.test.tsx`. |

### Epic 33 — TV staging / hot swap

Stories are `story-33-01-*.md` through `story-33-16-*.md` in `specs/epics/33-tv-staging-hot-swap/`; the README's dated “As built” sections were used as leads, not as current status by themselves.

| Story | Status | Evidence |
|---|---|---|
| 33.1 Two-slot runtime | **Done** | PR #97; `streaming/internal/frontdoor/slots.go`; `streaming/internal/frontdoor/slotserve.go`; `streaming/internal/frontdoor/slots_test.go`; `shared/db/migrations/0129_tv_slots.sql`. |
| 33.2 Releases / snapshots | **Done** | PR #96; `pipeline/src/maktaba_pipeline/tvrelease/store.py`; `pipeline/src/maktaba_pipeline/tvrelease/stage.py`; `pipeline/tests/tvrelease/test_releases.py`. |
| 33.3 Schedule generations | **Done** | PR #96; `pipeline/src/maktaba_pipeline/tvrelease/stage.py`; `api/internal/tvchange/store.go`; `api/internal/tvchange/controller_run_test.go`. |
| 33.4 Splice-ready output | **Done** | PR #97; `streaming/internal/frontdoor/splice.go`; `streaming/internal/frontdoor/tssplice.go`; `streaming/internal/frontdoor/stitch_test.go`; `streaming/internal/channel/align.go`. |
| 33.5 Staging warm-up / verification | **Done** | PR #104; `streaming/internal/frontdoor/staging.go`; `streaming/internal/frontdoor/verify/checks.go`; `streaming/internal/frontdoor/verify/verify_test.go`; `api/internal/tvchange/streamverify.go`; `api/internal/tvchange/streamverify_test.go`. |
| 33.6 Staging preview | **Done** | PR #104; `streaming/internal/frontdoor/preview.go`; `streaming/internal/frontdoor/staging_test.go`; `web/src/pages/Admin/LiveTV/ChangeDetail.tsx`; `web/src/pages/Admin/LiveTV/ChangeDetail.test.tsx`. |
| 33.7 Hot-swap front door | **Done** | PR #108; `streaming/internal/frontdoor/swap.go`; `streaming/internal/frontdoor/swap_test.go`; `api/internal/tvchange/swap.go`; `api/internal/tvremote/swap_e2e_test.go`. |
| 33.8 Continuous push / simulcast | **Partial** | PR #108; `streaming/internal/frontdoor/relaysource.go`; `streaming/internal/frontdoor/relay/session.go`; `api/internal/tvremote/swap_e2e_test.go`. The current README still names the missing TC7 spike, defer policy and multi-host staging. |
| 33.9 Rollback | **Partial** | PR #108; `pipeline/src/maktaba_pipeline/tvrelease/rollback.py`; `api/internal/tvchange/rollbackclaim_test.go`; `api/internal/tvremote/swap_e2e_test.go`. No post-swap health-check trigger was found. |
| 33.10 TV change state machine | **Done** | PR #96; `api/internal/tvchange/controller.go`; `api/internal/tvchange/store.go`; `pipeline/src/maktaba_pipeline/tvrelease/store.py`; `pipeline/tests/tvrelease/test_releases.py`. |
| 33.11 Changes page | **Done** | PR #100; `web/src/pages/Admin/LiveTV/Changes.tsx`; `web/src/pages/Admin/LiveTV/Changes.test.tsx`; `web/src/pages/Admin/LiveTV/ChangeDetail.tsx`; `api/internal/tvchange/review_test.go`. |
| 33.12 Producers review mode | **Done** | PR #96 and later update-rule changes; `pipeline/src/maktaba_pipeline/tvrelease/stage.py`; `pipeline/src/maktaba_pipeline/tvrelease/merge.py`; `api/internal/tvchange/merge.go`; `api/internal/tvchange/join_test.go`. |
| 33.13 Notifications / audit | **Done** | PR #100; `api/internal/tvchange/notifyfeed.go`; `api/internal/tvchange/notify_test.go`; `api/internal/tvchange/slotfeed.go`; `api/internal/tvchange/slotfeed_test.go`. |
| 33.14 Upgrade via swap | **Done** | Built through Epic 35 in #271/#287; `cmd/maktaba-server/internal/updater/tv.go`; `cmd/maktaba-server/internal/updater/tv_test.go`; `api/internal/tvchange/autoupdate.go`; `api/internal/tvchange/autoupdate_test.go`; `api/internal/updates/driver_tvswap_test.go`. |
| 33.15 Observability | **Partial** | PR #108; `streaming/internal/tvmetrics/tvmetrics.go`; `streaming/internal/tvmetrics/tvmetrics_test.go`; `web/src/pages/Admin/LiveTV/SlotBar.tsx`. The README still limits the shipped surface to swap/rollback/splice/stitch metrics; complete summaries/alerts were not verified. |
| 33.16 Docs / runbook | **Partial** | `web/src/help/content/en/tv-changes.md`; `web/src/help/content/ar/tv-changes.md`; `docs/cluster/runbook-updates.md`. Required `docs/tv/staging.md` and its playbook/link test are absent. |

### Epic 34 — TV public site

Stories are `story-34-01-*.md` through `story-34-11-*.md` in `specs/epics/34-tv-public-site/`. PR #326 and `specs/epics/32-tv-streaming-cluster-remote/audit-2026-10-cloudflare.md` explicitly call the implementation a first slice.

| Story | Status | Evidence |
|---|---|---|
| 34.1 Public flag / rights | **Partial** | PR #326; `api/internal/tvremote/public.go`; `api/internal/handlers/tvremote/public.go`; `api/internal/tvremote/public_test.go`; `web/src/pages/Admin/LiveTV/PublicPanel.tsx`; `web/src/pages/Admin/LiveTV/PublicPanel.test.tsx`. Exclusions/review/panic are absent. |
| 34.2 Guide / lineup publisher | **Partial** | PR #326; `api/internal/tvremote/public.go`; `api/internal/tvremote/public_test.go`; `web/packages/tvsite/src/worker.ts`. Only signed lineup/live state is posted; R2/deltas/LiveHub are absent. |
| 34.3 Public feed / slates / rotation | **Partial** | PR #326; `api/internal/tvremote/public.go`; `api/internal/tvremote/public_e2e_test.go`; `streaming/internal/frontdoor/relay/session.go`. Uses the shared pool; no dedicated A/B inputs, slates, rendition or tiering. |
| 34.4 Worker API / tokens / SiteControl | **Partial** | PR #326; `web/packages/tvsite/src/worker.ts`; `web/packages/tvsite/src/sign.ts`; `web/packages/tvsite/src/homeauth.ts`; `web/packages/tvsite/src/worker.test.ts`; `web/packages/tvsite/src/sign.test.ts`. The file itself lists the missing sessions/limits/control surfaces. |
| 34.5 Site UI / guide / player | **Partial** | PR #326; `web/packages/tvsite/site/app.js`; `web/packages/tvsite/site/index.html`; `web/packages/tvsite/site/site.css`; `api/internal/tvremote/public_e2e_test.go`. Only the first-slice grid/player is present. |
| 34.6 Channel switching | **Partial** | Shared module from #87: `web/packages/tvplayer/src/index.ts`; `web/packages/tvplayer/src/switcher.ts`; `web/packages/tvplayer/src/switcher.test.ts`. Current `web/packages/tvsite/site/app.js` does not integrate it. |
| 34.7 Access modes | **Not started** | `web/packages/tvsite/src/worker.ts` explicitly says Turnstile/Access are not yet built; no alternate implementation or tests found. |
| 34.8 Branding / domain | **Not started** | `api/internal/tvremote/public.go` has only the first-slice site URL; no branding/custom-domain deploy implementation found. |
| 34.9 Budget / abuse / analytics | **Not started** | `web/packages/tvsite/src/worker.ts` lists budget and limits as not yet built; no Analytics Engine or reconciliation code found. |
| 34.10 Deployment pipeline | **Not started** | `web/packages/tvsite/wrangler.toml` supports manual deployment only; no release/home deployer or promotion/rollback tests found. |
| 34.11 Docs / runbook | **Partial** | `docs/tv-cluster-and-remote.md` §4.1 documents the manual slice; the story's complete operational runbook is absent. |

### Epic 35 — Cluster full nodes and updates

Stories are `story-35-01-*.md` through `story-35-16-*.md` in `specs/epics/35-cluster-full-nodes-and-updates/`.

| Story | Status | Evidence |
|---|---|---|
| 35.1 Release manifest verification | **Done** | PR #102; `shared/go/releaseverify/verify.go`; `shared/go/releaseverify/manifest.go`; `shared/go/releaseverify/verify_test.go`; `shared/go/releaseverify/manifest_test.go`. |
| 35.2 Layout / launcher | **Done** | PR #103; `shared/go/layout/layout.go`; `shared/go/layout/guard.go`; `shared/go/layout/layout_test.go`; `cmd/maktaba-launcher/main.go`; `cmd/maktaba-launcher/launcher_test.go`. |
| 35.3 Policy / discovery / scheduling | **Done** | PR #103, expanded #271; `api/internal/updates/scheduler.go`; `api/internal/updates/policy.go`; `api/internal/updates/scheduler_test.go`; `web/src/pages/Settings/UpdatesSettings.test.tsx`. |
| 35.4 Home orchestrator | **Done** | PR #271; `api/internal/updates/driver.go`; `api/internal/updates/driver_test.go`; `api/internal/updates/driver_review_test.go`; `cmd/maktaba-server/internal/updater/machine.go`. |
| 35.5 Database safety | **Done** | PR #271/#287; `api/internal/updates/backups.go`; `api/internal/updates/backups_test.go`; `cmd/maktaba-server/internal/updater/backup.go`; `cmd/maktaba-server/internal/updater/backup_test.go`; `tools/compat-prev/`; `cmd/maktaba-server/internal/updater/schemadown_test.go`. |
| 35.6 TV during updates | **Done** | PR #287; `cmd/maktaba-server/internal/updater/tv.go`; `cmd/maktaba-server/internal/updater/tv_test.go`; `api/internal/tvchange/autoupdate.go`; `api/internal/updates/driver_tvswap_test.go`. |
| 35.7 Supervisor / launcher self-update | **Done** | PR #287; `cmd/maktaba-server/serve_handover.go`; `cmd/maktaba-server/serve_handover_test.go`; `cmd/maktaba-launcher/replace.go`; `cmd/maktaba-launcher/handover_test.go`. |
| 35.8 Full nodes | **Done** | PR #111 and conversion #287; `cmd/maktaba-server/internal/machineagent/agent.go`; `cmd/maktaba-server/serve_node.go`; `api/internal/updates/convert.go`; `api/internal/updates/convert_test.go`. |
| 35.9 Auth across machines | **Done** | PR #111; `api/internal/cluster/nodeauth/nodeauth.go`; `api/internal/cluster/nodeauth/localsess_test.go`; `api/internal/handlers/clusterauth/home.go`; `api/cluster_signin_removed_test.go`. The spec itself removes the old cluster-SSO criterion; that is not a separate missing story. |
| 35.10 Node routing | **Done** | PR #111; `api/internal/router/p35_nodes.go`; `api/internal/cluster/homeproxy/proxy.go`; `api/internal/cluster/homeproxy/proxy_test.go`; `api/node_offline_test.go`. |
| 35.11 Discovery | **Done** | PR #111; `cmd/maktaba-server/internal/mdns/machine.go`; `api/internal/cluster/advertise.go`; `api/internal/cluster/advertise_test.go`; `api/internal/system/version.go`. |
| 35.12 Cluster rollout | **Done** | PRs #271/#287; `api/internal/updates/driver.go`; `api/internal/updates/mirror.go`; `api/internal/updates/mirror_test.go`; `api/internal/updates/wheelhouse.go`; `api/twomachine_e2e_test.go`. |
| 35.13 Updates page / notifications | **Done** | PR #271/#287; `api/internal/updates/handlers.go`; `api/internal/updates/handlers_test.go`; `web/src/pages/Admin/Updates.tsx`; `web/src/pages/Admin/Updates.test.tsx`. |
| 35.14 Failure modes / E2E | **Done** | PR #271/#287 and later rehearsal fixes; `api/autoupdate_e2e_test.go`; `api/autoupdate_venv_e2e_test.go`; `api/twomachine_e2e_test.go`; `api/rehearsal_machines_e2e_test.go`; `.github/workflows/update-rehearsal.yml`; `docs/update-rehearsal.md`. |
| 35.15 Observability / docs | **Done** | PR #287; `api/internal/updates/metrics.go`; `api/internal/updates/metrics_test.go`; `api/internal/updates/events.go`; `docs/cluster/runbook-updates.md`; `web/src/help/content/en/updates.md`; `web/src/help/content/ar/updates.md`. |
| 35.16 Cluster-link TLS | **Done** | PR #287; `api/internal/cluster/ca/ca.go`; `api/internal/cluster/ca/ca_test.go`; `api/internal/cluster/link/server.go`; `api/internal/cluster/link/link_test.go`; `api/cluster_link_dbcert_pg_test.go`; `docs/cluster/tls.md`. |

### Epic 36 — Mac app

Thirteen units are listed in `specs/epics/36-mac-app/README.md`: 36.1–36.11 plus 36.15 and 36.16; the latter three have detailed sections in the same README.

| Story | Status | Evidence |
|---|---|---|
| 36.1 Bundled Postgres | **Done** | PR #151; `cmd/maktaba-server/internal/pgbundle/pgbundle.go`; `cmd/maktaba-server/internal/pgbundle/backup.go`; `cmd/maktaba-server/internal/supervisor/postgres.go`; `cmd/maktaba-server/internal/pgbundle/pgbundle_test.go`; `cmd/maktaba-server/internal/pgbundle/real_test.go`. |
| 36.2 Mac paths | **Done** | PR #149; `cmd/maktaba-server/internal/config/paths.go`; `cmd/maktaba-server/internal/config/paths_test.go`; `cmd/maktaba-server/internal/config/migrate.go`; `cmd/maktaba-server/internal/config/move_test.go`. |
| 36.3 Pipeline in the bundle | **Done** | PR #150; `cmd/maktaba-server/internal/appvenv/appvenv.go`; `cmd/maktaba-server/internal/appvenv/appvenv_test.go`; `pipeline/src/maktaba_pipeline/venv_bootstrap.py`; `pipeline/tests/test_venv_bootstrap.py`; `tools/release/mac-app/payload.sh`. |
| 36.4 Transcription workers / settings | **Done** | PR #154; `cmd/maktaba-server/internal/supervisor/stt.go`; `cmd/maktaba-server/internal/supervisor/stt_test.go`; `cmd/maktaba-server/internal/models/models.go`; `pipeline/src/maktaba_pipeline/models/downloader.py`. |
| 36.5 Login item / menu bar | **Done** | PR #153 with later login-item fixes; `apps/desktop/src-tauri/src/tray.rs`; `apps/desktop/src-tauri/src/agent.rs`; `apps/desktop/src-tauri/tests/menu_contract.rs`; `apps/desktop/src-tauri/LaunchAgents/com.hamzalabs.maktaba.server.plist`. |
| 36.6 First-run setup | **Done** | PR #153 and shared setup #190; `cmd/maktaba-server/cmd_setup_json.go`; `cmd/maktaba-server/cmd_setup_json_test.go`; `web/src/pages/Setup/SetupWizard.tsx`; `web/src/pages/Setup/SetupWizard.test.tsx`. Plex is now scoped to Live TV, so #969 does not remove this setup step. |
| 36.7 Build the payload | **Partial** | PR #152; `tools/release/mac-app/payload.sh`; `tools/release/mac-app/build-app.sh`; `tools/release/mac-app/check.sh`; `.github/workflows/_mac-app.yml`. PR #152 says the real Developer-ID hardened-runtime/notarization path was unavailable; later ad-hoc fallbacks do not verify that release path. |
| 36.8 Updates | **Done** | PR #287 and app payload hooks #155; `apps/desktop/src-tauri/src/autoinstall.rs`; `apps/desktop/src-tauri/src/autoinstall.rs` contains its tests; `cmd/maktaba-server/internal/updater/runtime.go`; `cmd/maktaba-launcher/bundle.go`; `.github/workflows/desktop-release.yml`. |
| 36.9 Migrate the home Mac | **Partial** | PR #158; `cmd/maktaba-server/internal/devmigrate/migrate.go`; `cmd/maktaba-server/internal/devmigrate/real_test.go`; `cmd/maktaba-server/cmd_migrate_dev.go`; `specs/epics/36-mac-app/migrate-from-dev.md`. PR #158 explicitly says the cutover was not run. |
| 36.10 Clean up | **Done** | Current `README.md`, `apps/desktop/README.md`, `docs/wiki/deployment.md`, and `tools/dev/stack.sh` describe the app versus developer paths; the broken Mac compose overlay is absent. No dedicated implementation PR was identified. |
| 36.11 Language model in the app | **Done** | PR #179; `cmd/maktaba-server/internal/llmserver/llmserver.go`; `cmd/maktaba-server/internal/llmserver/manager.go`; `cmd/maktaba-server/internal/llmserver/llmserver_test.go`; `tools/release/mac-app/pins.env`; `tools/release/mac-app/check.sh`. |
| 36.15 Where data lives | **Done** | PRs #350/#353 and related storage work; `cmd/maktaba-server/internal/storage/move.go`; `cmd/maktaba-server/internal/storage/move_test.go`; `cmd/maktaba-server/internal/storage/target.go`; `cmd/maktaba-server/internal/storage/target_test.go`; `web/src/pages/Settings/StorageSettings.test.tsx`. |
| 36.16 Backup settings | **Done** | PR #353; `cmd/maktaba-server/internal/config/backups.go`; `cmd/maktaba-server/internal/config/backups_test.go`; `cmd/maktaba-server/internal/pgbundle/backup.go`; `web/src/pages/Settings/BackupsSection.tsx`; `web/src/pages/Settings/BackupsSection.test.tsx`. |

### Epic 37 — Node replica

`specs/epics/37-node-replica/README.md` has no numbered story table. Five deliverable clusters are used: the live copy, offline node behaviour, planned switch, emergency promotion, and real-database verification. Follow-ups are not counted as shipping stories.

| Story | Status | Evidence |
|---|---|---|
| Live logical copy / automatic provisioning | **Done** | PR #196 and current `cmd/maktaba-server/internal/nodecopy/manager.go`; `cmd/maktaba-server/internal/nodecopy/copy.go`; `cmd/maktaba-server/internal/nodecopy/copy_pg_test.go`; `api/internal/cluster/copies.go`; `api/internal/cluster/copies_pg_test.go`; `shared/db/migrations/0147_node_replicas.sql`. |
| Offline reads / auth / playback / TV / jobs | **Done** | `api/internal/cluster/nodedb/nodedb.go`; `api/internal/cluster/nodedb/nodedb_test.go`; `api/internal/cluster/nodeauth/nodeauth.go`; `api/internal/cluster/nodeauth/offline_test.go`; `api/internal/cluster/vodfallback/vodfallback.go`; `api/twomachine_e2e_test.go`. |
| Planned home switch | **Done** | `api/internal/cluster/switchover/switchover.go`; `api/internal/cluster/switchover/switchover_pg_test.go`; `cmd/maktaba-server/internal/switchover/switchover_pg_test.go`; `api/switchover_e2e_test.go`; `web/src/components/cluster/SwitchHomeCard.test.tsx`. |
| Emergency promotion | **Done** | `api/internal/cluster/switchover/emergency.go`; `api/internal/cluster/switchover/standby.go`; `api/internal/cluster/switchover/standby_pg_test.go`; `api/emergency_e2e_test.go`; `web/src/components/cluster/EmergencyCard.test.tsx`. |
| Real-Postgres / multi-machine verification | **Done** | `.github/workflows/_replica.yml`; `api/internal/cluster/copy_tables_pg_test.go`; `cmd/maktaba-server/internal/nodecopy/copy_pg_test.go`; `api/twomachine_e2e_test.go`; `api/switchover_e2e_test.go`; `api/emergency_e2e_test.go`. |

### Epic 38 — Windows app

Stories are the fourteen numbered rows in `specs/epics/38-windows-app/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 38.1 Windows paths / node identity | **Done** | PRs #169/#217/#231/#239/#243; `cmd/maktaba-server/internal/config/paths.go`; `cmd/maktaba-server/internal/config/nodeconfig.go`; `cmd/maktaba-server/internal/config/nodeconfig_write.go`; `cmd/maktaba-server/internal/config/nodeconfig_write_test.go`; `pipeline/src/maktaba_pipeline/cluster/config.py`; `pipeline/tests/cluster/test_config.py`. |
| 38.2 Launcher in the Windows app | **Done** | PR #170; `cmd/maktaba-launcher/bundle.go`; `cmd/maktaba-launcher/bundle_test.go`; `cmd/maktaba-launcher/stopevent_windows.go`; `cmd/maktaba-launcher/stopevent_windows_test.go`. |
| 38.3 Shell runs the server | **Done** | PR #175; `apps/desktop/src-tauri/src/agent_windows.rs`; `apps/desktop/src-tauri/src/agent.rs`; `apps/desktop/src-tauri/src/shell.rs`; `apps/desktop/src-tauri/src/setup.rs`. |
| 38.4 Windows tray | **Done** | PR #202; `apps/desktop/src-tauri/src/tray.rs`; `apps/desktop/src-tauri/src/status.rs`; `apps/desktop/src-tauri/tests/menu_contract.rs`. |
| 38.5 Clean role shutdown | **Done** | PR #205 and follow-ups #219/#222/#225; `cmd/maktaba-server/internal/supervisor/signal_windows.go`; `cmd/maktaba-server/internal/supervisor/stop_windows_test.go`; `cmd/maktaba-server/internal/ctrlbreak/`; `apps/desktop/src-tauri/src/agent_windows.rs`. |
| 38.6 Shared setup steps | **Done** | PR #190/#213; `cmd/maktaba-server/cmd_setup_json.go`; `cmd/maktaba-server/cmd_setup_app.go`; `cmd/maktaba-server/cmd_setup_json_test.go`; `cmd/maktaba-server/cmd_setup_app_test.go`; `web/src/pages/Setup/SetupWizard.test.tsx`. |
| 38.7 Windows venv | **Done** | PR #182/#207; `cmd/maktaba-server/internal/appvenv/appvenv.go`; `cmd/maktaba-server/internal/appvenv/realpip_test.go`; `pipeline/src/maktaba_pipeline/venv_bootstrap.py`; `pipeline/tests/test_venv_bootstrap.py`; `.github/workflows/_win-app.yml`. |
| 38.8 Windows transcription | **Done** | PR #201/#221/#228; `pipeline/src/maktaba_pipeline/stt/faster_whisper.py`; `pipeline/src/maktaba_pipeline/stt/ct2.py`; `pipeline/tests/stt/test_ct2.py`; `cmd/maktaba-server/internal/supervisor/stt.go`; `cmd/maktaba-server/internal/supervisor/stt_gpupool_test.go`. |
| 38.9 Second listen on Windows | **Done** | PR #209; `pipeline/src/maktaba_pipeline/stt/cohere_cuda.py`; `pipeline/src/maktaba_pipeline/stt/cohere.py`; `pipeline/tests/stt/test_cohere_cuda.py`; `tools/release/win-app/payload.sh`. The code records its model/driver checks rather than claiming a real gated-model test. |
| 38.10 Windows Postgres | **Done** | PR #204; `cmd/maktaba-server/internal/supervisor/postgres.go`; `cmd/maktaba-server/internal/supervisor/postgres_test.go`; `cmd/maktaba-server/internal/pgbundle/pgbundle.go`; `cmd/maktaba-server/internal/pgbundle/real_test.go`; `tools/release/win-app/payload.sh`. |
| 38.11 Payload / installer | **Done** | PR #198/#212; `tools/release/win-app/payload.sh`; `tools/release/win-app/build-app.sh`; `tools/release/win-app/check.sh`; `tools/release/win-app/test_payload_tool.py`; `.github/workflows/_win-app.yml`; `.github/workflows/desktop-release.yml`. |
| 38.12 Updates | **Done** | PR #194/#287; `cmd/maktaba-server/internal/updater/runtime.go`; `cmd/maktaba-server/internal/updater/backup.go`; `cmd/maktaba-server/internal/updater/venv.go`; `cmd/maktaba-server/internal/updater/venv_test.go`; `apps/desktop/src-tauri/src/autoinstall.rs`. |
| 38.13 Move an existing install | **Done** | PR #200/#230; `cmd/maktaba-server/internal/adopt/move.go`; `cmd/maktaba-server/internal/adopt/find.go`; `cmd/maktaba-server/internal/adopt/adopt_test.go`; `cmd/maktaba-server/internal/adopt/shared_windows_test.go`; `specs/epics/38-windows-app/move-an-install.md`. |
| 38.14 Clean up | **Done** | PR #208; `README.md`; `apps/desktop/README.md`; `docs/wiki/deployment.md`; `deploy/packaging/node/windows/install-node.ps1`; `deploy/packaging/README.md`. |

### Epic 39 — Linux app

Stories are the twelve numbered rows in `specs/epics/39-linux-app/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 39.1 Specification | **Done** | `specs/epics/39-linux-app/README.md`; `specs/epics/39-linux-app/contract-runtime.md`; `specs/epics/39-linux-app/contract-stt-linux.md`; `specs/epics/39-linux-app/headless.md`. |
| 39.2 Linux payload | **Done** | PR #168; `tools/release/linux-app/payload.sh`; `tools/release/linux-app/check.sh`; `tools/release/linux-app/pins.env`; `tools/release/linux-app/test_payload_tool.py`; `.github/workflows/_linux-app.yml`. |
| 39.3 Runtime copy | **Done** | PR #174; `cmd/maktaba-server/internal/appruntime/install.go`; `cmd/maktaba-server/internal/appruntime/activate.go`; `cmd/maktaba-server/internal/appruntime/appruntime_test.go`; `cmd/maktaba-server/internal/updater/runtime.go`. |
| 39.4 Linux app paths | **Done** | PR #176; `cmd/maktaba-server/internal/config/cache.go`; `cmd/maktaba-server/internal/config/cache_test.go`; `cmd/maktaba-server/internal/config/appbundle.go`; `cmd/maktaba-server/internal/config/appbundle_test.go`. |
| 39.5 systemd user service | **Done** | PR #173; `cmd/maktaba-server/internal/service/service.go`; `cmd/maktaba-server/internal/service/appunit.go`; `cmd/maktaba-server/internal/service/service_test.go`; `cmd/maktaba-server/internal/service/appunit_test.go`. |
| 39.6 Linux / headless setup | **Done** | PR #190/#191; `cmd/maktaba-server/cmd_setup.go`; `cmd/maktaba-server/cmd_setup_json.go`; `cmd/maktaba-server/setup_cli_test.go`; `cmd/maktaba-server/cmd_setup_json_test.go`; `specs/epics/39-linux-app/headless.md`. |
| 39.7 Linux transcription | **Done** | PRs #201/#209; `pipeline/src/maktaba_pipeline/stt/ct2.py`; `pipeline/src/maktaba_pipeline/stt/cohere_cuda.py`; `pipeline/tests/stt/test_ct2.py`; `pipeline/tests/stt/test_cohere_cuda.py`; `.github/workflows/_linux-app.yml`. |
| 39.8 Desktop shell on Linux | **Done** | PR #180; `apps/desktop/src-tauri/src/agent_linux.rs`; `apps/desktop/src-tauri/src/tray.rs`; `apps/desktop/src-tauri/src/setup.rs`; `apps/desktop/src-tauri/tests/menu_contract.rs`. |
| 39.9 Linux updates | **Done** | PRs #187/#287; `tools/release/cmd/updater-feed/main.go`; `tools/release/updaterfeed/updaterfeed.go`; `tools/release/updaterfeed/updaterfeed_test.go`; `cmd/maktaba-server/internal/updater/runtime.go`; `.github/workflows/desktop-release.yml`. |
| 39.10 Release CI | **Done** | PRs #184/#189; `.github/workflows/desktop-release.yml`; `.github/workflows/_linux-app.yml`; `.github/workflows/release.yml`; `tools/release/linux-app/full-package.sh`; `tools/release/linux-app/test_full_package.sh`; `tools/release/linux-app/package-remove.sh`. |
| 39.11 Real-systemd E2E | **Done** | PR #186; `tools/release/linux-app/e2e.sh`; `.github/workflows/_linux-app.yml`; `cmd/maktaba-server/internal/appruntime/appruntime_test.go`. |
| 39.12 Clean up | **Done** | PR #193; `docs/wiki/deployment.md`; `apps/desktop/README.md`; `deploy/packaging/README.md`; `deploy/packaging/install.sh`; `README.md`. |

### Epic 40 — Queue health

Stories are the six numbered rows in `specs/epics/40-queue-health/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 40.1 Failure classes / parking | **Done** | PR #177; `pipeline/src/maktaba_pipeline/engine/failure_classes.py`; `pipeline/src/maktaba_pipeline/engine/failure_policy.py`; `pipeline/tests/engine/test_queue_health.py`; `shared/db/migrations/0143_queue_health.sql`. |
| 40.2 Dependency watchers | **Done** | PRs #177/#185; `pipeline/src/maktaba_pipeline/engine/watchers.py`; `pipeline/src/maktaba_pipeline/engine/health.py`; `pipeline/tests/engine/test_queue_health.py`; `pipeline/tests/engine/test_queue_health_backfill.py`. |
| 40.3 Missing files / access / re-download | **Done** | PR #185; `pipeline/src/maktaba_pipeline/engine/backfill.py`; `plugins/first-party/core-media/src/maktaba_fp_core_media/stages/access_check.py`; `pipeline/tests/engine/test_queue_health.py`; `api/internal/handlers/queues/failurelist.go`. |
| 40.4 Health / failures API | **Done** | PR #192; `api/internal/handlers/queues/health.go`; `api/internal/handlers/queues/failures.go`; `api/internal/handlers/queues/failurelist.go`; `api/internal/handlers/queues/failures_integration_test.go`; `api/internal/handlers/queues/failurelist_test.go`. |
| 40.5 Web tiles / failures page | **Done** | PR #195; `web/src/components/queue/QueueCard.tsx`; `web/src/components/queue/QueueCard.test.tsx`; `web/src/pages/queue/Failures.tsx`; `web/src/pages/queue/Failures.test.tsx`. |
| 40.6 Move existing failures | **Done** | PR #185; `pipeline/src/maktaba_pipeline/engine/backfill.py`; `pipeline/tests/engine/test_queue_health_backfill.py`; `pipeline/tests/engine/test_queue_health.py`. |

### Epic 41 — Help guide

Stories are the six numbered rows in `specs/epics/41-help-guide/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 41.1 Specification / IA | **Done** | `specs/epics/41-help-guide/README.md` contains the audit, information architecture, URL scheme and stories. |
| 41.2 Help area | **Done** | PR #183; `web/src/help/Help.tsx`; `web/src/help/search.ts`; `web/src/help/HelpMarkdown.tsx`; `web/src/help/Help.test.tsx`; `web/src/components/AppShell.tsx`. |
| 41.3 Links / registry | **Done** | PR #183; `web/src/help/registry.ts`; `web/src/help/HelpLink.tsx`; `web/src/help/topics.ts`; `web/src/help/help.coverage.test.ts`. |
| 41.4 Coverage tests | **Done** | PR #183; `web/src/help/help.coverage.test.ts`; `web/src/help/Help.test.tsx`; `pipeline/tests/test_help_coverage.py`; `api/internal/handlers/rules/rules_test.go`. |
| 41.5 Bilingual content | **Done** | PR #183 and later content PRs; `web/src/help/content.ts`; `web/src/help/content/en/getting-started.md`; `web/src/help/content/ar/getting-started.md`; the current content directories contain 46 English and 46 Arabic topics, exceeding the original 33. |
| 41.6 Queue-health classes | **Done** | PR #183; `web/src/help/registry.ts` implements `failureClassHelp`; `web/src/pages/queue/Failures.tsx`; `web/src/help/help.coverage.test.ts`; `web/src/help/content/en/failures.md`; `web/src/help/content/ar/failures.md`. |

### Epic 42 — Apps / player / remote / server

Stories are numbered sections 42.1–42.10 under “Stories (in order)” in `specs/epics/42-apps-player-remote-server/README.md`; its parenthetical build notes were confirmed in current code.

| Story | Status | Evidence |
|---|---|---|
| 42.1 Apple Player / Remote | **Done** | PR #306, expanded by #503; `apps/apple/MaktabaKit/Sources/MaktabaKit/Homes.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Browse.swift`; `apps/apple/Player/PlayerApp.swift`; `apps/apple/UITests/PlayerMockHomeTests.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/BrowseTests.swift`. |
| 42.2 Home picker | **Done** | PR #360; `apps/apple/MaktabaKit/Sources/MaktabaKit/HomeBrowser.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Homes.swift`; `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/session/HomeDiscovery.kt`; `api/internal/handlers/discovery/home.go`; `api/internal/handlers/discovery/home_test.go`; `web/src/pages/Profile/ConnectApp.tsx`. |
| 42.3 Android Player / Remote | **Done** | PR #321; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/MainActivity.kt`; `apps/android/remote/src/main/kotlin/com/hamzalabs/maktaba/remote/RemoteApplication.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/shell/PlayerShellTest.kt`; `apps/android/remote/src/test/kotlin/com/hamzalabs/maktaba/remote/FakeRemoteHome.kt`. |
| 42.4 Desktop Player on Windows / Linux | **Done** | PR #377; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/Main.kt`; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/libraries/SeriesState.kt`; `apps/desktop-player/src/test/kotlin/com/hamzalabs/maktaba/desktop/libraries/SeriesStateTest.kt`; `.github/workflows/_desktop-player.yml`; `.github/workflows/desktop-release.yml`. |
| 42.5 Web editions | **Done** | PR #367/#372; `web/src/lib/edition.ts`; `web/src/serverRoutes.tsx`; `web/src/pages/RemoteHome.tsx`; `web/src/pages/Profile/Preferences.tsx`; `web/src/App.editions.test.tsx`; `web/vite.config.ts`. |
| 42.6 Sign-in by code | **Done** | PR #360; `api/internal/handlers/discovery/device.go`; `api/internal/handlers/discovery/device42_test.go`; `apps/apple/MaktabaKit/Sources/MaktabaKit/CodeSignIn.swift`; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Pairing.kt`; `web/src/pages/Pair.tsx`. |
| 42.7 Linked accounts / cloud | **Done** | PR #327/#388; `api/internal/handlers/auth/cloud.go`; `api/internal/handlers/auth/cloud_pg_test.go`; `cloud/internal/handlers/homes/`; `web/src/pages/Profile/CloudLink.tsx`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Cloud.swift`; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/Cloud.kt`. |
| 42.8 Tablet / foldable | **Done** | PR #367; `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/layout/Panes.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/libraries/LibrariesPanesTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/libraries/LibrariesSplitTest.kt`; `apps/apple/Player/Libraries/LibrariesScreen.swift`; `apps/apple/UITests/PlayerLibrariesUITests.swift`. |
| 42.9 Cloud web player / remote | **Done** | PR #390; `cloud/internal/handlers/relay/editions.go`; `cloud/internal/handlers/relay/editions_test.go`; `web/src/lib/basePath.ts`; `web/cloud/src/pages/Homes.tsx`; `web/src/lib/edition.ts`. |
| 42.10 TV picker / code sign-in | **Done** | PR #369; `apps/tv/tvos/MaktabaTV/ContentView.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/TVStart.swift`; `apps/tv/tvos/MaktabaTVUITests/TVConnectUITests.swift`; `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/data/HomeChoices.kt`; `apps/tv/android/app/src/test/java/com/hamzalabs/maktaba/tv/TvConnectTest.kt`. |

### Epic 43 — pgvector search

Stories are the six numbered rows in `specs/epics/43-pgvector-search/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 43.1 pgvector in every Postgres | **Done** | PR #319; `shared/db/migrations/0152_segment_embeddings.sql`; `tools/release/mac-app/payload.sh`; `tools/release/linux-app/payload.sh`; `tools/release/win-app/payload.sh`; `.github/workflows/_integration.yml`; `deploy/compose/docker-compose.yml`. |
| 43.2 `segment_embeddings` / Postgres backend | **Done** | PR #319; `pipeline/src/maktaba_pipeline/search/semantic.py`; `pipeline/tests/search/test_pgstore.py`; `pipeline/tests/search/test_semantic.py`; `pipeline/tests/search/test_semantic_grpc.py`; `shared/db/migrations/0152_segment_embeddings.sql`; `shared/db/migrations/0152_segment_embeddings.sqlite.sql`. |
| 43.3 Copy existing vectors | **Done** | PR #319; `pipeline/src/maktaba_pipeline/search/chroma_copy.py`; `pipeline/tests/search/test_chroma_copy.py`; `cmd/maktaba-server/cmd_serve.go`; `cmd/maktaba-server/internal/supervisor/chromacopy.go`. Resumability, recall checks and deferred folder deletion are implemented and tested with synthetic data. |
| 43.4 Machines / switchover | **Done** | PR #319; `api/internal/cluster/copy_tables_pg_test.go`; `api/internal/cluster/copies.go`; `pipeline/src/maktaba_pipeline/engine/reconcile.py`; `pipeline/tests/search/test_pgstore.py`; `shared/db/migrations/0152_segment_embeddings.sql`. |
| 43.5 Chroma retired | **Done** | #1451: `chromadb` out of `pipeline/pyproject.toml`; search, `index` and `chapter_infer` on `segment_embeddings` only; a home whose Chroma copy is not done finishes it at the update (`search/chroma_copy.py` reads Chroma's files; `supervisor/chromaretire.go` runs it and holds every re-index meanwhile, `MAKTABA_CHROMA_COPY`), nothing embedded again, Chroma's own files deleted the start after; compose/CI/Dockerfile/docs without Chroma; update rehearsal scenario 18 (`api/rehearsal_chroma_e2e_test.go`: a part-done copy finished, no index job). |
| 43.6 Measured on home Mac | **Not started** | PR #319 explicitly says “Not in this PR”; no later merged 13.9M-row p50/p95/disk/recall report was found. |

### Epic 44 — Viewing analytics

Stories are the seven numbered rows in `specs/epics/44-viewing-analytics/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 44.1 Schema | **Done** | PR #344; `shared/db/migrations/0158_viewing_analytics.sql`; `shared/db/migrations/0158_viewing_analytics.sqlite.sql`; `api/internal/cluster/copy_tables_pg_test.go`. |
| 44.2 `X-Maktaba-Client` everywhere | **Done** | PR #344; `api/internal/client/client.go`; `api/internal/client/client_test.go`; `api/internal/middleware/client.go`; `api/internal/middleware/client_test.go`; `web/src/lib/clientHeader.ts`; `web/src/lib/clientHeader.test.ts`; `apps/apple/MaktabaKit/Sources/MaktabaKit/ClientHeader.swift`; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/ClientHeader.kt`. |
| 44.3 Plays from stream sessions | **Done** | PR #344; `api/internal/handlers/watch/stream.go`; `api/internal/handlers/watch/stream_test.go`; `api/internal/handlers/streaming/streaming.go`; `web/src/pages/VideoPlayer.tsx`; `web/src/pages/VideoPlayer.test.tsx`. |
| 44.4 Live TV viewings | **Done** | PR #344; `streaming/internal/viewing/tracker.go`; `streaming/internal/viewing/store_pg.go`; `streaming/internal/viewing/tracker_test.go`; `streaming/internal/viewing/store_pg_test.go`; `streaming/internal/frontdoor/tracker.go`; `streaming/internal/frontdoor/viewing_test.go`. |
| 44.5 API use per app | **Done** | PR #344; `api/internal/usage/usage.go`; `api/internal/usage/usage_test.go`; `api/internal/middleware/client.go`; `api/internal/middleware/client_test.go`. |
| 44.6 Analytics API | **Done** | PR #344; `api/internal/handlers/analytics/analytics.go`; `api/internal/handlers/analytics/repo.go`; `api/internal/handlers/analytics/analytics_test.go`; `api/internal/router/p29.go`. |
| 44.7 Analytics page | **Done** | PR #344; `web/src/pages/Admin/Analytics.tsx`; `web/src/pages/Admin/Analytics.test.tsx`; `web/src/pages/Admin/analytics/tabs.tsx`; `web/src/help/content/en/analytics.md`; `web/src/help/content/ar/analytics.md`. |

### Epic 45 — Libraries UI

`specs/epics/45-libraries-ui/README.md` lists 45.1–45.7, Phase 2's 45.8–45.16, Phase 3's 45.17–45.21, shelved 45.22–45.23, and Phase 4's 45.24.

| Story | Status | Evidence |
|---|---|---|
| 45.1 Path resolver | **Done** | PR #345; `api/internal/libpaths/libpaths.go`; `api/internal/libpaths/libpaths_test.go`; `api/internal/handlers/libraries/manage_test.go`. |
| 45.2 Library management API | **Done** | PR #345; `api/internal/handlers/libraries/manage.go`; `api/internal/handlers/libraries/merge.go`; `api/internal/handlers/libraries/manage_test.go`; `api/internal/handlers/libraries/merge_pg_test.go`. |
| 45.3 Supervisor / server settings | **Done** | PR #345; `api/internal/handlers/serversettings/serversettings.go`; `api/internal/handlers/serversettings/serversettings_test.go`; `cmd/maktaba-server/internal/config/editable.go`; `cmd/maktaba-server/internal/config/editable_test.go`. |
| 45.4 Browse / pins / filters API | **Done** | PR #345; `shared/db/migrations/0159_user_library_pins.sql`; `api/internal/handlers/browse/browse.go`; `api/internal/handlers/browse/routes.go`; `api/internal/handlers/browse/pins_pg_test.go`; `api/internal/handlers/videos/listsort_test.go`. |
| 45.5 Manage / storage web UI | **Done** | PR #345; `web/src/pages/Admin/Libraries.tsx`; `web/src/pages/Admin/Libraries.test.tsx`; `web/src/pages/Settings/StorageSettings.tsx`; `web/src/pages/Settings/StorageSettings.test.tsx`. |
| 45.6 Home / Libraries / library / all videos | **Done** | PR #345; `web/src/pages/Home.tsx`; `web/src/pages/Home.test.tsx`; `web/src/pages/Library/Libraries.tsx`; `web/src/pages/Library/Libraries.test.tsx`; `web/src/pages/Library/LibraryPage.tsx`; `web/src/pages/LibraryBrowser.tsx`; `web/src/pages/LibraryBrowser.test.tsx`. |
| 45.7 Help / browser verification / live merge | **Partial** | PR #345; `web/src/help/content/en/library-management.md`; `web/src/help/content/ar/library-management.md`; `web/src/help/help.coverage.test.ts`; `web/src/pages/Admin/Libraries.test.tsx`. PR #345 verifies a branch copy, not the owner's three live merges. |
| 45.8 Watched / unwatched | **Done** | PR #361; `api/internal/handlers/browse/mine.go`; `api/internal/handlers/videos/mine.go`; `api/internal/handlers/videos/mine_test.go`; `web/src/components/VideoActions.tsx`; `web/src/pages/VideoDetail.actions.test.tsx`. |
| 45.9 Favorites | **Done** | PR #361; `api/internal/handlers/browse/mine.go`; `shared/db/migrations/0162_user_favorites_playlists.sql`; `web/src/pages/Home.tsx`; `web/src/pages/Home.test.tsx`; `web/src/pages/VideoDetail.actions.test.tsx`. |
| 45.10 Playlists | **Done** | PR #361; `api/internal/handlers/browse/playlists.go`; `api/internal/handlers/browse/phase2_test.go`; `web/src/pages/Playlists/Playlists.tsx`; `web/src/pages/Playlists/PlaylistPage.tsx`; `web/src/pages/Playlists/Playlists.test.tsx`. |
| 45.11 Facets / grid filters | **Done** | PR #361; `api/internal/handlers/browse/facets.go`; `api/internal/handlers/browse/phase2_test.go`; `web/src/pages/LibraryBrowser.tsx`; `web/src/pages/LibraryBrowser.phase2.test.tsx`. |
| 45.12 A–Z jump bar | **Done** | PR #361; `api/internal/handlers/browse/facets.go`; `api/internal/handlers/videos/listsort_test.go`; `web/src/lib/browse.ts`; `web/src/pages/LibraryBrowser.phase2.test.tsx`. |
| 45.13 Play all / shuffle / queue / Up next | **Done** | PR #361; `web/src/lib/playQueue.ts`; `web/src/lib/playQueue.test.ts`; `web/src/components/UpNext.tsx`; `web/src/components/UpNext.test.tsx`; `web/src/pages/VideoPlayer.tsx`. |
| 45.14 Video actions / series position | **Done** | PR #361; `web/src/components/VideoActions.tsx`; `web/src/pages/VideoDetail.tsx`; `web/src/pages/VideoDetail.actions.test.tsx`; `api/internal/handlers/browse/mine.go`. |
| 45.15 On Deck | **Done** | PR #361; `api/internal/handlers/browse/ondeck.go`; `api/internal/handlers/browse/phase2_test.go`; `web/src/pages/Home.tsx`; `web/src/pages/Library/LibraryPage.tsx`. |
| 45.16 Multi-select / bulk actions | **Done** | PR #361; `web/src/pages/LibraryBrowser.tsx`; `web/src/pages/LibraryBrowser.phase2.test.tsx`; `web/src/components/AddToPlaylist.tsx`; `api/internal/handlers/videos/mine.go`. |
| 45.17 Collections web | **Done** | PR #368; `web/src/pages/Collections/Collections.tsx`; `web/src/pages/Collections/CollectionPage.tsx`; `web/src/pages/Collections/Collections.test.tsx`; `api/internal/handlers/collections/collections.go`; `api/internal/handlers/collections/smart.go`. |
| 45.18 Grouped recently added | **Done** | PR #368; `api/internal/handlers/browse/grouped.go`; `api/internal/handlers/browse/phase3_test.go`; `web/src/components/MediaRow.tsx`; `web/src/pages/Home.grouped.test.tsx`. |
| 45.19 Scrub thumbnails | **Done** | PR #368; `api/internal/handlers/videos/trickplay.go`; `api/internal/handlers/videos/trickplay_test.go`; `web/src/components/ScrubPreview.tsx`; `web/src/components/ScrubPreview.test.tsx`; `pipeline/src/maktaba_pipeline/thumbnail/`; `web/src/pages/VideoPlayer.tsx`. |
| 45.20 More filters | **Done** | PR #368; `api/internal/handlers/browse/facets.go`; `api/internal/handlers/videos/videos.go`; `api/internal/handlers/browse/phase3_test.go`; `web/src/pages/LibraryBrowser.tsx`. |
| 45.21 Featured hero | **Done** | PR #368; `api/internal/handlers/browse/featured.go`; `api/internal/handlers/browse/phase3_test.go`; `web/src/components/FeaturedHero.tsx`; `web/src/pages/Home.hero.test.tsx`. |
| 45.22 Star ratings | **Deferred** | `specs/epics/45-libraries-ui/README.md` explicitly shelves it; #399 records that Epic 45 is done without it. |
| 45.23 PIN profiles / parental limits | **Deferred** | `specs/epics/45-libraries-ui/README.md` explicitly shelves it; #399 records the decision. |
| 45.24 Library Series / Collections tabs | **Done** | PR #432; `api/internal/handlers/browse/groups.go`; `api/internal/handlers/browse/groups_test.go`; `api/internal/router/libtabs_pg_test.go`; `web/src/pages/Library/LibraryTabs.tsx`; `web/src/pages/Library/LibraryTabs.test.tsx`; `web/src/pages/Library/LibraryPage.collections.test.tsx`. |

### Epic 46 — Big screen

Stories are the seven numbered bullets in `specs/epics/46-big-screen/README.md`.

| Story | Status | Evidence |
|---|---|---|
| 46.1 Spec / epic index | **Done** | `specs/epics/46-big-screen/README.md`; `specs/epics/README.md`. |
| 46.2 Focus engine | **Done** | PR #363; `web/src/bigscreen/focus/graph.ts`; `web/src/bigscreen/focus/FocusProvider.tsx`; `web/src/bigscreen/focus/graph.test.ts`; `web/src/bigscreen/logic.test.ts`. |
| 46.3 Keyboard / gamepad input | **Done** | PR #363; `web/src/bigscreen/input/keys.ts`; `web/src/bigscreen/input/gamepad.ts`; `web/src/bigscreen/input/usePads.ts`; `web/src/bigscreen/input/input.test.ts`. |
| 46.4 Shell / screens | **Done** | PR #363; `web/src/bigscreen/BigScreen.tsx`; `web/src/bigscreen/BigScreen.test.tsx`; `web/src/bigscreen/screens/Home.tsx`; `web/src/bigscreen/screens/Libraries.tsx`; `web/src/bigscreen/screens/Series.tsx`; `web/src/bigscreen/screens/Search.tsx`; `web/src/bigscreen/screens/Live.tsx`; `web/src/bigscreen/screens/Settings.tsx`. |
| 46.5 Player | **Done** | PR #363; `web/src/bigscreen/screens/Watch.tsx`; `web/src/bigscreen/screens/Live.tsx`; `web/src/bigscreen/picture.test.tsx`; `web/src/pages/VideoPlayer.tsx`; `web/src/components/UpNext.tsx`. |
| 46.6 Entry points / desktop integration | **Done** | PR #363; `apps/desktop/src-tauri/src/bigscreen.rs`; `apps/desktop/src-tauri/tests/menu_contract.rs`; `web/src/lib/bigScreen.ts`; `web/src/components/AppShell.tsx`; `web/src/bigscreen/mode.ts`. |
| 46.7 i18n / help / browser verification | **Done** | PR #363 records keyboard-only English/Arabic browser verification at 1920×1080; `web/src/help/content/en/big-screen.md`; `web/src/help/content/ar/big-screen.md`; `web/src/help/help.coverage.test.ts`; `web/src/i18n/en.json`; `web/src/i18n/ar.json`. |

### Epic 47 — Offline convert / dedup

Stories are the fourteen numbered rows in `specs/epics/47-offline-convert-dedup/README.md`. Evidence uses current paths, including later moves into first-party plugins.

| Story | Status | Evidence |
|---|---|---|
| 47.1 Schema / stage registry | **Done** | PR #414; `shared/db/migrations/0166_offline_convert_dedup.sql`; `shared/db/migrations/0166_offline_convert_dedup.sqlite.sql`; `pipeline/tests/library_mgmt/test_offline_schema.py`; `pipeline/src/maktaba_pipeline/engine/graph.py`; `pipeline/src/maktaba_pipeline/engine/stages/registry.py`. |
| 47.2 Ledger / trash / swap / recovery | **Done** | PR #414; `pipeline/src/maktaba_pipeline/library_mgmt/ledger.py`; `pipeline/src/maktaba_pipeline/library_mgmt/trash.py`; `pipeline/src/maktaba_pipeline/library_mgmt/swap.py`; `pipeline/tests/library_mgmt/test_swap.py`; `pipeline/tests/library_mgmt/test_offline_schema.py`. |
| 47.3 Verification | **Done** | PR #414; `pipeline/src/maktaba_pipeline/library_mgmt/verify.py`; `pipeline/src/maktaba_pipeline/library_mgmt/unicode_open.py`; `pipeline/tests/library_mgmt/test_verify.py`; `pipeline/tests/library_mgmt/test_unicode_open.py`. |
| 47.4 `convert_mp4` | **Done** | PR #417; current plugin entry `plugins/first-party/convert/src/maktaba_fp_convert/stages/convert_mp4.py`; `plugins/first-party/convert/tests/test_convert_parity.py`; shared implementation `pipeline/src/maktaba_pipeline/library_mgmt/convert.py`; `pipeline/tests/library_mgmt/test_convert.py`. |
| 47.5 Scanner keeps every copy | **Done** | PR #421; `pipeline/src/maktaba_pipeline/scanner/copies.py`; `pipeline/src/maktaba_pipeline/scanner/store.py`; `pipeline/tests/library_mgmt/test_copies.py`; `api/internal/handlers/libraries/merge.go`; `api/internal/handlers/libraries/merge_pg_test.go`. |
| 47.6 `dedup_scan` | **Done** | PR #421; `pipeline/src/maktaba_pipeline/library_mgmt/duplicates.py`; `pipeline/tests/library_mgmt/test_dedup_scan.py`; `pipeline/tests/library_mgmt/test_dedup_order.py`; `plugins/first-party/dedup/tests/test_dedup_parity.py`. |
| 47.7 `dedup_apply` / merge | **Done** | PR #421; `pipeline/src/maktaba_pipeline/library_mgmt/dedup_merge.py`; `pipeline/tests/library_mgmt/test_dedup_merge.py`; `pipeline/src/maktaba_pipeline/library_mgmt/swap.py`; `plugins/first-party/dedup/tests/test_dedup_parity.py`. |
| 47.8 YouTube sources / errors | **Done** | PR #420; `pipeline/src/maktaba_pipeline/integrations/youtube_errors.py`; `pipeline/src/maktaba_pipeline/integrations/youtube_download.py`; `plugins/first-party/youtube/src/maktaba_fp_youtube/stages/youtube_sync.py`; `pipeline/tests/stages/test_youtube_sync.py`; `pipeline/tests/stages/test_youtube_fetch.py`. |
| 47.9 `youtube_fetch` | **Partial** | PR #420; current `plugins/first-party/youtube/src/maktaba_fp_youtube/stages/youtube_fetch.py`; `pipeline/src/maktaba_pipeline/integrations/youtube_download.py`; `pipeline/tests/stages/test_youtube_fetch.py`; `plugins/first-party/youtube/tests/test_youtube_parity.py`. PR #420's real smoke hit a bot check; open #900 reports the unresolved JS-runtime requirement. |
| 47.10 Back-catalogue backfill | **Done** | PR #423; `plugins/first-party/youtube/src/maktaba_fp_youtube/stages/youtube_backfill.py`; `pipeline/tests/stages/test_youtube_backfill.py`; `plugins/first-party/youtube/tests/test_youtube_parity.py`. Open #772 records one unreproduced long-run flake, not a missing implementation. |
| 47.11 Admin API | **Done** | PR #426; `api/internal/handlers/queues/offline_integration_test.go`; `api/internal/handlers/queues/duplicates.go`; `api/internal/handlers/queues/fileactions.go`; `shared/db/migrations/0168_file_restore_stage.sql`; `plugins/first-party/dedup/src/maktaba_fp_dedup/stages/file_restore.py`; `plugins/first-party/dedup/tests/test_dedup_parity.py`. |
| 47.12 Seeding / Tartube import | **Done** | PR #423; `pipeline/src/maktaba_pipeline/library_mgmt/tartube_import.py`; `pipeline/tests/library_mgmt/test_tartube_import.py`; `pipeline/tests/stages/test_youtube_backfill.py`; `api/internal/handlers/queues/offline_integration_test.go`. The PR confirms the home's real database was not opened. |
| 47.13 Web Queues page | **Partial** | PR #428; `web/src/components/queue/offline.test.tsx`; `web/src/pages/queue/offlinePages.test.tsx`; `web/src/pages/queue/Duplicates.tsx`; `web/src/pages/queue/FileActions.tsx`; `web/src/components/queue/YouTubeSourcesDialog.tsx`. The PR explicitly leaves the duplicate frame strip out. |
| 47.14 Help / rollout | **Partial** | PR #619; `web/src/help/content/en/processing.md`; `web/src/help/content/ar/processing.md`; `web/src/help/content/en/stages.md`; `web/src/help/content/ar/stages.md`; `web/src/help/help.coverage.test.ts`. Help and coverage are present; the owner rollout is unverified and the PR says no live settings/database were touched. |

### Epic 48 — Native players

`specs/epics/48-native-players/README.md` §8 lists 36 units: 48.1–48.35 plus 48.37, with 48.36 in Phase 4. Checkboxes were treated as unverified text and checked against current files and PR evidence.

| Story | Status | Evidence |
|---|---|---|
| 48.1 Specification | **Done** | `specs/epics/48-native-players/README.md`; `specs/epics/48-native-players/research.md`; `specs/epics/48-native-players/plan.md`; `specs/epics/README.md`. |
| 48.2 One token source | **Done** | PR #462; `web/design-system/build/build-tokens.mjs`; `web/design-system/build/verify-tokens.mjs`; `web/design-system/tokens/tokens.dark.json`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Tokens.swift`; `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Tokens.kt`; `apps/android/core/src/test/kotlin/com/hamzalabs/maktaba/core/TokensTest.kt`. |
| 48.3 Channel stream follows library access | **Done** | PR #461; `cmd/maktaba-server/stream_gate.go`; `cmd/maktaba-server/stream_gate_channel_test.go`; `api/internal/router/p57_node_pg_test.go` adds later role coverage. |
| 48.4 Subtitles as HLS renditions | **Done** | PR #468; `streaming/internal/handlers/manifest.go`; `streaming/internal/handlers/subtitle_playlist.go`; `streaming/internal/handlers/subtitle_playlist_test.go`; `cmd/maktaba-server/stream_gate_signed_test.go`. |
| 48.5 Small posters | **Done** | PR #459; `api/internal/handlers/videos/poster.go`; `api/internal/handlers/videos/poster_size_test.go`; `api/internal/handlers/videos/poster_test.go`. |
| 48.6 MaktabaKit browse client | **Done** | PR #467; `apps/apple/MaktabaKit/Sources/MaktabaKit/Browse.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/BrowseTests.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/BrowseReviewTests.swift`. |
| 48.7 MaktabaKit Live client | **Done** | PR #463; `apps/apple/MaktabaKit/Sources/MaktabaKit/Live.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/LiveRelay.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveRelayTests.swift`. |
| 48.8 Android browse client | **Done** | PR #464; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Browse.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/BrowseTest.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/browse/HomeRowsTest.kt`. |
| 48.9 Android Live client | **Done** | PR #465; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/Live.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/LiveApiTest.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/LiveSourceTest.kt`. |
| 48.10 Apple brand | **Done** | PR #480; `apps/apple/MaktabaKit/Sources/MaktabaKit/Brand.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/Theme.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/StartViews.swift`; `apps/apple/MaktabaKit/Package.swift`; `apps/apple/UITests/PlayerConnectProgressUITests.swift`. |
| 48.11 Android brand defaults | **Done** | PR #479; `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Theme.kt`; `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/StartScreens.kt`; `apps/android/core/src/test/kotlin/com/hamzalabs/maktaba/core/ThemeStoreTest.kt`; `apps/android/core/src/test/kotlin/com/hamzalabs/maktaba/core/LaunchThemeTest.kt`. |
| 48.12 Signed stream URLs pass the gate | **Done** | PR #474; `cmd/maktaba-server/stream_gate.go`; `cmd/maktaba-server/stream_gate_signed_test.go`; `streaming/internal/auth/middleware_test.go`; `api/twomachine_e2e_test.go`. |
| 48.13 Apple Player shell / harness | **Done** | PR #469; `apps/apple/Player/PlayerApp.swift`; `apps/apple/Player/Shell/`; `apps/apple/UITests/MockHome.swift`; `apps/apple/UITests/PlayerShellUITests.swift`; `.github/workflows/_apple-apps.yml`; `tools/apple/check-string-catalogs.py`. |
| 48.14 Apple Player components | **Done** | PR #485; `apps/apple/Player/Components/PosterCard.swift`; `apps/apple/Player/Components/Rail.swift`; `apps/apple/Player/Components/Hero.swift`; `apps/apple/Player/Components/ImageCache.swift`; `apps/apple/Player/Components/ComponentGallery.swift`; `apps/apple/UITests/PlayerComponentsUITests.swift`. |
| 48.15 Android Player shell / harness | **Done** | PR #466; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/MainActivity.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/shell/PlayerHome.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/shell/PlayerShellTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/harness/FakeHome.kt`; `.github/workflows/_android-apps.yml`. |
| 48.16 Android Player components | **Done** | PR #484; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/ui/components/HomePicture.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/ui/components/ComponentsTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/ui/components/ComponentsScreenshotTest.kt`. |
| 48.17 Apple Home | **Done** | PR #503; `apps/apple/Player/Home/HomeScreen.swift`; `apps/apple/Player/Home/HomeRows.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/HomeShelves.swift`; `apps/apple/UITests/PlayerHomeUITests.swift`; `apps/apple/PlayerMacTests/HomeScreenTests.swift`. |
| 48.18 Apple Libraries / Series | **Done** | PR #503; `apps/apple/Player/Libraries/LibrariesScreen.swift`; `apps/apple/Player/Libraries/LibraryScreen.swift`; `apps/apple/Player/Libraries/SeriesScreen.swift`; `apps/apple/UITests/PlayerLibrariesUITests.swift`; `apps/apple/PlayerMacTests/SeriesScreenTests.swift`. Open #876 requests additional panel/layout snapshot coverage; implementation and other tests are present. |
| 48.19 Apple video page | **Done** | PR #503; `apps/apple/Player/Video/VideoScreen.swift`; `apps/apple/Player/Video/VideoPoster.swift`; `apps/apple/Player/Video/VideoLink.swift`; `apps/apple/UITests/PlayerVideoUITests.swift`; `apps/apple/PlayerMacTests/PageSnapshotTests.swift`. |
| 48.20 Apple Search | **Done** | PR #503; `apps/apple/Player/Search/SearchScreen.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/SearchPage.swift`; `apps/apple/UITests/PlayerSearchUITests.swift`; `apps/apple/PlayerMacTests/SearchScreenTests.swift`. Open #1199 is a residual Eastern-Arabic-digit display bug, not missing search implementation. |
| 48.21 Apple Live TV | **Done** | PR #503; `apps/apple/Player/Live/LiveTVScreen.swift`; `apps/apple/Player/Live/LiveGuide.swift`; `apps/apple/Player/Live/LivePlayback.swift`; `apps/apple/UITests/PlayerLiveUITests.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/LiveTests.swift`. |
| 48.22 Apple Profile / Settings | **Done** | PR #503; `apps/apple/Player/Profile/ProfileScreen.swift`; `apps/apple/Player/Profile/ProfilePages.swift`; `apps/apple/Player/Profile/PlaybackPreferences.swift`; `apps/apple/UITests/PlayerProfileUITests.swift`. |
| 48.23 Apple player resume / next / tracks / quality | **Done** | PR #503; `apps/apple/Player/Watch/WatchScreen.swift`; `apps/apple/Player/Watch/PlayerTracks.swift`; `apps/apple/Player/Watch/TrackSheets.swift`; `apps/apple/Player/Watch/UpNext.swift`; `apps/apple/UITests/PlayerWatchUITests.swift`. |
| 48.24 Apple PiP / AirPlay / Now Playing | **Partial** | PR #512; `apps/apple/MaktabaKit/Sources/MaktabaKit/AirPlay.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/NowPlaying.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/AirPlayTests.swift`; `apps/apple/UITests/PlayerWatchUITests.swift`; `apps/apple/UITests/PlayerLiveUITests.swift`. Real-receiver verification remains explicitly unticked. |
| 48.25 Mac player | **Done** | PR #503; `apps/apple/Player/Mac/PlayerCommands.swift`; `apps/apple/Player/Mac/MacFullScreen.swift`; `apps/apple/Player/Mac/PlayerMenuTarget.swift`; `apps/apple/PlayerMacTests/PlayerMenuTests.swift`; `apps/apple/PlayerMacTests/UI/PlayerMacMenuUITests.swift`. Open #1198 records an Arabic window/sidebar layout bug. |
| 48.26 Android Home | **Done** | PR #488; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/home/HomeScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/home/HomeState.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/home/HomeScreenTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/home/HomeScreenshotTest.kt`. |
| 48.27 Android Libraries / Series | **Done** | PR #501; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/libraries/LibrariesScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/libraries/SeriesPage.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/libraries/LibrariesSectionTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/libraries/LibrariesPanesTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/libraries/SeriesPageTest.kt`. |
| 48.28 Android video page | **Done** | PR #487; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/video/VideoScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/video/VideoPoster.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/video/VideoLink.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/video/VideoScreenTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/video/VideoScreenshotTest.kt`. |
| 48.29 Android Search | **Done** | PR #486; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/search/SearchScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/search/SearchModel.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/search/SearchScreenTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/search/SearchModelTest.kt`. |
| 48.30 Android Live TV | **Done** | PR #501; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/live/LiveTvScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/live/LiveEngine.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/live/LiveTvScreenTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/live/LiveSectionsTest.kt`. |
| 48.31 Android Profile / Settings | **Done** | PR #491; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/profile/ProfileScreen.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/profile/ProfileState.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/profile/ProfileScreenTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/profile/ProfileScreenshotTest.kt`. |
| 48.32 Android player resume / next / tracks / quality | **Done** | PR #493; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/Watch.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/Playback.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/watch/WatchRouteScreen.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/watch/PlaybackTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/watch/WatchScreenTest.kt`. |
| 48.33 Android PiP / background / Cast | **Done** | PR #501/#505; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/media/PlaybackService.kt`; `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/Watch.kt`; `apps/android/player/src/main/AndroidManifest.xml`; `cmd/maktaba-server/stream_gate_signed_test.go`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/watch/PlaybackTest.kt`. |
| 48.34 Apple TV Live TV | **Done** | PR #503; `apps/tv/tvos/MaktabaTV/Views/LiveView.swift`; `apps/tv/tvos/MaktabaTV/Views/LiveGuide.swift`; `apps/tv/tvos/MaktabaTV/Views/LivePlayer.swift`; `apps/tv/tvos/MaktabaTVUITests/TVLiveUITests.swift`. |
| 48.35 Android TV Live TV | **Done** | PR #490; `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/LiveScreen.kt`; `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/LiveGuide.kt`; `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/LivePlayerScreen.kt`; `apps/tv/android/app/src/test/java/com/hamzalabs/maktaba/tv/LiveGuideTest.kt`. |
| 48.37 Connect progress | **Done** | PR #503/#505; `apps/apple/MaktabaKit/Sources/MaktabaKit/ConnectProgress.swift`; `apps/apple/MaktabaKit/Sources/MaktabaKit/ConnectProgressView.swift`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/ConnectProgressTests.swift`; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/ConnectProgress.kt`; `apps/android/client/src/test/kotlin/com/hamzalabs/maktaba/core/ConnectProgressTest.kt`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/start/ConnectProgressScreenshotTest.kt`. |
| 48.36 Help / screenshots / verification | **Done** | PRs #503/#505 plus current evidence: `web/src/help/content/en/apple-apps.md`; `web/src/help/content/ar/apple-apps.md`; `web/src/help/content/en/android-apps.md`; `web/src/help/content/ar/android-apps.md`; `web/src/help/content/en/apple-tv.md`; `web/src/help/content/ar/apple-tv.md`; `apps/apple/PlayerMacTests/PageSnapshotTests.swift`; `apps/android/player/src/test/kotlin/com/hamzalabs/maktaba/player/home/HomeScreenshotTest.kt`. Open #876 remains a snapshot-test follow-up. |

### Epic 49 — Search

Stories are the fifteen numbered rows in `specs/epics/49-search/README.md` §6 (49.14 appears before 49.13 there). Spec PRs #514/#515 are not implementation evidence. Searches found no `keyword_index` stage or Epic 49 AI-mode implementation.

| Story | Status | Evidence |
|---|---|---|
| 49.1 PostgreSQL 18 / pg_textsearch bundles | **Done** (#1441) | Every app payload carries PostgreSQL 18.7 in `postgres-18/` with pgvector and pg_textsearch, beside the 16 the server still runs until 49.2; `tools/release/pg18-check.sh` in each payload's `check.sh`, `TestRealPgTextsearchPreloaded`; README §9 "As built". |
| 49.2 Major updater upgrade | **Done** (#1445) | The server upgrades the 16 cluster into its own `postgres-18` with pg_upgrade (clone, else copy), the 16 cluster untouched for any rollback; held on 16 when it cannot run; update-rehearsal scenario 17 (16 → 18 with data, a rollback, the upgrade again), `TestRealMajorUpgrade` on Linux, Mac and Windows payloads; README §9 "As built". |
| 49.3 Cluster major-version rollout | **Done** (#1459) | A machine on 16 follows a home on 18 (logical replication; a copy whose slot the upgrade dropped is made anew); a copy runs on 16 or 18, one of the other major set aside and made again; the replica job runs `TestTwoMachinesOnePC` with the home on 18 and the machine on 16, then on 18; README §9 "As built". |
| 49.4 Arabic normalisation / word forms | **Done** (#1476) | `maktaba_normalize()` and the `maktaba_stem` configuration (migration 0231), the IMMUTABLE `maktaba_tsv`/`maktaba_query` pair, and `api/internal/textnorm` for SQLite; tested on Postgres 16 and 18 and SQLite in `api/internal/textnorm`; README §9 "As built". |
| 49.5 `video_search_docs` / BM25 schema | **Done** (#1478) | `video_search_docs` (migration 0232, its SQLite twin with FTS5), two BM25 indexes (exact on `maktaba_search`, stemmed on `maktaba_stem`) built CONCURRENTLY by `maktaba-api migrate up` (`api/internal/searchindex`) where pg_textsearch is, which the bundled server creates (`pgbundle.textsearchSQL`); tested on Postgres 16 and 18 and SQLite, and in each payload's `pg18-check.sh`; README §9 "As built". |
| 49.6 `keyword_index` stage / backfill | **Done** (#1482) | The `keyword_index` stage (`core.search`) after `second_listen`, queued by the API's title and speaker edits and by its library plan (one job a video at priority 900; migration 0233's `doc_version` and `transcript_revision`); the BM25 indexes built after the start by the API's `api:search_index` loop (issue #1479); tested on SQLite and Postgres, with a 45,000-document build on PostgreSQL 18; README §9 "As built". |
| 49.7 BM25 query / passages / hybrid | **Done** (#1491) | BM25 over the documents' exact and stemmed indexes with the library, language and account filters, a phrase boost for the top 100, the line path's passages, hybrid by RRF, and the line path while the indexes or a document are missing (`api/internal/handlers/search/bm25.go`, migration 0234's stored columns); `TestBM25_Postgres`, and `TestBM25_Perf45k` p95 under 50 ms on 45,000 documents (the replica job); README §9 "As built". |
| 49.8 Suggestions / did-you-mean / facets / sort API | **Done** (#1497) | `search_terms` and `search_speakers` (migration 0235) kept by the `keyword_index` stage; `GET /api/search/suggest` (history, completed word, titles, speakers), `did_you_mean`, `facets` and `sort` on `POST /api/search`, each on the caller's libraries; `TestBM25_SearchHelp`, `TestSearchHelp_SQLite`, and `TestBM25_Perf45k` with suggest under 2 ms p95 on 45,000 documents; README §9 "As built".
| 49.9 Transcript-derived relevance check | **Partial** (PR pending) | The generator (`relevance.go`: known-item with spelling and word-form variants, chapter, topic, common words), `TestBM25_Relevance` on a synthetic library (every search passes after two ranking fixes) and `maktaba-api search-relevance`; the home's own run and its tuning are open; README §9 "As built". |
| 49.10 Web search page | **Not started** | Only `specs/epics/49-search/README.md`; current `web/src/pages/Search.tsx` is the older search surface, without the new integrated API. |
| 49.11 Apple / Android / TV / big screen | **Not started** | Only `specs/epics/49-search/README.md`; existing `apps/apple/Player/Search/SearchScreen.swift` and `apps/android/player/src/main/kotlin/com/hamzalabs/maktaba/player/search/SearchScreen.kt` predate the Epic 49 API. |
| 49.12 AI-mode API | **Not started** | Only `specs/epics/49-search/README.md`; no grounded-answer/citation/streaming/follow-up API or tests found. |
| 49.13 Remove stopgap | **Not started** | Only `specs/epics/49-search/README.md`; its start condition (every video has a document) waits on 49.6's backfill running on the home. |
| 49.14 AI-mode client tab | **Not started** | Only `specs/epics/49-search/README.md`; no AI tab, streaming citation UI, or client tests found. |
| 49.15 Arabic titles rank by similarity | **Not started** | Found in 49.8: `runTitles` orders by `similarity(v.title, q)`, 0 for every Arabic title in a `--locale=C` database. |

## Epics 50–60 (every story checked)

### Epic 50 — Cloud with no downtime

All 35 stories have current implementation and test evidence. This is a code/history verdict, **not** evidence that two production nodes, paid billing, Cloudflare rules, or the availability SLOs are operating successfully.

| Story | Status | Evidence (merged PRs; current code/tests) |
|---|---|---|
| 50.01 Names/DNS | Done | #700; `cloud/internal/handlers/servers/subdomain.go`; `reserved_integration_test.go`; `tools/lint/no-foreign-domains-test.sh` |
| 50.02 Wildcard certificate | Done | #720; `cloud/rehearsal/fakeacmedns/fakeacmedns.go`; `cloud/rehearsal/scenarios_test.go` (`cert-on-standby`) |
| 50.03 Tunnel handover | Done | #701; `cloud/internal/relay/drain.go`; `cloud/internal/relay/simhome/handover_test.go` |
| 50.04 Tunnel directory | Done | #705; `cloud/internal/relay/directory.go`; `cloud/internal/handlers/relay/directory_test.go` |
| 50.05 Database blip | Done | #714; `cloud/internal/handlers/relay/cache.go`; `blip_integration_test.go` |
| 50.06 Cloud rehearsal | Done | #715; `cloud/rehearsal/fakehcloud/fakehcloud.go`; `cloud/rehearsal/scenarios_test.go`; `.github/workflows/cloud-rehearsal.yml` |
| 50.07 Two nodes | Done | #721; `deploy/cloud-ha/setup.sh`; `cloud/rehearsal/ha_test.go`; `deploy/cloud-ha/tests/scripts-test.sh` |
| 50.08 Zero-downtime deploys | Done | #722; `deploy/cloud-ha/deploy.sh`; `deploy/cloud-ha/tests/scripts-test.sh` blue/green cases |
| 50.09 Backups/restore | Done | #724; `deploy/cloud-ha/monitoring/backup-rules.yml`; `cloud/rehearsal/ha_test.go`, `restore_test.go` |
| 50.10 Monitoring/canary | Done | #727; `cloud/cmd/maktaba-canary/main.go`, `main_test.go`; `deploy/cloud-ha/monitoring/alerts.yml` |
| 50.11 Status page/app status | Done | #739, #752, #907; `status/render.mjs`; `status/test/status.test.mjs`; app status tests |
| 50.12 Incident handling | Done | #780; `tools/runbooks/lint.py`; `cloud/rehearsal/runbook_test.go`; `tools/ci/tests/test_runbooks_lint.py` |
| 50.13 Apps ride out outage | Done | #791; `web/src/lib/reconnect.ts`; `web/cloud/e2e/cloud-down.ui.spec.ts`; `apps/apple/MaktabaKit/Tests/MaktabaKitTests/CloudOutageTests.swift` |
| 50.14 Load test/sizing | Done | #798; `cloud/cmd/maktaba-loadtest/main.go`; `cloud/loadtest/loadtest_test.go`; `cloud/rehearsal/load_test.go` |
| 50.15 Direct remote | Done | #808, #845, #877, #892, #903; `cloud/internal/direct/dns.go`; `dns_test.go`; `cloud/internal/cloudlink/direct_test.go` |
| 50.16 Relay pool | Done | #1241; `cloud/internal/relay/pool.go`; `cloud/rehearsal/relaypool_test.go`; `cloud/internal/relay/pool_test.go` |
| 50.17 Stateless API pool | Done | #1236; `cloud/internal/db/leader.go`; `cloud/rehearsal/pool_test.go`; `leader_integration_test.go` |
| 50.18 Database nodes | Done | #1260, #1324; `deploy/cloud-ha/setup.sh`; `cloud/rehearsal/db_test.go`; `cloud/internal/db/pgbouncer_integration_test.go` |
| 50.19 Plans/relay billing | Done | #832, #849, #854, #856, #865; `cloud/internal/billing/plans.go`, `plans_test.go`; `meter_integration_test.go` |
| 50.20 Broadcaster tier | Done | #1238, #1393; `cloud/internal/billing/broadcast_usage.go`; `cloud/internal/billing/broadcast_test.go` |
| 50.21 Broadcast edge | Done | #1242, #1257, #1373, #1385, #1399; `web/packages/broadcast-edge/src/ingest.ts`; `test/ingest.test.ts`, `live.test.ts` |
| 50.22 Home broadcaster | Done | #1263, #1373; `streaming/internal/broadcast/ctl.go`; `ctl_test.go`, `uploader_test.go`; `api/internal/broadcast/service_pg_test.go` |
| 50.23 Publishable sources/geo | Done | #1274, #1393; `api/internal/broadcast/publishable.go`, `publishable_pg_test.go`; `pipeline/tests/channels/test_publishable_only.py` |
| 50.24 Takedown/reports | Done | #1284, #1389; `cloud/internal/moderation/decisions.go`; `cloud/cmd/maktaba-cloud/moderation_integration_test.go` |
| 50.25 Kill switch | Done | #1270; `cloud/internal/broadcast/revocation_store.go`; `web/packages/broadcast-edge/test/revocations.test.ts` |
| 50.26 Viewer access | Done | #1266; `web/packages/broadcast-edge/src/access.ts`; `test/access.test.ts`; `cloud/cmd/maktaba-cloud/viewer_access_integration_test.go` |
| 50.27 Guest viewers/apps | Done | #1279, #1389, #1400, #1408; `web/packages/broadcast-edge/src/play.ts`, `test/site.test.ts`; Apple `PlayerGuestUITests.swift`, Android guest tests |
| 50.28 Analytics/monitoring | Done | #1251, #1374, #1390; `cloud/internal/broadcast/rollup.go`; `cloud/internal/broadcast/analytics_test.go`; broadcast rehearsal coverage |
| 50.29 Broadcast rehearsal/load | Done | #1338, #1376, #1381; `tools/broadcast-viewers/rehearsal/verdict.go`; `scenarios_test.go`; `.github/workflows/broadcast-rehearsal.yml`. The six initially missing scenarios are now present. |
| 50.30 Cloudflare edge rules | Done | #873; `tools/cloudflare/zone.mjs`; `zone.test.mjs`; `cloud/internal/turnstile/turnstile_test.go` |
| 50.31 Origin through Cloudflare | Done | #883, #922, #927; `cloud/internal/middleware/clientip.go`, `clientip_test.go`; `tools/cloudflare/tunnel.test.mjs` |
| 50.32 Admin hostname | Done | #871, #928, #941; `cloud/internal/server/admin_router.go`, `admin_router_test.go`; `admin_listener_integration_test.go` |
| 50.33 Staff auth | Done | #1027, #1067; `cloud/internal/handlers/staff/staff.go`; `cloud/cmd/maktaba-cloud/staff_integration_test.go`, `staff_passkey_integration_test.go` |
| 50.34 Cloudflare deploys | Done | #867; `tools/cloudflare/cf.mjs`; `token-check.test.mjs`, `smoke.test.mjs`; CI workflow |
| 50.35 Remote-access capability | Done | #825; `shared/components/catalog.go`; `api/internal/components/features_test.go`; `handlers/components/capabilities_test.go` |

### Epic 51 — Configurable components

Integration PR **#608** plus follow-up #666 cover the epic. Opened the component catalog, services, supervisor, handlers, clients, and their tests. Each row also names the principal current artifact for that story.

| Story | Status | Evidence / missing work |
|---|---|---|
| 51.01 Catalog | Done | #608; `shared/components/catalog.go`, `catalog.toml`, `catalog_test.go`; Python loader parity |
| 51.02 State tables | Done | #608; `shared/components/store/store.go`, `store_test.go`; `api/internal/components/store_pg_test.go` |
| 51.03 Seed from today | Done | #608; `cmd/maktaba-server/internal/componentseed/seed.go`, `seed_test.go`; `api/internal/components/seed_pg_test.go` |
| 51.04 Supervisor reconciler | Done | #608, #666; `cmd/maktaba-server/internal/supervisor/reconcile.go`, `reconcile_test.go`, `reconcile_pg_test.go` |
| 51.05 Go loop runner | Done | #608; `shared/components/runner/runner.go`, `runner_test.go`; `api/internal/components/loops_test.go` |
| 51.06 Pipeline stage groups | Done | #608; `pipeline/src/maktaba_pipeline/components/runner.py`; `api/internal/handlers/queues/snapshot_component_test.go` |
| 51.07 Extensions | Done | #608; `api/internal/handlers/queues/extensions.go`, `extensions_component_test.go` |
| 51.08 Admin API/CLI | Done | #608; `api/components_cli.go`, `components_cli_test.go`; `api/internal/handlers/components/components_test.go` |
| 51.09 Capabilities | Done | #608; `api/internal/components/features.go`, `features_test.go`; `handlers/components/capabilities_test.go` |
| 51.10 Components page | Done | #608; `web/src/pages/Settings/ComponentsSection.tsx`, `ComponentsSection.test.tsx` |
| 51.11 Clients | Done | #608, #1042; `apps/apple/MaktabaKit/Sources/MaktabaKit/Capabilities.swift`; Android `apps/android/client/src/test/.../CapabilitiesTest.kt`; web `lib/capabilities.test.tsx` |
| 51.12 Per-machine controls | Done | #608; `api/internal/handlers/components/machines.go`, `machines_test.go`, `machines_pg_test.go`; `api/components_cli_machine_test.go` |
| 51.13 Settings | Done | #608; `api/internal/components/settings.go`; `api/internal/handlers/components/settings_test.go`; `api/components_cli_settings_test.go` |
| 51.14 Health | Done | #608; `api/internal/components/health.go`, `health_test.go`, `health_pg_test.go`; queue health tests |
| 51.15 Packs/index | Done | #608; `api/internal/components/packs.go`; `api/internal/handlers/components/packs_test.go`; `.github/workflows/_components-packs.yml` |
| 51.16 Install/remove | Done | #608; `api/internal/handlers/components/packs.go`, `packs_test.go`; `api/components_cli_packs_test.go` |
| 51.17 Bundles | Done | #608; `api/internal/components/bundle.go`; `shared/components/bundles.go`, `bundles_test.go`; `handlers/components/bundle_test.go` |
| 51.18 Update rehearsal | Done | #608; `api/rehearsal_packs_e2e_test.go`, `api/rehearsal_components_e2e_test.go`; `.github/workflows/update-rehearsal.yml` scenarios 13/14 |
| 51.19 Licence gating | Done | #608; `api/internal/components/licence.go`, `licence_test.go`, `licence_pg_test.go`; `shared/components/runner/licence_test.go` |

### Epic 52 — Transcript word review

Main implementation PR **#672**. Current files confirm the broad implementation, but #672 explicitly omitted real gold data and UI/E2E tests. Later code resolves 52.17: `api/rehearsal_components_e2e_test.go` scenario 12 checks new components against the bundle, is wired in `update-rehearsal.yml`, and was hardened by #748.

| Story | Status | Evidence / missing work |
|---|---|---|
| 52.01 Word evidence | Done | #672; `pipeline/src/maktaba_pipeline/stt/word_data.py`; `api/internal/transcript/worddata/worddata.go`, `worddata_test.go`; shared golden fixtures |
| 52.02 Evaluation set | **partial** | #672; `pipeline/src/maktaba_pipeline/word_review/eval/` and `pipeline/tests/word_review/eval/test_eval.py` exist. `pipeline/tests/fixtures/word_review/gold/README.md` says the CI set is **synthetic**. Missing the specified human-corrected 3h Arabic/1h English/Quran/code-switch/hearing-spot full gold set and its fitted real-data calibration. |
| 52.03 Language-pack contract | Done | #672; `pipeline/src/maktaba_pipeline/language_packs/base.py`, `registry.py`, `conformance.py`; conformance/unit tests |
| 52.04 Arabic pack | Done | #672; `language_packs/ar/pack.py`, `morphology.py`, `dialect.py`; pack tests |
| 52.05 English pack | Done | #672; `language_packs/en/pack.py`; dictionary/transliteration tests |
| 52.06 Word-check stage | Done | #672; `pipeline/src/maktaba_pipeline/engine/stages/word_check.py`; `pipeline/tests/stages/test_word_check.py` |
| 52.07 LLM judge | **deferred by design (removed)** | The story is explicitly marked **REMOVED 2026-11-03** and says nothing from it is built. No judge implementation is intended; this is not counted as done work. |
| 52.08 Review API | Done | #672; `api/internal/handlers/review/review.go`, `review_pg_test.go`, `review_roles_test.go`; `routes.go` |
| 52.09 Apply fixes | Done | #672; `api/internal/transcript/edit.go`, `edit_pg_test.go`; review resolve/undo tests |
| 52.10 Vocabulary | Done | #672; `api/internal/handlers/review/learn.go`; `pipeline/tests/word_review/test_vocabulary.py`; web vocabulary tests |
| 52.11 Web review queue | **partial** | #672; `web/src/pages/review/Review.tsx`, `Review.test.tsx`, `web/src/lib/reviewApi.ts` exist. Missing the specified Playwright branch-stack queue flow (clip/correct/undo/bulk/Arabic/dark screenshots); no corresponding `web/e2e` review spec exists. Unit/component tests do not replace it. |
| 52.12 Fix from transcript | Done | #672; `web/src/components/transcript/WordPicker.test.tsx`; API `FixWord` and edit tests |
| 52.13 Native review | **partial** | #672; `apps/apple/Remote/ReviewView.swift`, `apps/android/remote/.../ReviewScreens.kt`; MaktabaKit and Android API unit tests exist. Missing the specified Apple UI flow and Android Compose/Robolectric screen tests (current Android remote tests only cover attention wording/API logic); real clip UI paths therefore remain unproven. |
| 52.14 Settings/health | Done | #672; `api/internal/handlers/review/routes.go`; web settings/queue tests; component health wiring |
| 52.15 Quran check | Done | #672; `pipeline/src/maktaba_pipeline/language_packs/ar/quran.py`; Tanzil data/license and tests |
| 52.16 Language components | Done | #672; `pipeline/src/maktaba_pipeline/components/catalog.toml`, `packs.py`; pinned recipes and component tests |
| 52.17 New-component defaults | Done | #672, #748; `cmd/maktaba-server/internal/componentseed/seed.go`, `seednew_test.go`; `shared/components/store/seed.go`; rehearsal scenario 12 in `api/rehearsal_components_e2e_test.go` and CI |

### Epic 53 — Logs, errors and reports

All 18 stories have implementation and test evidence. Open issue **#1064** reports that nightly log-bench costs grew beyond 20%; the benchmark and regression mechanism exist, so 53.13 remains done with an open performance issue, not an unimplemented story.

| Story | Status | Evidence / missing work |
|---|---|---|
| 53.01 Log schema/redaction | Done | #664; `shared/log/go/rules.go`, `shared_cases_test.go`; Python/Swift/Kotlin/Rust redaction tests |
| 53.02 Log keeper | Done | #673, #891; `shared/logkeeper/go/keeper.go`, `keeper_test.go`, `segment.go` |
| 53.03 Status endpoint | Done | #673; `cmd/maktaba-server/internal/supervisor/status.go`, `status_test.go`, `statusmap_catalog_test.go` |
| 53.04 Recovery console | Done | #684; `cmd/maktaba-server/internal/recovery/handler.go`, `recovery_test.go` |
| 53.05 Safe mode | Done | #684; `cmd/maktaba-server/internal/supervisor/safemode.go`, `safemode_test.go`; launcher `cantstart_test.go` |
| 53.06 Status strip | Done | #685; `web/src/components/StatusStrip.test.tsx`; Apple/Android status tests |
| 53.07 Fault rehearsal | Done | #685; `api/rehearsal_faults_e2e_test.go`; `.github/workflows/update-rehearsal.yml` and fault tag guard |
| 53.08 Request IDs | Done | #686, #888; `api/internal/grpcclients/pipeline/reqid_test.go`, `api/internal/grpcclients/streaming/reqid_test.go`; `web/src/lib/requestIds.test.ts` |
| 53.09 Log index | Done | #686; `shared/logkeeper/go/logindex/index.go`, `search.go`, `index_test.go` |
| 53.10 Error groups | Done | #687; `shared/logkeeper/go/logindex/errors.go`, `errors_test.go`; `api/internal/handlers/logs/errors_test.go` |
| 53.11 Admin pages | Done | #688; `web/src/pages/Admin/LogSearch.tsx`, `Errors.tsx`; adjacent tests |
| 53.12 Dashboards | Done | #689, #896, #905, #915, #960; `api/internal/handlers/logs/dashboards.go`, `dashboards_test.go`; `web/src/pages/Admin/Dashboards.test.tsx` |
| 53.13 Performance | Done | #690, #740; `cmd/maktaba-server/internal/logbench/bench_test.go`, `calibrate.go`; **open #1064** reports current benchmark growth |
| 53.14 Crash reports | Done | #691, #952; `api/internal/handlers/logs/envelope.go`; `web/src/lib/crashReports.test.ts`; Android tombstone tests |
| 53.15 Report a problem | Done | #691; `api/internal/handlers/logs/reports.go`, `reports_test.go`; `web/src/pages/Profile/ReportProblem.test.tsx` |
| 53.16 Reports to Maktaba | Done | #824, #917, #961, #967; `api/internal/handlers/logs/maktaba.go`, `maktaba_test.go`; `cloud/internal/reports/reports_integration_test.go`; symbolication tests |
| 53.17 Cloud observability | Done | #852; `cloud/internal/logship/logship.go`, `logship_test.go`; `cloud/internal/obs/obs_test.go`; admin logs tests |
| 53.18 Catalog entries | Done | #664/#648 integration; `shared/components/catalog.toml`; `cmd/maktaba-server/internal/supervisor/statusmap_catalog_test.go`; component catalog checks |

### Epic 54 — Plugins

The wiki table omits 54.43–54.52; this audit includes all 52 spec files. Open issue **#804** is stale: scenarios 16.1–16.6 and 16.8–16.9 now exist in `api/rehearsal_waves_e2e_test.go`, 16.7 in `api/rehearsal_wave7_e2e_test.go`, and all are listed in `.github/workflows/update-rehearsal.yml`. Open issue **#876** is likewise superseded for 54.20 by #893/#936 and committed pixel/text snapshots.

| Story | Status | Evidence / missing work |
|---|---|---|
| 54.01 Manifest/package | Done | #683; `shared/plugins/manifest.schema.json`; `plugind/crates/audit/tests/corpus.rs`; shared manifest validators/tests |
| 54.02 Plugin host | Done | #683; `plugind/crates/core/src/` (Wasmtime host); `plugind/crates/core/tests/host.rs` |
| 54.03 OS sandbox/egress | Done | #683, #869/#879; `plugind/crates/plugind/src/sandbox/{macos,linux,windows}.rs`; `tools/ci/tests/test_plugin_probes.py`; Windows host e2e |
| 54.04 Plugin API | Done | #693; `api/internal/plugins/`; invocation/capability/scope tests and shared API corpus |
| 54.05 Media handles | Done | #693; `api/internal/plugins/` media routes; media handle/frame/audio tests |
| 54.06 Queue stages | Done | #693; `pipeline/src/maktaba_pipeline/plugins/dispatch.py`; plugin loader/dispatch tests |
| 54.07 Lifecycle | Done | #707, #795, #799, #814, #818; `api/internal/plugins/lifecycle.go`; `api/rehearsal_plugins_e2e_test.go`; migration/update tests. Open #1172 mentions a possible plugin-error status bug, not a missing lifecycle implementation. |
| 54.08 Grants/roles | Done | #707; `api/internal/plugins/tokens.go`; `api/internal/plugins/scope_routes_pg_test.go` and grant tests; component gates |
| 54.09 Isolation/health | Done | #707, #801; `cmd/maktaba-server/internal/supervisor/plugins.go`; plugin health and supervisor tests |
| 54.10 File install | Done | #717, #810; `api/internal/handlers/plugins/`; install role/policy tests; `api/plugins_cli.go` tests |
| 54.11 Settings as data | Done | #717; plugin schema/settings handlers and web tests |
| 54.12 SDK/harness/docs | Done | #717, #810; `plugind/crates/mkp/`, `plugind/crates/mkp/tests/mkp.rs`, `plugind/crates/mkp/tests/templates.rs`; `tools/plugin-sdk/`; template/build/run tests |
| 54.13 Admin Plugins page | Done | #717; `web/src/pages/Settings/PluginsSection.tsx`, `PluginsSection.test.tsx` |
| 54.14 Signing/trust | Done | #733; `plugind/crates/sig/tests/vectors.rs`; store/home signature and rotation tests |
| 54.15 Store service/accounts/link | Done | #753; `store/`; store account/link tests live under `store/` |
| 54.16 Checks/human review | Done | #770; `store/checks/`, `store/check-runner/`; `.github/workflows/store-check-runner.yml`; review/check tests |
| 54.17 Home catalog/kill switch | Done | #778; `api/internal/plugins/catalog/`; catalog/revocation/update tests |
| 54.18 Store browser | Done | #815, #835, #836, #837; `store/` listing/report routes; web store browser tests |
| 54.19 Paid plugins | Done | #735, #743, #781, #850, #861, #886, #887; `store/commerce/`; `api/internal/plugins/entitlements.go`; payment/entitlement tests |
| 54.20 Panels | Done | #742, #765, #787, #813, #893, #936; `shared/plugins/panel.schema.json`; `apps/apple/MaktabaKit/Sources/MaktabaKit/PluginPanels.swift`; `apps/apple/PlayerMacTests/PageSnapshotTests.swift` with committed PNG/TXT refs; Android Roborazzi and web tests. Issue #876 remains open but its requested Mac-hosted comparison now runs in `_apple-apps.yml`. |
| 54.21 Local compute | Done | #731; `api/internal/plugins/compute.go`, `compute_pg_test.go`; pipeline quota/lease tests |
| 54.22 Trusted manifest/signing | Done | #750; `shared/plugins/manifest.schema.json`; `api/internal/plugins/firstparty/` trust checks; shared validator corpus/tests |
| 54.23 Native runner | Done | #750, #956, #964–#1019 follow-ups; `pipeline/src/maktaba_plugin_runner/`; `cmd/maktaba-server/internal/supervisor/runners_windows_e2e_test.go`; sandbox/protocol/crash tests. GPU-in-AppContainer on real Windows hardware is unverified. |
| 54.24 Statement registry/roles | Done | #750; `api/internal/plugins/statements/`; `tools/plugins/statements_build.py`, `roles.py`; Postgres role/scope/rollback tests |
| 54.25 Graph/stage switch | Done | #750; `pipeline/src/maktaba_pipeline/engine/assemble.py`; `shared/plugins/graph.golden.json`; `api/internal/handlers/queues/impl_pg_test.go` |
| 54.26 Parity proof | Done | #750; `tools/plugins/parity.py`; `plugins/sdk/python/maktaba_plugin_sdk/parity.py`; first-party `test_*_parity.py` suites and self-tests |
| 54.27 Layout/packaging | Done | #750, #1440; `tools/plugins/build.py` builds one `.mkplugin` per first-party plugin with its `files.json`, both in the release's signed `checksums.txt`; `release.yml` `package-plugins` and the `fp-packages` check after signing; `api/internal/plugins/firstparty/release_test.go`, `pipeline/tests/plugins/firstparty/test_build_packages.py`; T01 dry run in `_plugins.yml` `first-party`. The payload does not carry the packages (nothing on a home reads them before 54.45/54.46; the spec's As built says why). |
| 54.28 Wave 1 classify leaves | Done | #760, #925; `plugins/first-party/classify/manifest.toml`, `plugins/first-party/classify/tests/test_classify_parity.py`; `api/rehearsal_waves_e2e_test.go` scenario 16.1. `display_title` followed in wave 4 as the spec records. |
| 54.29 Wave 2 thumbnails/subtitles | Done | #773; `plugins/first-party/thumbnails/`, `subtitles/`; parity tests; rehearsal 16.2 |
| 54.30 Wave 3 transcription/search | Done | #789; `plugins/first-party/transcription/`, `semantic-search/`; parity tests; rehearsal 16.3 |
| 54.31 Wave 4 classify/enrich | **partial** | #925; six classify stages, model forwarding and rehearsal 16.4 exist in `plugins/first-party/classify/`. **`enrich` remains in `pipeline/src/maktaba_pipeline/engine/stages/enrich.py`; `plugins/first-party/enrich/` does not exist.** The spec says moving it needs an owner decision about arbitrary page reads/secrets. T01/T03/T06 therefore cover only six of seven stages. |
| 54.32 Wave 5 file actions | Done | #800, #890; `plugins/first-party/convert/`, `dedup/`; parity and job-scoped path tests; rehearsal 16.5 |
| 54.33 Wave 6 YouTube/repair | Done | #806, #899; `plugins/first-party/youtube/`, `repair/`; parity tests; rehearsal 16.6; nightly host check. Open **#900** (yt-dlp JS runtime/degraded extraction) and **#772** (one unreproduced test failure) remain relevant defects/risks. |
| 54.34 Wave 7 live TV | Done | #823; `plugins/first-party/livetv/`, `livetv-slots/`; parity tests; `api/rehearsal_wave7_e2e_test.go` scenario 16.7 |
| 54.35 Wave 8 Plex/dizqueTV | **removed by #969** | #844 originally landed the wave; only `plex_livetv` remains in `plugins/first-party/plex/manifest.toml` with parity coverage. Merged **#969** removed Plex sync/collections and the entire dizqueTV plugin (`engine/retired.py` lists those stages). The story's original full Plex/dizqueTV deliverable is therefore not present on current main. |
| 54.36 Wave 9 core/removal | **partial** | #998, #1078; `plugins/first-party/core-media/manifest.toml`, `plugins/first-party/core-media/tests/test_core_media_parity.py`; rehearsal 16.9 now exists. Section 4's removal has not happened: in-process stages, the per-stage switch and `job_stages.impl` remain, with explicit preconditions (unconverted stages, SQLite, Windows shares, Chroma, and a fallback-free release). `categorize` also remains pending a decision. |
| 54.37 Runtime catalog entries | Done | #707; `shared/components/plugins.go`, `plugins_golden_test.go`; Python/Go shared vectors |
| 54.38 Plugin files | Done | #707; plugin unpack/storage code; hostile tar/path tests |
| 54.39 Flows/graph checks | Done | #759; `shared/plugins/pluginflow/`; Go/TypeScript/Python flow corpus and cycle tests |
| 54.40 Events/lineage | Done | #762; plugin event publish/subscribe and lineage tests |
| 54.41 Flow chart | Done | #763, #835; `tools/plugin-sdk/js/src/flowchart.ts`, `test/flowchart.test.ts`; store/home chart tests |
| 54.42 Joined machines | Done | #767; cluster plugin distribution/runner tests |
| 54.43 Fallback counts | Done | #1080; `shared/db/migrations/0218_stage_fallbacks.sql`; `api/internal/handlers/queues/fallbacks_pg_test.go`; `web/src/components/queue/FallbackCounts.test.tsx` |
| 54.44 One Plugins page | Done | #1243; `api/internal/plugins/published.go`, `published_pg_test.go`; `api/internal/handlers/plugins/published_pg_test.go`; `web/src/pages/Settings/PluginPage.tsx` and tests |
| 54.45 Maktaba store packages | **partial** | #1450; `shared/plugins/fppackage.CheckStore` (store countersigned, Maktaba's pinned key in `shared/plugins/maktaba-keys.json`, statements exactly the server's, as decided), `build.py --developer-key`, core not packaged; store: the Maktaba publisher (`POST /v1/admin/maktaba-publisher`), `prefixCovers`, trusted manifests and native-package checks. **Missing:** a store or file package installed and run with parity, which waits for an install path (54.46/54.49) and Maktaba's key. |
| 54.46 Maktaba plugin lifecycle | **partial** | #1452; `DELETE /api/plugins/{id}` (want absent, `component_required_by`, jobs parked, `plugin_data_kept`), `POST /api/plugins/{id}/install` from the release; the Plugins page's Remove and Install; rehearsal 15 keeps a removed plugin removed across a rollback and an update. **Missing:** installing a Maktaba plugin from a store package or a file (§2), which comes with 54.49's offline files and Maktaba's key. |
| 54.47 Maktaba plugin updates | **not started** | Spec says **Not built**; independent plugin update channel absent |
| 54.48 Preinstalled/migration | **not started** | Spec says **Not built**; package-based preinstall/migration absent |
| 54.49 Offline homes | **not started** | Spec says **Not built**; downloadable offline first-party packages absent |
| 54.50 Release variants | **not started** | Spec says **Not built**; Full/Core release-variant production path absent |
| 54.51 Variant updates | **not started** | Spec says **Not built**; update/migration between package-based variants absent |
| 54.52 Process CPU | Done | #1443; `cmd/maktaba-server/internal/hostinfo/cpu_*.go` (macOS, Linux, Windows); the reconciler's `cpu_pct`/`cpu_parts` beside `rss_mb`/`rss_parts` with hysteresis (`internal/supervisor/cpu_test.go`); `GET /api/plugins` `cpu_pct` (`api/internal/plugins/measure_test.go`, `published_pg_test.go`); the Plugins page's Processor column (`PluginsSection.test.tsx` T08) |

### Epic 55 — Desktop player parity

All ten stories landed in **#667** (after spec #652). Opened the desktop player and shared `:client` source/tests. JavaFX toolkit playback behavior is covered at the unit/menu/model level; full real-window playback is not independently verified here.

| Story | Status | Evidence / missing work |
|---|---|---|
| 55.01 Shell/sharing | Done | #667; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/`; shared `apps/android/client/` code and tests |
| 55.02 Capabilities gate | Done | #667; desktop capability/API client; shared capability tests |
| 55.03 Home | Done | #667; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/browse/HomeRows.kt`; `.../browse/HomeRowsTest.kt` |
| 55.04 Search | Done | #667; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/search/SearchScreen.kt`; `apps/desktop-player/src/test/.../search/SearchScreenTest.kt` |
| 55.05 Libraries | Done | #667; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/libraries/LibraryPage.kt`; library tests |
| 55.06 Series/video | Done | #667; desktop library/video pages and tests |
| 55.07 Live TV | Done | #667; `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/live/GuideWindow.kt`; `GuideWindowTest.kt` |
| 55.08 Watch | Done | #667; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/Playback.kt`; `apps/desktop-player/src/test/.../watch/HlsTest.kt`, `VttTest.kt`, `UpNextStateTest.kt` |
| 55.09 Profile | Done | #667; `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/profile/ProfilePages.kt`; profile tests |
| 55.10 Help/release | Done | #667; desktop help/release wiring and CI workflow `_desktop-player.yml` |

### Epic 56 — TV library view

Main implementation **#669** (spec #651), with big-screen integration #1234. Current API/UI code and Postgres/native/web tests exist for all stories, but 56.07's import engine was deliberately removed later and is not current functionality.

| Story | Status | Evidence / missing work |
|---|---|---|
| 56.01 Play queue | Done | #669; `api/internal/handlers/playqueue/playqueue.go`, `chunks.go`; `api/internal/router/playqueue_pg_test.go` |
| 56.02 TV Play/Shuffle | Done | #669; play-queue sources and native TV tests |
| 56.03 TV library tabs | Done | #669; `apps/android/player/src/test/.../libraries/QueueTabsTest.kt`; `web/src/bigscreen/Epic56.test.tsx` |
| 56.04 Series grouping | Done | #669; `api/internal/handlers/videos/group.go`; `api/internal/router/epic56_pg_test.go` |
| 56.05 Sort/filter/categories | Done | #669; browse routes/tests; TV category tests |
| 56.06 YouTube collections | Done | #669; `pipeline/src/maktaba_pipeline/engine/sourced_collections.py`; sourced-collection tests |
| 56.07 Plex collections | **removed by #969** | #669 originally added `plex_collections_import`, admin counts and tests. Merged **#969** removed the import stage and `/plex-missing`; `api/internal/handlers/libraries/manage_test.go` now asserts no `plex_collections` answer. Only rendering legacy `source_kind='plex'` rows remains. The specified ongoing Plex import/synchronization is absent. |
| 56.08 TV collections | Done | #669, #1234; `web/src/bigscreen/screens/Collection.tsx`; native/web collection tests |
| 56.09 Smart playlists | Done | #669; `api/internal/handlers/browse/smartplaylists.go`; Epic56 PG tests |
| 56.10 TV playlists | Done | #669; playlist APIs and TV tests |
| 56.11 Continue/Up Next | Done | #669; play-queue ordering and TV continue tests |
| 56.12 Players parity | Done | #669; Android/Apple TV shared views/tests |
| 56.13 Web parity | Done | #669, #1234; `web/src/bigscreen/Library*` and Epic56 tests |
| 56.14 OpenAPI/roles | Done | #669; `api/internal/router/openapi_coverage_test.go`; role/OpenAPI tests and current shared documents |

### Epic 57 — Parental controls

Main implementation **#1204**, with spec revisions #979/#1128/#1130 and later fixes. The code is broad and heavily tested, but the epic is not fully done: a performance acceptance target is unmet, the Apple age-range capability is absent, Family notification opt-in is absent, and 57.16 waits for Apple.

| Story | Status | Evidence / missing work |
|---|---|---|
| 57.01 Age tiers | Done | #1204; `api/internal/handlers/parental/tiers.go`, `tiers_roles_test.go`; pipeline/content tier tests |
| 57.02 Restricted accounts | Done | #1204; `api/internal/handlers/parental/accounts.go`, `accounts_pg_test.go`; web Family tests |
| 57.03 Server filter | **partial (acceptance target unmet)** | #1204; `api/internal/parental/filter.go`, `surfaces.go`; `api/internal/router/p57_bypass_pg_test.go` and role/filter tests exist. `p57_filter_bench_test.go` only reports, does not assert, T12; the spec records restricted Home measured **over the 60 ms p95 target**. Functional filtering landed, performance AC remains open. |
| 57.04 Device signal | **partial** | #1204; `api/internal/handlers/parental/signal.go`, `signal_pg_test.go`; `apps/apple/Player/Parental/DeviceAgeSignal.swift`; Apple tests exist. No iPhone/iPad entitlements files/`com.apple.developer.declared-age-range` entry exist; current code says no age range is sent until App IDs get it. Real-child-device T09 is also unverified. Screen Time ceiling reading and server ratchet are implemented. |
| 57.05 Restricted player | Done | #1204; Apple/Android/desktop restricted player code and UI/unit tests |
| 57.06 Ask a parent | Done | #1204; `api/internal/handlers/parental/requests.go`, `requests_pg_test.go`, `requests_own_pg_test.go`; app/web ask/PIN tests. Server-side guardian notices exist (`Notify`). |
| 57.07 Family settings | **partial** | #1204; `apps/apple/Player/Family/FamilyViews.swift`, `TimeAndActivityViews.swift`; web `/family`; `PlayerParentalUITests.swift`. The required Notifications control is not present in Family source (only strings/server notification categories); #1204 also records the iPad split view as not built. Android/desktop guardian screens are explicitly outside this story's built scope, not counted as omissions. |
| 57.08 Activity | Done | #1204; `api/internal/parental/events.go`; `api/internal/handlers/parental/activity.go`; retention/role tests and Apple Activity UI tests |
| 57.09 Screen Time/Family Sharing/App Store | Done (manual T04 unverified) | #1204; Player background/progress handling, `PlayerParentalUITests.swift`, Help and `docs/testflight.md`. Real-iPad Downtime check is documented as not run; this does not remove the code/tests but remains manual verification debt. |
| 57.10 Live TV controls | Done | #1204; `api/internal/handlers/parental/livetv.go`; streaming live guard tests; OpenAPI. Open **#1227** is a clock-dependent test bug around midnight, not missing functionality. |
| 57.11 Apple TV | Done | #1204; tvOS parental code and `apps/tv/tvos/MaktabaTVUITests/` |
| 57.12 Existing homes | Done | #1204; migration/settings/notice code and tests |
| 57.13 OpenAPI/roles/rehearsal | Done | #1204, #1321/#1342 related TV additions; `api/internal/router/p57_bypass_pg_test.go`, role table and CI workflow |
| 57.14 Time rules | Done | #1204; `api/internal/handlers/parental/time.go`, `time_pg_test.go`; streaming lease/time tests |
| 57.15 Time on clients | Done | #1204, #1357; web/Apple/Android/desktop/Roku time screens and tests |
| 57.16 Home rules through Screen Time | **deferred by design** | Spec explicitly says **not built** pending Apple's Family Controls entitlement and a real-device feasibility check. Home enforcement in 57.14/57.15 works independently. No DeviceActivity/shield implementation is claimed. |
| 57.17 Detected tiers | Done | #1204; `api/internal/handlers/parental/review.go`; `pipeline/src/maktaba_pipeline/engine/stages/content_screen.py`; review/API/pipeline tests |

### Epic 58 — Smart TV platforms

Seventeen stories have code and automated tests. 58.18 is deliberately waiting. Real-device playback, store review/submission, signing credentials, and production review-home availability are **unverified** in this repository audit; those manual/external checks are called out on the relevant rows rather than treated as passing.

| Story | Status | Evidence / missing work |
|---|---|---|
| 58.01 Compatibility contract | Done | #1235, #1244; `api/internal/compat/compat.go`, `levelcheck/levelcheck.go`; `levelcheck_test.go`, `api/internal/router/compat_floor_pg_test.go`; app update screens/tests |
| 58.02 Device playback caps | Done | #1239; `streaming/internal/capability/devicecaps.go`; `capability/tvprofiles_test.go`, `grpcsrv/devicecaps_test.go`, `handlers/remux_realffmpeg_test.go`; shared vectors |
| 58.03 Cloud TV code | Done | #1240, #1250, #1325, #1356; `cloud/internal/handlers/tvcodes/handler.go`, `handler_test.go`; `api/internal/router/p58_cloudcode_pg_test.go`; native approval/code tests |
| 58.04 Store-review home | Done | #1293; `api/internal/reviewhome/seed.go`, `seed_pg_test.go`; `tools/review_home/probe.py`; `tools/ci/tests/test_review_home_probe.py` |
| 58.05 Fire TV | Done (device checks unverified) | #1268, #1356; `apps/tv/android/app/src/androidTestAmazon/.../FireTvJourneyTest.kt`; `TvDeviceCapsTest.kt`; `tools/release/amazon_submit.py`, `tools/ci/tests/test_amazon_submit.py`; release flavor/workflow. Owner real-device checklist remains external. |
| 58.06 Cast senders | Done | #1286, #1287, #1292; `web/src/lib/cast/sender.ts`, `sender.test.ts`; Apple `PlayerCastUITests.swift`; Android Cast tests |
| 58.07 Cast receiver | Done | #1261, #1267; `api/internal/handlers/streaming/castgrant_test.go`; `web/packages/castreceiver/src/receiver/receiver.ts`; `test/receiver.test.ts`, `e2e/receiver.spec.ts` |
| 58.08 TV web UI | Done | #1234, #1247; `web/src/bigscreen/screens/Library.tsx`; `web/src/bigscreen/Epic56.test.tsx`; `/tv/` build and TV tests |
| 58.09 TV web launcher | Done | #1277, #1348; `apps/tv/web/src/app.ts`, `bridge.ts`, `bridge.test.ts`; `web/e2e/launcher/launcher.ui.spec.ts`; cloud-code part 2 landed |
| 58.10 Tizen/Samsung | Done (device/store checks unverified) | #1291; `apps/tv/web/src/platform/tizen/avplay.ts`, `avplay.test.ts`; `web/e2e/launcher/tizen-avplay.ui.spec.ts`; package/sign/lint and release workflow. Emulator intentionally absent per spec; real TVs/store submission not verified. |
| 58.11 webOS/LG | Done (device/store checks unverified) | #1290; `apps/tv/web/src/platform/webos-player.ts`, `webos.test.ts`; `web/e2e/launcher/webos.ui.spec.ts`; `.ipk` lint/build/release. Simulator intentionally absent per spec. |
| 58.12 Roku channel | Done | #1307, #1335; `apps/tv/roku/components/screens/LibraryScreen.brs`; `apps/tv/roku/tests/screens.test.mjs`, `channel_test.brs`; QR follow-up landed |
| 58.13 Roku player/certification | Done (device/certification unverified) | #1313; `apps/tv/roku/components/screens/PlayerScreen.brs`; `apps/tv/roku/tests/player_test.brs`, `player.test.mjs`; release workflow and checklist. Instant Resume intentionally not built unless its certification threshold applies. |
| 58.14 Parental controls on TVs | Done | #1321, #1331, #1350, #1357; `web/src/bigscreen/screens/Blocked.tsx`, `Blocked.test.tsx`; Roku/Android TV parental tests; bypass rows A23–A27 |
| 58.15 Strings/Arabic RTL | Done | #1233, #1288, #1322, #1347, #1351; `shared/i18n/visual_order.mjs`; Roku shaping/catalog tests; TV Playwright tests. Open **#1199** documents Arabic digits/subtitle defects in existing Android/TV captures and affects 60.09's imagery. |
| 58.16 Per-platform CI | Done | #1233, #1322; `.github/workflows/_tv-web.yml`, `_roku.yml`, `_android-tv.yml`; `tools/ci/tests/test_tv_checklist_guard.py`; emulator omissions are documented design decisions |
| 58.17 OpenAPI/roles/rehearsal | Done | #1342; `api/internal/router/p58_tvclients_pg_test.go`, `p58_roles_pg_test.go`, `p58_roles_routes_test.go`; CI job |
| 58.18 Vega OS | **deferred by design** | Spec explicitly waits for Vega ≥25% of Fire TV sign-ins or Amazon stopping Fire OS sticks in a listed market. No `apps/tv/vega` app is intended before then. The required interim Vega message is in the website/app guidance; actual market trigger is unverified. |

### Epic 59 — NAS

All 16 stories have current code/config/tests. Real Synology, TrueNAS, Unraid, ARM, VAAPI, and model-performance outcomes are not reproduced by this audit.

| Story | Status | Evidence / missing work |
|---|---|---|
| 59.01 Image any user | Done | #1090, #1100; `cmd/maktaba-server/container_preflight.go`, `container_preflight_test.go`; `tools/release/linux-app/docker-nas.sh`; NAS CI cells |
| 59.02 Data/database/quiet hours | Done | #1096, #1103; `pipeline/src/maktaba_pipeline/engine/quiet.py`; `pipeline/tests/engine/test_quiet_hours.py`; `cmd/maktaba-server/internal/pgbundle/tuning_test.go` |
| 59.03 Media permissions | Done | #1092; `shared/go/mediaaccess/mediaaccess.go`, `mediaaccess_test.go`; `api/internal/handlers/libraries/access_test.go`; read-only pipeline tests |
| 59.04 Hardware tiers | Done | #1089; `cmd/maktaba-server/internal/hostinfo/machine.go`, `machine_test.go`; `web/src/pages/Settings/ThisMachineSection.test.tsx` |
| 59.05 VAAPI | Done | #1110, #1111, #1134; `streaming/internal/ffmpeg/vaapi.go`, `vaapi_test.go`; transcode/busy tests |
| 59.06 STT on NAS | Done | #1112; `pipeline/src/maktaba_pipeline/stt/benchmark.py`; `pipeline/tests/stt/test_benchmark.py`; speech settings tests |
| 59.07 Plugin container isolation | Done | #1109; `api/internal/plugins/isolation.go`; `cmd/maktaba-server/internal/plugins/isolation_test.go`; `api/internal/handlers/plugins/isolation_pg_test.go` |
| 59.08 Networking | Done | #1119, #1121, #1224; `shared/go/clientip/clientip.go`, `clientip_test.go`; `cmd/maktaba-server/serve_network_test.go`; custom-port cells |
| 59.09 Synology | Done | #1120; `web/packages/install/src/synology.ts`, `synology.test.ts`; `deploy/nas/generate.py`; generated project/guide tests |
| 59.10 TrueNAS | Done | #1118; `deploy/nas/truenas/custom-app.yaml`; `tools/release/truenas_app.py`; `tools/ci/tests/test_truenas_app.py` |
| 59.11 Other NAS/Unraid | Done | #1117; `cloud/installers/docker/docker-compose.yml`; `web/packages/install/src/compose.ts`, `compose.test.ts`; install page tests |
| 59.12 Updates | Done | #1138; `api/internal/system/container_image.go`, `container_image_test.go`; NAS container rehearsal/workflow |
| 59.13 Cluster/NAS home | Done | #1140; `pipeline/src/maktaba_pipeline/sharepath/nashome.py`; `pipeline/tests/sharepath/test_nashome.py`; `api/internal/handlers/cluster/homeshares_test.go` |
| 59.14 Strings/guides | Done | #1137; `web/packages/install/src/nasPage.ts`; `twins.test.ts`, `e2e/install.ui.spec.ts`; `web/src/i18n/epic59.test.ts` |
| 59.15 CI | Done | #1091, #1100, #1103; `.github/workflows/nas.yml`; NAS shape/container cells and supporting tests |
| 59.16 OpenAPI/roles | Done | #1142; `api/internal/router/openapi_roles_test.go`; current OpenAPI docs and route walk |

### Epic 60 — Website

The site, build/deploy tooling, quality gates, content, and screenshots exist. 60.09 is partial because required captures are knowingly omitted; 60.13 is gated on undecided pricing and correctly has no page.

| Story | Status | Evidence / missing work |
|---|---|---|
| 60.01 Design | Done | #1163, #1187, #1192; `web/packages/website/design/render.mjs`, `mockups/make.mjs`, `mockups/part-b.mjs`; design contrast/review artifacts |
| 60.02 Voice/copy | Done | #1163, #1187; `web/packages/website/design/` voice/copy guide and home/download content; page content tests |
| 60.03 Site hosting | Done (live deploy unverified) | #1161; `web/packages/website/scripts/build.mjs`, `src/worker.test.ts`; `tools/cloudflare/website-deploy.mjs`, `website-deploy.test.mjs`; gated deploy workflow |
| 60.04 Quality gates | Done | #1160, #1171; `.github/workflows/_website.yml`, `website-nightly.yml`; `tools/ci/tests/test_website_deploy.py`; Lighthouse/axe/link/visual checks |
| 60.05 Install guides | Done | #1175; `web/packages/website/scripts/build.mjs`; install chooser/guides and build/e2e tests |
| 60.06 Problems | Done | #1162, #1180; `api/internal/httperror/registry.go`, `registry_test.go`, `literal_test.go`; website problem pages/tests. Open **#1172** reports wrong statuses for some registered types and a possible plugin-refusal 500; registry/site deliverables exist despite that defect. |
| 60.07 Legal | Done | #1175; `web/packages/website/scripts/legal.mjs`, `legal.test.mjs`; `e2e/legal.spec.ts`; copyright/privacy generation |
| 60.08 Link integrity | Done | #1160; `web/packages/website/scripts/links.mjs`, `links.test.mjs`; CI link and public-image checks |
| 60.09 Product imagery | **partial** | #1186; `tests/fixtures/demo-library/`, `web/packages/website/scripts/shots.mjs`, `shots.test.mjs`, `shots-manifest.json`, real WebP/AVIF shots and app capture tests exist. Missing the iPad Remote capture/CI job. Open **#1198** says Arabic Mac imagery is not shown because of RTL/truncation defects; **#1199** says the Arabic Android TV player shot is withheld. Required “every named app/screen, in the page's language” coverage is not met. |
| 60.10 Home page | Done | #1208, #1215; `web/packages/website/scripts/home.mjs`, `home.test.mjs`; `e2e/home.spec.ts`; bilingual page/tests |
| 60.11 Download page | Done | #1209, #1215/#1219; `web/packages/website/scripts/download.mjs`, `download.test.mjs`; `e2e/download.spec.ts`; release dispatch tests |
| 60.12 Help | Done | #1225; `web/packages/website/scripts/help.mjs`, `help.test.mjs`; `e2e/help.spec.ts`; `web/src/lib/desktop.test.ts` |
| 60.13 Pricing | **deferred by design** | Spec is gated on Epic 50 deciding plans/prices and explicitly requires **no `/pricing` page or link** until then. `cloud/internal/billing/plans.go` still says no payment provider is chosen and prices are not decided. No `cloud/cmd/plans-json/main.go` or website pricing page exists; current absence is correct, not an unfinished visible page. |
