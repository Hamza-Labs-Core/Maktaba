"""The reason, status and source codes are one list in Python, Go and the web (Epic 52)."""

from __future__ import annotations

import re
from pathlib import Path

from maktaba_pipeline.word_review.settings import REASONS, SOURCES, STATUSES

ROOT = Path(__file__).resolve().parents[3]


def _go(name: str) -> list[str]:
    src = (ROOT / "api" / "internal" / "handlers" / "review" / "codes.go").read_text(
        encoding="utf-8"
    )
    m = re.search(name + r"\s*=\s*\[\]string\{(.*?)\}", src, re.S)
    assert m, name
    return re.findall(r'"([a-z_]+)"', m.group(1))


def _ts(name: str) -> list[str]:
    src = (ROOT / "web" / "src" / "lib" / "reviewApi.ts").read_text(encoding="utf-8")
    m = re.search(r"export const " + name + r" = \[(.*?)\]", src, re.S)
    assert m, name
    return re.findall(r'"([a-z_]+)"', m.group(1))


def test_the_three_lists_agree() -> None:
    for name, py, go in (("REASONS", REASONS, "Reasons"), ("STATUSES", STATUSES, "Statuses"),
                         ("SOURCES", SOURCES, "Sources")):  # fmt: skip
        assert list(py) == _go(go), name
        assert list(py) == _ts(name), name
