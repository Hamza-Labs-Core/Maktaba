"""A plugin runner loads no extension, so the worker sends the names of the plan exporters
its extensions registered and the runner resolves them from the extensions it discovers
(Epic 54 wave 7, story 54.34)."""

from __future__ import annotations

from collections.abc import Iterator

import pytest

from maktaba_pipeline.extensions.dizquetv.exporter import EXPORT_NAME, export_channel
from maktaba_pipeline.schedule import plan_inputs


@pytest.fixture(autouse=True)
def _clean() -> Iterator[None]:
    plan_inputs.reset_plan_exporters()
    yield
    plan_inputs.reset_plan_exporters()


def test_the_runner_resolves_the_workers_exporters() -> None:
    assert plan_inputs.use_plan_exporters([EXPORT_NAME]) == []
    assert plan_inputs.plan_exporters() == {EXPORT_NAME: export_channel}


def test_an_exporter_it_cannot_load_is_answered_and_none_is_left() -> None:
    plan_inputs.register_plan_exporter("stale", export_channel)
    assert plan_inputs.use_plan_exporters([EXPORT_NAME, "not-installed"]) == ["not-installed"]
    assert set(plan_inputs.plan_exporters()) == {EXPORT_NAME}
    assert plan_inputs.use_plan_exporters([]) == []
    assert plan_inputs.plan_exporters() == {}


def test_a_runner_job_whose_exporters_it_cannot_load_runs_in_the_worker() -> None:
    from maktaba_plugin_runner.server import _use_plan_exporters

    assert _use_plan_exporters({}) == ""  # an older worker sends none: nothing changes
    assert _use_plan_exporters({"plan_exporters": [EXPORT_NAME]}) == ""
    why = _use_plan_exporters({"plan_exporters": ["not-installed"]})
    assert "not-installed" in why
