# Story 5.3 — ChromaDB vector index

## Description

The semantic layer.

## Acceptance criteria

- One Chroma collection per library, named `library-<library_id>`,
  configured with `{"hnsw:space": "cosine"}` (architecture §8.4).
- Embedding model `intfloat/multilingual-e5-large` by default,
  configurable via `pipeline.toml [search].embedding_model`. The model
  is loaded at process start and cached; `e5-base` is selected
  automatically on hosts without a GPU and `embedding_device = 'auto'`
  (recorded as `metrics.embedding_model_actual` in `transcripts`).
- For each search unit, the indexer adds a Chroma row with
  `id = "{transcript_id}:{seq}"`, `documents = unit.text`,
  `metadatas = {video_id, library_id, start, end, language, speaker}`.
  The id format is stable across re-runs so re-indexing upserts in place.
- An `Embed(text)` gRPC RPC encodes one query and returns the
  vector; the API uses this for query-time embedding (architecture
  §1.4) so the model stays in the Pipeline process only.
- Indexing throughput goal: at least 200 units/second on Apple Silicon
  with `e5-large`, sufficient to keep up with transcription.

## Test cases

- `test_chroma_add_and_query` — add 10 units; query the first unit's
  text → top-1 hit is itself.
- `test_chroma_idempotent_upsert` — add same id twice with different
  text → only the latest is stored.
- `test_embedding_dim_matches_model` — assert vector length equals the
  configured model's dim (1024 for `e5-large`, 768 for `e5-base`).
- `test_embed_grpc_returns_same_vector` — call `Embed("foo")` twice →
  identical vector; difference < 1e-6.
- `test_indexer_throughput` — fixture of 10,000 units → wall time
  ≤ 50 s on the reference machine (parameterized in CI).

## Edge cases

- **Library deleted while index exists.** A `DELETE FROM libraries`
  cascades to videos and transcripts (and through Story 5.1's
  `ON DELETE CASCADE`, to units), but Chroma is external; a
  cleanup hook removes the Chroma collection in the same transaction
  (best-effort; orphaned collections are removed by a nightly task).
- **Embedding model swap mid-library.** Vectors are not transferable;
  switching the model triggers a full library re-index. The settings
  endpoint shows a "this will reindex N hours of content" warning
  before applying.
- **GPU OOM during embedding.** The indexer falls back to CPU for the
  current batch (recorded), then resumes on GPU.
- **Single-writer constraint.** ChromaDB is single-writer per collection;
  scaling out the Pipeline to multiple processes is bounded by this
  constraint until the ChromaDB server is adopted (deferred). Mirrored
  in [04-nonfunctional Story 24.4](../../04-nonfunctional.md).
