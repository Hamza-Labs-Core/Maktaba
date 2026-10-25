"""The merge rules of the one open TV change (tvrelease/merge.py, story 33.12 AC3)."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.tvrelease.merge import (
    PLAN_CHANNELS,
    hand_layer,
    merge_pending,
    plan_pending,
    with_plan_layer,
)

pytestmark = pytest.mark.unit


LIVE = "00000000-0000-4000-8000-000000000001"
GONE = "00000000-0000-4000-8000-000000000002"
OLD_NEW = "00000000-0000-4000-8000-0000000000a1"  # the first plan's new channel
NEW_NEW = "00000000-0000-4000-8000-0000000000b1"  # the same key in the second plan


def _def(cid: str, number: int, name: str, **kw: Any) -> dict[str, Any]:
    return {"id": cid, "number": number, "name": name, "mode": "shuffle", **kw}


_LIVE = {LIVE: _def(LIVE, 101, "News"), GONE: _def(GONE, 102, "Old")}


def _plan(number: int, new_id: str, *, sched: list[str] | None = None) -> dict[str, Any]:
    return plan_pending(
        {
            "channels": {
                LIVE: _def(LIVE, number, "News"),
                new_id: _def(new_id, 110, "Kids"),
            },
            "links": [{"key": "kids", "channel_id": new_id, "title": "Kids", "digest": "d"}],
            "unlinks": [],
            "pools": {"write": [{"key": f"p{number}"}], "delete": []},
            "sched": sched or [],
        }
    )


def test_a_newer_plan_replaces_the_older_plans_part() -> None:
    first = _plan(105, OLD_NEW, sched=[LIVE])
    second = _plan(106, NEW_NEW)
    got = merge_pending(first, second, _LIVE)
    assert got["channels"] == second["channels"]
    assert got[PLAN_CHANNELS] == second[PLAN_CHANNELS]
    assert got["links"] == second["links"] and got["pools"] == second["pools"]
    assert got["sched"] == [LIVE]  # marks only grow


def test_hand_edits_survive_a_newer_plan() -> None:
    first = _plan(105, OLD_NEW)
    held = {**first, "channels": dict(first["channels"])}
    # the editor saves whole definitions over what the change shows
    held["channels"][LIVE] = _def(LIVE, 105, "News by hand")
    held["channels"][OLD_NEW] = _def(OLD_NEW, 110, "Kids by hand")
    held["channels"][GONE] = None  # removed by hand
    held["filler"] = {"pools": {"p1": {"name": "Stings"}}, "items": {"i1": None}}
    held["art"] = {OLD_NEW: {"logo": {"path": "/a.png"}}}
    held["settings"] = {"tv": {"filler": True}}
    held["edited"] = [LIVE]
    assert hand_layer(held, _LIVE) == {
        LIVE: ("fields", {"name": "News by hand"}),
        OLD_NEW: ("whole", _def(OLD_NEW, 110, "Kids by hand")),
        GONE: ("removed", None),
    }
    got = merge_pending(held, _plan(106, NEW_NEW), _LIVE)
    ch = got["channels"]
    assert ch[LIVE] == _def(LIVE, 106, "News by hand")  # the plan's number, the name by hand
    assert ch[NEW_NEW] == _def(NEW_NEW, 110, "Kids by hand")  # moved to the same plan key
    assert OLD_NEW not in ch
    assert ch[GONE] is None
    assert got["filler"] == held["filler"] and got["settings"] == held["settings"]
    assert got["art"] == {NEW_NEW: {"logo": {"path": "/a.png"}}}
    assert got["edited"] == [LIVE]


def test_a_plan_does_not_drop_a_hand_edit() -> None:
    edits = {"channels": {GONE: _def(GONE, 102, "Old by hand")}}
    plan = plan_pending({"channels": {GONE: None}, "links": [], "unlinks": [["old", GONE]]})
    got = merge_pending(edits, plan, _LIVE)
    assert got["channels"][GONE] == _def(GONE, 102, "Old by hand")
    assert got["unlinks"] == [["old", GONE]]


def test_newer_hand_edits_win_field_by_field() -> None:
    older = {"channels": {LIVE: _def(LIVE, 101, "Renamed")}, "sched": ["a"]}
    newer = {"channels": {LIVE: _def(LIVE, 120, "News")}, "sched": ["b"]}
    got = merge_pending(older, newer, _LIVE)
    assert got["channels"][LIVE] == _def(LIVE, 120, "Renamed")
    assert got["sched"] == ["a", "b"]
    assert PLAN_CHANNELS not in got


def test_an_update_without_a_plan_keeps_the_plan() -> None:
    first = _plan(105, OLD_NEW)
    got = merge_pending(first, {"sched": [GONE]}, _LIVE)
    assert got["channels"] == first["channels"]
    assert got[PLAN_CHANNELS] == first[PLAN_CHANNELS]
    assert got["links"] == first["links"] and got["pools"] == first["pools"]
    assert sorted(got["sched"]) == sorted([GONE])


def test_a_change_from_before_the_rule_reads_its_channels_as_its_plan() -> None:
    legacy = {"channels": {LIVE: _def(LIVE, 105, "News")}, "links": []}
    assert with_plan_layer(legacy, None) is legacy
    got = merge_pending(with_plan_layer(legacy, "digest"), _plan(106, NEW_NEW), _LIVE)
    assert got["channels"][LIVE]["number"] == 106
