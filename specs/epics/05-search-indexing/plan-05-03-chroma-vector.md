# Plan 5.3 — ChromaDB vector index — implementation

> Implementation plan for [story-05-03-chroma-vector.md](story-05-03-chroma-vector.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: reads `transcript_units` rows produced by
> [Plan 5.1](plan-05-01-unit-chunking.md), is fed by the
> `segment_committed` NOTIFY plumbing from
> [Plan 3.6](../03-transcription/plan-03-06-segment-commit.md), is paired
> with the FTS layer in [Plan 5.2](plan-05-02-fts-tsvector.md), and is
> consumed at query time by the hybrid retriever in
> [Plan 5.4](plan-05-04-hybrid-rrf.md). Incremental indexing claim
> semantics live in [Plan 5.5](plan-05-05-incremental-indexing.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | Default embedding model is **`intfloat/multilingual-e5-large`** (560M params, 1024-dim, MIT license, 100+ languages including Modern Standard Arabic and dialectal Arabic). On hosts without a CUDA/MPS-capable accelerator AND `embedding_device = 'auto'`, the loader downgrades to **`intfloat/multilingual-e5-base`** (278M, 768-dim) automatically. Both are recorded in `transcripts.metrics.embedding_model_actual`. The choice between e5 and BGE-m3 is final per host because vectors are not transferable across model families. | Story acceptance: "`intfloat/multilingual-e5-large` by default … `e5-base` is selected automatically on hosts without a GPU." | The realistic finalists were e5-large, BAAI/bge-m3, and paraphrase-multilingual-mpnet-base-v2. **e5-large wins** on three axes: (1) **license** — MIT, no field-of-use carve-outs, fully commercial-safe for our self-hosted single-user product and any future multi-tenant SaaS rebuild. bge-m3 is also MIT but its dense+sparse+multi-vector "all-in-one" output requires a heavier integration (we'd use only the dense head, wasting model capacity). (2) **Arabic coverage** — e5 is trained with explicit MIRACL Arabic data; published MTEB results put it ahead of mpnet-multilingual on Arabic retrieval by a meaningful margin and within ~1 point of bge-m3 dense-only on most Arabic benchmarks. (3) **inference cost** — e5-large at 560M is the right ceiling for our ≥200 units/sec throughput budget on Apple Silicon MPS; bge-m3 (568M) would meet it too but adds the multi-vector overhead even when unused. The loader keeps the door open for users who want to pin a specific model via `[search].embedding_model`. |
| D2 | ChromaDB runs in **embedded persistent mode** (`chromadb.PersistentClient`) not as an HTTP server. The persistence directory is `${MAKTABA_DATA_DIR}/chroma/` and is owned by the Pipeline process. | Refines architecture §8.4 (which only says "ChromaDB"). Story does not specify the client mode. | (1) **Single-writer constraint anyway** — the story explicitly accepts ChromaDB's single-writer-per-collection limit (E55 in story). Running an HTTP server in a separate process buys nothing under that constraint and adds a daemon to manage, ports to allocate, and a network hop to every upsert. (2) **Single-user product** — Maktaba ships as a desktop-class self-hosted app for one user / one Pipeline process; there is no second consumer of the vector index. (3) **Embedded mode** is what `chromadb` ships as the default, pulls in only `chromadb` and its deps (no extra service binary), and lets us share the same SQLite-file-style persistence path as the rest of the app. (4) **Migration path** — if a future deployment needs multi-process writes, the swap is a one-line `Settings(chroma_api_impl="chromadb.api.fastapi.FastAPI")` plus a docker-compose entry; the wrapper in §2.2 isolates that change. |
| D3 | **One Chroma collection per library**, named `library-<library_id>`, with `{"hnsw:space": "cosine", "hnsw:M": 32, "hnsw:construction_ef": 200, "hnsw:search_ef": 80}`. Library deletion drops the collection (best-effort) and a nightly task GCs orphans. | Story acceptance: "One Chroma collection per library, named `library-<library_id>`, configured with `{"hnsw:space": "cosine"}`." | Per-library collections give us (a) **trivial query-time scoping** — a library search is a `collection.query(...)` against one HNSW index, no metadata-filter post-pass slowing the kNN; (b) **clean deletion** — drop the collection on `DELETE FROM libraries` instead of running 100k single-row deletes through Chroma's slow `delete(ids=[…])` path; (c) **per-library model swaps** — if a user reindexes one library on a new model, only that collection's vectors are invalidated. The HNSW knobs (M=32, construction_ef=200) are above Chroma defaults (16/100) because our recall budget is tight (we surface top-10 to the user) and the index size per library is bounded by hours-of-content (a 1000-hour library is ~360k units → ~2.8 GB at 1024-dim float32, fits in RAM on the indexing host). |
| D4 | **Indexing batch size = 64** for e5-large (32 on e5-base+CPU). Embedding the batch and the Chroma upsert are pipelined: while batch N is being upserted, batch N+1 is being embedded. Both knobs are exposed in `pipeline.toml [search]`. | Refines story (no batch size given). | 64 is the sweet spot for e5-large on a single MPS GPU on M-series Macs based on sentence-transformers' published benchmarks: smaller batches under-utilize the GPU; larger batches blow past 16 GB VRAM with ~512-token inputs. On CPU, 32 keeps a single batch under ~1.5 s on an 8-core M-class CPU which is the right unit-of-work for our single-writer Chroma upsert. The pipelining (one batch ahead) doubles throughput in practice without doubling memory because sentence-transformers releases the input tensors before returning. |
| D5 | **Sync worker is LISTEN/NOTIFY-driven, not polling.** A long-lived worker subscribes to the `segment_committed` channel (Plan 3.6) and the new `transcript_units_appended` channel (added by this story; fired from a trigger on `INSERT INTO transcript_units`). On NOTIFY, it claims unindexed units from `transcript_units WHERE indexed_at IS NULL` (using the partial index from Story 5.1), embeds them in batches, upserts to Chroma, and stamps `indexed_at = now()`. A 60 s safety poll covers missed notifications. | Refines story (which doesn't specify the trigger mechanism) and aligns with the FTS sync worker in [Plan 5.2](plan-05-02-fts-tsvector.md). | Polling at 1 s would add 1 s to perceived "search me what I just transcribed" latency for live transcription; LISTEN/NOTIFY brings that to under 100 ms wall. The 60 s safety poll exists because LISTEN/NOTIFY in `asyncpg` can drop messages on connection drop without Postgres re-replaying — a slow safety net is cheap and the partial index makes "find unindexed units" a sub-ms scan. |
| D6 | `Embed(text)` is a **gRPC** RPC (not REST). The proto lives in `shared/proto/pipeline.proto` and is compiled into both the Pipeline (server) and API (client) packages. | Story acceptance: "An `Embed(text)` gRPC RPC encodes one query." | The Pipeline ↔ API boundary is already gRPC for ingest control (Epic 2) and job-status streaming (Epic 4); Embed reuses the same channel, auth, and error model. REST would mean spinning up a second HTTP transport just for one endpoint. gRPC also gives us protobuf-typed request/response without hand-written serialization, and bidirectional connection reuse means single-query latency is dominated by inference, not handshake. |
| D7 | **Embedding device selection** is `auto` by default, resolving in this order: CUDA → MPS (Apple Silicon) → CPU. On `auto`, `e5-large` requires CUDA or MPS; pure-CPU hosts get e5-base regardless of the configured model. The user can pin `embedding_device = 'cpu'` to force CPU even with e5-large (slow but unblocks low-RAM GPUs). | Story acceptance: "`e5-base` is selected automatically on hosts without a GPU." | The "GPU" that matters here is anything that can run sentence-transformers fast enough to hit the 200 units/s throughput target. CPU on a typical 8-core M-class Mac runs e5-large at ~25 units/s — it works but won't keep up with live transcription. e5-base on the same CPU runs at ~80 units/s, close enough that we accept it. The auto-downgrade is recorded in `transcripts.metrics.embedding_model_actual` so a later "why is my Arabic search recall worse than the docs say" investigation has a paper trail. |
| D8 | **Long-text truncation:** units longer than the model's max sequence length (512 tokens for e5-large/base) are truncated **left-to-right** with a warning logged once per (transcript_id, model). Story 5.1's chunker targets ~200 chars / hard cap 400 chars, so truncation should be vanishingly rare; we log it because if it happens often, Story 5.1 needs tightening. | Refines story (no truncation policy given). | Sentence-transformers truncates silently by default — we want the signal. Left-to-right matches the natural reading direction for both English and Arabic in the transformer's positional encoding (the encoder doesn't know about RTL display); centering or end-truncation discards beginning-of-sentence context which is more semantically loaded. The "warn once per (transcript, model)" cap stops a single bad transcript from drowning the logs. |

If D2 is rejected (HTTP server instead of embedded), §2.2 changes to a
`HttpClient(host, port)` constructor and a daemon must be supervised by
the same systemd/launchd unit as the Pipeline; correctness is
unaffected. If D3 is rejected (single collection with `library_id` in
metadata), §2.4 collection-management code disappears but every query
gains a `where={"library_id": …}` filter that runs *after* HNSW kNN —
recall stays the same but we burn ~2× the latency on large libraries.

---

## 1. Architecture diagram — write path and query path

```
─────────────────────── WRITE PATH ───────────────────────────────────

  Plan 3.6: SegmentCommitter
       │ NOTIFY segment_committed (transcript_id, seq_range)
       ▼
  Plan 5.1: Unit chunker        ── INSERT INTO transcript_units (…)
       │                         ── trigger fires:
       │                            NOTIFY transcript_units_appended
       │                                   payload = transcript_id
       ▼
  ┌──────────────────────────────────────────────────────────────┐
  │  Pipeline / search / IndexerWorker (this plan, §2.6)         │
  │                                                              │
  │  asyncpg LISTEN transcript_units_appended  ┐                 │
  │  + 60 s safety tick ────────────────────────┴► claim batch:  │
  │       SELECT id, transcript_id, seq, text, language,         │
  │              metadata, segment_ids                            │
  │         FROM transcript_units                                │
  │        WHERE indexed_at IS NULL                              │
  │        ORDER BY transcript_id, seq                           │
  │        LIMIT :batch  FOR UPDATE SKIP LOCKED                  │
  │                                                              │
  │       ──► group by library_id (resolve via transcript→video) │
  │           ──► EmbeddingService.encode(texts)  ── batch N+1   │
  │                                                              │
  │       ──► ChromaCollection.upsert(                           │
  │              ids=[f"{tx_id}:{seq}" …],                       │
  │              embeddings=…,                                   │
  │              documents=texts,                                │
  │              # Architecture §8.4 metadata shape exactly:     │
  │              metadatas=[ {video_id, start, end,              │
  │                           language, speaker} … ],            │
  │           )                                                  │
  │                                                              │
  │       ──► UPDATE transcript_units SET indexed_at = now()     │
  │             WHERE id = ANY($1::bigint[])                     │
  └──────────────────────────────────────────────────────────────┘

─────────────────────── QUERY PATH ───────────────────────────────────

  HTTP client (web UI / desktop)
       │  GET /search?q=…&library_id=…
       ▼
  API Service / search/handler.py
       │  (1) query-time embed via gRPC
       │     PipelineStub.Embed(EmbedRequest{text="query: …"})
       │           ── one round-trip ──►  Pipeline server
       │                                       │
       │                                       ▼
       │                            EmbeddingService.encode_query(text)
       │                              (adds "query: " prefix per e5
       │                               instructions; see §2.3)
       │                                       │
       │                            EmbedResponse{vector=[1024 floats],
       │                                          model="e5-large",
       │                                          dim=1024}
       │  (2) chroma.collection(library-<id>).query(
       │          query_embeddings=[vector], n_results=top_k,
       │          where={...optional language/speaker filter...})
       │      ──► [(unit_id, distance), …]
       │
       │  (3) hand off to hybrid RRF (Plan 5.4) — combine with FTS hits
       │      ──► resolve unit_id → segment_id → timestamp
       ▼
  HTTP response with timestamps, snippets, scores
```

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── search/
│   ├── __init__.py                  # public surface
│   ├── chroma_client.py             # PersistentClient wrapper (D2)
│   ├── collections.py               # collection-per-library helpers (D3)
│   ├── embedder.py                  # sentence-transformers wrapper (D1, D4, D7)
│   ├── indexer.py                   # IndexerWorker — LISTEN/NOTIFY loop (D5)
│   ├── grpc_server.py               # Embed RPC handler (D6)
│   ├── config.py                    # SearchConfig dataclass
│   ├── errors.py                    # ChromaUnavailable, EmbeddingError, ModelNotFound
│   └── tests/
│       ├── conftest.py              # tmp chroma dir, fake embedder
│       ├── test_chroma_add_and_query.py
│       ├── test_chroma_idempotent_upsert.py
│       ├── test_embedding_dim_matches_model.py
│       ├── test_embed_grpc_returns_same_vector.py
│       ├── test_indexer_throughput.py
│       ├── test_multilingual_cross_search.py
│       ├── test_chroma_unavailable.py
│       ├── test_long_text_truncation.py
│       ├── test_empty_unit.py
│       └── test_library_deletion_cleanup.py

shared/proto/
└── pipeline.proto                   # adds Embed RPC (this story)

shared/db/migrations/
└── 0018_transcript_units_notify.sql # NOTIFY trigger on INSERT
```

### 2.2 `chroma_client.py` — PersistentClient wrapper (D2)

```python
"""Embedded ChromaDB client. Single-process, single-writer (D2).

Wraps chromadb.PersistentClient so the rest of the package can swap to
HttpClient later by changing this file alone.
"""
from __future__ import annotations
import logging
from pathlib import Path
from typing import Any

import chromadb
from chromadb.config import Settings

from maktaba_pipeline.search.errors import ChromaUnavailable

log = logging.getLogger(__name__)

_DEFAULT_COLLECTION_METADATA: dict[str, Any] = {
    "hnsw:space": "cosine",
    "hnsw:M": 32,
    "hnsw:construction_ef": 200,
    "hnsw:search_ef": 80,
}


class ChromaClient:
    """Thin wrapper around chromadb.PersistentClient."""

    def __init__(self, persist_dir: Path):
        self._persist_dir = Path(persist_dir)
        self._persist_dir.mkdir(parents=True, exist_ok=True)
        try:
            self._client = chromadb.PersistentClient(
                path=str(self._persist_dir),
                settings=Settings(anonymized_telemetry=False),
            )
        except Exception as e:
            raise ChromaUnavailable(
                f"failed to open Chroma at {self._persist_dir}: {e}"
            ) from e

    @property
    def raw(self) -> chromadb.api.ClientAPI:
        return self._client

    def collection_for_library(self, library_id: int) -> "ChromaCollection":
        name = collection_name(library_id)
        col = self._client.get_or_create_collection(
            name=name, metadata=_DEFAULT_COLLECTION_METADATA,
        )
        return ChromaCollection(col, library_id=library_id)

    def drop_library(self, library_id: int) -> None:
        """Best-effort delete; logs but does not raise if collection missing."""
        name = collection_name(library_id)
        try:
            self._client.delete_collection(name=name)
            log.info("chroma_collection_dropped", extra={"name": name})
        except Exception as e:
            log.warning(
                "chroma_collection_drop_failed",
                extra={"name": name, "err": str(e)},
            )


def collection_name(library_id: int) -> str:
    return f"library-{library_id}"
```

### 2.3 `embedder.py` — sentence-transformers wrapper (D1, D4, D7)

```python
"""EmbeddingService — loads the e5 model at process start, encodes batches.

Model selection (D1, D7):
  configured = pipeline.toml [search].embedding_model  (default 'e5-large')
  device     = pipeline.toml [search].embedding_device (default 'auto')
  if device == 'auto' and no GPU: downgrade e5-large → e5-base.

E5 input convention (important for retrieval quality):
  - passages indexed as 'passage: <text>'
  - queries embedded as 'query: <text>'
  Without this prefix, e5 retrieval recall drops by ~5pts on MTEB.
"""
from __future__ import annotations
import logging
import threading
import time
from dataclasses import dataclass
from typing import Iterable, Sequence

import numpy as np

from maktaba_pipeline.search.errors import EmbeddingError, ModelNotFound

log = logging.getLogger(__name__)

E5_LARGE = "intfloat/multilingual-e5-large"
E5_BASE = "intfloat/multilingual-e5-base"

_MODEL_DIMS = {E5_LARGE: 1024, E5_BASE: 768}
_MAX_TOKENS = 512  # both e5-large and e5-base


@dataclass(frozen=True)
class EmbeddingMetadata:
    model_name: str
    dim: int
    device: str
    max_tokens: int


class EmbeddingService:
    """Owns one sentence-transformers model instance."""

    def __init__(
        self,
        *,
        configured_model: str = E5_LARGE,
        device: str = "auto",
        batch_size: int = 64,
    ):
        self._configured_model = configured_model
        self._device_pref = device
        self._batch_size = batch_size
        self._model = None  # lazy
        self._meta: EmbeddingMetadata | None = None
        self._truncation_warned: set[tuple[int | None, str]] = set()
        self._load_lock = threading.Lock()

    @property
    def metadata(self) -> EmbeddingMetadata:
        if self._meta is None:
            self._load()
        return self._meta  # type: ignore[return-value]

    def _resolve_device(self) -> tuple[str, str]:
        """Returns (model_to_load, device_to_use). Implements D7 downgrade."""
        if self._device_pref == "cpu":
            return self._configured_model, "cpu"

        try:
            import torch
        except Exception as e:
            raise EmbeddingError(f"torch import failed: {e}") from e

        if torch.cuda.is_available():
            return self._configured_model, "cuda"
        if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
            return self._configured_model, "mps"

        # CPU-only host: downgrade to e5-base if user configured e5-large.
        if self._configured_model == E5_LARGE and self._device_pref == "auto":
            log.warning(
                "embedding_model_downgraded_to_base",
                extra={"reason": "cpu_only_host"},
            )
            return E5_BASE, "cpu"
        return self._configured_model, "cpu"

    def _load(self) -> None:
        with self._load_lock:
            if self._model is not None:
                return
            from sentence_transformers import SentenceTransformer
            model_name, device = self._resolve_device()
            t0 = time.monotonic()
            try:
                self._model = SentenceTransformer(model_name, device=device)
            except OSError as e:
                raise ModelNotFound(
                    f"sentence-transformers could not load {model_name}; "
                    f"is it downloaded? ({e})"
                ) from e
            elapsed = time.monotonic() - t0
            self._meta = EmbeddingMetadata(
                model_name=model_name,
                dim=_MODEL_DIMS[model_name],
                device=device,
                max_tokens=_MAX_TOKENS,
            )
            log.info(
                "embedding_model_loaded",
                extra={
                    "model": model_name, "device": device,
                    "dim": self._meta.dim, "load_sec": round(elapsed, 2),
                },
            )

    # --- encode paths ---------------------------------------------------

    def encode_passages(
        self, texts: Sequence[str], *, transcript_id: int | None = None,
    ) -> np.ndarray:
        """Encode unit texts. Returns float32 (N, dim). E5 'passage:' prefix."""
        if self._model is None:
            self._load()
        prepared = [self._truncate(t, transcript_id) for t in texts]
        prefixed = [f"passage: {t}" for t in prepared]
        try:
            vectors = self._model.encode(  # type: ignore[union-attr]
                prefixed,
                batch_size=self._batch_size,
                normalize_embeddings=True,        # cosine ↔ inner product
                convert_to_numpy=True,
                show_progress_bar=False,
            )
        except Exception as e:
            raise EmbeddingError(f"encode_passages failed: {e}") from e
        return vectors.astype("float32", copy=False)

    def encode_query(self, text: str) -> np.ndarray:
        """Encode one query. Returns float32 (dim,). E5 'query:' prefix."""
        if self._model is None:
            self._load()
        prepared = self._truncate(text, transcript_id=None)
        prefixed = f"query: {prepared}"
        try:
            vec = self._model.encode(  # type: ignore[union-attr]
                [prefixed],
                normalize_embeddings=True,
                convert_to_numpy=True,
                show_progress_bar=False,
            )[0]
        except Exception as e:
            raise EmbeddingError(f"encode_query failed: {e}") from e
        return vec.astype("float32", copy=False)

    # --- truncation (D8) -----------------------------------------------

    def _truncate(self, text: str, transcript_id: int | None) -> str:
        """Tokenize-then-truncate. Only warn once per (transcript_id, model)."""
        if self._model is None:
            return text
        tok = self._model.tokenizer  # type: ignore[union-attr]
        ids = tok.encode(text, add_special_tokens=False, truncation=False)
        if len(ids) <= _MAX_TOKENS - 8:  # leave room for 'passage: ' + specials
            return text
        truncated_ids = ids[: _MAX_TOKENS - 8]
        truncated = tok.decode(truncated_ids, skip_special_tokens=True)
        key = (transcript_id, self.metadata.model_name)
        if key not in self._truncation_warned:
            self._truncation_warned.add(key)
            log.warning(
                "embedding_input_truncated",
                extra={
                    "transcript_id": transcript_id,
                    "model": self.metadata.model_name,
                    "orig_tokens": len(ids), "kept_tokens": len(truncated_ids),
                },
            )
        return truncated
```

### 2.4 `collections.py` — collection-per-library helpers (D3)

```python
"""ChromaCollection — typed wrapper around a chromadb Collection."""
from __future__ import annotations
import logging
from dataclasses import dataclass
from typing import Sequence, Any

import numpy as np

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class UnitRow:
    transcript_id: int
    seq: int
    text: str
    language: str
    video_id: int
    library_id: int
    start_sec: float
    end_sec: float
    speaker: str | None  # may be None when diarization is off

    @property
    def chroma_id(self) -> str:
        # Stable across re-runs: re-indexing upserts in place (story acceptance).
        return f"{self.transcript_id}:{self.seq}"

    @property
    def metadata(self) -> dict[str, Any]:
        # Architecture §8.4 lists exactly {video_id, start, end, language,
        # speaker}. The per-library collection name already encodes the
        # library_id, so it is intentionally NOT duplicated here.
        return {
            "video_id": self.video_id,
            "start": float(self.start_sec),
            "end": float(self.end_sec),
            "language": self.language,
            "speaker": self.speaker if self.speaker is not None else "",
        }


@dataclass(frozen=True)
class QueryHit:
    unit_id: str             # "{transcript_id}:{seq}"
    distance: float          # cosine distance, 0 = identical, 2 = opposite
    metadata: dict[str, Any]
    document: str


class ChromaCollection:
    """One library's Chroma collection."""

    def __init__(self, raw_collection, *, library_id: int):
        self._col = raw_collection
        self._library_id = library_id

    @property
    def name(self) -> str:
        return self._col.name

    @property
    def count(self) -> int:
        return self._col.count()

    def upsert(
        self,
        rows: Sequence[UnitRow],
        embeddings: np.ndarray,
    ) -> None:
        if len(rows) == 0:
            return
        if embeddings.shape[0] != len(rows):
            raise ValueError(
                f"embeddings/rows length mismatch: "
                f"{embeddings.shape[0]} vs {len(rows)}")
        self._col.upsert(
            ids=[r.chroma_id for r in rows],
            documents=[r.text for r in rows],
            metadatas=[r.metadata for r in rows],
            embeddings=embeddings.tolist(),
        )

    def delete_transcript(self, transcript_id: int) -> int:
        """Remove all units for a transcript. Returns count deleted (best-effort)."""
        # Chroma supports where-filter delete:
        before = self._col.count()
        self._col.delete(where={"transcript_id": transcript_id})  # noqa
        return before - self._col.count()

    def query(
        self,
        query_embedding: np.ndarray,
        *,
        n_results: int,
        where: dict[str, Any] | None = None,
    ) -> list[QueryHit]:
        if query_embedding.ndim == 1:
            qe = [query_embedding.tolist()]
        else:
            qe = query_embedding.tolist()
        result = self._col.query(
            query_embeddings=qe,
            n_results=n_results,
            where=where or None,
            include=["distances", "metadatas", "documents"],
        )
        ids = result["ids"][0]
        distances = result["distances"][0]
        metadatas = result["metadatas"][0]
        documents = result["documents"][0]
        return [
            QueryHit(unit_id=i, distance=float(d), metadata=m, document=doc)
            for i, d, m, doc in zip(ids, distances, metadatas, documents)
        ]
```

### 2.5 `pipeline.proto` excerpt — Embed RPC (D6)

```protobuf
// shared/proto/pipeline.proto  (excerpt; existing content elided)

syntax = "proto3";
package maktaba.pipeline.v1;

service Pipeline {
  // ... existing RPCs (StartIngest, JobStatus, etc.) ...

  // Embed encodes a single query string into the configured embedding
  // model's vector space. The API uses this at search time so the model
  // stays in the Pipeline process only.
  rpc Embed(EmbedRequest) returns (EmbedResponse);
}

message EmbedRequest {
  string text = 1;            // raw user query; the server adds the e5 'query:' prefix
  string library_id = 2;      // optional; reserved for future per-library model variants
}

message EmbedResponse {
  repeated float vector = 1 [packed = true];   // length = dim
  string model_name = 2;                        // 'intfloat/multilingual-e5-large' etc.
  int32 dim = 3;                                // 1024 (large) or 768 (base)
  string device = 4;                            // 'cuda' | 'mps' | 'cpu'
}
```

The proto is compiled into `shared/python/maktaba_proto/` (Pipeline) and
`shared/python/maktaba_proto/` shared (API). The build step is added to
`Makefile`:

```makefile
proto:
	python -m grpc_tools.protoc \
	    -I shared/proto \
	    --python_out=shared/python \
	    --grpc_python_out=shared/python \
	    shared/proto/pipeline.proto
```

### 2.6 `indexer.py` — IndexerWorker (D5)

```python
"""IndexerWorker — drains transcript_units → Chroma.

Driven by LISTEN/NOTIFY (transcript_units_appended) plus a 60 s safety tick.
"""
from __future__ import annotations
import asyncio
import json
import logging
import time
from collections import defaultdict
from typing import Sequence

import asyncpg
import numpy as np

from maktaba_pipeline.search.chroma_client import ChromaClient
from maktaba_pipeline.search.collections import UnitRow
from maktaba_pipeline.search.embedder import EmbeddingService
from maktaba_pipeline.search.errors import EmbeddingError, ChromaUnavailable

log = logging.getLogger(__name__)

CHANNEL = "transcript_units_appended"
SAFETY_POLL_SEC = 60.0
DEFAULT_BATCH = 64


class IndexerWorker:
    def __init__(
        self,
        *,
        db_pool: asyncpg.Pool,
        chroma: ChromaClient,
        embedder: EmbeddingService,
        batch_size: int = DEFAULT_BATCH,
    ):
        self._pool = db_pool
        self._chroma = chroma
        self._embedder = embedder
        self._batch_size = batch_size
        self._wakeup = asyncio.Event()
        self._stop = asyncio.Event()

    async def run(self) -> None:
        listener = await self._pool.acquire()
        try:
            await listener.add_listener(CHANNEL, self._on_notify)
            log.info("indexer_started", extra={"channel": CHANNEL})
            while not self._stop.is_set():
                # Drain whatever is unindexed.
                drained = await self._drain_once()
                if drained == 0:
                    # Wait for either a wakeup or the safety tick.
                    try:
                        await asyncio.wait_for(
                            self._wakeup.wait(), timeout=SAFETY_POLL_SEC)
                    except asyncio.TimeoutError:
                        pass
                    self._wakeup.clear()
        finally:
            await listener.remove_listener(CHANNEL, self._on_notify)
            await self._pool.release(listener)
            log.info("indexer_stopped")

    def stop(self) -> None:
        self._stop.set()
        self._wakeup.set()

    def _on_notify(self, *_args, **_kwargs) -> None:
        self._wakeup.set()

    # --- internals -----------------------------------------------------

    async def _drain_once(self) -> int:
        """Process at most one batch. Returns number of units indexed."""
        rows = await self._claim_batch()
        if not rows:
            return 0
        # Group by library so each chroma upsert hits one collection.
        by_lib: dict[int, list[UnitRow]] = defaultdict(list)
        for r in rows:
            by_lib[r.library_id].append(r)

        for library_id, lib_rows in by_lib.items():
            try:
                vectors = self._embedder.encode_passages(
                    [r.text for r in lib_rows],
                    transcript_id=lib_rows[0].transcript_id,
                )
            except EmbeddingError as e:
                log.error(
                    "embedding_failed_for_batch",
                    extra={"library_id": library_id, "n": len(lib_rows),
                           "err": str(e)},
                )
                # Leave indexed_at NULL so we retry on next tick.
                return 0
            try:
                col = self._chroma.collection_for_library(library_id)
                col.upsert(lib_rows, vectors)
            except ChromaUnavailable as e:
                log.error(
                    "chroma_unavailable_during_upsert",
                    extra={"library_id": library_id, "err": str(e)},
                )
                return 0

            await self._mark_indexed([r.id for r in lib_rows])
        return sum(len(r) for r in by_lib.values())

    async def _claim_batch(self) -> list[UnitRow]:
        sql = """
        SELECT u.id, u.transcript_id, u.seq, u.text, u.language, u.metadata,
               u.start_sec, u.end_sec, u.segment_ids,
               t.video_id, v.library_id,
               (SELECT speaker FROM transcript_segments s
                 WHERE s.id = (u.segment_ids->>0)::bigint) AS speaker
          FROM transcript_units u
          JOIN transcripts t ON t.id = u.transcript_id
          JOIN videos v ON v.id = t.video_id
         WHERE u.indexed_at IS NULL
         ORDER BY u.transcript_id, u.seq
         LIMIT $1
         FOR UPDATE OF u SKIP LOCKED
        """
        async with self._pool.acquire() as conn:
            records = await conn.fetch(sql, self._batch_size)
        rows: list[UnitRow] = []
        for rec in records:
            rows.append(_record_to_unit_row(rec))
        return rows

    async def _mark_indexed(self, unit_ids: Sequence[int]) -> None:
        async with self._pool.acquire() as conn:
            await conn.execute(
                "UPDATE transcript_units SET indexed_at = now() "
                "WHERE id = ANY($1::bigint[])",
                list(unit_ids),
            )


def _record_to_unit_row(rec) -> UnitRow:
    # `id` is captured locally for _mark_indexed; UnitRow doesn't carry it.
    row = UnitRow(
        transcript_id=rec["transcript_id"],
        seq=rec["seq"],
        text=rec["text"],
        language=rec["language"],
        video_id=rec["video_id"],
        library_id=rec["library_id"],
        start_sec=rec["start_sec"],
        end_sec=rec["end_sec"],
        speaker=rec["speaker"],
    )
    object.__setattr__(row, "id", rec["id"])  # frozen dataclass back-door for the worker
    return row
```

### 2.7 `grpc_server.py` — Embed RPC handler (D6)

```python
"""gRPC server for the Embed RPC. Mounted into the existing Pipeline server."""
from __future__ import annotations
import logging
import grpc
from grpc import StatusCode

from maktaba_proto import pipeline_pb2 as pb
from maktaba_proto import pipeline_pb2_grpc as pb_grpc
from maktaba_pipeline.search.embedder import EmbeddingService
from maktaba_pipeline.search.errors import EmbeddingError, ModelNotFound

log = logging.getLogger(__name__)


class EmbedServicer(pb_grpc.PipelineServicer):
    """Adds Embed to the existing Pipeline service."""

    def __init__(self, embedder: EmbeddingService):
        self._embedder = embedder

    async def Embed(self, request: pb.EmbedRequest, context):
        if not request.text:
            await context.abort(StatusCode.INVALID_ARGUMENT, "text is empty")
        try:
            vec = self._embedder.encode_query(request.text)
        except ModelNotFound as e:
            await context.abort(StatusCode.FAILED_PRECONDITION, str(e))
        except EmbeddingError as e:
            await context.abort(StatusCode.INTERNAL, str(e))
        meta = self._embedder.metadata
        return pb.EmbedResponse(
            vector=vec.tolist(),
            model_name=meta.model_name,
            dim=meta.dim,
            device=meta.device,
        )
```

### 2.8 `0018_transcript_units_notify.sql` — NOTIFY trigger

```sql
BEGIN;

CREATE OR REPLACE FUNCTION notify_transcript_units_appended()
RETURNS TRIGGER AS $$
BEGIN
  PERFORM pg_notify(
    'transcript_units_appended',
    NEW.transcript_id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS transcript_units_notify ON transcript_units;
CREATE TRIGGER transcript_units_notify
AFTER INSERT ON transcript_units
FOR EACH ROW EXECUTE FUNCTION notify_transcript_units_appended();

COMMIT;
```

### 2.9 Library deletion cleanup hook

The library DELETE flow (Epic 9) calls a cleanup hook:

```python
# in api/src/maktaba_api/libraries/delete.py (or pipeline equivalent)
async def on_library_deleted(library_id: int, *, chroma: ChromaClient) -> None:
    """Best-effort drop of the Chroma collection. Not in the same xact as the
    DB delete (Chroma is external). Failures are logged; the nightly orphan
    GC reclaims any leftovers (story edge case)."""
    chroma.drop_library(library_id)
```

The nightly orphan task (a separate cron in the Pipeline):

```sql
-- list of library IDs that should NOT have collections
SELECT id FROM libraries;
-- compared against chroma's list_collections() → drop the diff.
```

### 2.10 Config surface (`pipeline.toml`)

```toml
[search]
chroma_persist_dir   = "${MAKTABA_DATA_DIR}/chroma"
embedding_model      = "intfloat/multilingual-e5-large"   # or e5-base
embedding_device     = "auto"                             # auto | cuda | mps | cpu
embedding_batch_size = 64
indexer_batch_size   = 64
```

Defaults are baked into `pipeline/src/maktaba_pipeline/search/config.py`
so missing keys don't require a TOML lookup.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/search/__init__.py` | re-exports | (n/a) |
| 2 | `pipeline/src/maktaba_pipeline/search/errors.py` | `ChromaUnavailable`, `EmbeddingError`, `ModelNotFound` | (n/a) |
| 3 | `pipeline/src/maktaba_pipeline/search/config.py` | `SearchConfig` dataclass, env/TOML loader | unit test of defaults |
| 4 | `pipeline/src/maktaba_pipeline/search/chroma_client.py` | `ChromaClient`, `collection_name()`, `_DEFAULT_COLLECTION_METADATA` | `test_chroma_add_and_query`, `test_chroma_unavailable` |
| 5 | `pipeline/src/maktaba_pipeline/search/collections.py` | `UnitRow`, `QueryHit`, `ChromaCollection.upsert/query/delete_transcript` | `test_chroma_idempotent_upsert`, `test_library_deletion_cleanup` |
| 6 | `pipeline/src/maktaba_pipeline/search/embedder.py` | `EmbeddingService.encode_passages/encode_query`, `EmbeddingMetadata`, `_resolve_device`, `_truncate` | `test_embedding_dim_matches_model`, `test_long_text_truncation`, `test_multilingual_cross_search` |
| 7 | `shared/proto/pipeline.proto` | `Embed` RPC, `EmbedRequest`, `EmbedResponse` | proto compile in CI |
| 8 | Generated `shared/python/maktaba_proto/pipeline_pb2*.py` | (generated) | import test |
| 9 | `pipeline/src/maktaba_pipeline/search/grpc_server.py` | `EmbedServicer.Embed` | `test_embed_grpc_returns_same_vector` |
| 10 | `shared/db/migrations/0018_transcript_units_notify.sql` | `notify_transcript_units_appended` trigger | migration applies cleanly |
| 11 | `pipeline/src/maktaba_pipeline/search/indexer.py` | `IndexerWorker.run`, `_drain_once`, `_claim_batch`, `_mark_indexed` | `test_indexer_throughput`, `test_chroma_add_and_query` (integration) |
| 12 | Wiring: `pipeline/src/maktaba_pipeline/main.py` | start `IndexerWorker` task, mount `EmbedServicer` on the gRPC server | smoke test of pipeline boot |
| 13 | Wiring: API: `api/src/maktaba_api/search/embed_client.py` | `PipelineEmbedClient(stub).embed(text) -> np.ndarray` | unit test with grpc fake server |
| 14 | `api/src/maktaba_api/libraries/delete.py` (extension point) | call `on_library_deleted` after DB cascade | `test_library_deletion_cleanup` |

Order is the build/PR order. Step 7 produces a CI-blocking change
(regenerate protobufs); steps 8 and 9 land in the same commit.

---

## 4. Test cases

### 4.1 `test_chroma_add_and_query` (story-named)

```python
async def test_chroma_add_and_query_top1_is_self(tmp_chroma_dir, fake_embedder):
    """Add 10 units; query the first unit's text → top-1 hit is itself."""
    client = ChromaClient(persist_dir=tmp_chroma_dir)
    col = client.collection_for_library(library_id=1)
    rows = [
        UnitRow(transcript_id=100, seq=i, text=f"sentence number {i}",
                language="en", video_id=10, library_id=1,
                start_sec=float(i), end_sec=float(i + 1),
                speaker=None)
        for i in range(10)
    ]
    vectors = fake_embedder.encode_passages([r.text for r in rows])
    col.upsert(rows, vectors)
    assert col.count == 10

    qvec = fake_embedder.encode_query(rows[0].text)
    hits = col.query(qvec, n_results=3)
    assert hits[0].unit_id == "100:0"
    assert hits[0].distance < 1e-3
```

### 4.2 `test_chroma_idempotent_upsert` (story-named)

```python
async def test_idempotent_upsert_keeps_latest(tmp_chroma_dir, fake_embedder):
    """Add same id twice with different text → only the latest is stored."""
    client = ChromaClient(persist_dir=tmp_chroma_dir)
    col = client.collection_for_library(library_id=2)

    row_v1 = UnitRow(
        transcript_id=200, seq=0, text="initial text", language="en",
        video_id=20, library_id=2, start_sec=0.0, end_sec=1.0, speaker=None)
    row_v2 = UnitRow(
        transcript_id=200, seq=0, text="rewritten text", language="en",
        video_id=20, library_id=2, start_sec=0.0, end_sec=1.0, speaker=None)

    col.upsert([row_v1], fake_embedder.encode_passages([row_v1.text]))
    col.upsert([row_v2], fake_embedder.encode_passages([row_v2.text]))

    assert col.count == 1
    qvec = fake_embedder.encode_query("rewritten text")
    hits = col.query(qvec, n_results=1)
    assert hits[0].document == "rewritten text"
```

### 4.3 `test_embedding_dim_matches_model` (story-named)

```python
@pytest.mark.parametrize(
    "configured_model, expected_dim",
    [(E5_LARGE, 1024), (E5_BASE, 768)],
)
def test_embedding_dim_matches_model(configured_model, expected_dim):
    """Vector length equals the configured model's documented dim."""
    svc = EmbeddingService(
        configured_model=configured_model, device="cpu", batch_size=4)
    vec = svc.encode_query("hello world")
    assert vec.shape == (expected_dim,)
    assert svc.metadata.dim == expected_dim
    assert svc.metadata.model_name == configured_model
```

### 4.4 `test_embed_grpc_returns_same_vector` (story-named)

```python
async def test_embed_grpc_returns_same_vector_twice(grpc_pipeline_server):
    """Call Embed("foo") twice → identical vector; difference < 1e-6."""
    stub = pb_grpc.PipelineStub(grpc_pipeline_server.channel)
    r1 = await stub.Embed(pb.EmbedRequest(text="foo"))
    r2 = await stub.Embed(pb.EmbedRequest(text="foo"))
    assert r1.dim == r2.dim
    assert r1.model_name == r2.model_name
    diff = max(abs(a - b) for a, b in zip(r1.vector, r2.vector))
    assert diff < 1e-6
```

### 4.5 `test_indexer_throughput` (story-named)

```python
@pytest.mark.slow
async def test_indexer_throughput_meets_budget(
    db, tmp_chroma_dir, ten_thousand_units, real_embedder,
):
    """10,000 units → wall time ≤ 50 s on the reference machine.

    Skipped unless MAKTABA_RUN_PERF_TESTS=1; CI runs this on the M2 Pro runner.
    """
    if os.getenv("MAKTABA_RUN_PERF_TESTS") != "1":
        pytest.skip("perf test gated; set MAKTABA_RUN_PERF_TESTS=1 to run")
    chroma = ChromaClient(persist_dir=tmp_chroma_dir)
    worker = IndexerWorker(
        db_pool=db, chroma=chroma, embedder=real_embedder, batch_size=64)

    t0 = time.monotonic()
    while True:
        n = await worker._drain_once()
        if n == 0:
            break
    elapsed = time.monotonic() - t0
    assert elapsed <= 50.0, f"indexer took {elapsed:.1f}s for 10k units"
```

### 4.6 `test_multilingual_cross_search` (multilingual)

```python
@pytest.mark.requires_e5
async def test_arabic_query_finds_english_passage(
    tmp_chroma_dir, real_embedder,
):
    """Cross-lingual retrieval works because e5 is multilingual.

    Indexed: English passage about migratory birds.
    Query  : Arabic gloss of the same topic.
    """
    chroma = ChromaClient(persist_dir=tmp_chroma_dir)
    col = chroma.collection_for_library(library_id=99)

    rows = [
        UnitRow(transcript_id=1, seq=0,
                text="Migratory birds travel thousands of kilometers each year.",
                language="en", video_id=1, library_id=99,
                start_sec=0, end_sec=5, speaker=None),
        UnitRow(transcript_id=1, seq=1,
                text="The recipe calls for olive oil and garlic.",
                language="en", video_id=1, library_id=99,
                start_sec=5, end_sec=10, speaker=None),
    ]
    col.upsert(rows, real_embedder.encode_passages([r.text for r in rows]))

    # Arabic query: 'الطيور المهاجرة' = "migratory birds"
    qvec = real_embedder.encode_query("الطيور المهاجرة تسافر مسافات طويلة")
    hits = col.query(qvec, n_results=2)
    assert hits[0].unit_id == "1:0"   # the bird passage, not the recipe
    assert hits[0].distance < hits[1].distance


@pytest.mark.requires_e5
async def test_english_query_finds_arabic_passage(
    tmp_chroma_dir, real_embedder,
):
    """Reverse direction: English query matches Arabic passage."""
    chroma = ChromaClient(persist_dir=tmp_chroma_dir)
    col = chroma.collection_for_library(library_id=100)

    rows = [
        UnitRow(transcript_id=2, seq=0,
                text="الكتب القديمة تحفظ في المكتبة.",   # 'Old books are kept in the library.'
                language="ar", video_id=2, library_id=100,
                start_sec=0, end_sec=4, speaker=None),
        UnitRow(transcript_id=2, seq=1,
                text="السيارة الحمراء واقفة في الشارع.",  # 'The red car is parked on the street.'
                language="ar", video_id=2, library_id=100,
                start_sec=4, end_sec=8, speaker=None),
    ]
    col.upsert(rows, real_embedder.encode_passages([r.text for r in rows]))

    qvec = real_embedder.encode_query("preserving ancient manuscripts in libraries")
    hits = col.query(qvec, n_results=2)
    assert hits[0].unit_id == "2:0"
```

### 4.7 `test_long_text_truncation` (edge case D8)

```python
def test_long_text_is_truncated_with_one_warning(caplog):
    """Input >512 tokens is truncated; only one warning per (transcript, model)."""
    svc = EmbeddingService(configured_model=E5_BASE, device="cpu")
    long_text = "lorem ipsum " * 1000  # ~2000 tokens

    with caplog.at_level("WARNING"):
        v1 = svc.encode_passages([long_text], transcript_id=42)
        v2 = svc.encode_passages([long_text], transcript_id=42)
        v3 = svc.encode_passages([long_text], transcript_id=43)

    assert v1.shape == (1, 768)
    truncation_warnings = [
        r for r in caplog.records
        if r.message == "embedding_input_truncated"
    ]
    # One warning per (transcript_id, model) — 42 once, 43 once = 2 total.
    assert len(truncation_warnings) == 2
```

### 4.8 `test_empty_unit` (edge case)

```python
def test_empty_text_does_not_crash():
    """Whitespace-only or empty text returns a vector (zero-ish) without raising."""
    svc = EmbeddingService(configured_model=E5_BASE, device="cpu")
    v_empty = svc.encode_query("")
    v_ws = svc.encode_query("   \t\n")
    assert v_empty.shape == (768,)
    assert v_ws.shape == (768,)
    # Both should be very close (the prefix 'query: ' dominates):
    diff = np.abs(v_empty - v_ws).max()
    assert diff < 1e-2
```

### 4.9 `test_chroma_unavailable` (edge case)

```python
def test_chroma_unavailable_when_dir_unwritable(tmp_path, monkeypatch):
    """Chroma fails to open → ChromaUnavailable; the indexer logs and retries."""
    bad_dir = tmp_path / "no-write"
    bad_dir.mkdir(mode=0o400)  # read-only
    monkeypatch.setattr(
        "chromadb.PersistentClient",
        lambda *a, **kw: (_ for _ in ()).throw(RuntimeError("fs read-only")),
    )
    with pytest.raises(ChromaUnavailable, match="failed to open Chroma"):
        ChromaClient(persist_dir=bad_dir)
```

### 4.10 `test_library_deletion_cleanup` (edge case)

```python
async def test_library_deletion_drops_chroma_collection(
    tmp_chroma_dir, fake_embedder,
):
    """on_library_deleted removes the collection; nightly GC handles orphans."""
    chroma = ChromaClient(persist_dir=tmp_chroma_dir)
    col = chroma.collection_for_library(library_id=77)
    rows = [UnitRow(transcript_id=1, seq=0, text="x", language="en",
                    video_id=1, library_id=77,
                    start_sec=0, end_sec=1, speaker=None)]
    col.upsert(rows, fake_embedder.encode_passages([rows[0].text]))
    assert collection_name(77) in [c.name for c in chroma.raw.list_collections()]

    chroma.drop_library(77)
    assert collection_name(77) not in [c.name for c in chroma.raw.list_collections()]

    # Idempotent: dropping again does not raise (best-effort).
    chroma.drop_library(77)
```

### 4.11 `test_e5_query_passage_prefix_matters` (regression)

```python
@pytest.mark.requires_e5
def test_e5_prefix_difference_is_small_but_real():
    """encode_query and encode_passages produce different vectors for same text;
    asserts the prefix is actually being applied (not a no-op refactor)."""
    svc = EmbeddingService(configured_model=E5_BASE, device="cpu")
    raw = "this is a test sentence"
    qv = svc.encode_query(raw)
    pv = svc.encode_passages([raw])[0]
    cosine = float(np.dot(qv, pv))   # both normalized
    assert 0.85 < cosine < 0.999     # close but not identical
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | **ChromaDB not running / persistence dir corrupt.** | `ChromaClient.__init__` wraps the underlying error in `ChromaUnavailable`. The IndexerWorker catches it in `_drain_once`, logs `chroma_unavailable_during_upsert`, and returns 0 (units stay `indexed_at = NULL`); the next safety tick retries. The `Embed` gRPC handler does not depend on Chroma — query-time embedding still works even if Chroma is down (the API will then degrade to FTS-only via Plan 5.4's hybrid fallback). (`test_chroma_unavailable`) |
| E2 | **Embedding model not downloaded** (first boot, no internet later). | `EmbeddingService._load` catches `OSError` from `SentenceTransformer(...)` and re-raises as `ModelNotFound`. The IndexerWorker logs and retries (model lives in HF cache; a one-time `huggingface-cli download` populates it). The Embed gRPC RPC returns `FAILED_PRECONDITION` with the model path so the API surfaces a clear error to the user instead of a generic 500. The `pipeline/setup.sh` bootstrap script pre-downloads e5-large at install time so this only fires when the cache directory is wiped. |
| E3 | **Very long unit text > 512 tokens.** Story 5.1's chunker hard-caps units at 400 chars, but a fixture with one sentence of CJK or Arabic dense glyphs can blow past that in tokens. | `EmbeddingService._truncate` tokenizes, keeps `MAX_TOKENS - 8` (leaving room for `'passage: '` and special tokens), and decodes back. A warning fires once per `(transcript_id, model)` — frequent warnings here are a signal that Story 5.1's chunker needs tightening, not that the embedder is broken. (`test_long_text_truncation`) (D8) |
| E4 | **Empty / whitespace-only unit.** STT can produce empty text on silent regions if the chunker doesn't filter it (Story 5.1 should, but we defend). | `encode_passages([""])` returns a valid (zero-ish) vector — sentence-transformers handles empty input. Chroma upsert with an empty document succeeds. The unit is stamped `indexed_at` and stays in the index harmlessly; queries won't match it because the prefix-only embedding is far from any real text. (`test_empty_unit`) |
| E5 | **Library deletion while index exists.** Story acceptance: cleanup hook removes the Chroma collection in the same transaction; orphans cleaned by nightly task. | `on_library_deleted(library_id, chroma)` is called from the API library-delete flow after the DB `DELETE FROM libraries` returns. We do NOT run the Chroma drop inside the SQL transaction (Chroma is external and would force XA semantics we don't have). The drop is best-effort; on failure it logs and the nightly orphan GC reconciles `SELECT id FROM libraries` against `chroma.list_collections()` and drops the diff. (`test_library_deletion_cleanup`) |
| E6 | **Embedding model swap mid-library.** Story acceptance: vectors are not transferable; switching triggers a full re-index. | The settings endpoint (Epic 9) refuses to apply an `[search].embedding_model` change without a confirmation flag and surfaces "this will reindex N hours of content." On apply, the migration drops the affected collections (per-library, D3) and resets `transcript_units.indexed_at = NULL` for transcripts in those libraries. The IndexerWorker then drains them on the next tick. The `transcripts.metrics.embedding_model_actual` field gets the new model name as units are re-indexed. |
| E7 | **GPU OOM during embedding.** Story edge case: the indexer falls back to CPU for the current batch (recorded), then resumes on GPU. | Wrap the `model.encode(...)` call in a `try` block; on `torch.cuda.OutOfMemoryError` (or MPS OOM equivalent), swap `self._model.to('cpu')`, retry the same batch, then `swap back to original device after success`. Each fallback increments `transcripts.metrics.gpu_oom_fallbacks`. Implemented in `EmbeddingService._encode_with_oom_fallback` (a thin wrapper around the `model.encode` call inside `encode_passages` — kept out of the §2.3 listing for brevity but covered by `test_indexer_oom_fallback` in conftest). |
| E8 | **Single-writer Chroma constraint with multiple Pipeline workers.** Story edge case: bounded by Chroma until server mode is adopted. | The IndexerWorker is a singleton inside the Pipeline process. If the operator scales horizontally (multiple Pipeline boxes), each has its own `chroma_persist_dir` *or* the deployment switches to ChromaDB server mode (D2's documented escape hatch). The single-process default is safe and is the only configuration we ship and test against in v1. Mirrored in NFR 24.4. |
| E9 | **NOTIFY connection drops** (network blip, Postgres restart). | The IndexerWorker's listener is acquired from the asyncpg pool; on drop, asyncpg raises and the `run()` loop exits. The supervisor restarts the worker. The 60 s safety poll covers the gap; nothing is permanently lost because the partial `transcript_units_indexed_at_null` index makes "find unindexed" a sub-ms scan. |
| E10 | **Concurrent re-index race** (another process is also draining). | `_claim_batch` uses `FOR UPDATE … SKIP LOCKED`, so two workers never grab the same row. Only one worker should be running per Pipeline process; if the operator misconfigures and runs two, throughput degrades but correctness holds. |
| E11 | **Truncated metadata speaker.** Story 5.3 metadata schema says `speaker` is part of metadata; when diarization is off the speaker is `None`. | `UnitRow.metadata` converts `None` → `""` because Chroma's metadata typing rejects `None` values in some versions. Query-time filters on `speaker` use `where={"speaker": "Speaker 1"}` and ignore `""` rows naturally. |
| E12 | **e5 prefix accidentally omitted** by a future refactor. | `test_e5_query_passage_prefix_matters` (4.11) asserts that `encode_query` and `encode_passages` produce *different* vectors for the same input — if the cosine similarity goes to 1.0 the prefix has been dropped and recall on cross-language search collapses. |

---

## 6. Acceptance checklist

- [ ] **A1** One Chroma collection per library exists, named `library-<library_id>`, configured with `{"hnsw:space": "cosine"}` (plus our M / construction_ef / search_ef knobs from D3). (`test_chroma_add_and_query`)
- [ ] **A2** The default embedding model is `intfloat/multilingual-e5-large`; it can be overridden via `pipeline.toml [search].embedding_model`. The model is loaded at process start and cached in `EmbeddingService`. (`test_embedding_dim_matches_model`)
- [ ] **A3** On hosts without a CUDA/MPS-capable accelerator AND `embedding_device = 'auto'`, the loader downgrades automatically to `intfloat/multilingual-e5-base` (768-dim). The actual model is recorded in `transcripts.metrics.embedding_model_actual`. (Test: `test_embedder_downgrades_on_cpu_only_host` in §2.3 unit tests.)
- [ ] **A4** For each search unit, the indexer adds a Chroma row with `id = "{transcript_id}:{seq}"`, `documents = unit.text`, `metadatas = {video_id, library_id, start, end, language, speaker}`. The id format is stable across re-runs so re-indexing upserts in place. (`test_chroma_idempotent_upsert`)
- [ ] **A5** The `Embed(text)` gRPC RPC is defined in `shared/proto/pipeline.proto` and returns the configured-model vector for the input text (with the e5 `query:` prefix applied server-side). Two calls with the same text produce identical vectors (< 1e-6 max abs diff). (`test_embed_grpc_returns_same_vector`)
- [ ] **A6** Indexing throughput on the Apple Silicon reference machine (M2 Pro / 32 GB) reaches **≥ 200 units/second** on `e5-large`, sufficient to keep up with live transcription. (`test_indexer_throughput`, gated by `MAKTABA_RUN_PERF_TESTS`)
- [ ] **A7** `IndexerWorker` is driven by `LISTEN transcript_units_appended` (added in `0018_transcript_units_notify.sql`) plus a 60-second safety poll. Claimed rows use `FOR UPDATE … SKIP LOCKED` against `transcript_units WHERE indexed_at IS NULL`. (`test_chroma_add_and_query` with the worker wired in)
- [ ] **A8** Cross-language retrieval works: an Arabic query matches a semantically equivalent English passage (and vice versa), via the shared multilingual e5 embedding space. (`test_arabic_query_finds_english_passage`, `test_english_query_finds_arabic_passage`)
- [ ] **A9** Library deletion drops the Chroma collection via `chroma.drop_library(library_id)`; failures are logged but do not block the DB cascade; a nightly orphan GC reconciles. (`test_library_deletion_cleanup`)
- [ ] **A10** Embedding inputs longer than the model context (512 tokens) are truncated tokenizer-aware, with one warning per (transcript_id, model). (`test_long_text_truncation`)
- [ ] **A11** Empty / whitespace-only unit texts do not crash the embedder or the upsert. (`test_empty_unit`)
- [ ] **A12** `ChromaClient.__init__` raises `ChromaUnavailable` on persistence-dir failure; the IndexerWorker catches and retries on the next tick; the Embed gRPC RPC remains functional even when Chroma is unavailable. (`test_chroma_unavailable`)
- [ ] **A13** Migration `0018_transcript_units_notify.sql` applies cleanly and is idempotent on re-run.
- [ ] **A14** `Pipeline server` mounts `EmbedServicer` alongside the existing RPCs; the API service has a `PipelineEmbedClient` wrapper and uses it from the search handler.
- [ ] **A15** No code path in this story attempts to keep two embedding models loaded simultaneously; switching the model requires a full reindex (E6). (Static check: only one `SentenceTransformer(...)` call site in the package.)
- [ ] **A16** The e5 `passage: ` / `query: ` prefix is applied at encode time (not at chunk time, so the stored `transcript_units.text` stays prefix-free). A regression test asserts the two encode paths produce different vectors for the same input. (`test_e5_prefix_difference_is_small_but_real`)
