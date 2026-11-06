"""Story 54.06 T01, T02, T07, T08: plugin manifests as queue stages; the graph stays
valid; the new failure causes; the stable events name real stages."""

from __future__ import annotations

from pathlib import Path

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
