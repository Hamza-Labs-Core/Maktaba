# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""The extension mechanism: manifest, graph registration, state, disabled stages (notes N3)."""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Edge, GraphBuilder, GraphError, StageDef
from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.extensions import discover
from maktaba_pipeline.extensions.base import Extension, Manifest, SettingField
from maktaba_pipeline.extensions.store import (
    REDACTED,
    ExtensionState,
    ensure_rows,
    load_state,
    public_settings,
    save_settings,
    set_enabled,
)
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season


class FakeExt:
    manifest = Manifest(
        name="fakeext",
        version="1.0",
        description="test extension",
        settings=(
            SettingField("url", "URL", required=True, env="FAKEEXT_URL"),
            SettingField("token", "Token", secret=True, required=True, env="FAKEEXT_TOKEN"),
        ),
        stages=("fake_push",),
    )

    def register(self, graph: GraphBuilder) -> None:
        graph.add_stage(
            StageDef(
                "fake_push",
                "Push",
                ("season",),
                "network_plex",
                "seasons",
                9,
                1,
                external_write=True,
            )
        )
        graph.add_edge("detect", Edge("fake_push", "seasons_changed"))

    def impls(self) -> Mapping[str, StageImpl]:
        return {}

    def is_configured(self, settings: Mapping[str, str]) -> bool:
        return bool(settings.get("url") and settings.get("token"))


def test_fake_ext_satisfies_the_protocol() -> None:
    assert isinstance(FakeExt(), Extension)


@pytest.mark.unit
def test_core_graph_names_no_extension_stage() -> None:
    names = {s.name for s in GRAPH}
    assert (
        not {"plex_index", "plex_collection", "dizquetv_show", "dizquetv_channels", "fake_push"}
        & names
    )
    assert all(s.extension is None for s in GRAPH)
    for s in GRAPH:
        for e in s.edges:
            assert e.to in names


@pytest.mark.unit
def test_build_full_graph_adds_stages_edges_and_tags() -> None:
    graph = {s.name: s for s in build_full_graph([FakeExt()])}
    assert graph["fake_push"].extension == "fakeext"
    assert any(e.to == "fake_push" for e in graph["detect"].edges)
    assert graph["detect"].extension is None
    assert "fake_push" not in {s.name for s in GRAPH}  # the core tuple is untouched


@pytest.mark.unit
def test_manifest_must_match_registered_stages() -> None:
    class Liar(FakeExt):
        manifest = Manifest("liar", "1", "x", stages=("not_registered",))

    with pytest.raises(ValueError, match="differ from registered"):
        build_full_graph([Liar()])


@pytest.mark.unit
def test_graph_builder_rejects_unknown_source_and_duplicates() -> None:
    b = GraphBuilder(GRAPH)
    with pytest.raises(GraphError):
        b.add_edge("nope", Edge("tag", "same"))
    with pytest.raises(GraphError):
        b.add_stage(next(s for s in GRAPH if s.name == "tag"))
    assert b.has_stage("detect") and not b.has_stage("fake_push")
    b.add_edge("detect", Edge("tag", "same"))  # already present: no duplicate
    detect = next(s for s in b.build() if s.name == "detect")
    assert sum(1 for e in detect.edges if (e.to, e.map) == ("tag", "same")) == 1


@pytest.mark.unit
def test_public_settings_masks_secrets() -> None:
    out = public_settings(FakeExt.manifest, {"url": "http://x", "token": "s3cret", "other": ""})
    assert out == {"url": "http://x", "token": REDACTED, "other": ""}
    assert public_settings(FakeExt.manifest, {"token": ""}) == {"token": ""}


@pytest.mark.unit
def test_discover_finds_nothing_broken_when_builtins_are_absent() -> None:
    exts = discover(include_entry_points=False)
    assert all(hasattr(e, "manifest") for e in exts)


@pytest.mark.asyncio
async def test_bootstrap_from_env_then_the_database_wins(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    ext = FakeExt()
    monkeypatch.setenv("FAKEEXT_URL", "http://env")
    monkeypatch.setenv("FAKEEXT_TOKEN", "envtoken")
    await ensure_rows(db, [ext])
    st = await load_state(db, ext)
    assert st.enabled and st.configured and st.settings["url"] == "http://env"
    # database values now win over the environment
    await save_settings(db, ext, {"url": "http://db", "token": REDACTED})
    st = await load_state(db, ext)
    assert st.settings["url"] == "http://db"
    assert st.settings["token"] == "envtoken"  # a masked secret keeps the stored value
    await set_enabled(db, "fakeext", False)
    assert not (await load_state(db, ext)).enabled
    # ensure_rows never overwrites an existing row
    await ensure_rows(db, [ext])
    assert not (await load_state(db, ext)).enabled


@pytest.mark.asyncio
async def test_unconfigured_extension_starts_disabled(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("FAKEEXT_URL", raising=False)
    monkeypatch.delenv("FAKEEXT_TOKEN", raising=False)
    ext = FakeExt()
    await ensure_rows(db, [ext])
    st = await load_state(db, ext)
    assert not st.enabled and not st.configured


@pytest.mark.asyncio
async def test_sync_registers_extension_stages_with_their_state(db: Database) -> None:
    graph = build_full_graph([FakeExt()])

    async def stage_row() -> Any:
        return await db.fetchrow(
            "SELECT enabled, disabled_reason, extension FROM job_stages WHERE stage = 'fake_push'"
        )

    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", False, {}, False)})
    r = await stage_row()
    assert not r["enabled"] and r["disabled_reason"] == "extension_disabled"
    assert r["extension"] == "fakeext"

    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", True, {}, False)})
    r = await stage_row()
    assert not r["enabled"] and r["disabled_reason"] == "not_configured"

    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", True, {"url": "x"}, True)})
    r = await stage_row()
    assert r["enabled"] and r["disabled_reason"] is None
    core = await db.fetchrow("SELECT extension FROM job_stages WHERE stage = 'detect'")
    assert core["extension"] is None


@pytest.mark.asyncio
async def test_disabled_extension_stage_refuses_enqueue_and_is_never_claimed(db: Database) -> None:
    graph = build_full_graph([FakeExt()])
    lib = await make_library(db)
    _, season = await make_series_and_season(db, lib)

    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", False, {}, False)})
    res = await enqueue_job(db, subject_kind="season", subject_id=season, stage="fake_push")
    assert (res.outcome, res.job_id) == ("disabled", None)

    # a job that was queued while enabled stays put once the extension is disabled
    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", True, {"url": "x"}, True)})
    queued = await enqueue_job(db, subject_kind="season", subject_id=season, stage="fake_push")
    assert queued.outcome == "inserted"
    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", False, {}, False)})
    assert (
        await claim_for_pool(db, worker_id="w", pool="network_plex", stages=["fake_push"]) is None
    )
    await sync_graph(db, graph, {"fakeext": ExtensionState("fakeext", True, {"url": "x"}, True)})
    assert await claim_for_pool(db, worker_id="w", pool="network_plex", stages=["fake_push"])


@pytest.mark.asyncio
async def test_an_operator_disable_survives_the_extension_sync(db: Database) -> None:
    graph = build_full_graph([FakeExt()])
    on = {"fakeext": ExtensionState("fakeext", True, {"url": "x"}, True)}
    await sync_graph(db, graph, on)
    await db.execute(
        "UPDATE job_stages SET enabled = $1, disabled_reason = 'operator' WHERE stage = 'fake_push'",
        False,
    )
    await sync_graph(db, graph, on)
    r = await db.fetchrow("SELECT enabled, disabled_reason FROM job_stages WHERE stage='fake_push'")
    assert not r["enabled"] and r["disabled_reason"] == "operator"
