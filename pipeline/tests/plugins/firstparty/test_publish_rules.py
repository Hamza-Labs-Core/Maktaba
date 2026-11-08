"""The engine relay's ``publish`` op (plugins/publish_rules.py): the worker publishes a
plugin's event only when its kind, size, schema, topic and ids pass; every refusal is an
error back to the runner, publishes nothing and is logged without the payload's body."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest
from structlog.testing import capture_logs

from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.plugins import firstparty, native
from maktaba_pipeline.plugins.publish_rules import (
    KINDS,
    MAX_PAYLOAD_BYTES,
    PublishRefused,
    check_publish,
    payload_size,
)

pytestmark = pytest.mark.asyncio

LIB = "8f0e7c38-6c2e-4b8f-9d61-1a2b3c4d5e6f"
VIDEO = "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081"
SECRET_LABEL = "a user's private title"


class _Relay:
    def __init__(self) -> None:
        self.published: list[tuple[str, dict[str, Any]]] = []

    async def publish(self, type_: str, payload: dict[str, Any]) -> None:
        self.published.append((type_, payload))


def _job(stage: str = "transcribe", lib: str | None = LIB, video: str = VIDEO) -> Any:
    return SimpleNamespace(
        id=7, subject_kind="video", subject=Subject(kind="video", id=video),  # type: ignore[arg-type]
        library_id=lib, stage=stage,
    )  # fmt: skip


def _ref(job: Any, **over: Any) -> dict[str, Any]:
    """A job ref as engine.events.job_ref answers it."""
    ref = {
        "id": int(job.id), "stage": job.stage, "state": "running",
        "subject_kind": job.subject_kind, "subject_id": str(job.subject.id),
        "subject_label": SECRET_LABEL,
        "library_id": None if job.library_id is None else str(job.library_id),
        "priority": 100, "attempts": 1, "max_attempts": 3, "outcome": None,
        "error_cause": None, "created_at": "2026-11-08T10:00:00+00:00",
        "started_at": "2026-11-08T10:00:01+00:00", "finished_at": None,
        "progress": {"step": "listen", "steps": ["listen"], "done_units": 3, "total_units": 9,
                     "message": "listening"},
    }  # fmt: skip
    ref.update(over)
    return ref


def _log_line(job: Any, **over: Any) -> dict[str, Any]:
    """A ``job.log`` payload as engine.events.log_event builds it."""
    p = {
        "job_id": int(job.id), "stage": job.stage, "subject_label": SECRET_LABEL,
        "level": "info", "kind": "step", "message": "m", "data": {"to": [{"stage": "x"}]},
    }  # fmt: skip
    p.update(over)
    return p


async def _ask(job: Any, kind: Any, payload: Any) -> tuple[_Relay, Exception | None]:
    """The op through the worker's engine handler, as a runner's frame reaches it."""
    relay = _Relay()
    db = SimpleNamespace(engine_relay=relay)
    handle = native.engine_handler(db, SimpleNamespace(job=job), "tv.maktaba.transcription")
    try:
        await handle("publish", {"type": native.encode_any(kind),
                                 "payload": native.encode_any(payload)})  # fmt: skip
    except Exception as exc:  # noqa: BLE001 - the error the runner sees
        return relay, exc
    return relay, None


async def _refused(job: Any, kind: Any, payload: Any, reason: str) -> None:
    with capture_logs() as logs:
        relay, err = await _ask(job, kind, payload)
    assert isinstance(err, PublishRefused) and err.reason == reason, err
    assert str(err).startswith(f"plugin_publish_refused: {reason}")
    assert relay.published == []  # nothing published
    got = [e for e in logs if e["event"] == "plugin_publish_refused"]
    assert len(got) == 1
    line = got[0]
    assert line["plugin"] == "tv.maktaba.transcription" and line["reason"] == reason
    assert line["kind"] == str(kind)[:100] and line["size"] == payload_size(payload)
    assert SECRET_LABEL not in repr(line)  # the body is never logged


async def test_an_unknown_kind_is_refused() -> None:
    job = _job()
    # the engine's own lifecycle events are the worker's alone
    for kind in ("job.finished", "job.started", "job.failed", "scan.done", 5):
        await _refused(job, kind, {"job": _ref(job)}, "unknown_kind")


async def test_a_payload_that_does_not_match_its_kinds_schema_is_refused() -> None:
    job = _job()
    for kind, payload in (
        ("job.progress", {"job": _ref(job), "extra": 1}),  # an unknown key
        ("job.progress", {}),  # a missing key
        ("job.progress", {"job": _ref(job, id="7")}),  # a wrong type
        ("job.progress", {"job": _ref(job, injected={"a": 1})}),  # unknown in the ref
        ("job.log", _log_line(job, level="fatal")),  # not one of the allowed values
        ("job.log", _log_line(job, message="x" * 5000)),  # too long
        ("job.log", [1, 2]),  # not an object
    ):
        await _refused(job, kind, payload, "schema")


async def test_an_oversize_payload_is_refused() -> None:
    job = _job()
    big = _log_line(job, data={"blob": "x" * MAX_PAYLOAD_BYTES})
    await _refused(job, "job.log", big, "oversize")


async def test_another_plugins_topic_is_refused() -> None:
    """A payload about another job (another plugin's stage, or another job of this one)."""
    job = _job()
    for kind, payload in (
        ("job.progress", {"job": _ref(job, stage="thumb_small")}),
        ("job.progress", {"job": _ref(job, id=8)}),
        ("job.log", _log_line(job, stage="tag")),
        ("job.log", _log_line(job, job_id=8)),
    ):
        await _refused(job, kind, payload, "topic")


async def test_an_id_outside_the_jobs_scope_is_refused() -> None:
    job = _job()
    other = "2b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081"
    for over in (
        {"subject_id": other},
        {"subject_id": "not-a-uuid"},
        {"library_id": "9f0e7c38-6c2e-4b8f-9d61-1a2b3c4d5e6f"},
        {"library_id": None},
        {"subject_kind": "library"},
    ):
        await _refused(job, "job.progress", {"job": _ref(job, **over)}, "scope")
    # a job with no library names none
    await _refused(_job(lib=None), "job.progress", {"job": _ref(_job(lib=None), library_id=LIB)},
                   "scope")  # fmt: skip


async def test_the_cap_is_pinned() -> None:
    """64 KiB: well above the largest real payload (a progress ref of a few KB, a log line
    with a hundred queued stages), small enough that one event cannot flood every client."""
    assert MAX_PAYLOAD_BYTES == 64 * 1024
    job = _job()
    hundred = _log_line(job, data={"to": [{"stage": f"stage_{i}", "subject": VIDEO}
                                          for i in range(100)]})  # fmt: skip
    assert payload_size(hundred) * 4 < MAX_PAYLOAD_BYTES
    # at the cap passes, one byte over is refused
    base = _log_line(job, data={"blob": ""})
    at = _log_line(job, data={"blob": "x" * (MAX_PAYLOAD_BYTES - payload_size(base))})
    assert payload_size(at) == MAX_PAYLOAD_BYTES
    check_publish("p", "job.log", at, job)
    over = _log_line(job, data={"blob": "x" * (MAX_PAYLOAD_BYTES - payload_size(base) + 1)})
    with pytest.raises(PublishRefused, match="oversize"):
        check_publish("p", "job.log", over, job)


@pytest.mark.parametrize("stage", sorted(firstparty.stage_names()))
async def test_every_first_party_stages_own_publishes_pass(stage: str) -> None:
    """What shared code can publish from a first-party runner, for a job of each stage of
    each first-party plugin: its progress (StageContext.progress) and its log lines
    (log_event's mirror), for a per-video and a library job, with uuids in any form."""
    for job in (_job(stage), _job(stage, lib=None), _job(stage, video=VIDEO.upper())):
        for kind, payload in (
            ("job.progress", {"job": _ref(job)}),
            ("job.progress", {"job": _ref(job, progress=None, subject_label=None)}),
            ("job.log", _log_line(job)),
            ("job.log", {k: v for k, v in _log_line(job).items() if k != "data"}),
        ):
            relay, err = await _ask(job, kind, payload)
            assert err is None, (stage, kind, err)
            assert relay.published == [(kind, payload)]


async def test_each_kind_has_a_schema() -> None:
    assert set(KINDS) == {"job.progress", "job.log"}
    for schema in KINDS.values():
        assert schema["type"] == "object" and schema["additionalProperties"] is False
