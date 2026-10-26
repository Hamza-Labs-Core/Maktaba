"""``maktaba-pipeline copy-chroma``: the vectors in Chroma copied into Postgres, once (story 43.3).

The copy reads Chroma's files and never opens a Chroma client: the index worker has the
collection open (and loaded, 24 GB on the home Mac) and keeps writing it until the copy
is done, and a second client in another process is what broke the index before. What
it reads:

* ``chroma.sqlite3`` (read-only, a few small queries): the collection, its vector
  segment, how far that segment is applied (``max_seq_id``) and the writes after that
  still in ``embeddings_queue``;
* the vector segment's hnswlib files: ``header.bin``, ``index_metadata.pickle`` (each
  label's id, ``{transcript_id}:{seq}``) and ``data_level0.bin``, one fixed-size record
  per element: its links, its vector (normalised: the collection is cosine) and its
  label. Elements never move, so an element's place is the cursor.

An id is mapped to its segment by ``transcript_segments (transcript_id, seq)``, which is
unique and never changes while the transcript exists (segments are never updated or
deleted but with their transcript): the same row as the ``segment_id`` Chroma keeps.
Ids whose segment or video no longer exist are orphans and are skipped.

Steps, each saved in ``semantic_store_state.copy_cursor`` (a restart continues):

1. ``tail``: the writes Chroma has not put in its files yet, applied in order;
2. ``load``: pages of elements, written as halfvec with a binary COPY into a temp table
   and ``INSERT ... ON CONFLICT DO NOTHING`` (a row the index stage wrote meanwhile is
   newer); the HNSW index is dropped first and
3. ``index``: built after, once (per-row maintenance is far slower);
4. ``verify``: row count, and recall of the HNSW index against the exact top 10 of
   Chroma's own vectors; then ``store = 'postgres'`` and ``copy_done_at``.

While it runs the index stage writes both stores and search reads Chroma.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import io
import json
import os
import pickle
import random
import shutil
import signal
import sqlite3
import struct
import sys
import time
import uuid
from collections.abc import Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import numpy as np
import numpy.typing as npt

from ..log import get_logger
from . import semantic

__all__ = [
    "EXIT_FAILED",
    "EXIT_NOT_READY",
    "EXIT_OK",
    "EXIT_VERIFY_FAILED",
    "ChromaFiles",
    "CopyConfig",
    "Cursor",
    "Header",
    "add_parser",
    "copy_chroma",
    "main",
]

EXIT_OK = 0
EXIT_FAILED = 1
#: the copy and the index are there but the check failed: search stays on Chroma
EXIT_VERIFY_FAILED = 3
#: nothing to copy from or into (no pgvector, no Chroma): retrying does not help
EXIT_NOT_READY = 78

PAGE = 5000
HNSW_INDEX = "segment_embeddings_hnsw"
#: a fixed key: one copy at a time per database
LOCK_KEY = 0x43_03_C0_9E
GB = 1024**3

_log = get_logger()

# ------------------------------------------------------------------ Chroma's files

_HEADER = struct.Struct("<iQQQQQQiIQQQdQ")
#: hnswlib marks a deleted element in the third byte of its level-0 link header
_DELETE_MARK = 0x01


@dataclass(frozen=True, slots=True)
class Header:
    """hnswlib's ``header.bin`` (Chroma's fork: a format version first)."""

    elements: int
    size_per_element: int
    label_offset: int
    data_offset: int
    dim: int

    @classmethod
    def read(cls, path: Path) -> Header:
        raw = path.read_bytes()
        if len(raw) < _HEADER.size:
            raise ValueError(f"{path}: {len(raw)} bytes, not an hnswlib header")
        f = _HEADER.unpack_from(raw)
        size, label_off, data_off = int(f[4]), int(f[5]), int(f[6])
        dim = (label_off - data_off) // 4
        if not (0 < data_off < label_off < size) or dim <= 0:
            raise ValueError(f"{path}: unexpected layout {f}")
        return cls(int(f[3]), size, label_off, data_off, dim)


class _PlainUnpickler(pickle.Unpickler):
    """Chroma's metadata is plain dicts of ints and strings: anything else is refused, so
    a damaged or foreign file cannot run code."""

    def find_class(self, module: str, name: str) -> Any:
        raise pickle.UnpicklingError(f"{module}.{name} is not allowed here")


@dataclass(slots=True)
class Elements:
    """One run of elements: their place, label and vector, whether hnswlib marked them
    deleted and whether the vector reads whole (normalised, as a cosine index keeps it:
    a record the writer is rewriting this moment does not)."""

    first: int
    labels: npt.NDArray[np.int64]
    vectors: npt.NDArray[np.float32]
    deleted: npt.NDArray[np.bool_]
    sane: npt.NDArray[np.bool_]

    @property
    def live(self) -> npt.NDArray[np.bool_]:
        return ~self.deleted & self.sane


@dataclass
class ChromaFiles:
    """The collection's files in a Chroma directory, read without Chroma."""

    root: Path
    collection_id: str
    segment_id: str
    #: the last write the vector files hold; the queue has the ones after it
    applied_seq: int
    header: Header
    label_to_id: dict[int, str] = field(default_factory=dict)

    @property
    def segment_dir(self) -> Path:
        return self.root / self.segment_id

    @classmethod
    def open(cls, root: Path, collection: str) -> ChromaFiles:
        with _sqlite(root) as db:
            row = db.execute(
                "SELECT id, dimension FROM collections WHERE name = ?", (collection,)
            ).fetchone()
            if row is None:
                raise LookupError(f"no collection {collection} in {root}")
            cid, dim = str(row[0]), int(row[1] or 0)
            seg = db.execute(
                "SELECT id FROM segments WHERE collection = ? AND scope = 'VECTOR'", (cid,)
            ).fetchone()
            if seg is None:
                raise LookupError(f"collection {collection} has no vector segment")
            sid = str(seg[0])
            applied = db.execute(
                "SELECT seq_id FROM max_seq_id WHERE segment_id = ?", (sid,)
            ).fetchone()
        path = root / sid / "header.bin"
        # a collection under a thousand writes has no files yet: all of it is in the queue
        header = Header.read(path) if path.exists() else Header(0, 1, 0, 0, dim)
        return cls(root, cid, sid, _seq(applied[0] if applied else 0), header)

    def load_labels(self) -> None:
        """Each label's id (``index_metadata.pickle``, rewritten whole by Chroma: a read
        in the middle of that is tried again)."""
        path = self.segment_dir / "index_metadata.pickle"
        if not self.header.elements:
            self.label_to_id = {}
            return
        for attempt in range(10):
            try:
                with path.open("rb") as f:
                    data = _PlainUnpickler(f).load()
                # kept as loaded (14 million entries on the home Mac: a copy would double
                # it), the other maps dropped
                labels = data.pop("label_to_id")
                del data
                if not isinstance(labels, dict):
                    raise ValueError(f"label_to_id is a {type(labels).__name__}")
                self.label_to_id = labels
                return
            except (EOFError, pickle.UnpicklingError, KeyError, ValueError, AttributeError) as exc:
                if attempt == 9:
                    raise
                _log.warning("chroma_copy_labels_retry", error=f"{type(exc).__name__}: {exc}")
                time.sleep(3)

    def elements(self) -> int:
        """How many element records the data file holds now."""
        if not self.header.elements:
            return 0
        size = (self.segment_dir / "data_level0.bin").stat().st_size
        return min(self.header.elements, size // self.header.size_per_element)

    def read(self, fd: int, first: int, count: int) -> Elements:
        """Elements ``[first, first + count)`` from an open ``data_level0.bin``."""
        h = self.header
        raw = os.pread(fd, count * h.size_per_element, first * h.size_per_element)
        n = len(raw) // h.size_per_element
        dt = np.dtype(
            {
                "names": ["links", "vec", "label"],
                "formats": ["<u4", ("<f4", (h.dim,)), "<u8"],
                "offsets": [0, h.data_offset, h.label_offset],
                "itemsize": h.size_per_element,
            }
        )
        rec = np.frombuffer(raw, dtype=dt, count=n)
        labels = rec["label"].astype(np.int64)
        vectors = np.ascontiguousarray(rec["vec"], dtype=np.float32)
        deleted = ((rec["links"] >> 16) & _DELETE_MARK).astype(bool)
        with np.errstate(all="ignore"):
            norms = np.linalg.norm(vectors, axis=1) if n else np.zeros(0, dtype=np.float32)
            sane = np.isfinite(norms) & (np.abs(norms - 1.0) < 0.01)
        return Elements(first, labels, vectors, deleted, sane)

    def queue_tail(self) -> list[tuple[int, int, str, npt.NDArray[np.float32] | None]]:
        """The collection's writes after :attr:`applied_seq`, oldest first:
        ``(seq, operation, id, vector)`` (operation 0 add, 1 update, 2 upsert, 3 delete)."""
        out: list[tuple[int, int, str, npt.NDArray[np.float32] | None]] = []
        with _sqlite(self.root) as db:
            rows = db.execute(
                "SELECT seq_id, operation, id, vector, encoding FROM embeddings_queue "
                "WHERE topic LIKE ? AND seq_id > ? ORDER BY seq_id",
                (f"%/{self.collection_id}", self.applied_seq),
            ).fetchall()
        for seq, op, eid, blob, enc in rows:
            vec: npt.NDArray[np.float32] | None = None
            if blob is not None and (enc or "FLOAT32").upper() == "FLOAT32":
                vec = np.frombuffer(blob, dtype="<f4").astype(np.float32)
                norm = float(np.linalg.norm(vec))
                vec = vec / norm if norm > 0 else None
            out.append((int(seq), int(op), str(eid), vec))
        return out


def _seq(v: Any) -> int:
    """``max_seq_id.seq_id``: an integer, or 8 big-endian bytes in older Chromas."""
    if isinstance(v, bytes | bytearray):
        return int.from_bytes(v, "big")
    return int(v or 0)


@contextlib.contextmanager
def _sqlite(root: Path) -> Any:
    """Chroma's database, read-only; its writer holds it a moment at a time."""
    uri = f"file:{root / 'chroma.sqlite3'}?mode=ro"
    db = sqlite3.connect(uri, uri=True, timeout=120)
    try:
        yield db
    finally:
        db.close()


def split_id(eid: str) -> tuple[uuid.UUID, int] | None:
    """``{transcript_id}:{seq}``, or None for an id of another shape."""
    tid, sep, seq = eid.rpartition(":")
    if not sep:
        return None
    try:
        return uuid.UUID(tid), int(seq)
    except ValueError:
        return None


# ------------------------------------------------------------------ Postgres rows

_PGCOPY_HEAD = b"PGCOPY\n\xff\r\n\x00" + struct.pack(">ii", 0, 0)
_PGCOPY_TAIL = struct.pack(">h", -1)


def copy_rows(keys: Sequence[tuple[uuid.UUID, int]], vectors: npt.NDArray[np.float32]) -> bytes:
    """A binary COPY of ``(transcript_id uuid, seq int, embedding halfvec)`` rows, built
    whole by numpy: halfvec's wire form is its dimension, two unused bytes and the
    big-endian half floats."""
    dim = int(vectors.shape[1]) if vectors.ndim == 2 else 0
    dt = np.dtype(
        [
            ("fields", ">i2"),
            ("tid_len", ">i4"),
            ("tid", "V16"),
            ("seq_len", ">i4"),
            ("seq", ">i4"),
            ("vec_len", ">i4"),
            ("dim", ">i2"),
            ("unused", ">i2"),
            ("vec", ">f2", (dim,)),
        ]
    )
    rows = np.zeros(len(keys), dtype=dt)
    rows["fields"] = 3
    rows["tid_len"] = 16
    rows["tid"] = np.frombuffer(b"".join(k[0].bytes for k in keys), dtype="V16")
    rows["seq_len"] = 4
    rows["seq"] = [k[1] for k in keys]
    rows["vec_len"] = 4 + 2 * dim
    rows["dim"] = dim
    rows["vec"] = vectors.astype(np.float16)
    return _PGCOPY_HEAD + rows.tobytes() + _PGCOPY_TAIL


# the page's rows that still have a segment and a video, written unless already there
_INSERT = """
WITH m AS (
    SELECT ts.id AS segment_id, v.id AS video_id, v.library_id, v.detected_language AS language,
           p.embedding
    FROM chroma_copy_page p
    JOIN transcript_segments ts ON ts.transcript_id = p.transcript_id AND ts.seq = p.seq
    JOIN transcripts t ON t.id = ts.transcript_id
    JOIN videos v ON v.id = t.video_id
), ins AS (
    INSERT INTO segment_embeddings (segment_id, video_id, library_id, language, model, embedding)
    SELECT segment_id, video_id, library_id, language, $1, embedding FROM m
    ON CONFLICT (segment_id) DO {action}
    RETURNING 1
)
SELECT (SELECT count(*) FROM m) AS matched, (SELECT count(*) FROM ins) AS written
"""
_KEEP = "NOTHING"
_REPLACE = (
    "UPDATE SET embedding = EXCLUDED.embedding, video_id = EXCLUDED.video_id, "
    "library_id = EXCLUDED.library_id, language = EXCLUDED.language, model = EXCLUDED.model"
)


@dataclass
class Cursor:
    """Where the copy is: saved in ``semantic_store_state.copy_cursor`` (JSON)."""

    segment: str
    phase: str = "tail"  # tail, load, index, verify
    next: int = 0  # the next element of data_level0.bin
    total: int = 0  # elements when the load started
    orphans: int = 0
    unreadable: int = 0
    #: labels of the elements Chroma's queue names (deleted or written again after its
    #: files were): the load never copies them from the files
    skip: list[int] = field(default_factory=list)

    def dump(self) -> str:
        return json.dumps(
            {
                "v": 1,
                "segment": self.segment,
                "phase": self.phase,
                "next": self.next,
                "total": self.total,
                "orphans": self.orphans,
                "unreadable": self.unreadable,
                "skip": self.skip,
            }
        )

    @classmethod
    def parse(cls, raw: str | None) -> Cursor | None:
        if not raw:
            return None
        try:
            d = json.loads(raw)
            return cls(
                segment=str(d["segment"]),
                phase=str(d.get("phase", "tail")),
                next=int(d.get("next", 0)),
                total=int(d.get("total", 0)),
                orphans=int(d.get("orphans", 0)),
                unreadable=int(d.get("unreadable", 0)),
                skip=[int(x) for x in d.get("skip") or []],
            )
        except (ValueError, KeyError, TypeError):
            return None


@dataclass
class CopyConfig:
    chroma_dir: Path
    model: str
    page: int = PAGE
    samples: int = 1000
    min_recall: float = 0.95
    #: the check's ef_search. It checks the copy and its index, not the search's speed
    #: setting: at the search's 100 an HNSW index finds about 0.91 of the exact top 10 of
    #: a million synthetic vectors, Chroma's own index the same (both logged); at 400 the
    #: copy's finds 0.99, and a copy that lost or garbled vectors fails at any value
    ef_search: int = 400
    #: the search's ef_search (search/semantic.py), whose recall is logged
    search_ef: int = 100
    min_free_bytes: int = 15 * GB
    build_mem: str = ""
    build_workers: int = 0
    progress_file: Path | None = None
    done_file: Path | None = None
    switch: bool = True
    progress_every: float = 10.0


@dataclass
class State:
    store: str
    cursor: Cursor | None
    copied: int
    done: bool


async def _ready(conn: Any) -> bool:
    try:
        return bool(await conn.fetchval("SELECT semantic_store_ready()"))
    except Exception as exc:  # noqa: BLE001 - no migration 0152: not ready
        _log.warning("chroma_copy_store_check_failed", error=f"{type(exc).__name__}: {exc}")
        return False


async def _state(conn: Any) -> State:
    row = await conn.fetchrow(
        "SELECT store, copy_cursor, copied_rows, copy_done_at FROM semantic_store_state LIMIT 1"
    )
    if row is None:
        await conn.execute("INSERT INTO semantic_store_state (store) VALUES ('chroma')")
        return State("chroma", None, 0, False)
    return State(
        str(row["store"]),
        Cursor.parse(row["copy_cursor"]),
        int(row["copied_rows"] or 0),
        row["copy_done_at"] is not None,
    )


async def _save(conn: Any, cur: Cursor, copied: int) -> None:
    await conn.execute(
        "UPDATE semantic_store_state SET copy_cursor = $1, copied_rows = $2, updated_at = now()",
        cur.dump(),
        copied,
    )


# ------------------------------------------------------------------ progress


class Progress:
    """Logs the copy's progress and keeps it in a small JSON file the supervisor shows."""

    def __init__(self, cfg: CopyConfig) -> None:
        self.cfg = cfg
        self.last = 0.0
        self.started = time.monotonic()
        self.start_next = 0

    def report(self, phase: str, cur: Cursor, copied: int, *, force: bool = False) -> None:
        now = time.monotonic()
        if not force and now - self.last < self.cfg.progress_every:
            return
        self.last = now
        done = cur.next - self.start_next
        rate = done / max(now - self.started, 1e-6)
        left = max(cur.total - cur.next, 0)
        eta = int(left / rate) if rate > 0 else None
        fields = {
            "phase": phase,
            "elements": cur.next,
            "total": cur.total,
            "copied": copied,
            "orphans": cur.orphans,
            "rows_per_sec": round(rate, 1),
            "eta_sec": eta if phase == "load" else None,
        }
        _log.info("chroma_copy_progress", **fields)
        self.write(fields)

    def write(self, fields: dict[str, Any]) -> None:
        path = self.cfg.progress_file
        if path is None:
            return
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            tmp = path.with_suffix(path.suffix + ".tmp")
            tmp.write_text(json.dumps({**fields, "at": int(time.time())}))
            os.replace(tmp, path)
        except OSError as exc:
            _log.warning("chroma_copy_progress_unwritten", error=str(exc))


# ------------------------------------------------------------------ the steps


async def _temp_table(conn: Any, dim: int) -> None:
    await conn.execute(
        "CREATE TEMP TABLE IF NOT EXISTS chroma_copy_page "
        f"(transcript_id uuid, seq int, embedding halfvec({int(dim)})) ON COMMIT DELETE ROWS"
    )


async def _write_page(
    conn: Any,
    model: str,
    keys: Sequence[tuple[uuid.UUID, int]],
    vectors: npt.NDArray[np.float32],
    *,
    replace: bool = False,
) -> int:
    """Copy one page in (inside the caller's transaction); how many rows have their
    segment and video (the rest are orphans)."""
    if not keys:
        return 0
    await conn.execute("TRUNCATE chroma_copy_page")
    await conn.copy_to_table(
        "chroma_copy_page", source=io.BytesIO(copy_rows(keys, vectors)), format="binary"
    )
    row = await conn.fetchrow(_INSERT.format(action=_REPLACE if replace else _KEEP), model)
    return int(row["matched"])


async def _tail(conn: Any, files: ChromaFiles, cfg: CopyConfig, cur: Cursor) -> None:
    """The writes still in Chroma's queue (not in its files yet), the last one per id.
    The load skips every element they name: a deleted one stays deleted, a rewritten
    one is copied from here. Not counted in ``copied``: the count check asks Postgres
    for at least what the load copied."""
    last: dict[str, npt.NDArray[np.float32] | None] = {}
    for _seq_id, op, eid, vec in files.queue_tail():
        if op == 3:
            last[eid] = None
        elif vec is not None:
            last[eid] = vec
    id_to_label = {eid: label for label, eid in files.label_to_id.items()} if last else {}
    keys: list[tuple[uuid.UUID, int]] = []
    vecs: list[npt.NDArray[np.float32]] = []
    gone: list[tuple[uuid.UUID, int]] = []
    for eid, vec in last.items():
        key = split_id(eid)
        if key is None:
            continue
        if vec is None:
            gone.append(key)
        elif vec.shape[0] == files.header.dim:
            keys.append(key)
            vecs.append(vec)
    async with conn.transaction():
        written = 0
        for i in range(0, len(keys), cfg.page):
            written += await _write_page(
                conn,
                cfg.model,
                keys[i : i + cfg.page],
                np.stack(vecs[i : i + cfg.page]),
                replace=True,
            )
        if gone:
            await conn.execute(
                "DELETE FROM segment_embeddings se USING transcript_segments ts, "
                "unnest($1::uuid[], $2::int[]) AS g(tid, seq) "
                "WHERE ts.transcript_id = g.tid AND ts.seq = g.seq AND se.segment_id = ts.id",
                [k[0] for k in gone],
                [k[1] for k in gone],
            )
        cur.skip = sorted(id_to_label[e] for e in last if e in id_to_label)
        cur.phase = "load"
        await _save(conn, cur, 0)
    _log.info("chroma_copy_tail", written=written, deleted=len(gone), queued=len(last))


async def _load(
    conn: Any, files: ChromaFiles, cfg: CopyConfig, cur: Cursor, copied: int, progress: Progress
) -> int:
    skip = set(cur.skip)
    if not cur.total:
        cur.total = files.elements()
    progress.start_next = cur.next
    path = files.segment_dir / "data_level0.bin"
    fd = os.open(path, os.O_RDONLY) if cur.total else -1
    try:
        while cur.next < cur.total:
            count = min(cfg.page, cur.total - cur.next)
            el = await asyncio.to_thread(files.read, fd, cur.next, count)
            if len(el.labels) < count or not el.sane.all():
                # the writer is rewriting the file this moment: read it again
                await asyncio.sleep(2)
                el = await asyncio.to_thread(files.read, fd, cur.next, count)
                if not len(el.labels):
                    raise RuntimeError(f"{path} ends at element {cur.next} of {cur.total}")
            keys: list[tuple[uuid.UUID, int]] = []
            rows: list[int] = []
            labels = el.labels.tolist()
            deleted = el.deleted.tolist()
            sane = el.sane.tolist()
            for i, label in enumerate(labels):
                eid = files.label_to_id.get(label)
                if eid is None or label in skip or deleted[i]:
                    continue  # deleted (or added after the metadata was read: written twice)
                if not sane[i]:
                    cur.unreadable += 1
                    continue
                key = split_id(eid)
                if key is None:
                    cur.orphans += 1
                    continue
                keys.append(key)
                rows.append(i)
            async with conn.transaction():
                matched = await _write_page(conn, cfg.model, keys, el.vectors[rows])
                cur.orphans += len(keys) - matched
                cur.next += len(labels)
                copied += matched
                await _save(conn, cur, copied)
            progress.report("load", cur, copied)
    finally:
        if fd >= 0:
            os.close(fd)
    cur.phase = "index"
    await _save(conn, cur, copied)
    progress.report("load", cur, copied, force=True)
    return copied


async def _drop_index(conn: Any) -> None:
    for attempt in range(20):
        try:
            await conn.execute("SET lock_timeout = '10s'")
            await conn.execute(f"DROP INDEX IF EXISTS {HNSW_INDEX}")
            await conn.execute("RESET lock_timeout")
            _log.info("chroma_copy_index_dropped", index=HNSW_INDEX)
            return
        except Exception as exc:  # noqa: BLE001 - the index stage's writes hold the table
            await conn.execute("RESET lock_timeout")
            if attempt == 19:
                raise
            _log.warning("chroma_copy_index_drop_retry", error=f"{type(exc).__name__}: {exc}")
            await asyncio.sleep(5)


def _build_mem(cfg: CopyConfig) -> str:
    if cfg.build_mem:
        return cfg.build_mem
    try:
        import psutil

        total = int(psutil.virtual_memory().total)
    except Exception:  # noqa: BLE001
        total = 16 * GB
    # the graph of 13.9 M halfvec rows is about 18 GB (some 1.3 KB a row): as much of it in
    # memory as the machine spares (a sixth of it, at most 16 GB), the rest built on disk,
    # slower
    return f"{max(1, min(16, total // (6 * GB)))}GB"


async def _build_index(conn: Any, cfg: CopyConfig) -> None:
    """The HNSW index, concurrently: the index stage keeps writing while it builds.
    A build cut by a kill leaves an invalid index; one still running is waited for."""
    while True:
        row = await conn.fetchrow(
            "SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid "
            "WHERE c.relname = $1",
            HNSW_INDEX,
        )
        if row is not None and row["indisvalid"]:
            _log.info("chroma_copy_index_present", index=HNSW_INDEX)
            return
        running = await conn.fetchval(
            "SELECT count(*) FROM pg_stat_progress_create_index p "
            "WHERE p.relid = 'segment_embeddings'::regclass AND p.pid <> pg_backend_pid()"
        )
        if running:
            _log.info("chroma_copy_index_build_waiting")
            await asyncio.sleep(30)
            continue
        if row is not None:
            await conn.execute(f"DROP INDEX CONCURRENTLY IF EXISTS {HNSW_INDEX}")
        break
    mem = _build_mem(cfg)
    workers = cfg.build_workers or max(0, min(7, (os.cpu_count() or 2) // 2))
    await conn.execute(f"SET maintenance_work_mem = '{mem}'")
    await conn.execute(f"SET max_parallel_maintenance_workers = {int(workers)}")
    await conn.execute("SET statement_timeout = 0")
    started = time.monotonic()
    _log.info("chroma_copy_index_building", maintenance_work_mem=mem, workers=workers)
    await conn.execute(
        f"CREATE INDEX CONCURRENTLY IF NOT EXISTS {HNSW_INDEX} ON segment_embeddings "
        "USING hnsw (embedding halfvec_cosine_ops) WITH (m = 16, ef_construction = 64)"
    )
    await conn.execute("RESET maintenance_work_mem")
    await conn.execute("RESET max_parallel_maintenance_workers")
    _log.info("chroma_copy_index_built", seconds=round(time.monotonic() - started, 1))


@dataclass
class Verdict:
    ok: bool
    reason: str
    rows: int = 0
    expected: int = 0
    recall: float = 0.0
    queries: int = 0
    #: the recall at the search's own ef_search, for the log
    at_search: float = 0.0


def as_stored(v: npt.NDArray[np.float32]) -> npt.NDArray[np.float32]:
    """``v`` rounded to halfvec as Postgres stores it, normalised again (cosine)."""
    h = v.astype(np.float16).astype(np.float32)
    norms = np.linalg.norm(h, axis=-1, keepdims=True)
    return np.asarray(h / np.where(norms > 0, norms, 1.0), dtype=np.float32)


def _exact_top(
    files: ChromaFiles, fd: int, queries: npt.NDArray[np.float32], k: int, skip: set[int]
) -> list[list[tuple[str, float]]]:
    """Every query's ``k`` nearest ids among Chroma's live vectors, exactly, by cosine,
    with their cosine distances, on the vectors as Postgres keeps them (16-bit): the
    check is then of the copy and its index, not of near ties the rounding reorders."""
    nq = queries.shape[0]
    best_s = np.full((nq, k), -np.inf, dtype=np.float32)
    best_l = np.full((nq, k), -1, dtype=np.int64)
    chunk = 100_000
    total = files.elements()
    for first in range(0, total, chunk):
        el = files.read(fd, first, min(chunk, total - first))
        known = np.fromiter(
            (lb in files.label_to_id and lb not in skip for lb in el.labels.tolist()),
            dtype=bool,
            count=len(el.labels),
        )
        mask = el.live & known
        if not mask.any():
            continue
        vecs = as_stored(el.vectors[mask])
        labels = el.labels[mask]
        scores = queries @ vecs.T
        take = min(k, scores.shape[1])
        idx = np.argpartition(-scores, take - 1, axis=1)[:, :take]
        merged_s = np.concatenate([best_s, np.take_along_axis(scores, idx, axis=1)], axis=1)
        merged_l = np.concatenate([best_l, labels[idx]], axis=1)
        order = np.argsort(-merged_s, axis=1)[:, :k]
        best_s = np.take_along_axis(merged_s, order, axis=1)
        best_l = np.take_along_axis(merged_l, order, axis=1)
    return [
        [
            (files.label_to_id[lb], 1.0 - float(sc))
            for lb, sc in zip(row_l, row_s, strict=True)
            if lb >= 0
        ]
        for row_l, row_s in zip(best_l.tolist(), best_s.tolist(), strict=True)
    ]


def _sample(files: ChromaFiles, fd: int, n: int, skip: set[int]) -> npt.NDArray[np.float32]:
    """``n`` stored vectors at random (live ones)."""
    total = files.elements()
    rng = random.Random()
    picks: list[npt.NDArray[np.float32]] = []
    tries = 0
    while len(picks) < n and tries < n * 20 and total:
        tries += 1
        el = files.read(fd, rng.randrange(total), 1)
        if len(el.labels) and el.live[0]:
            label = int(el.labels[0])
            if label in files.label_to_id and label not in skip:
                picks.append(el.vectors[0])
    if not picks:
        return np.zeros((0, files.header.dim), dtype=np.float32)
    return np.stack(picks)


async def _verify(
    conn: Any, files: ChromaFiles, cfg: CopyConfig, cur: Cursor, copied: int
) -> Verdict:
    """Count: Postgres holds every row the load copied (less a few the index stage
    deleted since). Recall: sampled stored vectors as queries, the HNSW index's top 10
    against the exact top 10 of Chroma's vectors (what Chroma's own index approximates;
    a second Chroma client next to the index worker's is not opened)."""
    rows = int(await conn.fetchval("SELECT count(*) FROM segment_embeddings"))
    slack = max(100, copied // 1000)
    if cur.unreadable > slack:
        return Verdict(False, f"{cur.unreadable} vectors were unreadable", rows, copied)
    if rows + slack < copied:
        return Verdict(False, f"{rows} rows in Postgres, {copied} copied", rows, copied)
    skip = set(cur.skip)
    if not files.elements():
        # under a thousand writes: no files, nothing for an index to be checked against
        return Verdict(True, "no Chroma files: recall not checked", rows, copied)
    fd = os.open(files.segment_dir / "data_level0.bin", os.O_RDONLY)
    try:
        picked = await asyncio.to_thread(_sample, files, fd, cfg.samples, skip)
        if not len(picked):
            return Verdict(False, "no vector to sample", rows, copied)
        # each query is sent to Postgres exactly as a halfvec (16-bit values print and
        # parse back whole); the exact search uses the same vector
        queries = picked.astype(np.float16).astype(np.float32)
        truth_ids = await asyncio.to_thread(_exact_top, files, fd, as_stored(queries), 30, skip)
    finally:
        os.close(fd)
    keys = {eid: k for ids in truth_ids for eid, _ in ids if (k := split_id(eid)) is not None}
    found = await conn.fetch(
        "SELECT g.tid, g.seq, ts.id FROM unnest($1::uuid[], $2::int[]) AS g(tid, seq) "
        "JOIN transcript_segments ts ON ts.transcript_id = g.tid AND ts.seq = g.seq",
        [k[0] for k in keys.values()],
        [k[1] for k in keys.values()],
    )
    seg = {(r["tid"], int(r["seq"])): int(r["id"]) for r in found}
    # the exact neighbours that are in Postgres (an orphan is not), with their distances
    truths = [
        [(seg[keys[e]], d) for e, d in ids if e in keys and keys[e] in seg][:10]
        for ids in truth_ids
    ]
    recall, counted = await _recall(conn, queries, truths, cfg.ef_search)
    if not counted:
        return Verdict(False, "no sampled query has a neighbour in Postgres", rows, copied)
    at_search, _ = await _recall(conn, queries, truths, cfg.search_ef)
    if recall < cfg.min_recall:
        return Verdict(
            False,
            f"recall@10 {recall:.3f} below {cfg.min_recall}",
            rows,
            copied,
            recall,
            counted,
            at_search,
        )
    return Verdict(True, "", rows, copied, recall, counted, at_search)


#: how much closer than the exact 10th neighbour a result may be counted: Postgres
#: computes the distance in its own arithmetic on the same 16-bit values
TIE_EPS = 1e-3


def tie_hits(got: list[float], truth: list[float], eps: float = TIE_EPS) -> int:
    """How many of the index's results are true nearest neighbours: as close as the exact
    ``len(truth)``-th one. Transcripts repeat themselves (a greeting, an opening formula):
    many passages share one vector, so the exact top 10 is any 10 of them and an id
    overlap would count most of a perfect answer as missed."""
    if not truth:
        return 0
    bound = max(truth) + eps
    return min(len(truth), sum(1 for d in got if d <= bound))


async def _recall(
    conn: Any, queries: npt.NDArray[np.float32], truths: list[list[tuple[int, float]]], ef: int
) -> tuple[float, int]:
    """Mean share of each query's exact top 10 the HNSW index returns at ``ef``, ties
    counted (tie_hits)."""
    total = 0.0
    counted = 0
    async with conn.transaction():
        await conn.execute(f"SET LOCAL hnsw.ef_search = {int(ef)}")
        for q, truth in zip(queries, truths, strict=True):
            if not truth:
                continue
            vec = "[" + ",".join(repr(x) for x in q.tolist()) + "]"
            got = await conn.fetch(
                "SELECT embedding <=> $1::halfvec AS d FROM segment_embeddings "
                "ORDER BY embedding <=> $1::halfvec LIMIT 10",
                vec,
            )
            hits = tie_hits([float(r["d"]) for r in got], [d for _, d in truth])
            total += hits / len(truth)
            counted += 1
    return (total / counted if counted else 0.0), counted


def _free_bytes(path: Path) -> int:
    p = path
    while not p.exists() and p != p.parent:
        p = p.parent
    return int(shutil.disk_usage(p).free)


async def _check_disk(conn: Any, cfg: CopyConfig, cur: Cursor) -> str | None:
    """Room for what is left to copy (about 15 GB in all on the home Mac), on the
    database's disk when it is this machine's, else on the Chroma folder's."""
    where = cfg.chroma_dir
    with contextlib.suppress(Exception):
        data = await conn.fetchval("SELECT current_setting('data_directory')")
        if data and Path(data).exists():
            where = Path(data)
    left = 1.0 - (cur.next / cur.total if cur.total else 0.0)
    need = max(2 * GB, int(cfg.min_free_bytes * left))
    free = _free_bytes(where)
    if free < need:
        return f"{free / GB:.1f} GB free at {where}, the copy needs {need / GB:.1f} GB"
    return None


def _mark_done(cfg: CopyConfig) -> None:
    if cfg.done_file is None:
        return
    cfg.done_file.parent.mkdir(parents=True, exist_ok=True)
    tmp = cfg.done_file.with_suffix(cfg.done_file.suffix + ".tmp")
    tmp.write_text(json.dumps({"chroma_dir": str(cfg.chroma_dir), "done_at": int(time.time())}))
    os.replace(tmp, cfg.done_file)


async def copy_chroma(conn: Any, cfg: CopyConfig) -> int:
    """The whole copy on one connection; an exit code (``EXIT_*``)."""
    if not await _ready(conn):
        _log.error("chroma_copy_not_ready", reason="no pgvector or no segment_embeddings")
        return EXIT_NOT_READY
    if not await conn.fetchval("SELECT pg_try_advisory_lock($1)", LOCK_KEY):
        _log.error("chroma_copy_already_running")
        return EXIT_FAILED
    state = await _state(conn)
    if state.done:
        _log.info("chroma_copy_done_already", copied=state.copied, store=state.store)
        _mark_done(cfg)
        return EXIT_OK
    if not (cfg.chroma_dir / "chroma.sqlite3").exists():
        _log.error("chroma_copy_no_chroma", dir=str(cfg.chroma_dir))
        return EXIT_NOT_READY
    name = semantic.collection_name(cfg.model)
    try:
        files = await asyncio.to_thread(ChromaFiles.open, cfg.chroma_dir, name)
    except LookupError as exc:
        _log.error("chroma_copy_no_collection", error=str(exc))
        return EXIT_NOT_READY
    cur = state.cursor
    copied = state.copied
    if cur is None or cur.segment != files.segment_id:
        cur = Cursor(segment=files.segment_id)
        copied = 0
        await _drop_index(conn)
        await _save(conn, cur, copied)
        _log.info("chroma_copy_starting", collection=name, elements=files.elements())
    else:
        _log.info("chroma_copy_resuming", phase=cur.phase, element=cur.next, total=cur.total)
    progress = Progress(cfg)
    if cur.phase in ("tail", "load"):
        problem = await _check_disk(conn, cfg, cur)
        if problem:
            _log.error("chroma_copy_no_room", reason=problem)
            return EXIT_FAILED
        await asyncio.to_thread(files.load_labels)
        await _temp_table(conn, files.header.dim)
        await conn.execute("SET synchronous_commit = off")
        if cur.phase == "tail":
            await _tail(conn, files, cfg, cur)
        copied = await _load(conn, files, cfg, cur, copied, progress)
    if cur.phase == "index":
        progress.write({"phase": "index", "copied": copied, "total": cur.total})
        await _build_index(conn, cfg)
        cur.phase = "verify"
        await _save(conn, cur, copied)
    progress.write({"phase": "verify", "copied": copied, "total": cur.total})
    if not files.label_to_id:
        await asyncio.to_thread(files.load_labels)
    verdict = await _verify(conn, files, cfg, cur, copied)
    if not verdict.ok:
        _log.error(
            "chroma_copy_check_failed",
            reason=verdict.reason,
            rows=verdict.rows,
            copied=verdict.expected,
            recall=round(verdict.recall, 4),
            ef_search=cfg.ef_search,
            recall_at_search_ef=round(verdict.at_search, 4),
            queries=verdict.queries,
            orphans=cur.orphans,
        )
        progress.write({"phase": "failed", "reason": verdict.reason, "copied": copied})
        return EXIT_VERIFY_FAILED
    if cfg.switch:
        cur.phase = "done"
        await _save(conn, cur, copied)
        await conn.execute(
            "UPDATE semantic_store_state SET copy_done_at = now(), store = 'postgres', "
            "updated_at = now()"
        )
        _mark_done(cfg)
    _log.info(
        "chroma_copy_done",
        rows=verdict.rows,
        copied=copied,
        orphans=cur.orphans,
        recall=round(verdict.recall, 4),
        ef_search=cfg.ef_search,
        recall_at_search_ef=round(verdict.at_search, 4),
        queries=verdict.queries,
        switched=cfg.switch,
    )
    progress.write({"phase": "done", "copied": copied, "recall": round(verdict.recall, 4)})
    return EXIT_OK


# ------------------------------------------------------------------ the command


def add_parser(subparsers: Any) -> None:
    p = subparsers.add_parser(
        "copy-chroma",
        help="Copy the semantic search vectors from Chroma into Postgres, once (resumable).",
    )
    p.add_argument(
        "--database-url",
        default=os.getenv("MAKTABA_DATABASE_URL") or None,
        help="The home's Postgres (default MAKTABA_DATABASE_URL).",
    )
    p.add_argument(
        "--chroma-dir",
        default=None,
        help="The Chroma folder (default MAKTABA_CHROMA_DIR, ~/.maktaba/chroma).",
    )
    p.add_argument("--page", type=int, default=PAGE, help="Vectors per page (default 5000).")
    p.add_argument("--samples", type=int, default=1000, help="Recall check queries.")
    p.add_argument("--min-recall", type=float, default=0.95, help="Recall@10 to pass.")
    p.add_argument("--ef-search", type=int, default=400, help="hnsw.ef_search of the check.")
    p.add_argument(
        "--min-free-gb",
        type=float,
        default=float(os.getenv("MAKTABA_COPY_CHROMA_MIN_FREE_GB", "15")),
        help="Free disk the whole copy needs (MAKTABA_COPY_CHROMA_MIN_FREE_GB).",
    )
    p.add_argument(
        "--build-mem",
        default=os.getenv("MAKTABA_COPY_CHROMA_BUILD_MEM", ""),
        help="maintenance_work_mem for the index build (default: a sixth of RAM, ≤ 16GB).",
    )
    p.add_argument(
        "--progress-file",
        default=os.getenv("MAKTABA_COPY_CHROMA_PROGRESS") or None,
        help="Where to keep the progress as JSON (MAKTABA_COPY_CHROMA_PROGRESS).",
    )
    p.add_argument(
        "--done-file",
        default=os.getenv("MAKTABA_COPY_CHROMA_DONE") or None,
        help="Written once the copy is checked and search switched (MAKTABA_COPY_CHROMA_DONE).",
    )
    p.add_argument(
        "--no-switch",
        action="store_true",
        help="Copy and check, but leave search on Chroma.",
    )


async def _run(args: argparse.Namespace) -> int:
    import asyncpg  # type: ignore[import-untyped]

    cfg = CopyConfig(
        chroma_dir=Path(args.chroma_dir or semantic.chroma_dir()).expanduser(),
        model=semantic.embed_model(),
        page=max(1, args.page),
        samples=max(1, args.samples),
        min_recall=args.min_recall,
        ef_search=args.ef_search,
        min_free_bytes=int(args.min_free_gb * GB),
        build_mem=args.build_mem,
        progress_file=Path(args.progress_file) if args.progress_file else None,
        done_file=Path(args.done_file) if args.done_file else None,
        switch=not args.no_switch,
    )
    from ..db.pgtls import connect_args

    dsn, tls = connect_args(args.database_url)
    conn = await asyncpg.connect(dsn, ssl=tls)
    try:
        return await copy_chroma(conn, cfg)
    finally:
        with contextlib.suppress(Exception):
            await asyncio.wait_for(conn.close(), timeout=5)


def main(args: argparse.Namespace) -> int:
    url = args.database_url or ""
    if not url.startswith(("postgres://", "postgresql://")):
        sys.stderr.write("copy-chroma needs the home's Postgres (--database-url)\n")
        return EXIT_NOT_READY

    async def guarded() -> int:
        task = asyncio.current_task()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            # a stop cancels the statement running (asyncpg cancels it on the server);
            # the cursor has every page committed so far
            with contextlib.suppress(NotImplementedError, RuntimeError, AttributeError):
                if task is not None:
                    loop.add_signal_handler(sig, task.cancel)
        try:
            return await _run(args)
        except asyncio.CancelledError:
            _log.info("chroma_copy_stopped")
            return 143

    try:
        return asyncio.run(guarded())
    except Exception as exc:
        _log.error("chroma_copy_failed", error=f"{type(exc).__name__}: {exc}", exc_info=exc)
        return EXIT_FAILED
