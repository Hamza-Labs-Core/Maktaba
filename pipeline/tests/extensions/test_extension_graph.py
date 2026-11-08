"""Extension stages in the graph: registration, validation, enabled / disabled state."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.engine.graph import GRAPH, GraphBuilder, StageDef, validate
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.extensions import discover
from maktaba_pipeline.extensions.cloudflare_stream import extension as cloudflare_stream
from maktaba_pipeline.extensions.plex import extension as plex
from maktaba_pipeline.extensions.store import ExtensionState
from maktaba_pipeline.runtime import Database

EXT_STAGES = {"plex_livetv"}


def edges(graph: tuple[StageDef, ...], name: str) -> set[tuple[str, str]]:
    stage = next(s for s in graph if s.name == name)
    return {(e.to, e.map) for e in stage.edges}


def test_builtin_extensions_are_discovered() -> None:
    names = {e.manifest.name for e in discover(include_entry_points=False)}
    assert names == {"plex", "cloudflare_stream"}


def test_register_adds_stages_and_edges_and_validates() -> None:
    graph = build_full_graph([plex, cloudflare_stream])
    validate(graph)
    by = {s.name: s for s in graph}
    assert set(by) >= EXT_STAGES
    assert {n for n, s in by.items() if s.extension} == EXT_STAGES

    tv = by["plex_livetv"]
    assert (tv.subject_kinds, tv.pool, tv.lane, tv.lane_order) == (
        ("catalog",),
        "network_plex",
        "channels",
        3,
    )
    assert tv.external_write and tv.entry_for == ("catalog",) and tv.extension == "plex"
    assert ("plex_livetv", "same") in edges(graph, "write_channels")


def test_an_extension_without_stages_adds_none() -> None:
    graph = build_full_graph([cloudflare_stream])
    assert not {s.name for s in graph if s.extension}
    assert not any(e.to == "plex_livetv" for s in graph for e in s.edges)


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
                "integrations.plex",
            ):
                assert banned not in text, f"{f} imports {banned}"


def test_manifest_stages_match_registration() -> None:
    for ext in (plex, cloudflare_stream):
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
    graph = build_full_graph([plex, cloudflare_stream])
    states = {"plex": ExtensionState("plex", False, {"url": "u", "token": "t"}, True)}
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_livetv") == (False, "extension_disabled")
    assert await stage_state(db, "detect") == (True, None)  # core is unaffected

    states["plex"] = ExtensionState("plex", True, {"url": "u"}, False)  # no token
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_livetv") == (False, "not_configured")

    states["plex"] = ExtensionState("plex", True, {"url": "u", "token": "t"}, True)
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_livetv") == (True, None)

    # an operator disable always wins over the extension state
    await db.execute(
        "UPDATE job_stages SET enabled = $2, disabled_reason = 'operator' WHERE stage = $1",
        "plex_livetv",
        False,
    )
    await sync_graph(db, graph, states)
    assert await stage_state(db, "plex_livetv") == (False, "operator")


@pytest.mark.asyncio
async def test_extension_removed_from_the_graph_leaves_core_intact(db: Database) -> None:
    await sync_graph(db, build_full_graph([plex]), {"plex": ExtensionState("plex", True, {}, True)})
    await sync_graph(db, build_full_graph([]), {})
    assert await stage_state(db, "detect") == (True, None)


def test_plex_livetv_runs_on_the_home() -> None:
    """plex_livetv stays beside write_channels on the home; no extension stage names the
    relay any more (the stages that did were Plex library sync)."""
    graph = build_full_graph(discover(include_entry_points=False))
    plex = {s.name: (s.placement, s.relay) for s in graph if s.name.startswith("plex_")}
    assert plex == {"plex_livetv": ("home", None)}, plex
    assert not any(s.relay for s in graph if s.extension)
