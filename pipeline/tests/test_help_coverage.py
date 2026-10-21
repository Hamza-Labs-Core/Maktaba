"""Every stage and failure cause has its help entry (Epic 41).

The web app links each queue tile to ``/help/stages#<stage>`` and each
failure cause to ``/help/failures#<cause>`` (``_`` becomes ``-``). A stage
or cause added here without its entry in both languages fails this test.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

from maktaba_pipeline.engine.causes import _KNOWN
from maktaba_pipeline.engine.failure_classes import (
    DEFAULT_TABLE,
    ITEM_PERMANENT,
    TRANSIENT,
    UNKNOWN,
)
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions import discover

HELP = Path(__file__).resolve().parents[2] / "web" / "src" / "help" / "content"
ANCHOR = re.compile(r"^#{1,6}\s.*\{#([a-z0-9-]+)\}\s*$", re.M)


def anchors(locale: str, topic: str) -> set[str]:
    return set(ANCHOR.findall((HELP / locale / f"{topic}.md").read_text(encoding="utf-8")))


def anchor(code: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", code.lower())


pytestmark = pytest.mark.skipif(not HELP.is_dir(), reason="web/ is not in this checkout")


@pytest.mark.parametrize("locale", ["en", "ar"])
def test_every_stage_has_help(locale: str) -> None:
    stages = [s.name for s in build_full_graph(discover(include_entry_points=False))]
    assert len(stages) > 40
    have = anchors(locale, "stages")
    missing = [s for s in stages if anchor(s) not in have]
    assert not missing, f"stages without an entry in web/src/help/content/{locale}/stages.md"


@pytest.mark.parametrize("locale", ["en", "ar"])
def test_every_cause_has_help(locale: str) -> None:
    have = anchors(locale, "failures")
    causes = [cause for cause, _ in _KNOWN]
    missing = [c for c in causes if anchor(c) not in have]
    assert not missing, f"causes without an entry in web/src/help/content/{locale}/failures.md"
    assert "other" in have, "the entry for generated kind:message causes"


@pytest.mark.parametrize("locale", ["en", "ar"])
def test_every_failure_class_has_help(locale: str) -> None:
    """/help/failures#class-<class> (a class's detail after its second part is dropped)."""
    have = anchors(locale, "failures")
    classes = {cls for _, cls in DEFAULT_TABLE} | {TRANSIENT, ITEM_PERMANENT, UNKNOWN}
    missing = [c for c in classes if "class-" + anchor(":".join(c.split(":")[:2])) not in have]
    assert not missing, f"failure classes without an entry in {locale}/failures.md"
