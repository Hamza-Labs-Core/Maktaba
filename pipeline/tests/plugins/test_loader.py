"""Story 54.06 T01, T02, T07, T08: plugin manifests as queue stages; the graph stays
valid; the new failure causes; the stable events name real stages."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine import causes, failure_classes
from maktaba_pipeline.engine.graph import GRAPH, POOLS, validate
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.plugins import manifest as pm
from maktaba_pipeline.plugins.loader import PluginExtension, _toml_of, plugin_of

pytestmark = pytest.mark.unit

ROOT = Path(__file__).resolve().parents[3]
FULL = ROOT / "shared" / "plugins" / "testdata" / "manifests" / "valid" / "full.toml"
PKG = ROOT / "shared" / "plugins" / "testdata" / "manifests" / "package"


def _files(p: str) -> bytes | None:
    f = PKG / p
    return f.read_bytes() if f.is_file() else None


def ext() -> PluginExtension:
    doc = pm.validate(FULL.read_bytes(), _files)
    return PluginExtension(doc["id"], doc["version"], "/data/plugins/x", doc)


def test_manifest_becomes_stage_defs_edges_and_a_schedule() -> None:
    e = ext()
    graph = build_full_graph([e])
    by = {s.name: s for s in graph}
    pid = "tv.maktaba.community.chapters"
    find = by[pm.queue_name(pid, "find_silences")]
    assert find.pool == "plugins" and find.extension == f"plugin:{pid}"
    assert find.placement == "home" and find.max_attempts == 2 and find.version == 3
    assert find.may_request == (pm.queue_name(pid, "write_chapters"),)
    # triggers: edges from the events' core stages
    assert any(edge.to == find.name for edge in by["extract"].edges)
    assert any(edge.to == find.name for edge in by["content_hash"].edges)
    season = by[pm.queue_name(pid, "season_pass")]
    assert season.subject_kinds == ("season",)
    assert any(
        edge.to == season.name and edge.map == "seasons_changed" for edge in by["detect"].edges
    )
    weekly = by[pm.queue_name(pid, "weekly_report")]
    assert weekly.every_minutes_rule == (f"plugins.{pid}", "report_interval")
    assert weekly.subject_kinds == ("catalog",)
    assert plugin_of(find.extension) == pid and plugin_of("plex") is None


def test_graph_with_plugin_stages_is_valid() -> None:
    validate(build_full_graph([ext()]))
    assert any(p.pool == "plugins" and p.scope == "global" and (p.size or 0) >= 1 for p in POOLS)


def test_stored_manifest_round_trips_through_toml() -> None:
    doc = pm.validate(FULL.read_bytes(), _files)
    again = pm.validate(_toml_of(doc))
    assert pm.normalised(again) == pm.normalised(doc)


def test_plugin_failure_causes_and_classes() -> None:
    for cause, cls in [
        ("plugin_trap", "transient"),
        ("plugin_timeout", "transient"),
        ("plugin_memory", "item_permanent"),
        ("plugin_capability", "item_permanent"),
        ("plugin_scope", "item_permanent"),
        ("plugin_quota", "item_permanent"),
        ("plugin_failed", "item_permanent"),
        ("plugin_held", "dependency:plugin"),
    ]:
        assert causes.classify(cause, "wasm trap at 0x1f") == cause
        assert failure_classes.class_for(cause) == cls, cause


def test_cause_words_exist() -> None:
    import json

    en = json.loads((ROOT / "web" / "src" / "i18n" / "en.json").read_text())
    ar = json.loads((ROOT / "web" / "src" / "i18n" / "ar.json").read_text())
    for c in (
        "plugin_trap",
        "plugin_timeout",
        "plugin_memory",
        "plugin_capability",
        "plugin_scope",
        "plugin_quota",
        "plugin_failed",
        "plugin_held",
    ):
        assert f"queue.issues.cause.{c}" in en and f"queue.issues.cause.{c}" in ar


def test_events_name_stages_of_the_graph() -> None:
    """54.06 T08: every event's core stage is in GRAPH."""
    names = {s.name for s in GRAPH}
    for e in pm.events().values():
        if e.get("trigger") is False:
            continue
        assert e["stage"] in names, e["name"]


GRAPH_CASE = ROOT / "shared" / "plugins" / "testdata" / "manifests" / "valid" / "graph.toml"


def graph_ext() -> PluginExtension:
    doc = pm.validate(GRAPH_CASE.read_bytes(), _files)
    return PluginExtension(doc["id"], doc["version"], "/data/plugins/g", doc)


def test_a_stage_trigger_is_an_edge_and_a_published_event_is_not() -> None:
    """54.39 §7: ``stage`` triggers wire an edge from the plugin's own stage; a trigger on
    another plugin's event is delivered as a request (54.40), never as an edge."""
    graph = build_full_graph([graph_ext()])
    by = {s.name: s for s in graph}
    pid = "dev.example.silence-chapters"
    names = ("find_silences", "write_chapters", "mark_music")
    find, write, music = (pm.queue_name(pid, n) for n in names)
    assert any(e.to == write and e.map == "same" for e in by[find].edges)
    assert not any(e.to == music for s in graph for e in s.edges)
    assert any(e.to == find for e in by["extract"].edges)


def _forged(**stage: object) -> PluginExtension:
    doc = pm.validate(GRAPH_CASE.read_bytes(), _files)
    doc["stage"][0].update(stage)
    return PluginExtension(doc["id"], doc["version"], "/data/plugins/g", doc)


@pytest.mark.parametrize(
    "stage",
    [
        {"may_request": ["transcribe"]},
        {"trigger": [{"stage": "extract"}]},
        {"trigger": [{"event": "transcribe"}]},
        {"trigger": [{"event": "plugin:dev.example.silence-chapters/silences.found"}]},
    ],
    ids=["request-core", "after-core-stage", "core-stage-as-event", "follows-itself"],
)
def test_check_leaves_refuses_a_manifest_that_reaches_core(stage: dict[str, Any]) -> None:
    """54.39 T07: a stored manifest edited to reach a core stage is refused."""
    from maktaba_pipeline.engine.graph import GraphViolation

    with pytest.raises(GraphViolation):
        _forged(**stage).check_leaves()


def test_load_installed_leaves_out_a_plugin_that_breaks_the_leaf_rule() -> None:
    import json

    bad = pm.validate(GRAPH_CASE.read_bytes(), _files)
    good = json.loads(json.dumps(bad))
    good["id"] = "dev.example.good"
    bad["stage"][0]["may_request"] = ["transcribe"]

    class DB:
        async def fetch(self, *_: object) -> list[dict[str, Any]]:
            row = {"version": "1.4.0", "path": "/x", "review_level": "none"}
            return [
                {**row, "plugin_id": "dev.example.bad", "manifest": bad},
                {**row, "plugin_id": "dev.example.good", "manifest": good},
            ]

    from maktaba_pipeline.plugins.loader import load_installed

    # the fake never suspends, so the coroutine runs to its end on the first step (a unit
    # test opens no event loop: its self-pipe is a socket)
    coro = load_installed(DB())
    with pytest.raises(StopIteration) as done:
        coro.send(None)
    got = done.value.value
    assert [p.plugin_id for p in got] == ["dev.example.good"]


def test_assert_plugin_leaves_refuses_an_edge_from_a_plugin_to_core() -> None:
    from dataclasses import replace

    from maktaba_pipeline.engine.graph import Edge, GraphViolation
    from maktaba_pipeline.plugins.loader import assert_plugin_leaves

    graph = list(build_full_graph([graph_ext()]))
    assert_plugin_leaves(graph)
    i = next(i for i, s in enumerate(graph) if s.extension and s.extension.startswith("plugin:"))
    graph[i] = replace(graph[i], edges=(*graph[i].edges, Edge("transcribe", "same")))
    with pytest.raises(GraphViolation):
        assert_plugin_leaves(graph)
    graph = list(build_full_graph([graph_ext()]))
    event_stages = {e["stage"] for e in pm.events().values()}
    core = next(
        j for j, s in enumerate(graph) if s.extension is None and s.name not in event_stages
    )
    graph[core] = replace(graph[core], edges=(*graph[core].edges, Edge(graph[i].name, "same")))
    with pytest.raises(GraphViolation):
        assert_plugin_leaves(graph)
