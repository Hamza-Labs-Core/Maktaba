"""The evaluation harness (Epic 52, story 52.2): alignment labels, metrics, calibration."""

from __future__ import annotations

import json
import random
import time
from pathlib import Path

from maktaba_pipeline.text.arabic import fold
from maktaba_pipeline.word_review.eval import calibrate
from maktaba_pipeline.word_review.eval.__main__ import CI_DICTS, CI_SET, evaluate, main
from maktaba_pipeline.word_review.eval.align import align
from maktaba_pipeline.word_review.eval.metrics import Report, score


def _key(w: str) -> str:
    return fold(w).replace(" ", "")


def test_a_hamza_difference_is_spelling_not_substitution() -> None:
    assert align(["اسلام"], ["إسلام"], _key).labels == ("spelling",)
    assert align(["كتاب"], ["قلم"], _key).labels == ("substitution",)
    assert align(["كتاب"], ["كتاب"], _key).labels == ("ok",)


def test_insertions_and_deletions() -> None:
    a = align(["ذهب", "الولد", "الى", "البيت"], ["ذهب", "الى", "البيت"], _key)
    assert a.labels == ("ok", "insertion", "ok", "ok") and a.deletions == 0
    b = align(["ذهب", "البيت"], ["ذهب", "الولد", "البيت"], _key)
    assert b.labels == ("ok", "ok") and b.deletions == 1 and sum(b.deletions_near) == 1


def test_an_english_word_uses_the_english_key() -> None:
    assert align(["Colour"], ["colour"], str.casefold).labels == ("spelling",)


def test_flagging_every_word_has_full_recall_and_the_error_rate_as_precision() -> None:
    r = Report()
    labels = ["ok", "substitution", "ok", "spelling"]
    score(r, "ar", labels, [0, 0, 0, 0], {i: ["low_confidence"] for i in range(4)})
    c = r.by_language["ar"]
    assert c.recall == 1.0 and c.precision == 0.5


def test_the_calibration_is_monotone_and_better() -> None:
    rng = random.Random(52)
    probs = [rng.random() for _ in range(2000)]
    # Whisper over-confident: the true error rate is (1 - p) squared, not 1 - p
    wrong = [rng.random() < (1 - p) ** 2 for p in probs]
    xs, ys = calibrate.fit(probs, wrong)
    assert xs == sorted(xs) and all(a >= b for a, b in zip(ys, ys[1:], strict=False))
    from maktaba_pipeline.word_review.scoring import Curve

    before = calibrate.ece(probs, wrong, lambda p: 1 - p)
    after = calibrate.ece(probs, wrong, Curve(tuple(xs), tuple(ys)))
    assert after < before


def test_the_ci_set_runs_fast_and_reports_the_spots(tmp_path: Path) -> None:
    t = time.monotonic()
    assert main(["--set", "ci", "--out", str(tmp_path / "r.json")]) == 0
    assert time.monotonic() - t < 30
    report = json.loads((tmp_path / "r.json").read_text(encoding="utf-8"))
    assert set(report["languages"]) == {"ar", "en"}
    assert report["hearing_test"]["spots"] >= 3 and "missed" in report["hearing_test"]
    assert "quran_mismatch" in report["reasons"]


def test_the_baseline_is_what_the_ci_set_gives() -> None:
    base = json.loads((CI_SET / "baseline.json").read_text(encoding="utf-8"))
    got = evaluate(CI_SET, CI_DICTS).as_dict()
    assert got["languages"] == base["languages"]
