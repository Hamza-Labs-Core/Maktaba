"""The component store's rules that need no database (story 51.02): the rollup, the
notification payload and the refusal of codes the catalog does not have (T11)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.components.catalog import KNOWN_PHASES, default
from maktaba_pipeline.components.store import (
    _RANK,
    HOME,
    MAX_DETAIL_BYTES,
    Condition,
    Event,
    Status,
    StatusError,
    _wire,
    parse_notify,
    rollup,
)

pytestmark = pytest.mark.unit


def test_rollup_worst_phase_wins_with_its_reason() -> None:
    rows = [
        Status(id="livetv", phase="running", machine=HOME, part="api"),
        Status(
            id="livetv",
            phase="failed",
            machine=HOME,
            part="pipeline",
            conditions=(Condition(type="Ready", status="false", reason="crash_loop"),),
        ),
        Status(id="livetv", phase="degraded", machine=HOME, part="streaming"),
    ]
    got = rollup(rows)
    assert got is not None
    assert (got.phase, got.reason, got.part, got.machine) == ("failed", "crash_loop", "", HOME)
    assert rollup([]) is None


def test_rollup_order_matches_go_and_covers_every_phase() -> None:
    order = list(_RANK)
    for worse, better in zip(order, order[1:], strict=False):
        r = rollup([Status(id="a", phase=better), Status(id="a", phase=worse)])
        assert r is not None and r.phase == worse
    assert set(_RANK) == set(KNOWN_PHASES)
    unknown = rollup([Status(id="a", phase="running"), Status(id="a", phase="from_the_future")])
    assert unknown is not None and unknown.phase == "from_the_future"


def test_parse_notify() -> None:
    assert parse_notify('{"revision":7,"id":"llm","node":null}') == Event(revision=7, id="llm")
    assert parse_notify('{"revision":8,"id":"llm","node":"abc"}').node == "abc"
    assert parse_notify("garbage").reload
    assert parse_notify("{}").reload


@pytest.mark.parametrize(
    ("row", "message"),
    [
        (
            Status(
                id="llm",
                phase="waiting",
                conditions=(Condition(type="Ready", status="false", reason="nonsense"),),
            ),
            "unknown reason 'nonsense'",
        ),
        (Status(id="llm", phase="sleeping"), "unknown phase"),
        (
            Status(id="llm", phase="running", conditions=(Condition(type="Ready", status="yes"),)),
            "true, false or unknown",
        ),
        (
            Status(id="llm", phase="running", conditions=(Condition(type="", status="true"),)),
            "type",
        ),
        (Status(id="", phase="running"), "needs an id"),
    ],
)
def test_unknown_codes_are_refused(row: Status, message: str) -> None:
    """T11: report_status checks every row this way before it writes (on the database the
    refusal is also checked to leave no row: tests/db/test_components_migration.py)."""
    with pytest.raises(StatusError, match=message):
        _wire([row], default(), "pipeline", "")


def test_a_component_twice_in_one_report_is_refused() -> None:
    with pytest.raises(StatusError, match="twice"):
        _wire([Status(id="a", phase="off"), Status(id="a", phase="off")], default(), "pipeline", "")


def test_a_big_detail_is_truncated() -> None:
    (w,) = _wire(
        [Status(id="a", phase="off", detail={"log": "x" * MAX_DETAIL_BYTES})], default(), "p", "v1"
    )
    assert w["detail"] == {"truncated": True} and w["version"] == "v1"
