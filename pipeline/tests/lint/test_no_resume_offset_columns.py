"""No table other than ``processing_jobs`` carries a resume offset.

Resume positioning is owned by ``processing_jobs.last_segment_end_sec``
(Story 6.10 / architecture §7.6). Adding a parallel column anywhere
else creates an invariant gap that silent reads can fall into.

The lint reads every migration in ``shared/db/migrations`` and
flags column declarations whose names contain any forbidden suffix.
The check intentionally does not match free-text occurrences: docs
and inline SQL comments may discuss the concept freely.
"""

from __future__ import annotations

import pathlib
import re

MIGRATIONS = (
    pathlib.Path(__file__).resolve().parents[3] / "shared" / "db" / "migrations"
)


_COLUMN_DECL = re.compile(
    r"^\s*(?P<col>[A-Za-z_][A-Za-z0-9_]*)\s+(?:REAL|FLOAT|DOUBLE|INT|BIGINT|INTEGER|TEXT|TIMESTAMPTZ|UUID|BOOLEAN|JSONB|NUMERIC|VARCHAR)",
    re.IGNORECASE | re.MULTILINE,
)


_FORBIDDEN_SUFFIXES = (
    "_resume_offset",  # resume-invariant-ok: lint pattern
    "_resume_position",  # resume-invariant-ok: lint pattern
    "_resume_at_sec",  # resume-invariant-ok: lint pattern
)


def _columns_in_sql(text: str) -> set[str]:
    return {match.group("col").lower() for match in _COLUMN_DECL.finditer(text)}


def test_no_resume_offset_column_in_any_table() -> None:
    if not MIGRATIONS.is_dir():
        # The lint is only meaningful inside the source tree; if a
        # downstream package vendors the pipeline without the
        # migrations directory, silently pass.
        return

    hits: list[str] = []
    for path in sorted(MIGRATIONS.rglob("*.sql")):
        text = path.read_text()
        for col in _columns_in_sql(text):
            if any(suffix in col for suffix in _FORBIDDEN_SUFFIXES):
                hits.append(f"{path.name}: column `{col}` violates Story 6.10 invariant")

    assert not hits, (
        "Forbidden resume-offset columns found. last_segment_end_sec "
        "is the canonical resume offset.\n" + "\n".join(hits)
    )
