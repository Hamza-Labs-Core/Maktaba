"""``compute_metrics`` after its speed-up: the same answers as before, at a lower cost.

The version before the speed-up is kept in ``style_reference`` (tests only), with its
answers over every input in ``style_reference.json``. These tests carry no tier marker
(they run in the integration job's untiered step): they take a few seconds.
``bench_style_metrics`` prints the numbers.
"""

from __future__ import annotations

import json
import random
import time
from collections.abc import Callable, Iterator, Sequence
from dataclasses import asdict
from typing import Any

import pytest

from maktaba_pipeline.classify import production_style
from maktaba_pipeline.classify.production_style import (
    THUMB_H,
    THUMB_W,
    compute_metrics,
    episode_style,
    metrics_from_dict,
)
from tests.classify import style_reference
from tests.classify.style_inputs import Case, parity_cases, season_cases


def _all_cases() -> Iterator[Case]:
    yield from parity_cases()
    yield from season_cases()


def test_metrics_are_exactly_the_reference_on_the_parity_worlds_and_a_season() -> None:
    """Every video of the parity worlds and the season, against the reference's answers
    (``style_reference.json``, written by the reference itself)."""
    want = json.loads(style_reference.FIXTURE.read_text())
    got = {name: compute_metrics(windows, info) for name, windows, info in _all_cases()}
    assert sorted(got) == sorted(want) and len(got) == 105
    for name, m in got.items():
        # == on floats is exact; the fixture's floats read back as exactly what was written
        assert asdict(m) == want[name], name
        assert m == metrics_from_dict(want[name]), name


def test_metrics_are_exactly_the_live_reference_on_a_sample() -> None:
    """The fixture checked against the reference code itself, on a slice of each set."""
    for name, windows, info in (*parity_cases()[::9], *season_cases()[::6]):
        got, want = compute_metrics(windows, info), style_reference.compute_metrics(windows, info)
        assert got == want and repr(got) == repr(want), name


def test_grids_and_edges_are_exactly_the_reference_on_extreme_pictures() -> None:
    """The lane arithmetic at its bounds: black, white, the largest gradients (510), an
    edge only across the last column, and noise."""
    n = THUMB_W * THUMB_H
    rng = random.Random(7)
    checker = bytes(255 * ((i // THUMB_W + i % THUMB_W) % 2) for i in range(n))
    last_col = bytes(255 if i % THUMB_W == THUMB_W - 1 else 0 for i in range(n))
    first_col = bytes(255 if i % THUMB_W == 0 else 0 for i in range(n))
    rows = bytes(255 * (i // THUMB_W % 2) for i in range(n))
    near = bytes(39 * (i % 2) for i in range(n))  # gradients of 39 and 40 around the cutoff
    near40 = bytes(40 * (i % 2) for i in range(n))
    pictures = [bytes(n), b"\xff" * n, checker, last_col, first_col, rows, near, near40]
    pictures += [rng.randbytes(n) for _ in range(40)]
    for i, t in enumerate(pictures):
        assert production_style._look_vec(t) == bytes(style_reference._look_vec(t)), i
        assert production_style._edge_share(t) == style_reference._edge_share(t), i


def test_the_inputs_cover_what_the_look_search_meets() -> None:
    """Guards the test above against inputs that stopped exercising the code: windows of
    one and of four, talk and produced episodes, pictures that recur and ones that don't."""
    windows = {len(ws) for _n, ws, _i in parity_cases()}
    assert windows == {1, 4}
    season = [compute_metrics(ws, i) for _n, ws, i in season_cases()]
    assert {"talk", "produced"} <= {episode_style(m) for m in season}
    assert min(m.insert_share for m in season) < 0.1 < 0.3 < max(m.insert_share for m in season)
    assert all(m.looks_per_min >= 2 and m.edge_density > 0 for m in season)


def _count(monkeypatch: pytest.MonkeyPatch, module: Any, name: str) -> list[int]:
    calls = [0]
    real: Callable[..., Any] = getattr(module, name)

    def counted(*args: Any) -> Any:
        calls[0] += 1
        return real(*args)

    monkeypatch.setattr(module, name, counted)
    return calls


def test_the_look_search_compares_far_fewer_grids(monkeypatch: pytest.MonkeyPatch) -> None:
    """Work counted, not timed: each distinct picture's grid is worked out once, and looks
    whose grid sums are too far apart are never compared cell by cell."""
    cases = season_cases()[10:20]  # talk and produced episodes
    before = _count(monkeypatch, style_reference, "_dist")
    grids_before = _count(monkeypatch, style_reference, "_look_vec")
    for _n, ws, i in cases:
        style_reference.compute_metrics(ws, i)
    after = _count(monkeypatch, production_style, "_absdiff")  # grid compares and edges
    grids = _count(monkeypatch, production_style, "_look_vec")
    edges = _count(monkeypatch, production_style, "_edge_share")
    for _n, ws, i in cases:
        compute_metrics(ws, i)
    distinct = sum(len({t for w in ws for t in w.thumbs}) for _n, ws, _i in cases)
    assert grids[0] == distinct <= grids_before[0]
    compares = after[0] - 2 * edges[0]  # each edge share takes two lane differences
    assert compares * 3 <= before[0], (compares, before[0])


def _cpu(compute: Callable[..., Any], cases: Sequence[Case]) -> float:
    best = float("inf")
    for _ in range(3):
        t = time.process_time()
        for _n, ws, i in cases:
            compute(ws, i)
        best = min(best, time.process_time() - t)
    return best


def test_cpu_time_is_well_below_the_reference() -> None:
    """A ratio of CPU times in one process, not a wall-clock limit: it holds on a slow or
    loaded runner. Measured 4-6x on an M3 Ultra; failing under 2.5x means a regression."""
    cases = list(parity_cases()[:10]) + list(season_cases()[13:17])
    before = _cpu(style_reference.compute_metrics, cases)
    after = _cpu(compute_metrics, cases)
    assert after * 2.5 <= before, f"after {after:.3f}s, before {before:.3f}s"
