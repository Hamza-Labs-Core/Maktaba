"""Story 54.37 T04 (the pipeline runner's half), 54.07 (keep-off), 54.09 (the breaker's
hold): a plugin entry's own gate closes its stages with the story's reason, and the
runner's status rows say it."""

from __future__ import annotations

from datetime import UTC, datetime

import pytest

from maktaba_pipeline.components import catalog
from maktaba_pipeline.components.runner import ComponentRunner
from maktaba_pipeline.components.stages import gates, machine_reasons
from maktaba_pipeline.components.store import Desired, Hold

pytestmark = pytest.mark.unit

PID = "dev.example.gated"
CID = "plugin." + PID
STAGE = catalog.plugin_queue_name(PID, "work")


def _cat() -> catalog.Catalog:
    return catalog.with_entries(catalog.default(), [catalog.plugin_entry(PID, (STAGE,))])


def _row(
    phase_of: dict[str, Desired], plugins: dict[str, tuple[str, dict[str, object]]]
) -> tuple[str, str]:
    cat = _cat()
    r = ComponentRunner(None, catalog=cat, reconcile=True, graph=())
    r._graph_names = {STAGE}
    r.gates = gates(phase_of, cat, None, plugins)
    row = next(s for s in r.status_rows() if s.id == CID)
    reason = next((c.reason for c in row.conditions if c.reason), "")
    return row.phase, reason


def test_a_plugin_waits_on_its_own_gate() -> None:
    on = {CID: Desired(CID, "enabled", 3)}
    g = gates(on, _cat(), None, {CID: ("grants_needed", {"capabilities": "media.read"})})
    assert not g[CID].open and g[CID].reason == "grants_needed"
    assert machine_reasons(g, _cat())[STAGE] == f"component_off:{CID}"
    assert _row(on, {CID: ("grants_needed", {})}) == ("waiting", "grants_needed")
    assert _row(on, {CID: ("unreviewed_disallowed", {})}) == ("waiting", "unreviewed_disallowed")
    assert _row(on, {CID: ("", {})}) == ("running", "")


def test_kept_off_and_the_breaker() -> None:
    off = {CID: Desired(CID, "installed", 2)}
    assert _row(off, {CID: ("", {"kept_off": True})}) == ("off", "kept_off")
    assert _row(off, {CID: ("", {})}) == ("off", "")
    held = {CID: Desired(CID, "enabled", 4, Hold("plugin-breaker", datetime.now(UTC)))}
    assert _row(held, {CID: ("plugin_crash_loop", {})}) == ("stopped", "plugin_crash_loop")
    assert _row(held, {CID: ("", {})}) == ("stopped", "held")


def test_the_host_off_keeps_every_plugin_waiting() -> None:
    desired = {
        CID: Desired(CID, "enabled", 3),
        "plugins.host": Desired("plugins.host", "installed", 1),
    }
    g = gates(desired, _cat(), None, {CID: ("", {})})
    assert not g[CID].open and g[CID].reason == "dependency_off"
