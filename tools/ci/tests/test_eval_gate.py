"""tools/eval-gate: a drop fails and names the metric; a trailer accepts it."""

from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "eval-gate"))

import gate  # noqa: E402

BASE = {
    "languages": {"ar": {"precision": 0.7, "recall": 0.7}, "en": {"precision": 0.6, "recall": 0.6}}
}


def test_a_drop_fails_and_names_the_metric(tmp_path: Path) -> None:
    worse = {
        "languages": {
            "ar": {"precision": 0.65, "recall": 0.7},
            "en": {"precision": 0.6, "recall": 0.59},
        }
    }
    assert gate.drops(worse, BASE) == ["ar precision: 0.650, baseline 0.700"]
    (tmp_path / "r.json").write_text(json.dumps(worse))
    (tmp_path / "b.json").write_text(json.dumps(BASE))
    assert (
        gate.main(["--report", str(tmp_path / "r.json"), "--baseline", str(tmp_path / "b.json")])
        == 1
    )


def test_a_trailer_accepts_it() -> None:
    assert gate.has_trailer(None, "fix: x\n\nEval-Change: the Arabic pack's new dialect list\n")
    assert not gate.has_trailer(None, "fix: x\n\nNo-Test-Change: docs\n")
