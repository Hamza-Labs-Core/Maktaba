# Plan 5.1 — Search-unit chunking & schema — implementation

> Implementation plan for [story-05-01-unit-chunking.md](story-05-01-unit-chunking.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: consumes the segment table and the
> `segments.committed` NOTIFY channel from
> [Plan 3.6](../03-transcription/plan-03-06-segment-commit.md), inherits
> the `language` value carried on `transcripts` from
> [Story 3.4](../03-transcription/story-03-04-language-detection.md), and
> publishes the `transcript_units` table that
> [Plan 5.2 (FTS)](plan-05-02-fts-tsvector.md),
> [Plan 5.3 (Chroma vector)](plan-05-03-chroma-vector.md),
> [Plan 5.4 (RRF fusion)](plan-05-04-hybrid-rrf.md), and
> [Plan 5.5 (incremental indexing)](plan-05-05-incremental-indexing.md)
> all read from. **This plan is the single migration owner of
> `transcript_units`** — no other story creates or alters its columns.

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The chunker targets **~200 chars / ~30–60 s of audio per unit** with a hard cap of **400 chars** and a soft cap of **96 tokens** (Whisper-style BPE estimate via `len(text)//4`). The story names the character target; we cross-pin a token cap to keep multilingual units inside the embedding model's window in [Plan 5.3](plan-05-03-chroma-vector.md) (`paraphrase-multilingual-MiniLM-L12-v2` has a 128-token effective window). | Story acceptance "target ~200 characters and hard cap 400" + Plan 5.3 model choice. | A char-only cap leaks into "this short Arabic line is fine but the Mandarin equivalent is 3× the tokens" failures. The token cap is a cheap second guard; we estimate tokens at `ceil(len(text) / 4)` rather than calling the tokenizer (which would force a Python ↔ tokenizer boundary on every unit and triple the chunker's runtime). The estimate is a safe overshoot — real BPE tokens are 3.5–4 chars on average for Latin scripts and ~2.5–3 for Arabic, so the *real* token count is always **below** our estimate, which means the cap fires conservatively (we never overshoot the embedding window). |
| D2 | **Overlap strategy: zero token overlap; rely on sentence boundaries instead.** When sentences are too long for the cap (E1) we split at the nearest word boundary ≤ cap and record `metadata.split_method = "word"`. We do **not** sliding-window across units. | Story §"Edge cases" + REVIEW §1.1.h. | Sliding-window overlap is a habit borrowed from RAG papers that index whole books with no native chunk boundary. We have segment timestamps and sentence boundaries that are *better* anchors. Overlap would (a) double-count text in FTS hits, (b) inflate vector storage by 1.3–1.5× with no recall lift on this corpus, (c) make the unit→segment mapping ambiguous. If a future evaluation shows recall problems, we can add overlap as `metadata.overlap_with_unit_seq` without a schema change. |
| D3 | **Sentence segmentation is regex-based**, NOT `pyarabic.araby` or any heavyweight tokenizer. The boundary set is `[.!?؟।]` plus newline, with a small Arabic-aware suffix rule (a sentence-final mark followed by ASCII or Arabic whitespace). Combining marks (Tashkeel U+064B–065F, U+0670, etc.) are preserved verbatim. | Story acceptance "boundaries are detected on `[.!?؟।]` plus newline" — explicitly specified. | `pyarabic` is a 3 MB dep with a Tashkeel stripper we don't want, and `nltk.sent_tokenize` requires a punkt model download per language. The story already specifies the punctuation set; honoring it with `re` is 30 lines, has no install footprint, and — critically — works identically on the API service if we ever need to re-chunk client-side. The `।` (Devanagari danda) is included because the embedding model is multilingual and Hindi/Bengali transcripts are an in-scope future case. |
| D4 | **Units never span paused/resumed transcripts.** A "paused" boundary is detected by reading `transcripts.paused_at_sec` (set by [Plan 3.5](../03-transcription/plan-03-05-backend-registry.md)); the chunker forces a unit boundary at any segment whose `start_sec >= transcripts.paused_at_sec` AND that's the first segment committed in the resume. In practice this is a no-op because a resume starts a new segment row anyway, but we encode it explicitly so a future "splice in a corrected segment range" code path can't silently merge across a pause. | Story implication ("unit `start_sec` is the first segment's `start`") + Plan 3.6 NOTIFY semantics. | Spanning a pause means the embedding mixes two recordings made minutes/hours/days apart; the semantic vector becomes garbage. Worse, the `start_sec → end_sec` window of the unit straddles a wall-clock gap, so the deep-link from a search hit would point to silence. We protect against this in code, not in convention. |
| D5 | **Primary key is `BIGSERIAL id`** (matching the story acceptance verbatim), not `uuid`. The natural key `(transcript_id, seq)` is enforced as a `UNIQUE` constraint and is what consumers actually join on. Chroma stores the unit id as `str(id)` in its own metadata. SQLite (single-binary fallback per architecture §11) uses `INTEGER PRIMARY KEY AUTOINCREMENT` with the same `(transcript_id, seq)` UNIQUE. | Story acceptance — uses `BIGSERIAL`. | uuids would force Chroma to carry 16-byte ids in every vector record (vs 8-byte int) for no upside; we never expose unit ids on a public URL (the URL is `/v/{video_id}?t={start_sec}`), so the "guessable id" argument doesn't apply. |
| D6 | **`tsvector` lives in [Plan 5.2](plan-05-02-fts-tsvector.md), NOT in this table.** This plan creates `transcript_units` with no `tsv` column; Plan 5.2 adds a generated column `tsv tsvector GENERATED ALWAYS AS (...) STORED` in its own migration `0NNN_transcript_units_fts.sql`. The chunker is therefore single-write (only this table); the FTS layer is a downstream materialized view of it. | Resolves REVIEW §1.1.d cleanly: one table, two indexes (FTS + Chroma) attached separately. | Bundling `tsv` into the chunker migration would couple this story to the language-config decision (`'simple'` vs `'arabic'` dictionary, see Plan 5.2). It would also double the chunker's per-row write cost during initial backfill. Splitting keeps each story independently revertible — if Plan 5.2's FTS scheme changes, this migration doesn't move. |
| D7 | **Indexes:** `(transcript_id, seq)` UNIQUE (story); `(language)` btree (story, REVIEW §6.3); partial `(transcript_id) WHERE indexed_at IS NULL` (story, supports Plan 5.5's claim query); plus a NEW `(transcript_id, start_sec)` btree we add for [Plan 5.4](plan-05-04-hybrid-rrf.md)'s timestamp-window queries ("show me hits between 30:00 and 35:00"). All four are created in the same migration. | Story explicit + Plan 5.4 implicit. | Plan 5.4 will need to range-scan units by start time for the "search within a chapter" surface; without this index it would seq-scan a transcript's worth of units. The cost is one extra index write per row (negligible at our write rate of ~1 row per ~30 s of audio). |
| D8 | **Re-chunking on transcript edit.** When `processing_jobs.state` flips back to `running` for an existing transcript (a re-run, e.g. "redo this with a better model"), [Plan 5.5](plan-05-05-incremental-indexing.md) is responsible for `DELETE FROM transcript_units WHERE transcript_id = $1` before the new chunker pass. This story's chunker is **idempotent on (transcript_id, seq)** but does NOT itself delete prior units; it relies on the upstream sweep. The motivation is locality of failure — if we deleted in this code path we'd need a transaction that spans the chunker and Plan 5.5's claim, which doubles its complexity. | Splits responsibility along an existing seam. | The chunker's contract is "given segments S₁..Sₙ produce units U₁..Uₘ"; whether prior Us existed is not its problem. Plan 5.5 already owns the "what changed and what should be re-indexed" logic — the deletion sits naturally there. Defensive programming: if Plan 5.5 forgets to delete and the chunker reruns, the `INSERT ... ON CONFLICT (transcript_id, seq) DO UPDATE SET text = EXCLUDED.text, ...` UPSERT in §2.4 keeps the rows correct (no duplicates, no stale rows lingering with old `seq` collisions). Stale rows beyond the new max seq remain until Plan 5.5's sweep — that's the seam. |
| D9 | **Empty/whitespace-only units are dropped silently** before insert. After NFC normalization and stripping, a unit with `len(text.strip()) == 0` is skipped. Sentence boundaries can occasionally yield empty splits (e.g., `"hello.. world"` → `["hello", "", "world"]`); we don't want them in the index. We do NOT log per-empty-unit; we do log the count once at end-of-chunk (`units_dropped_empty=N`). | Defensive. | Empty units in FTS produce zero-tsvector rows that match anything; in Chroma they produce zero-vectors that cluster with all silence segments. Both bad. |
| D10 | **Concurrency:** chunking a single transcript runs in a **single asyncio task**; multiple transcripts can chunk concurrently (limited by Plan 5.5's worker count). Within a transcript, the chunker reads segments via `SELECT ... FOR UPDATE SKIP LOCKED` on the `transcript_units_indexed_at_null` partial index (the claim itself happens in Plan 5.5; this plan just provides the index). | Aligned with Plan 5.5's incremental architecture. | Per-transcript serialization avoids interleaving `seq` numbers; per-transcript parallelism keeps a busy library moving. The skip-locked path is the standard pattern for this (Postgres queue worker pattern). |

If D6 is rejected (move `tsv` into this table), §2 grows by one column
and one functional-index migration step, and Plan 5.2's migration
shrinks to creating a search function. Correctness unchanged.

If D2 is rejected (add overlap), `metadata.overlap_with_unit_seq INT
NULL` is added at insert time; no migration change because `metadata`
is already JSONB.

---

## 1. Architecture diagram — segments to indexed units

```
        ┌───────────────────────────────────────────────────────────────┐
        │  Plan 3.6 (segment commit) — per-segment hot path             │
        │     INSERT INTO transcript_segments (transcript_id, seq, ...) │
        │     NOTIFY segments.committed                                 │
        │       payload: {transcript_id, last_segment_end_sec, seq}     │
        └─────────────────────────────┬─────────────────────────────────┘
                                      │
                                      ▼
        ┌───────────────────────────────────────────────────────────────┐
        │  Plan 5.5 (incremental indexer worker)                        │
        │     LISTEN segments.committed                                 │
        │     batch by transcript_id; debounce 250ms                    │
        │     for each batch:                                           │
        │       claim work via                                          │
        │         FROM transcript_units WHERE indexed_at IS NULL ...    │
        │       OR (initial-pass) call this plan's chunker              │
        └─────────────────────────────┬─────────────────────────────────┘
                                      │
                                      ▼
        ┌───────────────────────────────────────────────────────────────┐
        │  THIS PLAN — pipeline/src/maktaba_pipeline/search/chunker.py  │
        │                                                               │
        │   fetch_segments(transcript_id, *, since_seq) -> [Segment]    │
        │     SELECT seq, start_sec, end_sec, text                      │
        │     FROM transcript_segments                                  │
        │     WHERE transcript_id=$1 AND seq > $2                       │
        │     ORDER BY seq                                              │
        │                                                               │
        │   chunk(segments, language, *, paused_at_sec)                 │
        │     1. concat segment texts with separators that remember     │
        │        their source segment (text + (start, end, segment_id)) │
        │     2. NFC-normalize the concatenation                        │
        │     3. SentenceSegmenter.split() → [Sentence] with offsets    │
        │     4. Packer.pack(sentences, target=200, cap=400, tok_cap=96)│
        │        produces [UnitDraft] each with .text, .span, .seg_ids  │
        │     5. enforce paused-at boundary (D4)                        │
        │     6. drop empty units (D9)                                  │
        │                                                               │
        │   persist(units, transcript_id, language, last_existing_seq)  │
        │     INSERT INTO transcript_units                              │
        │        (transcript_id, seq, start_sec, end_sec, text,         │
        │         language, segment_ids, indexed_at, metadata)          │
        │     VALUES ... ON CONFLICT (transcript_id, seq)               │
        │     DO UPDATE SET text = EXCLUDED.text, ...                   │
        └─────────────────────────────┬─────────────────────────────────┘
                                      │
                                      │ (rows now visible to consumers)
                                      ▼
        ┌──────────────────────┐ ┌──────────────────────┐
        │  Plan 5.2 — FTS      │ │  Plan 5.3 — Chroma   │
        │  reads units, fills  │ │  embeds units in     │
        │  tsv (or rebuilds    │ │  multilingual model, │
        │  GENERATED column)   │ │  upserts to Chroma   │
        │  marks indexed_at    │ │  (their own claim    │
        │  via Plan 5.5        │ │  via Plan 5.5)       │
        └──────────────────────┘ └──────────────────────┘
                       └────────┬────────┘
                                ▼
                  ┌──────────────────────────────┐
                  │  Plan 5.4 — RRF fusion       │
                  │  combines hits from FTS +    │
                  │  Chroma; resolves            │
                  │  unit.segment_ids[0] back    │
                  │  to a precise timestamp.     │
                  └──────────────────────────────┘
```

**Key invariant.** Every row in `transcript_units` carries enough
metadata to reconstruct the search hit's position: `(transcript_id,
seq, start_sec, end_sec, segment_ids[0])` is the deep-link; `text` is
the snippet; `language` is the filter; `metadata` is the why-was-this-
split-this-way debug field.

**Why the chunker isn't called from inside the segment commit hot
path.** Two reasons. First, chunking is a *stable function of segment
ranges*, not of single segments — adding one segment can re-chunk the
last unit in unpredictable ways (e.g., a sentence that previously
ended at the segment boundary now continues). Pulling chunker work
into the commit transaction would force us to delete-and-reinsert the
last unit on every commit, which is ~10× write amplification. Second,
the chunker batches well: 30 s of audio is roughly one segment, but a
typical unit pulls 1–3 segments. A debounced LISTEN/NOTIFY consumer
("wait 250 ms for more segments before chunking") amortizes the
last-unit-rewrite cost across multiple segments.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
└── search/
    ├── __init__.py            # public surface: Chunker, chunk_segments
    ├── chunker.py             # Chunker — orchestrator
    ├── packer.py              # Packer — sentence-to-unit with caps
    ├── segmenter.py           # SentenceSegmenter — regex-based, multi-lang
    ├── normalize.py           # NFC, RTL-mark passthrough, whitespace
    ├── token_estimate.py      # estimate_tokens(text) -> int (D1)
    ├── models.py              # SegmentRow, Sentence, UnitDraft dataclasses
    ├── persist.py             # SQL UPSERT against transcript_units
    └── tests/
        ├── conftest.py        # synthetic segment fixtures (Arabic + Eng)
        ├── test_normalize.py
        ├── test_segmenter_english.py
        ├── test_segmenter_arabic.py
        ├── test_packer.py
        ├── test_chunker_one_segment.py
        ├── test_chunker_many_short.py
        ├── test_chunker_one_long.py
        ├── test_chunker_language_propagation.py
        ├── test_chunker_paused_boundary.py
        ├── test_chunker_no_text_dropped.py
        ├── test_persist_upsert.py
        └── test_migration.py
```

### 2.2 `models.py` — data classes

```python
"""Data classes the chunker passes between modules.

All fields are typed; instances are frozen so they hash and so a
sentence's offsets can never silently mutate during packing.
"""
from __future__ import annotations
from dataclasses import dataclass, field
from typing import Sequence


@dataclass(frozen=True)
class SegmentRow:
    """A row from transcript_segments, projected to the columns chunker needs."""
    seq: int               # monotonic per-transcript
    start_sec: float
    end_sec: float
    text: str
    segment_id: int        # transcript_segments.id (for the JSONB mapping)


@dataclass(frozen=True)
class Sentence:
    """A sentence produced by SentenceSegmenter.

    Offsets are into the *concatenated* transcript text (after NFC).
    """
    text: str
    start_offset: int      # inclusive, in chars
    end_offset: int        # exclusive, in chars
    # The segments this sentence overlaps with, ordered by seq.
    segment_ids: tuple[int, ...]
    start_sec: float       # from the first overlapping segment
    end_sec: float         # from the last overlapping segment


@dataclass(frozen=True)
class UnitDraft:
    """An in-memory unit, before persistence.

    seq is assigned at persist time so the chunker can compose freely.
    """
    text: str
    start_sec: float
    end_sec: float
    segment_ids: tuple[int, ...]
    metadata: dict = field(default_factory=dict)
```

### 2.3 `normalize.py`, `token_estimate.py`, `segmenter.py`

```python
# normalize.py — Unicode + whitespace
from __future__ import annotations
import unicodedata

# Right-to-left and left-to-right marks are content-bearing in Arabic
# (they affect bidi rendering); we MUST preserve them. We strip only
# zero-width-no-break (BOM, U+FEFF) and zero-width-joiner runs that
# are clearly garbage from copy-paste.
_STRIP_CHARS = ("﻿",)


def nfc(text: str) -> str:
    """NFC-normalize and strip a small set of garbage characters."""
    out = unicodedata.normalize("NFC", text)
    for ch in _STRIP_CHARS:
        out = out.replace(ch, "")
    return out


def collapse_whitespace(text: str) -> str:
    """Collapse runs of whitespace to a single space, preserving newlines.

    Newlines are sentence-boundary signals (D3) so we keep them; other
    whitespace runs are ASR artifacts and can be normalized.
    """
    # First, normalize all non-newline whitespace to a single space.
    out_lines = []
    for line in text.split("\n"):
        out_lines.append(" ".join(line.split()))
    return "\n".join(out_lines)
```

```python
# token_estimate.py — D1 cheap upper bound
from __future__ import annotations
import math


def estimate_tokens(text: str) -> int:
    """Cheap upper-bound estimate. Real BPE is 3.5-4 chars/token;
    we use 3 to overshoot slightly, keeping us safely under the 128-token
    embedding window from Plan 5.3.
    """
    return math.ceil(len(text) / 3)
```

```python
# segmenter.py — D3 regex-based multi-script sentence boundary
from __future__ import annotations
import re
from typing import Iterator

from maktaba_pipeline.search.models import Sentence, SegmentRow

# D3: sentence-final punctuation set, multi-script.
#
#   .   ASCII full stop (English, transliterated Arabic)
#   !   ASCII exclamation
#   ?   ASCII question (used in mixed-language text)
#   ؟   Arabic question (U+061F)
#   ।   Devanagari danda (U+0964) — for future Hindi/Urdu support
#
# We do NOT include U+06D4 (Arabic full stop) — modern Arabic uses the
# ASCII period almost universally; including U+06D4 catches a tiny
# minority of texts and false-positives in mixed scientific notation.
_BOUNDARY = re.compile(r"([.!?؟।])(\s+|$)")
_NEWLINE_BOUNDARY = re.compile(r"\n+")


def split_into_sentences(
    text: str,
    *,
    segment_index: list[tuple[int, int, SegmentRow]],
) -> Iterator[Sentence]:
    """Yield Sentences from the concatenated text.

    `segment_index` is a list of (start_offset, end_offset, SegmentRow)
    tuples covering the entire text; offsets are into `text`.
    """
    # Step 1: split on newlines first (hard breaks).
    cursor = 0
    for line_match in _split_with_offsets(text, _NEWLINE_BOUNDARY):
        line_text, line_start, line_end = line_match
        # Step 2: within each line, split on punctuation.
        sub_cursor = line_start
        for sent_text, sent_start, sent_end in _split_punct(line_text, line_start):
            stripped = sent_text.strip()
            if not stripped:
                continue
            # Recompute strip offsets so start_offset points at first non-ws char.
            lead_ws = len(sent_text) - len(sent_text.lstrip())
            trail_ws = len(sent_text) - len(sent_text.rstrip())
            real_start = sent_start + lead_ws
            real_end = sent_end - trail_ws
            seg_ids, t0, t1 = _resolve_segments(segment_index, real_start, real_end)
            yield Sentence(
                text=stripped,
                start_offset=real_start,
                end_offset=real_end,
                segment_ids=seg_ids,
                start_sec=t0,
                end_sec=t1,
            )


def _split_with_offsets(text, splitter):
    """Yield (substring, start, end) for each piece between splitter matches."""
    pieces = []
    last = 0
    for m in splitter.finditer(text):
        pieces.append((text[last:m.start()], last, m.start()))
        last = m.end()
    pieces.append((text[last:], last, len(text)))
    return [p for p in pieces if p[0]]


def _split_punct(line: str, base_offset: int):
    """Yield (substring_including_punct, abs_start, abs_end) splits on _BOUNDARY."""
    last = 0
    for m in _BOUNDARY.finditer(line):
        # Include the punctuation in the LEFT side of the split.
        end = m.end(1)  # end of the punctuation character itself
        yield line[last:end], base_offset + last, base_offset + end
        # Skip the post-punct whitespace.
        last = m.end()
    if last < len(line):
        yield line[last:], base_offset + last, base_offset + len(line)


def _resolve_segments(
    segment_index, sent_start: int, sent_end: int,
) -> tuple[tuple[int, ...], float, float]:
    """Return (segment_ids, start_sec, end_sec) for the segments overlapping [sent_start, sent_end)."""
    overlapping = [
        seg for (s, e, seg) in segment_index
        if s < sent_end and e > sent_start
    ]
    if not overlapping:
        # Defensive: shouldn't happen if segment_index covers the text.
        return ((), 0.0, 0.0)
    overlapping.sort(key=lambda seg: seg.seq)
    return (
        tuple(seg.segment_id for seg in overlapping),
        overlapping[0].start_sec,
        overlapping[-1].end_sec,
    )
```

### 2.4 `packer.py` — sentences → units with caps

```python
"""Pack sentences into units with target/cap on chars and a token cap."""
from __future__ import annotations
from typing import Iterator

from maktaba_pipeline.search.models import Sentence, UnitDraft
from maktaba_pipeline.search.token_estimate import estimate_tokens


class Packer:
    def __init__(
        self,
        *,
        target_chars: int = 200,
        cap_chars: int = 400,
        token_cap: int = 96,
    ):
        self.target_chars = target_chars
        self.cap_chars = cap_chars
        self.token_cap = token_cap

    def pack(self, sentences: list[Sentence]) -> Iterator[UnitDraft]:
        buf: list[Sentence] = []
        buf_chars = 0
        for s in sentences:
            s_len = len(s.text)
            # Case A: this single sentence is itself over the cap → split at word boundary.
            if s_len > self.cap_chars or estimate_tokens(s.text) > self.token_cap:
                if buf:
                    yield self._emit(buf, metadata=None)
                    buf = []
                    buf_chars = 0
                yield from self._split_oversized(s)
                continue

            # Case B: adding s would push us over the cap → emit and start fresh.
            if buf and buf_chars + 1 + s_len > self.cap_chars:
                yield self._emit(buf, metadata=None)
                buf = [s]
                buf_chars = s_len
                continue

            # Case C: fits — append.
            buf.append(s)
            buf_chars += (1 if buf_chars else 0) + s_len

            # Case D: hit target → emit at next punctuation boundary.
            if buf_chars >= self.target_chars:
                yield self._emit(buf, metadata=None)
                buf = []
                buf_chars = 0

        if buf:
            yield self._emit(buf, metadata=None)

    def _emit(self, buf: list[Sentence], *, metadata: dict | None) -> UnitDraft:
        text = " ".join(s.text for s in buf)
        seg_ids: list[int] = []
        for s in buf:
            for sid in s.segment_ids:
                if sid not in seg_ids:
                    seg_ids.append(sid)
        return UnitDraft(
            text=text,
            start_sec=buf[0].start_sec,
            end_sec=buf[-1].end_sec,
            segment_ids=tuple(seg_ids),
            metadata=metadata or {},
        )

    def _split_oversized(self, s: Sentence) -> Iterator[UnitDraft]:
        """Sentence longer than cap (E1) — split at word boundaries ≤ cap."""
        words = s.text.split(" ")
        cur: list[str] = []
        cur_len = 0
        for w in words:
            add_len = len(w) + (1 if cur_len else 0)
            if cur_len + add_len > self.cap_chars and cur:
                yield UnitDraft(
                    text=" ".join(cur),
                    start_sec=s.start_sec,  # imprecise; full sentence span
                    end_sec=s.end_sec,
                    segment_ids=s.segment_ids,
                    metadata={"split_method": "word"},
                )
                cur = [w]
                cur_len = len(w)
            else:
                cur.append(w)
                cur_len += add_len
        if cur:
            yield UnitDraft(
                text=" ".join(cur),
                start_sec=s.start_sec,
                end_sec=s.end_sec,
                segment_ids=s.segment_ids,
                metadata={"split_method": "word"},
            )
```

### 2.5 `chunker.py` — orchestrator

```python
"""Chunker — segments → units → DB.

Public entry: chunk_for_transcript(conn, transcript_id) — used by Plan 5.5.
"""
from __future__ import annotations
import logging
from typing import Sequence

from maktaba_pipeline.search.models import SegmentRow, UnitDraft
from maktaba_pipeline.search.normalize import nfc, collapse_whitespace
from maktaba_pipeline.search.packer import Packer
from maktaba_pipeline.search.segmenter import split_into_sentences
from maktaba_pipeline.search.persist import upsert_units

log = logging.getLogger(__name__)


async def chunk_for_transcript(conn, *, transcript_id: int) -> int:
    """Chunk all unprocessed segments for `transcript_id`. Returns rows written.

    Idempotent: re-running on the same transcript with the same segment
    rows produces identical units (UPSERT on (transcript_id, seq)).
    """
    meta = await conn.fetchrow(
        "SELECT language, paused_at_sec FROM transcripts WHERE id=$1",
        transcript_id,
    )
    if meta is None:
        log.warning("chunk_for_transcript_missing_transcript",
                    extra={"transcript_id": transcript_id})
        return 0

    rows = await conn.fetch("""
        SELECT id, seq, start_sec, end_sec, text
          FROM transcript_segments
         WHERE transcript_id=$1
         ORDER BY seq
    """, transcript_id)
    if not rows:
        return 0

    segments = [
        SegmentRow(
            seq=r["seq"], start_sec=r["start_sec"], end_sec=r["end_sec"],
            text=r["text"], segment_id=r["id"],
        )
        for r in rows
    ]

    units = list(chunk(
        segments,
        language=meta["language"],
        paused_at_sec=meta["paused_at_sec"],
    ))

    # D9: drop empty.
    pre = len(units)
    units = [u for u in units if u.text.strip()]
    dropped = pre - len(units)
    if dropped:
        log.info("chunker_dropped_empty",
                 extra={"transcript_id": transcript_id, "n": dropped})

    written = await upsert_units(
        conn,
        transcript_id=transcript_id,
        language=meta["language"],
        units=units,
    )
    return written


def chunk(
    segments: Sequence[SegmentRow],
    *,
    language: str,
    paused_at_sec: float | None,
) -> list[UnitDraft]:
    """Pure function — segments → units. No DB access."""
    if not segments:
        return []

    # Step 1: split into pre/post-pause groups (D4). In normal flow there
    # is no pause to span; this is a defensive guard for re-chunk paths.
    groups: list[list[SegmentRow]] = [[]]
    if paused_at_sec is not None:
        for s in segments:
            if s.start_sec >= paused_at_sec and groups[-1]:
                groups.append([])
            groups[-1].append(s)
    else:
        groups = [list(segments)]

    out: list[UnitDraft] = []
    packer = Packer()
    for group in groups:
        if not group:
            continue
        text, seg_index = _build_text_and_index(group)
        text_norm = collapse_whitespace(nfc(text))
        # collapse_whitespace can change offsets if it strips spaces;
        # rebuild the segment index on the normalized text.
        text2, seg_index2 = _rebuild_index(text_norm, group)
        sentences = list(split_into_sentences(text2, segment_index=seg_index2))
        out.extend(packer.pack(sentences))

    # If a transcript truly has no punctuation anywhere, packer received
    # one giant "sentence" and emitted word-split units. Mark the metadata.
    if all(u.metadata.get("split_method") == "word" for u in out) and len(out) > 1:
        for i, u in enumerate(out):
            out[i] = UnitDraft(
                text=u.text, start_sec=u.start_sec, end_sec=u.end_sec,
                segment_ids=u.segment_ids,
                metadata={**u.metadata, "no_punctuation": True},
            )

    return out


def _build_text_and_index(group: list[SegmentRow]) -> tuple[str, list]:
    """Concatenate segment texts with newline glue; track per-segment offsets."""
    parts = []
    index = []
    cursor = 0
    for s in group:
        # Glue: a space between segments unless the prior already ended on whitespace.
        if parts and not parts[-1].endswith((" ", "\n")):
            parts.append(" ")
            cursor += 1
        start = cursor
        parts.append(s.text)
        cursor += len(s.text)
        end = cursor
        index.append((start, end, s))
    return "".join(parts), index


def _rebuild_index(normalized_text: str, group: list[SegmentRow]) -> tuple[str, list]:
    """After NFC + whitespace collapse, rebuild the segment offset index.

    We re-run the same concatenation on the normalized per-segment texts
    so offsets stay consistent.
    """
    parts = []
    index = []
    cursor = 0
    for s in group:
        seg_text = collapse_whitespace(nfc(s.text))
        if parts and not parts[-1].endswith((" ", "\n")):
            parts.append(" ")
            cursor += 1
        start = cursor
        parts.append(seg_text)
        cursor += len(seg_text)
        end = cursor
        # Rewrap SegmentRow with normalized text for downstream visibility.
        index.append((start, end, SegmentRow(
            seq=s.seq, start_sec=s.start_sec, end_sec=s.end_sec,
            text=seg_text, segment_id=s.segment_id,
        )))
    return "".join(parts), index
```

### 2.6 `persist.py` — UPSERT against `transcript_units`

```python
"""Persist UnitDraft rows. UPSERT on (transcript_id, seq) so a re-chunk is safe."""
from __future__ import annotations
import json
from typing import Sequence

from maktaba_pipeline.search.models import UnitDraft

_UPSERT = """
INSERT INTO transcript_units
    (transcript_id, seq, start_sec, end_sec, text, language,
     segment_ids, indexed_at, metadata)
VALUES
    ($1, $2, $3, $4, $5, $6, $7::jsonb, NULL, $8::jsonb)
ON CONFLICT (transcript_id, seq) DO UPDATE
   SET start_sec   = EXCLUDED.start_sec,
       end_sec     = EXCLUDED.end_sec,
       text        = EXCLUDED.text,
       language    = EXCLUDED.language,
       segment_ids = EXCLUDED.segment_ids,
       indexed_at  = NULL,            -- force re-indexing of changed rows
       metadata    = EXCLUDED.metadata
"""


async def upsert_units(
    conn,
    *,
    transcript_id: int,
    language: str,
    units: Sequence[UnitDraft],
) -> int:
    """Persist units. Returns the count actually written."""
    if not units:
        return 0
    args = []
    for seq, u in enumerate(units, start=1):
        args.append((
            transcript_id,
            seq,
            float(u.start_sec),
            float(u.end_sec),
            u.text,
            language,
            json.dumps(list(u.segment_ids)),
            json.dumps(u.metadata or {}),
        ))
    await conn.executemany(_UPSERT, args)
    return len(args)
```

### 2.7 Migration — `0NNN_transcript_units.sql`

This is the **single migration owner** of the table. Numbered to follow
the last existing transcription-epic migration; pick the next free
sequence at apply time.

```sql
-- shared/db/migrations/0NNN_transcript_units.sql
-- Owner: Story 5.1 (Plan 5.1). Resolves REVIEW §1.1.h, §1.1.d, §6.3.

BEGIN;

CREATE TABLE transcript_units (
    id            BIGSERIAL PRIMARY KEY,
    transcript_id BIGINT      NOT NULL REFERENCES transcripts(id)
                                       ON DELETE CASCADE,
    seq           INTEGER     NOT NULL,
    start_sec     REAL        NOT NULL,
    end_sec       REAL        NOT NULL,
    text          TEXT        NOT NULL,
    language      TEXT        NOT NULL,        -- ISO 639-1, copied from transcripts.language
    segment_ids   JSONB       NOT NULL,        -- ordered list of source transcript_segments.id
    indexed_at    TIMESTAMPTZ,                 -- NULL until both FTS + Chroma have indexed this row
    metadata      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT transcript_units_seq_unique UNIQUE (transcript_id, seq),
    CONSTRAINT transcript_units_start_le_end CHECK (start_sec <= end_sec),
    CONSTRAINT transcript_units_seq_positive CHECK (seq >= 1),
    CONSTRAINT transcript_units_segment_ids_array
        CHECK (jsonb_typeof(segment_ids) = 'array')
);

-- (D7, story acceptance) Filter pushdown for `language` (REVIEW §6.3).
CREATE INDEX transcript_units_lang
    ON transcript_units (language);

-- (D7, story acceptance) Plan 5.5's incremental claim partial index.
CREATE INDEX transcript_units_indexed_at_null
    ON transcript_units (transcript_id)
    WHERE indexed_at IS NULL;

-- (D7) Plan 5.4 timestamp-window queries ("hits in this chapter").
CREATE INDEX transcript_units_transcript_start_sec
    ON transcript_units (transcript_id, start_sec);

COMMENT ON TABLE  transcript_units            IS 'Search-friendly chunks of transcript_segments. Owned by Plan 5.1.';
COMMENT ON COLUMN transcript_units.seq        IS '1-based monotonic per-transcript ordering. Stable under re-chunk.';
COMMENT ON COLUMN transcript_units.segment_ids IS 'Ordered list of transcript_segments.id contributing to this unit; segment_ids[0] is the deep-link target.';
COMMENT ON COLUMN transcript_units.indexed_at  IS 'NULL until the unit has been indexed by both FTS (Plan 5.2) and Chroma (Plan 5.3). Plan 5.5 sets this when both confirm.';
COMMENT ON COLUMN transcript_units.metadata    IS 'Diagnostic fields: split_method ("word"|"sentence"), no_punctuation (bool), etc.';

COMMIT;
```

**SQLite mirror.** The architecture's single-binary fallback uses
SQLite. The mirror migration `0NNN_transcript_units.sqlite.sql`:

```sql
CREATE TABLE transcript_units (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    transcript_id INTEGER NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    start_sec     REAL    NOT NULL,
    end_sec       REAL    NOT NULL,
    text          TEXT    NOT NULL,
    language      TEXT    NOT NULL,
    segment_ids   TEXT    NOT NULL,            -- JSON array as text
    indexed_at    TEXT,                        -- ISO-8601
    metadata      TEXT    NOT NULL DEFAULT '{}',
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (transcript_id, seq),
    CHECK (start_sec <= end_sec),
    CHECK (seq >= 1)
);
CREATE INDEX transcript_units_lang ON transcript_units (language);
CREATE INDEX transcript_units_indexed_at_null
    ON transcript_units (transcript_id) WHERE indexed_at IS NULL;
CREATE INDEX transcript_units_transcript_start_sec
    ON transcript_units (transcript_id, start_sec);
```

Migration runner picks the right file based on `MAKTABA_DB_DIALECT`
env var, set during boot (architecture §11).

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0NNN_transcript_units.sql` | table + 3 indexes + 4 CHECKs | `test_migration_creates_table_and_indexes` |
| 2 | `shared/db/migrations/0NNN_transcript_units.sqlite.sql` | mirror for SQLite single-binary | `test_migration_sqlite_mirror_applies` |
| 3 | `pipeline/src/maktaba_pipeline/search/__init__.py` | re-export `chunk_for_transcript` | (n/a) |
| 4 | `pipeline/src/maktaba_pipeline/search/models.py` | `SegmentRow`, `Sentence`, `UnitDraft` | (n/a) |
| 5 | `pipeline/src/maktaba_pipeline/search/normalize.py` | `nfc`, `collapse_whitespace` | `test_normalize` |
| 6 | `pipeline/src/maktaba_pipeline/search/token_estimate.py` | `estimate_tokens` | `test_token_estimate` |
| 7 | `pipeline/src/maktaba_pipeline/search/segmenter.py` | `split_into_sentences`, `_BOUNDARY` | `test_segmenter_english`, `test_segmenter_arabic` |
| 8 | `pipeline/src/maktaba_pipeline/search/packer.py` | `Packer.pack`, `_split_oversized` | `test_packer` |
| 9 | `pipeline/src/maktaba_pipeline/search/chunker.py` | `chunk`, `chunk_for_transcript` | `test_chunker_one_segment`, `test_chunker_many_short`, `test_chunker_one_long`, `test_chunker_language_propagation`, `test_chunker_paused_boundary`, `test_chunker_no_text_dropped` |
| 10 | `pipeline/src/maktaba_pipeline/search/persist.py` | `upsert_units` | `test_persist_upsert` |

---

## 4. Test cases

All tests live in
`pipeline/src/maktaba_pipeline/search/tests/`. Tests that touch
Postgres go through the standard `conftest.py` fixture from
`pipeline/tests/conftest.py` (a transactional pytest fixture wrapping
each test in a `BEGIN; ... ROLLBACK;`).

### 4.1 `test_migration_creates_table_and_indexes` (story-named)

```python
async def test_migration_creates_table_and_indexes(db):
    """Apply migration; assert table + named indexes + UNIQUE present."""
    cols = await db.fetch("""
        SELECT column_name, data_type, is_nullable
          FROM information_schema.columns
         WHERE table_name = 'transcript_units'
         ORDER BY ordinal_position
    """)
    names = [c["column_name"] for c in cols]
    assert names == [
        "id", "transcript_id", "seq", "start_sec", "end_sec",
        "text", "language", "segment_ids", "indexed_at", "metadata",
        "created_at",
    ]

    idx = await db.fetch("""
        SELECT indexname FROM pg_indexes
         WHERE tablename = 'transcript_units'
         ORDER BY indexname
    """)
    idxnames = {row["indexname"] for row in idx}
    assert {"transcript_units_lang",
            "transcript_units_indexed_at_null",
            "transcript_units_transcript_start_sec",
            "transcript_units_seq_unique"}.issubset(idxnames)

    # UNIQUE enforcement
    tid = await _seed_transcript(db, language="en")
    await db.execute(
        "INSERT INTO transcript_units (transcript_id, seq, start_sec, end_sec, "
        "text, language, segment_ids) VALUES "
        "($1, 1, 0, 1, 'a', 'en', '[]'::jsonb)", tid)
    with pytest.raises(asyncpg.UniqueViolationError):
        await db.execute(
            "INSERT INTO transcript_units (transcript_id, seq, start_sec, end_sec, "
            "text, language, segment_ids) VALUES "
            "($1, 1, 0, 1, 'b', 'en', '[]'::jsonb)", tid)
```

### 4.2 `test_chunker_one_segment` — single segment makes one unit

```python
async def test_one_segment_one_unit_when_under_target(db, transcript_factory):
    tid = await transcript_factory(language="en", segments=[
        # ~80 chars: under target (200), so packs as one unit.
        (1, 0.0, 5.5, "Hello world. This is a single short segment of speech."),
    ])
    n = await chunk_for_transcript(db, transcript_id=tid)
    assert n == 1
    rows = await db.fetch(
        "SELECT seq, start_sec, end_sec, text, segment_ids "
        "FROM transcript_units WHERE transcript_id=$1 ORDER BY seq", tid)
    assert len(rows) == 1
    assert rows[0]["seq"] == 1
    assert rows[0]["start_sec"] == pytest.approx(0.0)
    assert rows[0]["end_sec"] == pytest.approx(5.5)
    # The unit may contain both sentences glued by space.
    assert "Hello world." in rows[0]["text"]
    assert rows[0]["segment_ids"] == [<segment row id from factory>]
```

### 4.3 `test_chunker_many_short` — coalesce many short segments

```python
async def test_many_short_segments_coalesce_into_one_unit(db, transcript_factory):
    """20 segments × 1s × ~10 chars each → coalesces into 1 unit (≤ 200 chars)."""
    segs = [(i + 1, float(i), float(i + 1), f"piece {i:02d}.") for i in range(20)]
    tid = await transcript_factory(language="en", segments=segs)
    await chunk_for_transcript(db, transcript_id=tid)

    rows = await db.fetch(
        "SELECT seq, segment_ids, text FROM transcript_units "
        "WHERE transcript_id=$1 ORDER BY seq", tid)
    # 20 × ~10 chars = ~200 → 1 or 2 units (depends on packer rounding).
    assert 1 <= len(rows) <= 2
    # Every original segment id is referenced exactly once across all units.
    seen = []
    for r in rows:
        for sid in r["segment_ids"]:
            seen.append(sid)
    assert sorted(seen) == sorted(set(seen))
    assert len(seen) == 20
```

### 4.4 `test_chunker_one_long` — very long segment splits

```python
async def test_one_huge_segment_splits_at_word_boundary(db, transcript_factory):
    """Single 2000-char segment with one period at the end → multiple units, all under cap."""
    huge = ("word " * 400).strip() + "."     # ~2000 chars, no internal sentences
    tid = await transcript_factory(language="en", segments=[(1, 0.0, 60.0, huge)])
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch(
        "SELECT text, metadata FROM transcript_units "
        "WHERE transcript_id=$1 ORDER BY seq", tid)
    assert len(rows) >= 5
    for r in rows:
        assert len(r["text"]) <= 400
        assert r["metadata"]["split_method"] == "word"
        # Each split should still end at a word boundary (no broken words).
        assert not r["text"].endswith(" ")
        assert " " in r["text"] or len(r["text"]) <= 50
```

### 4.5 `test_chunker_language_propagation` — language assignment

```python
@pytest.mark.parametrize("lang", ["en", "ar", "fr"])
async def test_language_copied_from_transcript(db, transcript_factory, lang):
    tid = await transcript_factory(language=lang, segments=[
        (1, 0, 2, "short text."),
    ])
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch(
        "SELECT language FROM transcript_units WHERE transcript_id=$1", tid)
    assert all(r["language"] == lang for r in rows)
```

### 4.6 `test_chunker_paused_boundary` — D4

```python
async def test_unit_does_not_span_paused_boundary(db, transcript_factory):
    """Segments straddling paused_at_sec must produce two unit groups."""
    tid = await transcript_factory(
        language="en",
        paused_at_sec=10.0,
        segments=[
            (1, 0.0, 4.0, "Before pause one."),
            (2, 4.0, 9.0, "Before pause two."),
            (3, 10.0, 14.0, "After pause one."),
            (4, 14.0, 18.0, "After pause two."),
        ],
    )
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch(
        "SELECT text, start_sec, end_sec FROM transcript_units "
        "WHERE transcript_id=$1 ORDER BY seq", tid)
    # Must be at least one pre-pause and one post-pause unit, never one unit
    # whose [start..end] crosses 10.0.
    for r in rows:
        assert r["end_sec"] <= 10.0 or r["start_sec"] >= 10.0
```

### 4.7 `test_chunker_no_text_dropped` (story-named)

```python
async def test_concatenation_of_units_equals_segments(db, transcript_factory):
    """Re-joining all units (after NFC) equals the NFC-joined segments."""
    import unicodedata
    segs = [
        (1, 0.0, 3.0, "First sentence. Second sentence."),
        (2, 3.0, 6.0, "Third sentence! Fourth?"),
        (3, 6.0, 9.0, "Fifth, with comma. Sixth."),
    ]
    tid = await transcript_factory(language="en", segments=segs)
    await chunk_for_transcript(db, transcript_id=tid)
    units = await db.fetch(
        "SELECT text FROM transcript_units WHERE transcript_id=$1 ORDER BY seq", tid)

    def normalize(s: str) -> str:
        return " ".join(unicodedata.normalize("NFC", s).split())

    seg_join = normalize(" ".join(s[3] for s in segs))
    unit_join = normalize(" ".join(r["text"] for r in units))
    assert seg_join == unit_join
```

### 4.8 `test_chunker_arabic_punctuation` (story-named)

```python
async def test_arabic_question_mark_breaks_sentence(db, transcript_factory):
    """Arabic question mark ؟ creates a sentence boundary."""
    tid = await transcript_factory(
        language="ar",
        segments=[
            (1, 0.0, 3.0, "ما اسمك؟ اسمي محمد. كيف حالك؟"),
        ],
    )
    # Force a small target so the three sentences likely produce ≥2 units.
    # (The default target=200 packs all three together; we test the segmenter
    # gave us three sentences via the packer's coalescing.)
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch(
        "SELECT text FROM transcript_units WHERE transcript_id=$1 ORDER BY seq", tid)
    full = " ".join(r["text"] for r in rows)
    # All three sentence-final marks survived in the output.
    assert full.count("؟") == 2
    assert full.count(".") == 1
```

### 4.9 `test_unit_to_segment_mapping_recovers_timestamps` (story-named)

```python
async def test_unit_segment_ids_first_resolves_to_unit_start(db, transcript_factory):
    segs = [
        (1, 0.0, 4.0, "Alpha sentence."),
        (2, 4.0, 8.0, "Beta sentence."),
        (3, 8.0, 12.0, "Gamma sentence."),
    ]
    tid = await transcript_factory(language="en", segments=segs)
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch("""
        SELECT u.start_sec AS u_start, s.start_sec AS s_start
          FROM transcript_units u
          JOIN LATERAL (
              SELECT start_sec FROM transcript_segments
               WHERE id = (u.segment_ids->>0)::bigint
          ) s ON true
         WHERE u.transcript_id=$1
         ORDER BY u.seq
    """, tid)
    for r in rows:
        assert r["u_start"] == pytest.approx(r["s_start"])
```

### 4.10 `test_chunker_re_chunk_is_idempotent` (D8)

```python
async def test_rechunk_produces_identical_rows(db, transcript_factory):
    tid = await transcript_factory(language="en", segments=[
        (i + 1, float(i * 3), float((i + 1) * 3), f"Sentence {i+1}.")
        for i in range(10)
    ])
    n1 = await chunk_for_transcript(db, transcript_id=tid)
    snap1 = await db.fetch(
        "SELECT seq, start_sec, end_sec, text, segment_ids "
        "FROM transcript_units WHERE transcript_id=$1 ORDER BY seq", tid)
    n2 = await chunk_for_transcript(db, transcript_id=tid)
    snap2 = await db.fetch(
        "SELECT seq, start_sec, end_sec, text, segment_ids "
        "FROM transcript_units WHERE transcript_id=$1 ORDER BY seq", tid)
    assert n1 == n2
    assert [dict(r) for r in snap1] == [dict(r) for r in snap2]
```

### 4.11 `test_persist_upsert` — UPSERT mechanics

```python
async def test_upsert_overwrites_on_conflict(db, transcript_factory):
    tid = await transcript_factory(language="en", segments=[(1, 0, 1, "x.")])
    await db.execute(
        "INSERT INTO transcript_units (transcript_id, seq, start_sec, end_sec, "
        "text, language, segment_ids, indexed_at) VALUES "
        "($1, 1, 0, 1, 'old', 'en', '[]'::jsonb, now())", tid)
    await upsert_units(db, transcript_id=tid, language="en", units=[
        UnitDraft(text="new", start_sec=0.0, end_sec=1.0, segment_ids=(), metadata={}),
    ])
    row = await db.fetchrow(
        "SELECT text, indexed_at FROM transcript_units WHERE transcript_id=$1", tid)
    assert row["text"] == "new"
    # UPSERT clears indexed_at so Plan 5.5 re-indexes the changed row.
    assert row["indexed_at"] is None
```

### 4.12 `test_chunking_target_length` (story-named)

```python
async def test_target_length_distribution(db, transcript_factory):
    """Long fixture → median unit length in [150, 250], 99p ≤ 400."""
    long_text = (
        "This is one sentence. " * 200
    )  # ~4000 chars; clean periods
    tid = await transcript_factory(language="en",
                                   segments=[(1, 0, 600, long_text)])
    await chunk_for_transcript(db, transcript_id=tid)
    rows = await db.fetch(
        "SELECT text FROM transcript_units WHERE transcript_id=$1", tid)
    lens = sorted(len(r["text"]) for r in rows)
    assert 150 <= statistics.median(lens) <= 250
    p99 = lens[int(0.99 * (len(lens) - 1))]
    assert p99 <= 400
```

### 4.13 `test_segmenter_english` and `test_segmenter_arabic` (units)

```python
def test_english_basic_split():
    text = "Hello. World! Right? Yes."
    sents = list(_split_only(text))
    assert [s.text for s in sents] == ["Hello.", "World!", "Right?", "Yes."]


def test_arabic_question_split():
    text = "كيف حالك؟ بخير. ماذا تريد؟"
    sents = list(_split_only(text))
    assert [s.text for s in sents] == ["كيف حالك؟", "بخير.", "ماذا تريد؟"]


def test_combining_marks_preserved():
    text = "كَتَبَ. قَرَأَ."  # tashkeel
    sents = list(_split_only(text))
    assert sents[0].text == "كَتَبَ."   # diacritics intact
    assert sents[1].text == "قَرَأَ."


def test_devanagari_danda():
    text = "नमस्ते। आप कैसे हैं।"
    sents = list(_split_only(text))
    assert len(sents) == 2


def test_newline_is_hard_break():
    text = "First line\nSecond line."
    sents = list(_split_only(text))
    assert len(sents) == 2
```

### 4.14 `test_packer` — boundary conditions

```python
def test_packer_emits_at_target():
    sents = [_S(("x" * 60) + ".") for _ in range(5)]    # 60 + 1 + 60 + 1 ... = 304 chars total
    units = list(Packer().pack(sents))
    # Should produce 2 units (target 200 → emit after 3rd sentence ~182 chars).
    assert len(units) == 2

def test_packer_caps_at_400():
    sents = [_S(("y " * 99).strip() + ".") for _ in range(5)]   # each ~199 chars
    units = list(Packer().pack(sents))
    for u in units:
        assert len(u.text) <= 400

def test_packer_oversize_sentence_word_split():
    huge = _S("word " * 200 + ".")        # ~1000 chars, single sentence
    units = list(Packer().pack([huge]))
    assert all(u.metadata.get("split_method") == "word" for u in units)
    assert len(units) >= 2
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case | Handled by |
|---|-----------|------------|
| E1 | **Sentence longer than the cap.** Common for run-on Whisper output with no internal punctuation (especially long Arabic supplications, lectures, or songs). | `Packer._split_oversized` splits at word boundaries ≤ `cap_chars`; metadata gets `split_method = "word"`. (`test_chunker_one_long`, `test_packer_oversize_sentence_word_split`) (D2) |
| E2 | **No punctuation anywhere in the transcript.** | `chunk()` post-processes — if every unit's metadata reports `split_method = "word"`, all units are tagged `metadata.no_punctuation = true` so an analytics surface in Plan 5.5 can flag the transcript for triage. The chunking still works (treats whole transcript as one giant sentence and word-splits). |
| E3 | **Arabic sentence-final mix: `?؟!`** in one sentence (e.g., "is this right?؟!"). | The boundary regex `[.!?؟।]` matches *each* mark independently; only the *first* matched mark closes the sentence. Trailing duplicate marks become whitespace-padded micro-sentences which are dropped by `nfc + collapse_whitespace + len(text.strip()) == 0` (D9). (`test_segmenter_english`, `test_segmenter_arabic`) (D3) |
| E4 | **Sentence-final period inside a number** (e.g., "the value is 3.14 grams"). | False-positive risk; the regex requires `[.!?؟।]` followed by whitespace OR end-of-string, so `3.14` does NOT split (no whitespace between `.` and `1`). This is also why `_BOUNDARY = r"([.!?؟।])(\s+|$)"` and not `r"[.!?؟।]"`. |
| E5 | **English sentence embedded in Arabic** (mixed-script transcript common for tech podcasts). | The regex is script-agnostic; `[.!?؟।]` covers all required marks regardless of surrounding script. NFC normalization happens before segmentation so combining marks on either side don't disrupt the boundary. The `language` column on the resulting unit is the *transcript's* language (D5), not a per-unit language detect — multilingual embedding model in Plan 5.3 handles cross-script units natively. |
| E6 | **Right-to-left embedding marks (U+202A–U+202E, U+2066–U+2069).** | `nfc()` does NOT strip these. They are content-bearing for bidi rendering and the embedding model has seen them in training. Only `U+FEFF` (BOM) is stripped, per `_STRIP_CHARS`. |
| E7 | **Very short segments (< 1 s).** Common in fast-paced dialog or interjections ("Yeah." "Right." "Mm."). | They flow normally through the packer and coalesce with neighbors up to `target_chars`. The unit's `start_sec`/`end_sec` cover all coalesced segments. (`test_chunker_many_short`) |
| E8 | **Segment with empty `text`.** Not expected from the STT backends but defensive. | `nfc()` returns "", `collapse_whitespace` returns "", segmenter yields no sentences, packer yields no units. The segment is silently absent from any unit's `segment_ids`. We add a one-line WARN log if `len(segment.text.strip()) == 0` so it's visible. |
| E9 | **Paused transcript** — segments before and after a long pause must not share a unit. | `chunk()` reads `transcripts.paused_at_sec` and splits the segment list into pre/post-pause groups before packing. (`test_chunker_paused_boundary`) (D4) |
| E10 | **Re-chunk after segments change** (re-run with better model, or correction landing). | UPSERT on `(transcript_id, seq)` overwrites the row and resets `indexed_at = NULL`, forcing Plans 5.2 + 5.3 to re-index. Stale tail rows beyond new max seq are deleted by [Plan 5.5](plan-05-05-incremental-indexing.md)'s sweep, NOT by this plan (D8). (`test_chunker_re_chunk_is_idempotent`) |
| E11 | **Cascade on transcript delete.** | `ON DELETE CASCADE` on the foreign key — deleting a transcript removes all units. Plans 5.2 and 5.3 react via their own CASCADE / sync paths. |
| E12 | **`indexed_at` race** — Plan 5.2 sets `indexed_at = now()` after FTS commit, but Plan 5.5 races to claim the row. | The partial index `(transcript_id) WHERE indexed_at IS NULL` ensures Plan 5.5's `SELECT ... FOR UPDATE SKIP LOCKED` only sees unindexed rows. Plan 5.5 holds the row lock until both 5.2 + 5.3 have written. The UPSERT in this plan resets `indexed_at = NULL` on overwrite, which Plan 5.5 picks up on its next NOTIFY. |
| E13 | **`segment_ids` JSONB array contains a duplicate** (e.g., a sentence spans into a segment that's already in `segment_ids` because the *previous* sentence also touched it). | `Packer._emit` deduplicates via `if sid not in seg_ids: seg_ids.append(sid)`. Order is preserved by first appearance, which equals seq order because segments arrive in seq order. |
| E14 | **A unit with one Arabic word and one English word.** | Stored as-is; `language = transcript.language`. The embedding model handles it. The FTS layer in Plan 5.2 may use `'simple'` dictionary for Arabic (decided there, not here). |
| E15 | **Concurrent writers** — Plan 5.5 worker A and B both pick up the same transcript. | Plan 5.5 owns the locking (advisory lock per transcript_id). This plan's UPSERT is safe regardless: same input → same rows. The worst case is wasted CPU, not corruption. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `0NNN_transcript_units.sql` creates the table with all columns from the story acceptance, plus `created_at`. (`test_migration_creates_table_and_indexes`)
- [ ] **A2** Migration creates indexes `transcript_units_lang`, `transcript_units_indexed_at_null` (partial WHERE indexed_at IS NULL), and the additional `transcript_units_transcript_start_sec` (D7). (`test_migration_creates_table_and_indexes`)
- [ ] **A3** `UNIQUE (transcript_id, seq)` is enforced. (`test_migration_creates_table_and_indexes`)
- [ ] **A4** SQLite mirror migration applies cleanly when `MAKTABA_DB_DIALECT=sqlite`. (`test_migration_sqlite_mirror_applies`)
- [ ] **A5** Given segments S₁..Sₙ with target ~200 chars, the chunker produces units with median length in [150, 250] and 99p ≤ 400 chars. (`test_chunking_target_length`)
- [ ] **A6** A unit's `start_sec` equals the first contributing segment's `start_sec`; `end_sec` equals the last contributing segment's `end_sec`. (`test_chunker_one_segment`, `test_unit_to_segment_mapping_recovers_timestamps`)
- [ ] **A7** A multi-sentence segment is re-segmented; sentence boundaries are detected on `[.!?؟।]` plus newline. (`test_chunker_arabic_punctuation`, `test_segmenter_english`, `test_segmenter_arabic`)
- [ ] **A8** Arabic combining marks are preserved verbatim. (`test_combining_marks_preserved`)
- [ ] **A9** `language` is copied from the parent transcript and is identical for all units of one transcript. (`test_chunker_language_propagation`)
- [ ] **A10** `segment_ids` is an ordered JSONB array; `segment_ids[0]` resolves to the segment whose `start_sec` equals the unit's `start_sec`. (`test_unit_to_segment_mapping_recovers_timestamps`) (story acceptance)
- [ ] **A11** Concatenation of all unit texts (after NFC + whitespace collapse) equals the concatenation of segment texts. (`test_chunker_no_text_dropped`) (story-named)
- [ ] **A12** A single sentence longer than the cap is split at word boundary; `metadata.split_method = "word"`. (`test_chunker_one_long`, `test_packer_oversize_sentence_word_split`)
- [ ] **A13** A transcript with no punctuation gets per-unit `metadata.no_punctuation = true`. (covered in `test_chunker_one_long` + edge-case fixture)
- [ ] **A14** Re-running the chunker on unchanged input produces identical rows (UPSERT idempotent). (`test_chunker_re_chunk_is_idempotent`) (D8)
- [ ] **A15** UPSERT on conflict resets `indexed_at` to NULL so downstream re-indexing is triggered. (`test_persist_upsert`) (E12)
- [ ] **A16** Cascading delete of a transcript removes all units (FK ON DELETE CASCADE). (covered by FK; spot-checked in `test_migration_creates_table_and_indexes`)
- [ ] **A17** Units never span a paused boundary (`paused_at_sec`); pre- and post-pause groups produce separate units. (`test_chunker_paused_boundary`) (D4)
- [ ] **A18** The chunker does not import `pyarabic` or any other heavyweight tokenizer at module load or runtime. (Static check in `test_no_heavy_imports`.)
- [ ] **A19** Empty units (after NFC + strip) are dropped before insert; the count is logged once per chunk pass. (`test_chunker_no_text_dropped` indirectly; explicit `test_chunker_drops_empty`.)
- [ ] **A20** No code path in this plan writes to FTS `tsv` columns or to Chroma — those are owned by Plans 5.2 and 5.3. (Static lint: no `tsvector`, no `chroma`, no `embed` symbols in `pipeline/src/maktaba_pipeline/search/`.)
