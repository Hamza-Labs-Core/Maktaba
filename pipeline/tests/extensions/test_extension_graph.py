"""Extension stages in the graph: registration, validation, enabled / disabled state."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.engine.graph import GRAPH, GraphBuilder, StageDef, validate
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.extensions import discover
from maktaba_pipeline.extensions.dizquetv import extension as dizquetv
from maktaba_pipeline.extensions.plex import extension as plex
from maktaba_pipeline.extensions.store import ExtensionState
from maktaba_pipeline.runtime import Database

EXT_STAGES = {
    "plex_index",
    "plex_collection",
    "plex_groups",
    "plex_livetv",
    "dizquetv_show",
    "dizquetv_channels",
    "dizquetv_audit",
}


def edges(graph: tuple[StageDef, ...], name: str) -> set[tuple[str, str]]:
    stage = next(s for s in graph if s.name == name)
    return {(e.to, e.map) for e in stage.edges}


def test_builtin_extensions_are_discovered() -> None:
    names = {e.manifest.name for e in discover(include_entry_points=False)}
    assert names == {"plex", "dizquetv", "cloudflare_stream"}


def test_register_adds_stages_and_edges_and_validates() -> None:
    graph = build_full_graph([plex, dizquetv])
    validate(graph)
    by = {s.name: s for s in graph}
    assert set(by) >= EXT_STAGES
    assert {n for n, s in by.items() if s.extension} == EXT_STAGES

    pc, show, chans = by["plex_collection"], by["dizquetv_show"], by["dizquetv_channels"]
    assert (pc.subject_kinds, pc.pool, pc.lane, pc.lane_order) == (
        ("season",),
        "network_plex",
        "seasons",
        4,
    )
    assert pc.version == 1 and pc.external_write and pc.entry_for == ("season",)
    assert pc.extension == "plex"
    assert (show.pool, show.lane, show.lane_order, show.version) == ("network_tv", "seasons", 5, 1)
    assert show.entry_for == ("season",) and show.external_write
    assert (chans.subject_kinds, chans.pool, chans.lane, chans.lane_order) == (
        ("catalog",),
        "network_tv",
        "channels",
        3,
    )
    assert chans.entry_for == ("catalog",) and chans.extension == "dizquetv"

    assert ("plex_collection", "seasons_changed") in edges(graph, "detect")
    assert ("dizquetv_show", "seasons_changed") in edges(graph, "detect")
    assert ("dizquetv_show", "same") in edges(graph, "plex_collection")
    assert ("dizquetv_channels", "same") in edges(graph, "plan_channels")


def test_dizquetv_alone_has_no_plex_edge() -> None:
    graph = build_full_graph([dizquetv])
    assert {s.name for s in graph if s.extension} == {
        "dizquetv_show",
        "dizquetv_channels",
        "dizquetv_audit",
    }
    assert not any(e.to == "plex_collection" for s in graph for e in s.edges)


def test_core_graph_names_no_extension_stage_and_core_does_not_import_them() -> None:
    assert not EXT_STAGES & {s.name for s in GRAPH}
    assert not any(e.to in EXT_STAGES for s in GRAPH for e in s.edges)
    assert all(s.extension is None for s in GRAPH)
    src = Path(__file__).resolve().parents[2] / "src" / "maktaba_pipeline"
    for core in ("engine", "classify", "schedule", "discovery"):
        for f in (src / core).rglob("*.py"):
            text = f.read_text(encoding="utf-8")
            for banned in (
                "extensions.plex",
                "extensions.dizquetv",
                "integrations.plex",
                "integrations.dizquetv",
            ):
                assert banned not in text, f"{f} imports {banned}"


def test_manifest_stages_match_registration() -> None:
    for ext in (plex, dizquetv):
        b = GraphBuilder(GRAPH)
        before = {s.name for s in b.build()}
        ext.register(b)
        assert {s.name for s in b.build()} - before == set(ext.manifest.stages)
        assert set(ext.impls()) == set(ext.manifest.stages)


async def stage_state(db: Database, name: str) -> tuple[bool, str | None]:
    row = await db.fetchrow(
        "SELECT enabled, disabled_reason FROM job_stages WHERE stage = $1", name
    )
    return bool(row["enabled"]), row["disabled_reason"]


@pytest.mark.asyncio
async def test_sync_marks_stages_enabled_disabled_or_unconfigured(db: Database) -> None:
    graph = build_full_graph([plex, dizquetv])
    states = {
        "plex": ExtensionState("plex", True, {"url": "u", "token": "t"}, True),
        "dizquetv": ExtensionState("dizquetv", False, {"url": "u"}, True),
    }
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_collection") == (True, None)
    assert await stage_state(db, "dizquetv_show") == (False, "extension_disabled")
    assert await stage_state(db, "dizquetv_channels") == (False, "extension_disabled")
    assert await stage_state(db, "detect") == (True, None)  # core is unaffected

    states["plex"] = ExtensionState("plex", True, {"url": "u"}, False)  # no token
    states["dizquetv"] = ExtensionState("dizquetv", True, {"url": "u"}, True)
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_collection") == (False, "not_configured")
    assert await stage_state(db, "dizquetv_show") == (True, None)

    # an operator disable always wins over the extension state
    await db.execute(
        "UPDATE job_stages SET enabled = $2, disabled_reason = 'operator' WHERE stage = $1",
        "dizquetv_show",
        False,
    )
    await sync_graph(db, graph, states)
    assert await stage_state(db, "dizquetv_show") == (False, "operator")


@pytest.mark.asyncio
async def test_extension_removed_from_the_graph_leaves_core_intact(db: Database) -> None:
    await sync_graph(db, build_full_graph([plex]), {"plex": ExtensionState("plex", True, {}, True)})
    await sync_graph(db, build_full_graph([]), {})
    assert await stage_state(db, "detect") == (True, None)


def test_plex_stages_are_home_only_unless_the_relay_carries_them() -> None:
    """The Plex token is in home_secrets (slot 0120), which a node cannot read: the stages
    that only need the token name the relay (a node runs them through the home, story
    31.R); plex_livetv stays beside write_channels on the home."""
    graph = build_full_graph(discover(include_entry_points=False))
    plex = {s.name: (s.placement, s.relay) for s in graph if s.name.startswith("plex_")}
    assert plex == {
        "plex_index": ("home", "plex"),
        "plex_collection": ("home", "plex"),
        "plex_groups": ("home", "plex"),
        "plex_livetv": ("home", None),
    }, plex
    assert not any(s.relay for s in graph if s.extension in ("dizquetv", "cloudflare_stream"))
