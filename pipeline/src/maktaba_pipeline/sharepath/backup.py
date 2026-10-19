"""A copy of the path columns, kept inside the database (story 31.3, automatic).

Before the home converts the stored paths it copies every value of every column the
conversion may touch (:data:`~.migrate.COLUMNS`) into ``storage_path_backups`` under
one backup id: one ``INSERT ... SELECT`` per column, no shell, no ``pg_dump``, the
same on Postgres and SQLite. ``storage restore-backup`` writes a backup's values back
(the last resort behind ``storage revert``, which undoes one batch value by value).
The home keeps the last few backups (rules ``storage.backups_kept``) and the first one.

A backup also holds two ``_meta`` rows: the machine it was taken on and the batch
its conversion wrote, so a restore can mark that batch reverted.
"""

from __future__ import annotations

import uuid
from collections.abc import Sequence
from typing import Any

from ..engine.sql import is_pg
from ..log import get_logger
from .migrate import COLUMNS, Column, _table_exists

__all__ = ["list_backups", "prune_backups", "restore_backup", "set_backup_batch", "take_backup"]

_log = get_logger()
META = "_meta"


async def _meta(db: Any, backup_id: str, name: str, value: str) -> None:
    cast = "::uuid" if is_pg(db) else ""
    await db.execute(
        "DELETE FROM storage_path_backups"
        f" WHERE backup_id = $1{cast} AND table_name = $2 AND column_name = $3",
        backup_id,
        META,
        name,
    )
    await db.execute(
        "INSERT INTO storage_path_backups (backup_id, table_name, column_name, row_key, value)"
        f" VALUES ($1{cast}, $2, $3, $3, $4)",
        backup_id,
        META,
        name,
        value,
    )


async def take_backup(
    db: Any, *, machine: str, columns: Sequence[Column] = COLUMNS, backup_id: str | None = None
) -> tuple[str, int]:
    """Copy every path value; ``(backup id, values copied)``."""
    bid = backup_id or str(uuid.uuid4())
    pg = is_pg(db)
    n = 0
    async with db.transaction():
        for col in columns:
            if not await _table_exists(db, col.table):
                continue
            if pg:
                value = f"array_to_json({col.column})::text" if col.array else f"{col.column}::text"
                sql = (
                    "INSERT INTO storage_path_backups"
                    " (backup_id, table_name, column_name, row_key, value)"
                    f" SELECT $1::uuid, $2::text, $3::text, {col.key}::text, {value}"
                    f" FROM {col.table} WHERE {col.column} IS NOT NULL ON CONFLICT DO NOTHING"
                )
            else:
                sql = (
                    "INSERT OR IGNORE INTO storage_path_backups"
                    " (backup_id, table_name, column_name, row_key, value)"
                    f" SELECT $1, $2, $3, CAST({col.key} AS TEXT), CAST({col.column} AS TEXT)"
                    f" FROM {col.table} WHERE {col.column} IS NOT NULL"
                )
            await db.execute(sql, bid, col.table, col.column)
        await _meta(db, bid, "machine", machine)
    cast = "::uuid" if pg else ""
    n = int(
        await db.fetchval(
            f"SELECT COUNT(*) FROM storage_path_backups WHERE backup_id = $1{cast}"
            " AND table_name <> $2",
            bid,
            META,
        )
        or 0
    )
    _log.info("storage_path_backup_taken", backup=bid, values=n)
    return bid, n


async def set_backup_batch(db: Any, backup_id: str, batch: str) -> None:
    await _meta(db, backup_id, "batch", batch)


async def list_backups(db: Any) -> list[dict[str, Any]]:
    """Newest first: id, when, values, machine, batch."""
    try:
        rows = await db.fetch(
            "SELECT backup_id, MIN(created_at) AS at,"
            " SUM(CASE WHEN table_name = $1 THEN 0 ELSE 1 END) AS n,"
            " MAX(CASE WHEN table_name = $1 AND column_name = 'machine' THEN value END) AS machine,"
            " MAX(CASE WHEN table_name = $1 AND column_name = 'batch' THEN value END) AS batch"
            " FROM storage_path_backups GROUP BY backup_id ORDER BY MIN(created_at) DESC",
            META,
        )
    except Exception as exc:  # noqa: BLE001 - an older schema: no backups
        _log.debug("storage_path_backups_unavailable", error=str(exc))
        return []
    return [
        {
            "backup": str(r["backup_id"]),
            "at": str(r["at"]),
            "values": int(r["n"] or 0),
            "machine": r["machine"],
            "batch": r["batch"],
        }
        for r in rows
    ]


async def prune_backups(db: Any, keep: int) -> list[str]:
    """Drop all but the ``keep`` newest backups and the oldest one; the ids dropped.

    The oldest is the copy taken before the first conversion, the only one that still
    holds the paths as the scanner stored them: every later run backs up values the
    first one already converted, so after ``keep`` more runs a restore could no longer
    reach the original form."""
    listed = await list_backups(db)
    drop = [b["backup"] for b in listed[max(keep, 1) : -1]]
    cast = "::uuid" if is_pg(db) else ""
    for bid in drop:
        await db.execute(f"DELETE FROM storage_path_backups WHERE backup_id = $1{cast}", bid)
    return drop


async def restore_backup(db: Any, backup_id: str | None = None) -> tuple[str | None, int]:
    """Write a backup's values back (the newest when ``backup_id`` is ``None``);
    ``(backup id, rows written)``. The batch its conversion wrote is marked reverted."""
    listed = await list_backups(db)
    if backup_id is None:
        if not listed:
            return None, 0
        backup_id = listed[0]["backup"]
    info = next((b for b in listed if b["backup"] == backup_id), None)
    if info is None:
        raise ValueError(f"no backup {backup_id}")
    pg = is_pg(db)
    n = 0
    async with db.transaction():
        for col in COLUMNS:
            if not await _table_exists(db, col.table):
                continue
            if pg:
                value = (
                    "ARRAY(SELECT json_array_elements_text(b.value::json))"
                    if col.array
                    else "b.value"
                )
                status = await db.execute(
                    f"UPDATE {col.table} AS t SET {col.column} = {value}"
                    " FROM storage_path_backups b"
                    " WHERE b.backup_id = $1::uuid AND b.table_name = $2 AND b.column_name = $3"
                    f" AND b.row_key = t.{col.key}::text"
                    f" AND t.{col.column} IS DISTINCT FROM {value}",
                    backup_id,
                    col.table,
                    col.column,
                )
                n += _rowcount(status)
                continue
            sel = (
                "SELECT b.value FROM storage_path_backups b WHERE b.backup_id = $1"
                " AND b.table_name = $2 AND b.column_name = $3"
                f" AND b.row_key = CAST(t.{col.key} AS TEXT)"
            )
            before = int(
                await db.fetchval(
                    f"SELECT COUNT(*) FROM {col.table} AS t WHERE EXISTS ({sel})"
                    f" AND t.{col.column} IS NOT ({sel})",
                    backup_id,
                    col.table,
                    col.column,
                )
                or 0
            )
            await db.execute(
                f"UPDATE {col.table} AS t SET {col.column} = ({sel})"
                f" WHERE EXISTS ({sel}) AND t.{col.column} IS NOT ({sel})",
                backup_id,
                col.table,
                col.column,
            )
            n += before
        if info.get("batch"):
            cast = "::uuid" if pg else ""
            now_sql = "now()" if pg else "CURRENT_TIMESTAMP"
            await db.execute(
                f"UPDATE storage_path_migrations SET reverted_at = {now_sql}"
                f" WHERE batch_id = $1{cast} AND reverted_at IS NULL",
                info["batch"],
            )
    _log.info("storage_path_backup_restored", backup=backup_id, rows=n)
    return backup_id, n


def _rowcount(status: Any) -> int:
    """asyncpg's ``UPDATE 12`` status text."""
    try:
        return int(str(status).rsplit(" ", 1)[-1])
    except (ValueError, IndexError):
        return 0
