# Epic 09 — Library Management

> A library is a named collection of root paths sharing a configuration profile. This epic owns the long-lived behaviors that turn a folder on disk into a curated, browsable catalog: filesystem watching, dedup, auto-categorization, the user-facing organization primitives (collections, smart collections, tags, speakers), library-level lifecycle (create, scan, stats, delete-with-purge), and chapter inference.

- **Spec README:** [`specs/epics/09-library-management/README.md`](../../../specs/epics/09-library-management/README.md)
- **Architecture anchors:** §5 (libraries), §5.1 (watcher), §8 (schema)
- **REST surface owner:** [Epic 07](epic-07-api-server.md) stories 7.3 and 7.14 — this epic implements the *behavior* behind those handlers.
- **Filesystem watcher runtime:** Pipeline Service (Python `watchdog`); this epic owns the rules around debounce, dedup, and ignore.
- **Out of scope:** transcribe / index pipeline stages (Epics 01–06); this epic only triggers them.

## Stories & Plans

| #    | Story                                                        | Plan                                                          | Depends on                  |
|------|--------------------------------------------------------------|---------------------------------------------------------------|------------------------------|
| 9.1  | [Library config schema and validation](../../../specs/epics/09-library-management/story-09-01-library-config-schema.md) | [plan](../../../specs/epics/09-library-management/plan-09-01-library-config-schema.md) | —                           |
| 9.2  | [Filesystem watcher (debounced, settling-aware)](../../../specs/epics/09-library-management/story-09-02-filesystem-watcher.md) | [plan](../../../specs/epics/09-library-management/plan-09-02-filesystem-watcher.md) | 9.1                         |
| 9.3  | [Periodic full sweep (sparse, idempotent)](../../../specs/epics/09-library-management/story-09-03-periodic-sweep.md) | [plan](../../../specs/epics/09-library-management/plan-09-03-periodic-sweep.md) | 9.2                         |
| 9.4  | [Content-hash dedup (move/rename/copy detection)](../../../specs/epics/09-library-management/story-09-04-content-hash-dedup.md) | [plan](../../../specs/epics/09-library-management/plan-09-04-content-hash-dedup.md) | 9.2                         |
| 9.5  | [Ignore rules and supported-extension filtering](../../../specs/epics/09-library-management/story-09-05-ignore-rules.md) | [plan](../../../specs/epics/09-library-management/plan-09-05-ignore-rules.md) | 9.2                         |
| 9.6  | [Manual scan trigger and scan progress](../../../specs/epics/09-library-management/story-09-06-manual-scan.md) | [plan](../../../specs/epics/09-library-management/plan-09-06-manual-scan.md) | 9.2, 7.3                    |
| 9.7  | [Library stats query](../../../specs/epics/09-library-management/story-09-07-library-stats.md) | [plan](../../../specs/epics/09-library-management/plan-09-07-library-stats.md) | 9.1, 7.3                    |
| 9.8  | [Auto-categorization: language tag](../../../specs/epics/09-library-management/story-09-08-language-tag.md) | [plan](../../../specs/epics/09-library-management/plan-09-08-language-tag.md) | 9.1, transcribe stage       |
| 9.9  | [Auto-categorization: topic tag (k-means recluster)](../../../specs/epics/09-library-management/story-09-09-topic-tag.md) | [plan](../../../specs/epics/09-library-management/plan-09-09-topic-tag.md) | 9.8, embedder               |
| 9.10 | [Auto-categorization: content type classifier](../../../specs/epics/09-library-management/story-09-10-content-type-classifier.md) | [plan](../../../specs/epics/09-library-management/plan-09-10-content-type-classifier.md) | 9.1, probe stage            |
| 9.11 | [Speakers, voiceprints, naming, merge](../../../specs/epics/09-library-management/story-09-11-speakers.md) | [plan](../../../specs/epics/09-library-management/plan-09-11-speakers.md) | 9.1, diarization stage      |
| 9.12 | [Tag CRUD and normalization](../../../specs/epics/09-library-management/story-09-12-tag-crud.md) | [plan](../../../specs/epics/09-library-management/plan-09-12-tag-crud.md) | 9.1                         |
| 9.13 | [Collections (manual, ordered)](../../../specs/epics/09-library-management/story-09-13-collections-manual.md) | [plan](../../../specs/epics/09-library-management/plan-09-13-collections-manual.md) | 9.1                         |
| 9.14 | [Smart collections (saved-search-backed)](../../../specs/epics/09-library-management/story-09-14-smart-collections.md) | [plan](../../../specs/epics/09-library-management/plan-09-14-smart-collections.md) | 9.13, 7.9                   |
| 9.15 | [Library deletion (catalog vs file purge)](../../../specs/epics/09-library-management/story-09-15-library-deletion.md) | [plan](../../../specs/epics/09-library-management/plan-09-15-library-deletion.md) | 9.1                         |
| 9.16 | [Multi-root and overlap detection](../../../specs/epics/09-library-management/story-09-16-multi-root-overlap.md) | [plan](../../../specs/epics/09-library-management/plan-09-16-multi-root-overlap.md) | 9.1                         |
| 9.17 | [Library audit log](../../../specs/epics/09-library-management/story-09-17-library-audit.md) | [plan](../../../specs/epics/09-library-management/plan-09-17-library-audit.md) | 9.1                         |
| 9.18 | [Chapter inference from transcript topic shifts](../../../specs/epics/09-library-management/story-09-18-chapter-inference.md) | [plan](../../../specs/epics/09-library-management/plan-09-18-chapter-inference.md) | 9.9, indexer                |

## DB tables owned

| Table                   | Story | Purpose                                                                          |
|-------------------------|-------|----------------------------------------------------------------------------------|
| `library_topics`        | 9.9   | Per-library topic centroids (k-means recluster), with label and video count.     |
| `video_topics`          | 9.9   | Per-video topic membership scores; FK back to `library_topics`.                  |
| `media_features`        | 9.10  | Per-video probe-derived signals (music/speech ratio, silence%, LUFS, density).   |
| `library_sweeps`        | 9.3   | One row per sweep run; counts new/moved/removed videos and any error JSON.       |
| `library_stats_cache`   | 9.7   | Trigger-maintained cache backing the < 50 ms `GET /libraries/{id}/stats` target. |
| `audit_log`             | 9.17 (jointly with 10.16) | Canonical append-only audit table partitioned by month; `category` distinguishes `library` from `security`. |

> See [`specs/epics/09-library-management/README.md`](../../../specs/epics/09-library-management/README.md#schema-additions-owned-by-this-epic) for full DDL.

## API endpoints (REST surface)

The REST surface is owned by [Epic 07](epic-07-api-server.md). This epic implements the behavior behind:

| Endpoint                              | Behavior story                                  |
|---------------------------------------|-------------------------------------------------|
| `GET/POST /libraries`                 | 9.1, 9.16 (overlap rejection)                   |
| `GET/PATCH/DELETE /libraries/{id}`    | 9.1, 9.15 (delete-with-purge)                   |
| `POST /libraries/{id}/scan`           | 9.6 (manual scan)                               |
| `GET /libraries/{id}/stats`           | 9.7 (cache-backed)                              |
| `GET/POST /collections`, `.../{id}`   | 9.13 (manual), 9.14 (smart)                     |
| `GET/POST /tags`                      | 9.12                                            |
| `GET/POST /speakers`, `.../{id}`, `/speakers/merge` | 9.11                              |

## Mockups

| File | Story | Platform | UI states |
|---|---|---|---|
| [`web/mockups/admin/library-config.html`](../../../web/mockups/admin/library-config.html) | 9.1, 9.5, 9.16 | admin (web) | Library form, root paths, ignore rules, overlap warning |
| [`web/mockups/admin/duplicates.html`](../../../web/mockups/admin/duplicates.html) | 9.4 | admin (web) | Dedup review queue (move/copy/rename outcomes) |
| [`web/mockups/admin/speaker-manager.html`](../../../web/mockups/admin/speaker-manager.html) | 9.11 | admin (web) | Modal · rename speaker; Confirm · delete; Confirm · split; Toast · merge complete; Toast · re-cluster started; Toast · merge failed; Dropdown · overflow; Skeleton · loading list; Empty · no speakers; Empty · search no results; Tooltip · confidence score; Inline progress · re-clustering |

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`api-streaming-stories.drawio`](../../../specs/diagrams/api-streaming-stories.drawio) | Story-relationship | Library-management stories grouped with 07/08/10 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | Pipeline / API / Streaming sharing the media volume + Postgres |
| [`data-flow.drawio`](../../../specs/diagrams/data-flow.drawio) | Flow | New file → watcher → dedup → ingest → categorize → index |
| [`entity-relationship.drawio`](../../../specs/diagrams/entity-relationship.drawio) | ER | `libraries → videos → ...`, plus topic and audit tables |
| [`epic-dependencies.drawio`](../../../specs/diagrams/epic-dependencies.drawio) | Story-relationship | Library mgmt → Pipeline / API edges |

## Dependencies on other epics

- **[Epic 07](epic-07-api-server.md):** REST handlers (7.3, 7.14) and saved-search store (7.9, used by smart collections in 9.14).
- **Epic 01 (Scanner) / 03 (Transcription) / 05 (Search):** the Pipeline stages this epic triggers.
- **[Epic 10](epic-10-auth-security.md) story 10.16:** shares the `audit_log` table (this epic owns the schema).
- **Epic 06 (Job Queue):** scans, dedup checks, and sweep finalizers run as queue jobs.

## Key decisions

- **One canonical `audit_log` table.** Three competing audit-table proposals from REVIEW.md were unified: `category='library'` rows are surfaced by 9.17, `category='security'` by [story 10.16](../../../specs/epics/10-auth-security/story-10-16-security-audit.md). Append-only is enforced by triggers; old monthly partitions are detached after `audit_retention_days` (default 365).
- **Library deletion has two modes** (9.15): "catalog only" preserves files on disk; "purge" deletes both. The cascade chain `libraries → videos → streaming_sessions` propagates from a library delete.
- **Filesystem watcher is debounced and settling-aware** (9.2) — large `cp -r`s emit a flurry of events; we wait for size and mtime to stabilize before ingesting.
- **Content-hash dedup** (9.4) uses a partial-content hash (head + tail + length) so a 4 GB file moved between roots doesn't have to be fully re-read.
- **Stats endpoint is cache-backed** (9.7) — trigger-maintained cache table because computing on demand misses the 50 ms target.
- **Smart collections are saved-search-backed** (9.14), not duplicated — a smart collection is a name + saved-search ID + ordering policy.
- **Topic tagging is k-means recluster** (9.9), not fixed taxonomy — labels can be edited but centroids are recomputed on a cadence.

## Sequencing

Land in order: **9.1 → 9.16 → 9.5 → 9.2 → 9.3 → 9.4 → 9.6 → 9.15 → 9.7 → 9.8 → 9.10 → 9.11 → 9.9 → 9.18 → 9.12 → 9.13 → 9.14 → 9.17.**
