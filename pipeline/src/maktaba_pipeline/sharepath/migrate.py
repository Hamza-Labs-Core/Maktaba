"""Convert stored media paths to the canonical form, and back (story 31.3).

Run on the **home** (the machine whose scanner wrote the paths): its mount table says
which share each local prefix is, so the mapping is machine-specific and cannot be a
blind SQL migration. ``plan`` is the dry run; ``apply`` writes every change with its
old value to ``storage_path_migrations`` under one batch id; ``revert`` puts the old
values back where a column still holds the new one. All three are idempotent: a value
already canonical is left alone, a reverted batch is not reverted twice.

Columns: the paths that point at files on the shares - ``videos.path``,
``libraries.roots``, ``library_roots.path`` / ``path_canonical``,
``subtitle_files.path`` (generated subtitles beside the media), ``video_repairs.
backup_path`` and ``purge_log.path``. The home's caches (``poster_path``,
``sprite_path``, ``audio_cache.path``, channel art, review pages) are not on a share
and stay as they are; a value not under any share mount is reported as *unmatched*
and left alone (``--include-local`` writes ``file:///...`` for those).
"""

from __future__ import annotations

import json
import os
import platform
import random
import uuid
from collections import Counter
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..db import schema_compat
from ..engine.sql import is_pg, loads
from .canonical import format_canonical, is_canonical, nfc, parse_canonical
from .resolver import Resolver

__all__ = ["COLUMNS", "Column", "ColumnReport", "MigrationPlan", "apply", "plan", "revert"]

CHUNK = 2000


@dataclass(frozen=True, slots=True)
class Column:
    table: str
    key: str
    column: str
    key_type: str  #: Postgres cast of the key (``uuid`` | ``bigint``)
    array: bool = False  #: ``libraries.roots`` (TEXT[] on Postgres, JSON text on SQLite)
    #: the column the value is unique together with (``library_roots``: ``UNIQUE
    #: (library_id, path_canonical)``): two rows must not be given one value
    unique_with: str | None = None
    #: the rows whose file must exist, as SQL (the dry run's sampled check opens only
    #: those); ``None``: never checked. A purged file (``purge_log``), a soft-deleted
    #: video or subtitle and a repair's kept backup are gone on purpose, and a check
    #: that sampled them would hold the conversion back for good.
    verify: str | None = "1 = 1"


COLUMNS: tuple[Column, ...] = (
    Column("libraries", "id", "roots", "uuid", array=True),
    Column("library_roots", "id", "path", "uuid"),
    Column("library_roots", "id", "path_canonical", "uuid", unique_with="library_id"),
    Column("videos", "id", "path", "uuid", verify="deleted_at IS NULL"),
    Column("subtitle_files", "id", "path", "bigint", verify="deleted_at IS NULL"),
    Column("video_repairs", "video_id", "backup_path", "uuid", verify=None),
    Column("purge_log", "id", "path", "bigint", verify=None),
)


@dataclass
class ColumnReport:
    table: str
    column: str
    rows: int = 0
    already: int = 0
    converted: int = 0
    unmatched: int = 0
    collisions: int = 0
    #: values on no share whose only change is NFC
    nfc_only: int = 0
    by_share: Counter[str] = field(default_factory=Counter)
    unmatched_samples: list[str] = field(default_factory=list)
    examples: list[tuple[str, str]] = field(default_factory=list)
    #: (canonical, local, exists) for a random sample, checked through the resolver
    verified: list[tuple[str, str, bool]] = field(default_factory=list)

    def as_json(self) -> dict[str, Any]:
        return {
            "table": self.table,
            "column": self.column,
            "rows": self.rows,
            "already_canonical": self.already,
            "to_convert": self.converted,
            "unmatched": self.unmatched,
            "collisions": self.collisions,
            "nfc_only": self.nfc_only,
            "by_share": dict(self.by_share.most_common()),
            "unmatched_samples": self.unmatched_samples,
            "examples": [{"from": a, "to": b} for a, b in self.examples],
            "verified": [{"path": a, "local": b, "exists": c} for a, b, c in self.verified],
        }


@dataclass
class MigrationPlan:
    machine: str
    shares: list[dict[str, str]]
    columns: list[ColumnReport]
    #: (column, key, old, new) of every change
    changes: list[tuple[Column, str, str, str]] = field(default_factory=list)

    @property
    def total(self) -> int:
        return len(self.changes)

    def as_json(self) -> dict[str, Any]:
        return {
            "machine": self.machine,
            "shares": self.shares,
            "changes": self.total,
            "columns": [c.as_json() for c in self.columns],
        }


def convert(value: str, resolver: Resolver, *, include_local: bool = False) -> str | None:
    """The canonical form of one stored value; ``None`` when it stays as it is.

    Every value comes out NFC: a canonical value is re-normalised, a value on no share
    (left in its local form) is still turned NFC, so a name the Mac stored NFD opens
    on Windows and Linux too."""
    if not value:
        return None
    if is_canonical(value):
        n = nfc(value)
        return n if n != value else None
    c = resolver.canonical(value)
    if c is not None:
        return c
    if include_local and (value.startswith("/") or (len(value) > 2 and value[1] == ":")):
        return format_canonical("file", "", "", value)
    n = nfc(value)
    return n if n != value else None


async def _rows(db: Any, col: Column) -> list[tuple[str, Any, str | None, bool]]:
    """``(key, value, unique_with value, file must exist)`` of every row with a value,
    in key order."""
    out: list[tuple[str, Any, str | None, bool]] = []
    group = f", {col.unique_with} AS g" if col.unique_with else ""
    if col.verify is not None:
        group += f", CASE WHEN {col.verify} THEN 1 ELSE 0 END AS live"
    last: Any = None
    while True:
        cast = f"::{col.key_type}" if is_pg(db) else ""
        where = f"WHERE {col.column} IS NOT NULL" + (
            f" AND {col.key} > $1{cast}" if last is not None else ""
        )
        sql = (
            f"SELECT {col.key} AS k, {col.column} AS v{group} FROM {col.table} {where}"
            f" ORDER BY {col.key} LIMIT {CHUNK}"
        )
        rows = await (db.fetch(sql, last) if last is not None else db.fetch(sql))
        if not rows:
            return out
        for r in rows:
            out.append(
                (
                    str(r["k"]),
                    r["v"],
                    str(r["g"]) if col.unique_with else None,
                    col.verify is not None and bool(r["live"]),
                )
            )
        last = rows[-1]["k"]
        if not isinstance(last, int):
            last = str(last)
        if len(rows) < CHUNK:
            return out


async def _table_exists(db: Any, table: str) -> bool:
    """False only when the table is missing (an older schema). Any other failure is
    raised: taken for a missing table it left the table out of a backup, a dry run or
    a restore, silently."""
    try:
        await db.fetch(f"SELECT 1 FROM {table} LIMIT 1")
    except Exception as exc:
        if schema_compat.older_schema(exc):
            return False
        raise
    return True


def _array(value: Any) -> list[str]:
    if isinstance(value, str):
        v = loads(value)
        return [str(x) for x in v] if isinstance(v, list) else [value]
    return [str(x) for x in (value or [])]


async def plan(
    db: Any,
    resolver: Resolver,
    *,
    include_local: bool = False,
    columns: Sequence[Column] = COLUMNS,
    verify: int = 20,
    exists: Callable[[str], bool] = os.path.exists,
    machine: str | None = None,
) -> MigrationPlan:
    """The dry run: what would change, per column, without writing anything."""
    result = MigrationPlan(
        machine=machine or platform.node(), shares=resolver.share_list(), columns=[]
    )
    for col in columns:
        rep = ColumnReport(col.table, col.column)
        result.columns.append(rep)
        if not await _table_exists(db, col.table):
            continue
        rows = await _rows(db, col)
        # (group, value) -> the row holding it: a unique column's values as they stand,
        # so a change onto a value another row keeps (or already took) is a collision,
        # left as it is, rather than a constraint error that stops the apply mid-way
        taken: dict[tuple[str, str], str] = {}
        if col.unique_with:
            for key, raw, grp, _ in rows:
                taken.setdefault((grp or "", nfc(str(raw)).lower()), key)
        live: set[str] = set()
        for key, raw, grp, must_exist in rows:
            rep.rows += 1
            values = _array(raw) if col.array else [str(raw)]
            new_values: list[str] = []
            changed = False
            for v in values:
                nv = convert(v, resolver, include_local=include_local)
                if nv is not None and not is_canonical(nv):
                    rep.nfc_only += 1
                    rep.unmatched += 1
                if nv is None:
                    if is_canonical(v):
                        rep.already += 1
                    else:
                        rep.unmatched += 1
                        if len(rep.unmatched_samples) < 5:
                            rep.unmatched_samples.append(v)
                    new_values.append(v)
                    continue
                changed = True
                sp = parse_canonical(nv)
                rep.by_share[sp.prefix if sp else "(left local, made NFC)"] += 1
                if len(rep.examples) < 3:
                    rep.examples.append((v, nv))
                new_values.append(nv)
            if not changed:
                continue
            old_text = json.dumps(values, ensure_ascii=False) if col.array else values[0]
            new_text = json.dumps(new_values, ensure_ascii=False) if col.array else new_values[0]
            if col.unique_with:
                # (library_id, path_canonical) is unique: two spellings of one folder
                ident = (grp or "", nfc(new_text).lower())
                holder = taken.get(ident)
                if holder is not None and holder != key:
                    rep.collisions += 1
                    continue
                taken[ident] = key
            rep.converted += 1
            result.changes.append((col, key, old_text, new_text))
            if must_exist:
                live.add(key)
        samples = [c for c in result.changes if c[0] is col and not col.array and c[1] in live]
        for _, _, _, new in random.sample(samples, min(verify, len(samples))):
            local = resolver.to_local(new)
            rep.verified.append((new, local, bool(exists(local))))
    return result


Progress = Callable[[int, int], Awaitable[None]]


async def apply(
    db: Any,
    p: MigrationPlan,
    *,
    batch: str | None = None,
    chunk_size: int = CHUNK,
    progress: Progress | None = None,
) -> str | None:
    """Write the plan's changes (each with its backup row); the batch id, or ``None``
    when there was nothing to do.

    Each chunk is one transaction, so a run cut short leaves whole chunks written;
    passing the same ``batch`` again (the automatic conversion resuming) adds the rest
    to it. ``progress(done, total)`` is awaited after every chunk."""
    if not p.changes:
        return None
    batch = batch or str(uuid.uuid4())
    pg = is_pg(db)
    size = max(1, int(chunk_size))
    by_col: dict[Column, list[tuple[str, str, str]]] = {}
    for col, key, old, new in p.changes:
        by_col.setdefault(col, []).append((key, old, new))
    done, total = 0, p.total
    for col, items in by_col.items():
        for i in range(0, len(items), size):
            chunk = items[i : i + size]
            async with db.transaction():
                await _backup(db, batch, col, chunk, p.machine)
                await _update(db, col, chunk, pg=pg)
            done += len(chunk)
            if progress is not None:
                await progress(done, total)
    return batch


async def _backup(
    db: Any, batch: str, col: Column, chunk: Sequence[tuple[str, str, str]], machine: str
) -> None:
    if is_pg(db):
        await db.execute(
            "INSERT INTO storage_path_migrations"
            " (batch_id, table_name, column_name, row_key, old_value, new_value, machine)"
            " SELECT $1::uuid, $2, $3, u.k, u.o, u.n, $4"
            " FROM unnest($5::text[], $6::text[], $7::text[]) AS u(k, o, n)"
            " ON CONFLICT DO NOTHING",
            batch,
            col.table,
            col.column,
            machine,
            [c[0] for c in chunk],
            [c[1] for c in chunk],
            [c[2] for c in chunk],
        )
        return
    for key, old, new in chunk:
        await db.execute(
            "INSERT OR IGNORE INTO storage_path_migrations"
            " (batch_id, table_name, column_name, row_key, old_value, new_value, machine)"
            " VALUES ($1, $2, $3, $4, $5, $6, $7)",
            batch,
            col.table,
            col.column,
            key,
            old,
            new,
            machine,
        )


async def _update(
    db: Any, col: Column, chunk: Sequence[tuple[str, str, str]], *, pg: bool, reverse: bool = False
) -> None:
    """Set ``new`` where the column still holds ``old`` (``reverse``: the other way)."""
    pairs = [(k, n, o) if reverse else (k, o, n) for k, o, n in chunk]
    if pg:
        if col.array:
            for key, frm, to in pairs:
                await db.execute(
                    f"UPDATE {col.table} SET {col.column} = $2::text[]"
                    f" WHERE {col.key} = $1::{col.key_type} AND {col.column} = $3::text[]",
                    key,
                    json.loads(to),
                    json.loads(frm),
                )
            return
        await db.execute(
            f"UPDATE {col.table} AS t SET {col.column} = u.n"
            f" FROM unnest($1::text[], $2::text[], $3::text[]) AS u(k, o, n)"
            f" WHERE t.{col.key} = u.k::{col.key_type} AND t.{col.column} = u.o",
            [p[0] for p in pairs],
            [p[1] for p in pairs],
            [p[2] for p in pairs],
        )
        return
    for key, frm, to in pairs:
        if col.array:
            # SQLite keeps the JSON text; compare decoded, write compact JSON as stored
            row = await db.fetchrow(
                f"SELECT {col.column} AS v FROM {col.table} WHERE {col.key} = $1", key
            )
            if row is None or _array(row["v"]) != json.loads(frm):
                continue
            await db.execute(
                f"UPDATE {col.table} SET {col.column} = $2 WHERE {col.key} = $1",
                key,
                json.dumps(json.loads(to), ensure_ascii=False),
            )
            continue
        await db.execute(
            f"UPDATE {col.table} SET {col.column} = $2 WHERE {col.key} = $1 AND {col.column} = $3",
            key,
            to,
            frm,
        )


async def batches(db: Any) -> list[dict[str, Any]]:
    rows = await db.fetch(
        "SELECT batch_id, machine, MIN(created_at) AS at, COUNT(*) AS n,"
        " COUNT(reverted_at) AS reverted FROM storage_path_migrations"
        " GROUP BY batch_id, machine ORDER BY MIN(created_at) DESC"
    )
    return [
        {
            "batch": str(r["batch_id"]),
            "machine": r["machine"],
            "at": str(r["at"]),
            "changes": int(r["n"]),
            "reverted": int(r["reverted"]),
        }
        for r in rows
    ]


async def revert(db: Any, batch: str | None = None) -> tuple[str | None, int]:
    """Put a batch's old values back (the latest when ``batch`` is ``None``);
    ``(batch, rows reverted)``."""
    if batch is None:
        listed = [b for b in await batches(db) if b["reverted"] < b["changes"]]
        if not listed:
            return None, 0
        batch = listed[0]["batch"]
    pg = is_pg(db)
    cast = "::uuid" if pg else ""
    rows = await db.fetch(
        "SELECT table_name, column_name, row_key, old_value, new_value"
        f" FROM storage_path_migrations WHERE batch_id = $1{cast} AND reverted_at IS NULL",
        batch,
    )
    cols = {(c.table, c.column): c for c in COLUMNS}
    grouped: dict[Column, list[tuple[str, str, str]]] = {}
    for r in rows:
        col = cols.get((r["table_name"], r["column_name"]))
        if col is not None:
            grouped.setdefault(col, []).append((r["row_key"], r["old_value"], r["new_value"]))
    n = 0
    for col, items in grouped.items():
        for i in range(0, len(items), CHUNK):
            chunk = items[i : i + CHUNK]
            async with db.transaction():
                await _update(db, col, chunk, pg=pg, reverse=True)
                if pg:
                    await db.execute(
                        "UPDATE storage_path_migrations SET reverted_at = now()"
                        " WHERE batch_id = $1::uuid AND table_name = $2 AND column_name = $3"
                        " AND row_key = ANY($4::text[])",
                        batch,
                        col.table,
                        col.column,
                        [c[0] for c in chunk],
                    )
                    n += len(chunk)
                    continue
                for key, _, _ in chunk:
                    await db.execute(
                        "UPDATE storage_path_migrations SET reverted_at = CURRENT_TIMESTAMP"
                        f" WHERE batch_id = $1{cast} AND table_name = $2 AND column_name = $3"
                        " AND row_key = $4",
                        batch,
                        col.table,
                        col.column,
                        key,
                    )
            n += len(chunk)
    return batch, n
