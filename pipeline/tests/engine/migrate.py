"""Minimal goose-compatible migration applier for tests.

Only understands what the repo's migrations use: ``-- +goose Up`` /
``Down`` sections, ``StatementBegin``/``StatementEnd`` blocks and plain
``;``-terminated statements. Used to apply the real migration files to a
real SQLite database (always) or a real Postgres (when
``MAKTABA_TEST_PG_URL`` is set), so the engine tests run against the
shipped schema rather than a hand-written copy.
"""

from __future__ import annotations

import re
import sqlite3
from pathlib import Path

MIGRATIONS_DIR = Path(__file__).resolve().parents[3] / "shared" / "db" / "migrations"

_SLOT_RE = re.compile(r"^(\d{4})_.*?(\.sqlite)?\.sql$")


def list_migrations(*, sqlite: bool, upto: int | None = None) -> list[Path]:
    """Migration files in slot order for one dialect."""
    out: list[tuple[int, Path]] = []
    for p in MIGRATIONS_DIR.glob("[0-9][0-9][0-9][0-9]_*.sql"):
        m = _SLOT_RE.match(p.name)
        if not m:
            continue
        is_sqlite = m.group(2) is not None
        if is_sqlite != sqlite:
            continue
        slot = int(m.group(1))
        if upto is not None and slot > upto:
            continue
        out.append((slot, p))
    return [p for _, p in sorted(out)]


def up_statements(sql: str) -> list[str]:
    """Statements of the Up section, in order."""
    return _section_statements(sql, "Up")


def down_statements(sql: str) -> list[str]:
    """Statements of the Down section, in order."""
    return _section_statements(sql, "Down")


def _section_statements(sql: str, section: str) -> list[str]:
    up: list[str] = []
    in_up = False
    for line in sql.splitlines():
        s = line.strip()
        if s.startswith("-- +goose Up") or s.startswith("-- +goose Down"):
            if in_up:
                break
            in_up = s.startswith(f"-- +goose {section}")
            continue
        if in_up:
            up.append(line)
    stmts: list[str] = []
    buf: list[str] = []
    in_block = False
    for line in up:
        s = line.strip()
        if s.startswith("-- +goose StatementBegin"):
            in_block = True
            buf = []
            continue
        if s.startswith("-- +goose StatementEnd"):
            text = "\n".join(buf).strip()
            if text and _has_code(text):
                stmts.append(text)
            buf = []
            in_block = False
            continue
        if s.startswith("-- +goose"):
            continue
        buf.append(line)
        if not in_block and s.endswith(";") and not s.startswith("--"):
            text = "\n".join(buf).strip()
            if _has_code(text):
                stmts.append(text)
            buf = []
    tail = "\n".join(buf).strip()
    if tail and _has_code(tail):
        stmts.append(tail)
    return stmts


def _has_code(text: str) -> bool:
    return any(ln.strip() and not ln.strip().startswith("--") for ln in text.splitlines())


_ADD_COL_IF_NOT_EXISTS = re.compile(
    r"ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+(\w+)\s+(.*?);?\s*$",
    re.IGNORECASE | re.DOTALL,
)


def apply_sqlite(
    conn: sqlite3.Connection, *, upto: int | None = None, strict_from: int = 87
) -> list[str]:
    """Apply the SQLite twins to ``conn`` in slot order.

    Slots below ``strict_from`` are applied tolerantly: stock SQLite has no
    ``ADD COLUMN IF NOT EXISTS`` (Q-note in plan 06-11), so that form is
    rewritten to a PRAGMA-guarded ADD COLUMN, and a failing legacy statement is
    recorded and skipped. Slots ``>= strict_from`` (this workstream) must apply
    unmodified: any error raises.
    """
    problems: list[str] = []
    for path in list_migrations(sqlite=True, upto=upto):
        slot = int(path.name[:4])
        for stmt in up_statements(path.read_text(encoding="utf-8")):
            if slot >= strict_from:
                # strict: any error raises. ``ADD COLUMN IF NOT EXISTS`` (which
                # migration-lint requires) is rewritten exactly as the Go migrator does
                # in production (api/migrate_sqlite.go), the only rewrite allowed
                apply_statement(conn, stmt)
                continue
            try:
                _apply_tolerant(conn, stmt)
            except sqlite3.Error as exc:
                problems.append(f"{path.name}: {exc}")
    conn.commit()
    return problems


def apply_statement(conn: sqlite3.Connection, stmt: str) -> None:
    """Run one SQLite migration statement; ``ADD COLUMN IF NOT EXISTS`` is rewritten as
    the Go migrator does (api/migrate_sqlite.go). Any error raises."""
    _apply_tolerant(conn, stmt)


def _apply_tolerant(conn: sqlite3.Connection, stmt: str) -> None:
    body = "\n".join(ln for ln in stmt.splitlines() if not ln.strip().startswith("--")).strip()
    m = _ADD_COL_IF_NOT_EXISTS.match(body)
    if m:
        table, col, rest = m.group(1), m.group(2), m.group(3)
        cols = {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}
        if col not in cols:
            conn.execute(f"ALTER TABLE {table} ADD COLUMN {col} {rest}")
        return
    conn.executescript(stmt if stmt.rstrip().endswith(";") else stmt + ";")


async def apply_postgres(
    conn: object, *, upto: int | None = None, strict_from: int = 87
) -> list[str]:
    """Apply the Postgres migrations (Up sections) with an asyncpg connection.

    Statements run one at a time in autocommit mode, which is what lets
    ``CREATE INDEX CONCURRENTLY`` work (goose's no-transaction mode). Legacy
    slots below ``strict_from`` tolerate failures that stem from contrib
    extensions a minimal test server lacks (pgcrypto, unaccent, pg_trgm,
    pgvector); those are returned as problems. This workstream's slots must
    apply cleanly: any error raises.
    """
    problems: list[str] = []
    for path in list_migrations(sqlite=False, upto=upto):
        slot = int(path.name[:4])
        for stmt in up_statements(path.read_text(encoding="utf-8")):
            try:
                await conn.execute(stmt)  # type: ignore[attr-defined]
            except Exception as exc:
                if slot < strict_from:
                    problems.append(f"{path.name}: {exc}")
                    continue
                raise RuntimeError(f"{path.name}: {exc} | {stmt[:600]}") from exc
    return problems


def _is_optional_extension(stmt: str) -> bool:
    lines = [ln for ln in stmt.splitlines() if not ln.strip().startswith("--")]
    return " ".join(lines).strip().upper().startswith("CREATE EXTENSION")
