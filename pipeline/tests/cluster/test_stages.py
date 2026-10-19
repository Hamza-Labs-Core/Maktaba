"""Stage selection: the reasons, in order (design section 5)."""

from __future__ import annotations

from collections.abc import Coroutine
from typing import Any, Literal, cast

import pytest

from maktaba_pipeline.cluster.stages import (
    StageSelector,
    is_shared_url,
    lifted_placements,
    select_stages,
)
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


def test_a_lifted_home_stage_runs_on_a_node() -> None:
    _, off = select_stages(GRAPH, handlers=HANDLERS, role="node")
    assert off["index"] == "home_only"
    served, off = select_stages(GRAPH, handlers=HANDLERS, role="node", lifted={"index"})
    assert "index" in served and "index" not in off


@pytest.mark.parametrize(
    ("url", "shared"),
    [
        ("http://localhost:11434", False),
        ("http://127.0.0.1:11434", False),
        ("http://[::1]:11434", False),
        ("", False),
        ("http://192.168.1.20:11434", True),
        ("https://ollama.example.org", True),
    ],
)
def test_only_a_shared_model_server_lifts_home_placement(url: str, shared: bool) -> None:
    assert is_shared_url(url) is shared


class _RulesDb:
    def __init__(self, value: str | None) -> None:
        self.value = value

    async def fetchrow(self, sql: str, *args: object) -> dict[str, object] | None:
        return None if self.value is None else {"value": self.value}


def _run[T](coro: Coroutine[Any, Any, T]) -> T:
    """Drive a coroutine that never waits (a unit test opens no event loop: no sockets)."""
    try:
        coro.send(None)
    except StopIteration as done:
        return cast(T, done.value)
    raise AssertionError("the coroutine waited")


def test_model_stages_are_home_only_unless_the_settings_name_a_shared_server() -> None:
    from maktaba_pipeline.engine.graph import GRAPH as CORE

    by = {s.name: s for s in CORE}
    for name in ("check_tags", "check_clips", "plan_channels", "write_channels"):
        assert by[name].placement == "home", name
    assert _run(lifted_placements(_RulesDb(None), CORE)) == frozenset()
    local = _RulesDb('{"url": "http://localhost:11434"}')
    assert _run(lifted_placements(local, CORE)) == frozenset()
    shared = _RulesDb('{"url": "http://10.0.0.5:11434"}')
    assert _run(lifted_placements(shared, CORE)) == {"check_tags", "check_clips"}


def test_the_dizquetv_channel_stages_are_home_only() -> None:
    from maktaba_pipeline.engine.graph import GRAPH as CORE
    from maktaba_pipeline.engine.graph import GraphBuilder
    from maktaba_pipeline.extensions.dizquetv import DizqueTVExtension

    gb = GraphBuilder(CORE)
    DizqueTVExtension().register(gb)
    by = {s.name: s for s in gb.build()}
    assert by["dizquetv_channels"].placement == "home"
    assert by["dizquetv_audit"].placement == "home"
