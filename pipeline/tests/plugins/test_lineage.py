"""Story 54.40 T02, T08 (python, no database): a plugin job's requests and published
events become jobs with its lineage extended; past the hop cap or into a pair already in
the path they fail the job as plugin_loop; an event goes only from a plugin stage to
another plugin's stage."""

from __future__ import annotations

from types import SimpleNamespace
from uuid import uuid4

import pytest

from maktaba_pipeline.engine import causes, failure_classes
from maktaba_pipeline.engine.context import StageFailure
from maktaba_pipeline.engine.graph import Request
from maktaba_pipeline.engine.propagate import _plugin_event
from maktaba_pipeline.plugins import dispatch
from maktaba_pipeline.plugins import manifest as pm

pytestmark = pytest.mark.unit

PID = "dev.example.silences"
HEAR = pm.queue_name("dev.example.writer", "write_chapters")


def _lineage(hops: int, *pairs: list[str]) -> dict:
    return {
        "origin": {"kind": "core", "stage": "extract"},
        "hops": hops,
        "path": [list(p) for p in pairs],
    }


def test_requests_and_events_carry_the_extended_lineage() -> None:
    vid, other = str(uuid4()), str(uuid4())
    me = pm.queue_name(PID, "find_silences")
    res = {
        "outcome": "ok",
        "output_hash": "h",
        "requests": [
            {"stage": "tidy", "subject": {"kind": "video", "id": other}, "payload": '{"a":1}'}
        ],
        "events": [
            {
                "name": "silences.found",
                "subject": {"kind": "video", "id": vid},
                "payload": '{"n":3}',
                "stages": [HEAR],
            }
        ],
    }
    out = dispatch.result_to_stage(res, PID, _lineage(1, [me, f"video:{vid}"]))
    req, ev = out.requests
    assert req.stage == pm.queue_name(PID, "tidy") and req.payload["a"] == 1
    assert req.payload["_lineage"]["hops"] == 2
    assert req.payload["_lineage"]["path"][-1] == [pm.queue_name(PID, "tidy"), f"video:{other}"]
    assert ev.stage == HEAR and ev.reason == f"plugin-event:{PID}/silences.found"
    assert ev.payload["event"] == "silences.found" and ev.payload["from"] == PID
    assert ev.payload["payload"] == {"n": 3}
    assert ev.payload["_lineage"]["origin"]["stage"] == "extract"
    assert ev.payload["_lineage"]["path"] == [[me, f"video:{vid}"], [HEAR, f"video:{vid}"]]


def test_a_guest_cannot_forge_its_lineage_in_a_request() -> None:
    vid = str(uuid4())
    res = {
        "outcome": "ok",
        "requests": [
            {
                "stage": "tidy",
                "subject": {"kind": "video", "id": vid},
                "payload": '{"_lineage":{"hops":1,"path":[]}}',
            }
        ],
    }
    (req,) = dispatch.result_to_stage(res, PID, _lineage(5)).requests
    assert req.payload["_lineage"]["hops"] == 6


def test_the_hop_cap_and_reentry_fail_as_plugin_loop() -> None:
    vid = str(uuid4())
    res = {
        "outcome": "ok",
        "requests": [{"stage": "tidy", "subject": {"kind": "video", "id": vid}}],
    }
    with pytest.raises(StageFailure) as cap:
        dispatch.result_to_stage(res, PID, _lineage(dispatch.MAX_HOPS))
    assert cap.value.kind == "plugin_loop"
    tidy = [pm.queue_name(PID, "tidy"), f"video:{vid}"]
    with pytest.raises(StageFailure) as again:
        dispatch.result_to_stage(res, PID, _lineage(2, tidy))
    assert again.value.kind == "plugin_loop" and not again.value.retryable


def test_a_refused_loop_fails_the_job() -> None:
    with pytest.raises(StageFailure) as f:
        dispatch.result_to_stage({"outcome": "refused", "error_code": "plugin_loop"}, PID)
    assert f.value.kind == "plugin_loop" and not f.value.retryable


def test_plugin_loop_is_a_cause_that_is_not_retried() -> None:
    assert causes.classify("plugin_loop", "p_x on video:1 would loop") == "plugin_loop"
    assert failure_classes.class_for("plugin_loop") == "item_permanent"


def test_an_event_goes_from_a_plugin_stage_to_another_plugin_only() -> None:
    a = SimpleNamespace(name="p_a_x", extension=f"plugin:{PID}", may_request=())
    b = SimpleNamespace(name=HEAR, extension="plugin:dev.example.writer")
    core = SimpleNamespace(name="transcribe", extension=None)
    graph = {s.name: s for s in (a, b, core)}
    ev = Request(
        stage=HEAR, subject_kind="video", subject_id=uuid4(), reason=f"plugin-event:{PID}/x"
    )
    assert _plugin_event(a, ev, graph)  # type: ignore[arg-type]
    plain = Request(stage=HEAR, subject_kind="video", subject_id=uuid4(), reason=f"plugin:{PID}")
    assert not _plugin_event(a, plain, graph)  # type: ignore[arg-type]
    to_core = Request(
        stage="transcribe", subject_kind="video", subject_id=uuid4(), reason=f"plugin-event:{PID}/x"
    )
    assert not _plugin_event(a, to_core, graph)  # type: ignore[arg-type]
    own = Request(
        stage="p_a_x", subject_kind="video", subject_id=uuid4(), reason=f"plugin-event:{PID}/x"
    )
    assert not _plugin_event(a, own, graph)  # type: ignore[arg-type]
    assert not _plugin_event(core, ev, graph)  # type: ignore[arg-type]
