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
- **Node copies:** a machine with a desktop app runs its copy on the app's bundled Postgres
  (`pgbundle.FindHome`), so it has pgvector. A machine without one runs the stock
  theseus-rs archive `pgbin` fetches, which has no pgvector and no compiler to build it: its
  copy does not need it (`segment_embeddings` is excluded), and if it becomes the home by a
  switchover it runs with semantic search off (FTS only) until it runs a Postgres with
  pgvector. Shipping prebuilt pgvector archives for `pgbin` (built in CI, pinned by SHA-256)
  is a follow-up.
- **Extension creation:** `vector` is not a trusted extension, so the app role's migrations
  cannot create it on a bundled Postgres: the superuser does, in `pgbundle`'s bootstrap and in
  the cluster grants (`clusterRev` 4, so a home made before runs it at its next start), only
  when `pg_available_extensions` has it. Migration 0152 should therefore go by `pg_extension`
  (installed) and try `CREATE EXTENSION IF NOT EXISTS vector` only where its role may (CI's and
  Docker's superuser), never fail on it.
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
checked; one release reads Chroma for the copy, the next drops it). The owner moved 43.5 ahead of
43.6 on 2026-11-19 ("retire Chroma first"), without waiting for a checked live copy: a home whose
copy was not done when it updates indexes its videos again (see As built).

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

## As built

### 43.5 Chroma retired

Search, the `index` stage and `chapter_infer` read and write Postgres only (`segment_embeddings`).
Nothing opens Chroma any more, and nothing depends on `chromadb`.

- **Dependencies.** `chromadb` is out of the pipeline's `search` extra (`pipeline/pyproject.toml`,
  `uv.lock`). About forty packages only it pulled in went with it, `oauthlib` and `kubernetes`
  among them. The Mac, Windows and Linux payloads build their venvs from that extra, so they
  lose it too. The node-packaging checks (`_node-packaging.yml`) no longer import it, and
  componentseed no longer counts it as part of `runtime.ml`.
- **Code.**
  - Gone: the Chroma store in `search/semantic.py` (`SemanticIndex`, `open_collection`, the
    sync threshold, `close_clients`), the copy (`search/chroma_copy.py` and its `copy-chroma`
    command), `pgstore.chroma_has_vectors` and `delete_ids`, and the supervisor's
    `pipeline/chroma-copy` job (`chromacopy.go`, and its process in the catalog).
  - `StoreState` keeps only `ready` (pgvector installed, table made). The plugin stages run in
    their runner whatever the row says (`index_store_chroma` is gone).
  - The legacy `--no-engine` INDEX writes no vectors: its default target names no store. The
    doctor no longer checks a Chroma folder. `MAKTABA_CHROMA_DIR` is no longer given to any
    worker.
  - The dev-stack move (`devmigrate`) and adopt no longer carry a Chroma folder. Uninstall
    still removes one that is left.
- **Compose, CI and docs.** The `chroma` service, its volumes and `MAKTABA_CHROMA_*` are out of
  `deploy/compose/*.yml`, `pipeline/Dockerfile`, `.env.example`, `tools/dev/stack.sh`, CI's
  integration services, the Makefile and the developer docs. A Docker home keeps its old
  `chroma` volume unused until the operator removes it.
- **A home that still has Chroma data finishes its copy** (the owner's decision of 2026-11-19:
  a home whose copy never finished must not embed anything again).
  - **The copy runs at the update.** `copy-chroma` is the story 43.3 copy, kept: it reads
    Chroma's files (`chroma.sqlite3`, the HNSW segment's `header.bin`,
    `index_metadata.pickle` and `data_level0.bin`), never Chroma, so it needs no chromadb,
    only numpy (the `search` extra every home that had Chroma has). On a home on Postgres
    whose Chroma folder is there and not copied, the supervisor runs it at every start
    until it is done (`pipeline/chroma-copy`, `BuildChromaCopy`). It goes on from the
    cursor an earlier copy saved (the same code and cursor as the releases before), and
    never overwrites a vector the index stage wrote.
  - **Nothing is embedded again meanwhile.** The supervisor gives every child
    `MAKTABA_CHROMA_COPY=pending`. While that holds and the state row says `chroma`, the
    reconciler's sweeps queue nothing, and the index fingerprint never says "not stored"
    (`StoreState.copying`).
  - **"Marked indexed".** The copied videos keep the fingerprints their index jobs
    answered, so once their vectors are in nothing indexes them.
  - **It ends like the old copy.** Once the copy has checked itself (row count, and recall
    of the HNSW index against Chroma's own vectors), it writes `store = 'postgres'`,
    `copy_done_at` and `chroma-copy.done`, as the releases before did. A rollback to the
    release before then finds a copy done. One that comes back before it is done finds
    the folder and the cursor, and goes on copying.
  - **What search gives meanwhile.** Search reads Postgres throughout: what is copied so
    far. The copy drops the HNSW index for its load and builds it after, so the semantic
    leg is slow, and the API falls back to full-text past its budget, until the copy is
    done.
  - **Videos copied in part, with no copy to finish them.** A home whose copy stopped part
    way and whose Chroma folder is gone has videos with some of their vectors only. The
    fingerprint counts the vectors against the active transcript's non-blank segments
    (`PgVectorStore.has_vectors`), and the reconciler makes one bounded pass
    (`_partial_copy_sweep`, 500 videos a minute, once per process) that queues them. Those
    are indexed again: there is nothing to copy them from.
- **SQLite homes**, and a Postgres without pgvector, have no semantic search: search is
  full-text. Before 43.5 a SQLite home's store was Chroma; there is no pgvector to copy
  into. `index` and `chapter_infer` succeed `unchanged` with the reason, so no job fails
  and nothing downstream runs. The index fingerprint carries `semantic: off` only there,
  so once pgvector is installed every video is indexed, and a home with pgvector keeps
  its fingerprints.
- **The folder is cleaned up** (`supervisor.RetireChroma`, at every start), only after
  the copy.
  - **Copied:** a folder whose copy is done (`chroma-copy.done` names it) loses Chroma's
    files at the start after the copy (kept that start, for a rollback, as before). A
    folder not copied on a Postgres home is kept, however often the release starts,
    until the copy is done.
  - **SQLite:** a SQLite home's folder (nothing to copy into) is kept by the first start
    and marked (`chroma-retired`), then deleted by a start a day after the mark
    (`ChromaKeep`; `MAKTABA_CHROMA_KEEP_SECONDS` changes it).
  - **Only Chroma's own entries** are deleted: `chroma.sqlite3` and its side files, and the
    segment folders it names by UUID. Then the folder goes, if that left it empty. A
    `MAKTABA_CHROMA_DIR` pointed at a folder holding other things (even the state dir)
    loses nothing else. A folder without `chroma.sqlite3` that is not empty is never
    touched, and a leading `~` is the home folder, as the pipeline read it.
  - **Rollback.** Before the copy is done, a rollback finds the folder and the cursor and
    goes on copying with the release before's copy. After it, it finds `postgres` and the
    done file, and reads Postgres. On a SQLite home, a rollback after the day finds no
    folder and an empty Chroma; the day is its rollback window.
- **Tests.**
  - `chromaretire_test.go`: copied, never copied, the keep, a folder holding other things
    (the state dir), other folders, `~`, nothing there.
  - `test_pgstore.py` on a pgvector Postgres: the stage, search, `chapter_infer` and the sweep
    on a row that still says `chroma`; a video copied in part, queued by the one-time
    pass and indexed whole; without pgvector both stages succeed `unchanged`; `prepare`
    writes no row.
  - The engine stage and analysis tests, on an in-memory store.
  - The semantic-search parity test, with the row left on `chroma`.
  - `test_chroma_copy.py`: the reader against a folder Chroma 1.5.9 wrote
    (`tests/search/data/chroma-1.5.9`, made by `tests/search/chroma_fixture.py`), and on
    pgvector the whole copy, a killed copy that goes on, a failed check, no pgvector.
  - `test_pgstore.py`: while the copy runs no sweep queues an index and no fingerprint
    says "not stored"; after it a copied video's fingerprint is the plain one.
  - `chromaretire_test.go`: a Postgres home runs the copy first and keeps the folder until
    it is done.
  - Update rehearsal scenario 17 (`api/rehearsal_chroma_e2e_test.go`): a home whose copy
    was stopped part way updates; the copy finishes, every vector is in Postgres as Chroma
    held it, no index job runs, and Chroma's files go only the start after.
