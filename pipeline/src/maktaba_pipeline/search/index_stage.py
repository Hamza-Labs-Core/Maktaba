"""Track R4 — INDEX-stage glue (the TRANSCRIBE analogue for vector search).

The INDEX stage consumes exactly what TRANSCRIBE persisted: a complete,
active ``transcripts`` row plus its committed ``transcript_segments``
(the create-then-activate ordering means the transcript the INDEX job
points at is always complete). This module is to INDEX what
:mod:`maktaba_pipeline.stt.transcribe` is to TRANSCRIBE — the heavy
logic the thin :func:`maktaba_pipeline.handlers.index_handler` adapter
delegates to:

- :func:`load_segment_docs` reconstructs the
  :class:`~maktaba_pipeline.search.embedder.SegmentDoc` list for the
  job's ``transcript_id`` (the transcript header for ``video_id`` /
  ``language``, then every ``transcript_segments`` row ordered by
  ``seq``). It returns ``None`` when the transcript header is missing
  (a data inconsistency — TRANSCRIBE only enqueues INDEX *after*
  activating a complete transcript) and an empty list when the header
  exists but has no segments.
- :func:`default_index_target` is the production DI default for the
  handler's collection/embed seam: it resolves the *configured*
  process-wide Chroma collection via
  :func:`maktaba_pipeline.search.embedder.make_collection` (collection
  name + persist dir come from config/env, never hardcoded vendor
  choices). Tests inject a fake returning an in-memory collection + a
  deterministic embed fn so the suite never loads a model or touches a
  real vector store.
- :func:`commit_index` is the INDEX analogue of
  :func:`maktaba_pipeline.stt.transcribe.commit_transcribe`: it calls
  the existing :func:`maktaba_pipeline.search.embedder.index_segments`
  (the embed + upsert hot path stays there — NOT reimplemented here),
  advances the FSM ``TRANSCRIBED -> INDEXED`` via
  :func:`advance_after_stage` (replay-guarded exactly like
  ``commit_transcribe``), and returns the upsert count.

Idempotency: :func:`index_segments` keys every vector by
``embed_id_for(transcript_id, seq)`` — deterministic, so re-running
INDEX upserts the *same* ids (Chroma upsert overwrites in place; no
duplicate vectors). The loader does not need a bespoke id scheme; the
existing ``{transcript_id}:{seq}`` key already supports replay.

NOTE(wave-0): that determinism only covers replay of the *same*
transcript. Re-transcription creates a NEW ``transcript_id``, so INDEX
writes a DISJOINT vector-id set and the prior transcript's vectors are
NOT deleted — no delete-by-transcript path exists yet, by design.
Consequence: stale vectors from a superseded transcript stay queryable
(search-relevance drift) and the vector store grows unbounded across
re-transcriptions. Deterministic-id replay does NOT reconcile this (it
only overwrites the same ``transcript_id:seq``); supersede-cleanup
(delete-by-transcript on re-transcription) is a SEPARATE later story —
deliberately deferred here, not silent.

Scope (Wave 0): exactly one job -> one transcript -> one collection,
straight-through. Re-embedding policy on a transcript revision,
multi-collection / per-library collection routing, and hybrid-search
wiring are SEPARATE later stories — see the NOTE markers below.
"""

from __future__ import annotations

import os
from collections.abc import Awaitable, Callable
from typing import Any, Protocol
from uuid import UUID

from .embedder import ChromaCollection, EmbeddingFunction, SegmentDoc

__all__ = [
    "IndexTarget",
    "IndexTargetResolver",
    "commit_index",
    "default_index_target",
    "load_segment_docs",
]


class _IndexDB(Protocol):
    """The connection shape this module needs.

    A strict subset of ``commit_transcribe``'s ``_TranscribeDB`` (it
    only reads the transcript header + segments and drives
    ``advance_after_stage``); the runtime ``Database`` facade satisfies
    it. Tests pass the canonical fake.
    """

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


class IndexTarget:
    """The resolved vector-store destination for one INDEX run.

    ``collection`` is the Chroma collection the segments upsert into;
    ``embed`` is the embedding callback (``None`` => the collection's
    own bound embedding function handles it — the production default
    when sentence-transformers is wired). The handler injects this via
    the DI seam; tests substitute a fake collection + deterministic
    embed fn so no model loads and no network is touched.
    """

    __slots__ = ("collection", "embed")

    def __init__(
        self,
        collection: ChromaCollection,
        embed: EmbeddingFunction | None = None,
    ) -> None:
        self.collection = collection
        self.embed = embed


# DI seam for the handler: ``(*, video_id) -> IndexTarget``. The default
# resolves the *configured* process-wide Chroma collection; tests inject
# a fake returning an in-memory collection so the suite never loads a
# model / touches a real vector DB (mirrors PROBE/EXTRACT/TRANSCRIBE
# seams).
IndexTargetResolver = Callable[..., Awaitable[IndexTarget]]


# Default collection name + persist dir. These default to the existing
# configured process-wide values (env override, stable on-disk path);
# per-library / per-video collection routing is a SEPARATE settings-
# plumbing story (see NOTE in default_index_target), not an INDEX-stage
# change — the seam keeps the handler agnostic.
_DEFAULT_COLLECTION = "maktaba_segments"
_COLLECTION_ENV = "MAKTABA_CHROMA_COLLECTION"
_PERSIST_DIR_ENV = "MAKTABA_CHROMA_DIR"


_SELECT_TRANSCRIPT_HEADER = """
SELECT id, video_id, language
  FROM transcripts
 WHERE id = $1
"""

_SELECT_SEGMENTS_ORDERED = """
SELECT id, seq, start_sec, end_sec, text, speaker
  FROM transcript_segments
 WHERE transcript_id = $1
 ORDER BY seq ASC
"""


async def load_segment_docs(
    db: _IndexDB,
    *,
    transcript_id: UUID,
) -> list[SegmentDoc] | None:
    """Reconstruct the :class:`SegmentDoc` list for ``transcript_id``.

    Reads the transcript header (for ``video_id`` + ``language``) then
    every ``transcript_segments`` row ordered by ``seq``. Returns
    ``None`` when the transcript header row is absent — the caller
    treats that as an unrecoverable data inconsistency (TRANSCRIBE only
    enqueues INDEX *after* activating a complete transcript). Returns an
    empty list when the header exists but has no segments (also a data
    inconsistency: a committed transcript always has >= 1 segment).
    """
    header = await db.fetchrow(_SELECT_TRANSCRIPT_HEADER, transcript_id)
    if header is None:
        return None

    video_id = header["video_id"]
    if not isinstance(video_id, UUID):
        video_id = UUID(str(video_id))
    language = str(header["language"])

    rows = await db.fetch(_SELECT_SEGMENTS_ORDERED, transcript_id)
    docs: list[SegmentDoc] = []
    for row in rows:
        docs.append(
            SegmentDoc(
                segment_id=int(row["id"]),
                transcript_id=transcript_id,
                video_id=video_id,
                seq=int(row["seq"]),
                start_sec=float(row["start_sec"]),
                end_sec=float(row["end_sec"]),
                text=str(row["text"]),
                language=language,
                speaker=row["speaker"],
            )
        )
    return docs


async def default_index_target(
    *,
    video_id: UUID,  # noqa: ARG001 — part of the resolver seam signature
    settings: Any | None = None,  # noqa: ARG001 — see NOTE below
) -> IndexTarget:
    """Production default for the handler's collection/embed DI seam.

    Resolves the *configured* process-wide Chroma collection via
    :func:`maktaba_pipeline.search.embedder.make_collection`. The
    collection name and on-disk persist directory come from the
    environment (``MAKTABA_CHROMA_COLLECTION`` / ``MAKTABA_CHROMA_DIR``)
    defaulting to the stable process-wide values — defaulting to the
    existing configured default is intentional, not an infra guess.
    ``chromadb`` is imported lazily inside ``make_collection`` so
    importing this module costs nothing.

    NOTE(wave-0): per-library / per-video collection routing (a
    ``video_id``-scoped collection name, the gap-analysis Chroma
    single-writer concern, and the ``embedding.model``/``device``
    library-settings plumbing) is a SEPARATE settings story (Epic 5.x /
    9.1) — deliberately deferred, not silent. Until it lands this
    resolves the one configured process-wide collection and lets
    Chroma's bound embedding function (``embed=None``) handle vectors.
    The ``video_id``/``settings`` params are kept so the future
    library-scoped resolver is a drop-in seam swap, not a handler change.
    """
    from .embedder import make_collection  # noqa: PLC0415

    name = os.getenv(_COLLECTION_ENV, _DEFAULT_COLLECTION)
    persist_dir = os.getenv(_PERSIST_DIR_ENV) or None
    collection = make_collection(name, persist_dir=persist_dir)
    return IndexTarget(collection=collection, embed=None)


async def commit_index(
    db: _IndexDB,
    *,
    video_id: UUID,
    transcript_id: UUID,
    docs: list[SegmentDoc],
    target: IndexTarget,
) -> tuple[int, str]:
    """Upsert ``docs`` into the vector store, advance the FSM.

    Returns ``(upsert_count, new_state)``. The INDEX analogue of
    :func:`maktaba_pipeline.stt.transcribe.commit_transcribe`:

    1. :func:`maktaba_pipeline.search.embedder.index_segments` embeds +
       upserts every doc (the hot path stays there — NOT reimplemented).
       Its id scheme is ``embed_id_for(transcript_id, seq)``, so a
       re-run upserts the *same* ids: Chroma overwrites in place, no
       duplicate vectors (idempotent on replay).
    2. advance the FSM ``TRANSCRIBED -> INDEXED`` via
       :func:`advance_after_stage` (its terminal-drop guard + the
       explicit state check make a replay a no-op — exactly the
       ``commit_transcribe`` shape). INDEX and SUBTITLE_GEN both branch
       off ``TRANSCRIBED`` and *converge* on ``INDEXED`` (the FSM has
       ``TRANSCRIBED --SUBTITLE_GEN/INDEX--OK--> INDEXED``); whichever
       runs second finds the state already past ``TRANSCRIBED`` and
       leaves it (the explicit ``== TRANSCRIBED`` guard), so the order
       of the two does not matter and neither double-advances.

    No follow-on enqueue: INDEX's FSM successor is ``THUMBNAIL``
    (``INDEXED --THUMBNAIL--OK--> THUMBNAILED``) but THUMBNAIL has no
    implementing module yet (mirrors SUBTITLE_GEN, which also does not
    enqueue a successor). Wiring THUMBNAIL is a SEPARATE story — not
    invented here.
    """
    from ..domain.states import Outcome, State, Trigger  # noqa: PLC0415
    from ..log import get_logger  # noqa: PLC0415
    from ..orchestrator.advance import advance_after_stage  # noqa: PLC0415
    from .embedder import index_segments  # noqa: PLC0415

    log = get_logger()

    upserted = index_segments(target.collection, docs, embed=target.embed)

    state_row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", video_id)
    if state_row is None:
        raise LookupError(f"video {video_id} not found")
    current_state = State(state_row["state"])

    if current_state == State.TRANSCRIBED:
        new_state = await advance_after_stage(db, video_id, Trigger.INDEX, Outcome.OK, log=log)
    else:
        # Replay / converged-with-SUBTITLE_GEN / out-of-order: leave the
        # row where it is. The FSM has no INDEX edge out of INDEXED (or
        # any later state), mirroring the commit_transcribe replay guard.
        new_state = current_state

    log.info(
        "index_committed",
        video_id=str(video_id),
        transcript_id=str(transcript_id),
        segments_indexed=upserted,
        new_state=str(new_state),
    )
    return upserted, str(new_state)
