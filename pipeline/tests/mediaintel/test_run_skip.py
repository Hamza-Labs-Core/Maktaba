"""``run --skip``: a step can be left out, and an unknown name is refused."""

from __future__ import annotations

import sqlite3
from pathlib import Path

import pytest

from scripts.mediaintel import pipeline


def _run(skip: tuple[str, ...]) -> None:
    pipeline._run(
        sqlite3.connect(":memory:"), [], server=None, path_map=[], youtube=False,
        exact=False, lang="ar", delay_s=0, report_dir=Path("."), workers=1, plex=False,
        tv=False, tv_url="", backup_dir=Path("."), t0=0.0, from_step="export-dizquetv",
        skip=skip,
    )  # fmt: skip


def test_an_unknown_step_is_refused() -> None:
    with pytest.raises(ValueError, match="--skip 'visual'"):
        _run(("visual",))


def test_skipped_steps_are_named(capsys: pytest.CaptureFixture[str]) -> None:
    _run(("scan-visual",))
    assert "leaving out: scan-visual" in capsys.readouterr().out
