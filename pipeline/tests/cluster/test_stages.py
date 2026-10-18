"""Stage selection: the reasons, in order (design section 5)."""

from __future__ import annotations

from typing import Literal

import pytest

from maktaba_pipeline.cluster.stages import StageSelector, select_stages
from maktaba_pipeline.engine.graph import StageDef

pytestmark = pytest.mark.unit


def _st(name: str, pool: str = "cpu", placement: Literal["any", "home"] = "any") -> StageDef:
    return StageDef(
        name=name,
        label=name,
        subject_kinds=("video",),
        pool=pool,
        lane="media",
        lane_order=0,
        version=1,
        legacy=True,  # served when this worker has a handler for it
        placement=placement,
    )


GRAPH = [
    _st("probe", "nas_read"),
    _st("subtitle_gen", "nas_write"),
    _st("tag"),
    _st("index", placement="home"),
    _st("scan"),
    _st("nohandler"),
]
HANDLERS = frozenset({"probe", "subtitle_gen", "tag", "index", "scan"})


def test_home_worker_serves_everything_it_can_run() -> None:
    served, off = select_stages(GRAPH, handlers=HANDLERS)
    assert served == ["probe", "subtitle_gen", "tag", "index", "scan"]
    assert off == {"nohandler": "unavailable"}


def test_each_reason_in_order() -> None:
    detail: dict[str, str] = {}
    served, off = select_stages(
        GRAPH,
        requested={"probe", "subtitle_gen", "tag", "index", "nohandler"},
        excluded={"tag"},
        handlers=HANDLERS,
        ahead={"probe"},
        role="node",
        storage={"read": "ok", "write": "error"},
        detail=detail,
    )
    assert served == []
    assert off == {
        "probe": "version_ahead",
        "subtitle_gen": "storage",
        "tag": "not_selected",
        "index": "home_only",
        "scan": "not_selected",
        "nohandler": "unavailable",
    }
    assert detail == {"nohandler": "no handler in this worker"}


def test_earlier_reasons_win() -> None:
    # excluded and ahead and home-only and switched off and draining: the first wins
    _, off = select_stages(
        GRAPH,
        excluded={"index"},
        handlers=HANDLERS,
        ahead={"index"},
        role="node",
        switches={"index": False, "tag": False},
        drain=True,
    )
    assert off["index"] == "not_selected"
    assert off["tag"] == "switched_off"
    assert off["scan"] == "draining"


def test_storage_reasons_follow_the_pool() -> None:
    _, off = select_stages(
        GRAPH, handlers=HANDLERS, role="node", storage={"read": "error", "write": "ok"}
    )
    assert off["probe"] == "storage" and "subtitle_gen" not in off and "tag" not in off
    # unchecked storage (a home worker) never blocks
    served, _ = select_stages(GRAPH, handlers=HANDLERS, storage=None)
    assert "probe" in served


def test_a_switch_set_to_true_or_absent_is_automatic() -> None:
    served, _ = select_stages(GRAPH, handlers=HANDLERS, switches={"tag": True})
    assert "tag" in served


def test_selector_reports_changes_and_pools() -> None:
    sel = StageSelector(graph=GRAPH, handlers=HANDLERS, role="node")
    assert sel.compute() is True
    assert sel.current() == ("probe", "subtitle_gen", "tag", "scan")
    assert sel.pools == ["cpu", "nas_read", "nas_write"]
    assert sel.compute() is False
    sel.switches = {"tag": False}
    assert sel.compute() is True and "tag" not in sel.current()
    assert sel.stages_off["tag"] == "switched_off"
    sel.drain = True
    sel.compute()
    assert sel.current() == ()
    assert sel.detail == {"nohandler": "no handler in this worker"}
