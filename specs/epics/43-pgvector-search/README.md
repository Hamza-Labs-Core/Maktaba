# Epic 43: Semantic search in Postgres (pgvector), Chroma retired

## Why

Semantic search ("find the talk about patience", not only the words) keeps one embedding per
transcript passage: about 13.9 million passages, 384 numbers each (`intfloat/multilingual-e5-small`).
Today they live in Chroma, embedded in the pipeline's index worker
(`<StateDir>/chroma`, 42 GB on the home Mac):

- **It corrupts.** A forced stop mid-write broke the index five times on 2026-10-24/25 (each kept as
  `chroma.broken-*`, about 190 GB until deleted). Postgres recovers from a crash by design.
- **It is twice the size it needs to be.** Chroma's SQLite keeps the passage text again (already in
  `transcript_segments`), and the vectors are 32-bit where 16-bit loses nothing measurable for search.
- **It is outside everything else.** Not in the pre-update backup (`pgbundle`), not copied to the
  machines, not cleaned up when a video or library is deleted (orphans take top-k slots), and a second
  store to run, size and explain.

With pgvector the vectors sit in the home's Postgres next to the text they belong to:
`halfvec(384)` plus an HNSW index, about 13 GB instead of 42, crash-safe, in every backup, deleted
with their segments by a foreign key, and filtered by library in the same query.

## Goals

1. Semantic search reads Postgres (`segment_embeddings`, pgvector HNSW, cosine): same results, same
   or better speed (p95 of the semantic leg under the API's 200 ms budget on the home Mac at full size).
2. The index stage and `chapter_infer` write and read Postgres; deleting a video, a transcript or a
   library deletes its vectors (FK `ON DELETE CASCADE`).
3. The 13.9 M existing vectors are copied from Chroma once, without embedding anything again; search
   keeps working during the copy (Chroma serves until the copy is complete and checked).
4. pgvector ships with every bundled Postgres (macOS arm64, Linux x86_64/aarch64, Windows x64) and is in
   CI's and Docker's Postgres; `CREATE EXTENSION vector` where the bundle creates its extensions.
5. Chroma is gone: the `chromadb` dependency, the `<StateDir>/chroma` folder (deleted after the copy is
   checked), the compose service, the CI service, the env vars and the docs.

## Non-goals

- A different embedding model, or re-embedding (the copy keeps the vectors as they are).
- Semantic search on SQLite installs: there the semantic leg is off and search is full-text, as when
  the pipeline answers "backend not configured" today.
- Moving `video_embeddings` / `library_topics.centroid_vec` (BYTEA, small) to `vector`: later, if useful.

## Design

### Schema (migration 0152, Postgres only)

```sql
CREATE EXTENSION IF NOT EXISTS vector;          -- only when available, see below
CREATE TABLE segment_embeddings (
    segment_id  BIGINT PRIMARY KEY REFERENCES transcript_segments(id) ON DELETE CASCADE,
    video_id    UUID   NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id  UUID   NOT NULL,
    language    TEXT,
    model       TEXT   NOT NULL,                -- 'intfloat/multilingual-e5-small'
    embedding   halfvec(384) NOT NULL
);
CREATE INDEX segment_embeddings_hnsw ON segment_embeddings
    USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX segment_embeddings_video ON segment_embeddings (video_id);
CREATE INDEX segment_embeddings_library ON segment_embeddings (library_id);
```

- No text: the passage is `transcript_segments.text`, joined by `segment_id` (the API joins it today).
- `library_id` is kept on the row for filtering inside the index scan (pgvector 0.8 iterative scans:
  `SET hnsw.iterative_scan = relaxed_order`), so a non-admin's library filter does not starve top-k.
- The migration checks `pg_available_extensions`: without `vector` (an old bundle, a plain image) it
  creates nothing and logs it; the pipeline then reports the semantic backend unavailable (FTS only),
  and the next start with pgvector runs the deferred part (a `semantic_store_ready()` check the
  pipeline calls). The `.sqlite.sql` twin is a no-op with a comment.
- `cluster_copy_excluded()` gains `segment_embeddings`: the machines' copies do not carry 13 GB of
  vectors they never query. After a switchover the new home has none: the index stage sees an empty
  store for a video whose fingerprint says indexed, and indexes it again (story 43.4).

### Writes and reads (pipeline)

`search/semantic.py` keeps its interface (`write_vectors`, `delete_ids`, `delete_video`,
`video_vectors`, `search`) with a Postgres backend (`asyncpg`, the pipeline's pool) replacing the
Chroma one:

- **upsert:** `INSERT ... ON CONFLICT (segment_id) DO UPDATE`, batches of 500 via `COPY` into a temp
  table, then an `INSERT ... SELECT`.
- **delete:** by `segment_id` / by `video_id`; most deletes now happen by cascade.
- **search:** `SELECT segment_id, 1 - (embedding <=> $q) AS score FROM segment_embeddings
  WHERE library_id = ANY($libs) [AND language = ANY($langs)] ORDER BY embedding <=> $q LIMIT $k`
  with `hnsw.ef_search` 100 and iterative scans; the query is embedded with `"query: "` as now.
- **`chapter_infer`:** `video_vectors()` reads the video's rows.
- The write gate (`search/gate.py`) stays for ordering, but a stop no longer has to wait for a
  Chroma flush: Postgres transactions are atomic. The index worker's 45 s stop grace can shrink.
- The gRPC contract with the API (`SemanticSearch`) is unchanged; the API still joins
  `transcript_segments` (its `deleted_at` / `is_active` filters stay).
- `INDEX_VERSION` is not bumped (the vectors are the same); the fingerprint gains the store, so a
  video indexed into Chroma counts as indexed only after the copy (43.3) moved its vectors.

### The one-time copy (43.3)

`maktaba-pipeline copy-chroma`, run by the supervisor at start when `<StateDir>/chroma` exists and the
copy is not marked done:

1. Reads Chroma's collection in pages of 5000 (`get(include=[embeddings, metadatas])`), maps each id
   `{transcript_id}:{seq}` via its `segment_id` metadata, skips ids whose segment no longer exists,
   and inserts into `segment_embeddings` as `halfvec` (resumable: a cursor in `settings`).
2. Builds the HNSW index after the bulk load (much faster than per-row maintenance):
   `maintenance_work_mem` raised for the session.
3. Checks: row counts match (minus orphans), and 1000 sampled queries return the same top-10 from
   both stores (recall@10 ≥ 0.95).
4. Marks the copy done; search switches to Postgres; the Chroma folder is deleted on the next start
   (kept one start for a rollback).

During the copy, search and the index stage keep using Chroma.

### Packaging (43.1)

- **macOS:** `tools/release/mac-app/payload.sh` builds pgvector (pinned tag + sha256 in `pins.env`)
  against the downloaded Postgres's `pg_config` (`PG_SYSROOT=$(xcrun --show-sdk-path)`: the
  archive names its builder's SDK), installs `vector.dylib` and its SQL into the bundle, and signs it
  with the rest. `check.sh` asserts `CREATE EXTENSION vector` works in a scratch cluster. Proven
  locally: pgvector 0.8.7 against theseus-rs 16.15.0 arm64, HNSW plans and queries.
- **Linux:** the same in `linux-app/payload_tool.py` (gcc, pgxs).
- **Windows:** pgvector's `Makefile.win` with MSVC (`nmake`) in `win-app/payload_tool.py`.
- **Node copies:** `pgbin.go`'s pinned bundle gets the same build.
- **CI / Docker:** `pgvector/pgvector:pg16` replaces `postgres:16.x` in `_integration.yml`,
  `_media-e2e.yml`, `relay.yml` and the compose files; `deploy/docker/postgres/init.sql` creates the
  extension; the Chroma service and its env go.
- **pgbundle:** `CREATE EXTENSION IF NOT EXISTS vector` with the others (`pgbundle.go`, `restore.go`).

## Stories

| # | Story | Done when |
|---|---|---|
| 43.1 | pgvector in every Postgres we run | the app bundles (mac, linux, windows), CI and compose create `vector`; checks prove it |
| 43.2 | `segment_embeddings` and the Postgres backend | migration 0152; `semantic.py` on Postgres behind the same interface; index stage, `chapter_infer`, gRPC search on it; tests on a pgvector Postgres |
| 43.3 | Copy the existing vectors | resumable copy, index built after, recall check, switch, Chroma folder deleted a start later |
| 43.4 | Machines and switchover | `segment_embeddings` excluded from copies; a new home re-indexes what it lacks |
| 43.5 | Chroma retired | `chromadb` out of the pipeline's deps and venv checks, compose/CI services, env vars, doctor, devmigrate/adopt/uninstall references, docs |
| 43.6 | Measured on the home Mac | p50/p95 of the semantic leg at 13.9 M rows, disk before/after, recall vs Chroma, in the PR |

Order: 43.1 → 43.2 → 43.3 + 43.4 → 43.6 → 43.5 (Chroma's code goes only after the live copy is
checked; one release reads Chroma for the copy, the next drops it).

## Risks

- **HNSW build time and memory at 13.9 M rows:** built once after the bulk copy with raised
  `maintenance_work_mem`; measured in 43.6. If too slow, `ivfflat` or a binary-quantised index with
  re-ranking are the fallbacks.
- **Filtered queries:** a narrow library filter can return fewer than k from an HNSW scan; pgvector
  0.8's iterative scans address it, tested with a small library.
- **Windows build of pgvector:** MSVC in CI; if it fails, Windows ships with semantic search off
  (FTS only) until fixed, never a broken database.
- **Disk during the copy:** Chroma (42 GB) and Postgres (about 13 GB) side by side until the folder is
  deleted; the copy checks free space first.
