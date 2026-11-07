"""Story 54.37 T01, T02, T04 (python): plugin entries exist only at run time, derived and
gated as the Go loader does (the shared goldens of ``shared/components/testdata``)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from maktaba_pipeline.components import catalog
from maktaba_pipeline.components.plugingate import GateInput, plugin_gate

pytestmark = pytest.mark.unit

TESTDATA = Path(__file__).resolve().parents[3] / "shared" / "components" / "testdata"


def test_static_plugin_entries_are_refused() -> None:
    base = catalog.raw().decode()
    extra = (
        '\n[[component]]\nid = "plugin.dev.example.x"\nkind = "stages"\ngroup = "plugins"\n'
        'title_key = "a"\ndesc_key = "b"\nstages = ["p_x"]\n'
        'platforms = ["darwin-arm64"]\nsince = "1"\n'
    )
    with pytest.raises(catalog.CatalogError, match="plugin"):
        catalog.load(base + extra)


def test_entries_merge_like_go() -> None:
    static = catalog.default()
    a = catalog.plugin_entry("dev.example.a", ("p_aaaaaaaa_x",), ("any",), 600)
    cat = catalog.with_entries(static, [a])
    got = cat.get("plugin.dev.example.a")
    assert got is not None and got.kind == "plugin" and got.group == "plugins"
    assert got.placement == "home" and got.requires == ("plugins.host",)
    assert got.drain == catalog.Drain(120, True) and len(got.platforms) == 4 and got.bundles == ()
    assert cat.by_stage("p_aaaaaaaa_x") is got
    assert static.get("plugin.dev.example.a") is None
    with pytest.raises(catalog.CatalogError):
        catalog.with_entries(static, [catalog.plugin_entry("dev.example.c", ("transcribe",))])
    host = static.get("plugins.host")
    assert host is not None and host.on_demand
    for r in (
        "incompatible",
        "grants_needed",
        "kept_off",
        "plugin_crash_loop",
        "sandbox_unavailable",
    ):
        assert r in static.reasons


def test_the_merged_dump_is_gos() -> None:
    specs = json.loads((TESTDATA / "plugin_specs.json").read_text())
    entries = [catalog.plugin_entry_of(s["manifest"], s["since"]) for s in specs]
    cat, skipped = catalog.with_plugins(catalog.default(), entries)
    assert skipped == []
    full = json.loads(cat.dump())
    got = {
        "components": [c for c in full["components"] if c["kind"] == "plugin"],
        "order": full["order"],
    }
    want = json.loads((TESTDATA / "plugin_entries.dump.json").read_text())
    assert got == want


def test_with_plugins_leaves_out_what_it_refuses() -> None:
    good = catalog.plugin_entry("dev.example.good", ("p_11111111_x",))
    bad = catalog.plugin_entry("dev.example.bad", ("transcribe",))
    cat, skipped = catalog.with_plugins(catalog.default(), [good, bad])
    assert cat.get("plugin.dev.example.good") is not None
    assert cat.get("plugin.dev.example.bad") is None
    assert len(skipped) == 1 and "dev.example.bad" in skipped[0]


def test_queue_names_follow_the_shared_vector() -> None:
    vec = json.loads(
        (TESTDATA.parent.parent / "plugins" / "testdata" / "stage_names.json").read_text()
    )
    for c in vec:
        assert catalog.plugin_queue_name(c["id"], c["stage"]) == c["name"]


def test_gates_table_matches_go() -> None:
    doc = json.loads((TESTDATA / "plugin_gates.json").read_text())
    assert len(doc["cases"]) >= 10
    for c in doc["cases"]:
        reason, params = plugin_gate(GateInput(**c["in"]))
        assert reason == c["reason"], c["name"]
        for k, v in (c.get("params") or {}).items():
            assert str(params[k]) == v, c["name"]


def test_only_a_store_countersignature_needs_no_opt_in() -> None:
    """Story 54.14 §2: automated and reviewed (and Phase 1's "store") are reviewed."""
    from maktaba_pipeline.components.plugingate import unreviewed

    assert [unreviewed(lv) for lv in ("automated", "reviewed", "store")] == [False, False, False]
    assert all(unreviewed(lv) for lv in ("none", "developer", "development", ""))
