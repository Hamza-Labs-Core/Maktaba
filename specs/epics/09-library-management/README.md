# Epic 9 — Library Management

A library is a named collection of root paths sharing a configuration
profile (§5). This epic owns the long-lived behaviors that turn a folder
on disk into a curated, browsable catalog: filesystem watching, dedup,
auto-categorization, the user-facing organization primitives (collections,
smart collections, tags, speakers), library-level lifecycle (create,
scan, stats, delete-with-purge), and chapter inference.

The split with the rest of the platform:

- The **REST surface** for libraries, collections, tags, and speakers
  lives in Epic 7 (Stories 7.3, 7.14). This epic implements the
  *behavior* behind those handlers.
- The **filesystem watcher** is owned by the Pipeline Service (§5.1) — a
  Python `watchdog` observer per library. This epic implements the
  watcher and the rules around debounce, dedup, and ignore.
- The **transcribe / index pipeline stages** are Epics 1–6 in the
  separate doc; this epic only triggers them.
- **Auto-categorization** runs after `INDEXED` and is its own stage in
  the Pipeline.

## Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 9.1   | [Library config schema and validation](story-09-01-library-config-schema.md)                 | —          |
| 9.2   | [Filesystem watcher (debounced, settling-aware)](story-09-02-filesystem-watcher.md)       | 9.1        |
| 9.3   | [Periodic full sweep (sparse, idempotent)](story-09-03-periodic-sweep.md)             | 9.2        |
| 9.4   | [Content-hash dedup (move/rename/copy detection)](story-09-04-content-hash-dedup.md)      | 9.2        |
| 9.5   | [Ignore rules and supported-extension filtering](story-09-05-ignore-rules.md)             | 9.2        |
| 9.6   | [Manual scan trigger and scan progress](story-09-06-manual-scan.md)                | 9.2, 7.3   |
| 9.7   | [Library stats query](story-09-07-library-stats.md)                                  | 9.1, 7.3   |
| 9.8   | [Auto-categorization: language tag](story-09-08-language-tag.md)                    | 9.1, transcribe stage |
| 9.9   | [Auto-categorization: topic tag (k-means recluster)](story-09-09-topic-tag.md)   | 9.8, embedder |
| 9.10  | [Auto-categorization: content type classifier](story-09-10-content-type-classifier.md)         | 9.1, probe stage |
| 9.11  | [Speakers, voiceprints, naming, merge](story-09-11-speakers.md)                 | 9.1, diarization stage |
| 9.12  | [Tag CRUD and normalization](story-09-12-tag-crud.md)                           | 9.1        |
| 9.13  | [Collections (manual, ordered)](story-09-13-collections-manual.md)                        | 9.1        |
| 9.14  | [Smart collections (saved-search-backed)](story-09-14-smart-collections.md)              | 9.13, 7.9  |
| 9.15  | [Library deletion (catalog vs file purge)](story-09-15-library-deletion.md)             | 9.1        |
| 9.16  | [Multi-root and overlap detection](story-09-16-multi-root-overlap.md)                     | 9.1        |
| 9.17  | [Library audit log](story-09-17-library-audit.md)                                    | 9.1        |
| 9.18  | [Chapter inference from transcript topic shifts](story-09-18-chapter-inference.md)             | 9.9, indexer |

## Schema additions owned by this epic

### `library_topics` and `video_topics`

Owned by Story 9.9.

```
CREATE TABLE library_topics (
  library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  topic_id       INTEGER NOT NULL,
  label          TEXT,
  centroid_vec   BYTEA NOT NULL,        -- packed float32[]
  video_count    INTEGER NOT NULL DEFAULT 0,
  computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (library_id, topic_id)
);

CREATE TABLE video_topics (
  video_id       UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
  library_id     UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  topic_id       INTEGER NOT NULL,
  score          REAL NOT NULL,
  PRIMARY KEY (video_id, topic_id),
  FOREIGN KEY (library_id, topic_id) REFERENCES library_topics(library_id, topic_id) ON DELETE CASCADE
);
CREATE INDEX video_topics_topic ON video_topics (library_id, topic_id, score DESC);
```

### `media_features`

Owned by Story 9.10.

```
CREATE TABLE media_features (
  video_id                 UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
  music_speech_ratio       REAL,
  silence_pct              REAL,
  mean_loudness_lufs       REAL,
  diarization_turn_density REAL,
  segment_density          REAL,
  computed_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### `library_sweeps`

Owned by Story 9.3.

```
CREATE TABLE library_sweeps (
  id              UUID PRIMARY KEY,
  library_id      UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ,
  scanned         INTEGER NOT NULL DEFAULT 0,
  new_videos      INTEGER NOT NULL DEFAULT 0,
  moved_videos    INTEGER NOT NULL DEFAULT 0,
  removed_videos  INTEGER NOT NULL DEFAULT 0,
  errors_jsonb    JSONB
);
CREATE INDEX library_sweeps_lookup ON library_sweeps (library_id, started_at DESC);
```

### `library_stats_cache`

Owned by Story 9.7. Backs the < 50 ms `GET /api/libraries/{id}/stats`
target. Updated by triggers on `videos`, `processing_jobs`, and the
sweep finalizer.

```
CREATE TABLE library_stats_cache (
  library_id          UUID PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
  total_videos        INTEGER NOT NULL DEFAULT 0,
  total_duration_sec  BIGINT NOT NULL DEFAULT 0,
  source_size_bytes   BIGINT NOT NULL DEFAULT 0,
  derived_size_bytes  BIGINT NOT NULL DEFAULT 0,
  by_state_jsonb      JSONB NOT NULL DEFAULT '{}'::jsonb,
  by_language_jsonb   JSONB NOT NULL DEFAULT '{}'::jsonb,
  by_content_type_jsonb JSONB NOT NULL DEFAULT '{}'::jsonb,
  jobs_jsonb          JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### `audit_log` (canonical audit table)

Owned by Story 9.17 (jointly with Epic 10 Story 10.16). The three
audit-table proposals from REVIEW.md §1.1.f are unified into a single
`audit_log` table with a `category` column that distinguishes
`library`, `security`, and any future categories.

```
CREATE TABLE audit_log (
  id              UUID PRIMARY KEY,                     -- v7 (so ordered)
  ts              TIMESTAMPTZ NOT NULL DEFAULT now(),
  category        TEXT NOT NULL,                         -- 'library' | 'security' | ...
  event           TEXT NOT NULL,
  actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
  library_id      UUID REFERENCES libraries(id) ON DELETE SET NULL,
  video_id        UUID REFERENCES videos(id) ON DELETE SET NULL,
  ip              INET,
  user_agent      TEXT,
  payload_jsonb   JSONB NOT NULL DEFAULT '{}'::jsonb
) PARTITION BY RANGE (ts);

-- Append-only enforcement
CREATE OR REPLACE FUNCTION audit_log_no_mutation() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'audit_log is append-only'; END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutation();
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_no_mutation();

CREATE INDEX audit_log_lookup ON audit_log (category, ts DESC);
CREATE INDEX audit_log_actor ON audit_log (actor_user_id, ts DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_log_library ON audit_log (library_id, ts DESC) WHERE library_id IS NOT NULL;
```

Monthly partitions are managed by a maintenance job (Epic 22). Old
partitions are detached and archived after `audit_retention_days`
(default 365). Story 9.17 surfaces `category='library'` rows; Epic 10
Story 10.16 surfaces `category='security'` rows.

## Sequencing

Land in order: 9.1 → 9.16 → 9.5 → 9.2 → 9.3 → 9.4 → 9.6 → 9.15 →
9.7 → 9.8 → 9.10 → 9.11 → 9.9 → 9.18 → 9.12 → 9.13 → 9.14 → 9.17.
