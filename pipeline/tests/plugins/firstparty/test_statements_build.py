"""tools/plugins/statements_build.py's scan of a stage's statements (story 54.24 §1)."""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "tools" / "plugins"))

import statements_build as sb  # noqa: E402


def test_a_clause_appended_in_a_branch_is_both_statements() -> None:
    """``sql += " AND …"`` under an ``if``: the runner may run either text, so both are
    registered (pgstore.video_vectors with and without a transcript)."""
    src = (
        "async def f(db, t=None):\n"
        '    sql = "SELECT a FROM x WHERE v = $1"\n'
        "    if t is not None:\n"
        '        sql += " AND w = $2"\n'
        '    return await db.fetch(sql + " ORDER BY a", 1)\n'
    )
    got = sorted(x.text or "" for x in sb.scan_source(src, "m.py", module="m"))
    assert got == [
        "SELECT a FROM x WHERE v = $1 AND w = $2 ORDER BY a",
        "SELECT a FROM x WHERE v = $1 ORDER BY a",
    ]


def test_a_text_built_from_run_time_values_is_reported() -> None:
    src = "async def f(db, t):\n    return await db.fetch(f'SELECT {t}')\n"
    (x,) = sb.scan_source(src, "m.py", module="m")
    assert x.text is None and x.why == "built at run time"
