"""Story 54.37 T01, T02 (python): plugin entries exist only at run time, derived as the Go
loader derives them, merged with the static catalog."""

from __future__ import annotations

import pytest

from maktaba_pipeline.components import catalog

pytestmark = pytest.mark.unit


def test_static_plugin_entries_are_refused() -> None:
    base = catalog.raw().decode()
    extra = (
        '\n[[component]]\nid = "plugin.dev.example.x"\nkind = "stages"\ngroup = "plugins"\n'
        'title_key = "a"\ndesc_key = "b"\nstages = ["p_x"]\nplatforms = ["darwin-arm64"]\nsince = "1"\n'
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
    for r in ("incompatible", "grants_needed", "kept_off", "plugin_crash_loop", "sandbox_unavailable"):
        assert r in static.reasons
