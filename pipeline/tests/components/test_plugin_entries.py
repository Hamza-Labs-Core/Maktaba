"""Story 54.37 T01, T02, T04 (python): plugin entries exist only at run time, derived and
gated as the Go loader does (the shared goldens of ``shared/components/testdata``)."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.components import catalog, plugingate
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


class _GateDB:
    """The tables plugingate.read asks, in memory."""

    def __init__(self, price: str, until: datetime | None) -> None:
        self.manifest = {
            "maktaba": ">=0.1.0",
            "plugin_api": "1.0",
            "price": price,
            "capabilities": {},
        }
        self.until = until

    async def fetch(self, q: str, *a: object) -> list[dict[str, object]]:
        if "FROM plugin_installs" in q:
            return [
                {"plugin_id": "dev.example.pro", "manifest": self.manifest, "review_level": "store"}
            ]
        if "FROM plugin_entitlements" in q:
            return (
                []
                if self.until is None
                else [{"plugin_id": "dev.example.pro", "until": self.until}]
            )
        return []

    async def fetchrow(self, q: str, *a: object) -> None:
        return None


def _run(coro: Any) -> Any:
    """Runs a coroutine that never suspends (the fake answers at once): no event loop, whose
    self-pipe is a socket the unit tier refuses."""
    try:
        coro.send(None)
    except StopIteration as done:
        return done.value
    raise AssertionError("the coroutine suspended")


def test_a_paid_plugin_waits_for_its_entitlement() -> None:
    """Story 54.19 T04 (the pipeline's half): as the supervisor's runner, a paid plugin with
    no entitlement valid now (72-hour grace) is entitlement_missing; a free one needs none."""
    now = datetime(2026, 11, 20, 12, tzinfo=UTC)
    until = now - timedelta(hours=71)
    cases = [
        ("dev.example.pro", None, "entitlement_missing"),
        ("dev.example.pro", until, ""),
        ("dev.example.pro", now - timedelta(hours=73), "entitlement_missing"),
        ("free", None, ""),
    ]
    for price, u, want in cases:
        got = _run(plugingate.read(_GateDB(price, u), "0.3.83", now=now))
        assert got["plugin.dev.example.pro"][0] == want, (price, u)
