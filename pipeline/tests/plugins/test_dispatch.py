"""Story 54.06 T03, T04 (dispatch, no database): the plugin stage handler against a fake
maktaba-plugind on a unix socket and a fake plugin API: each host outcome maps to its
StageResult or failure cause; a host that is gone or closes mid-job gives the job back
without an attempt; the token is always ended."""

from __future__ import annotations

import asyncio
import json
import struct
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.engine.context import JobDeferred, StageFailure
from maktaba_pipeline.plugins import dispatch
from maktaba_pipeline.plugins import manifest as pm

# the event loop opens a socket pair, which the unit tier forbids: these need no service
pytestmark = [pytest.mark.integration, pytest.mark.asyncio]

ROOT = Path(__file__).resolve().parents[3]
PKG = ROOT / "shared" / "plugins" / "testdata" / "manifests" / "package"
PID = "dev.example.silence-chapters"


def _files(p: str) -> bytes | None:
    f = PKG / p
    return f.read_bytes() if f.is_file() else None


DOC = pm.validate(
    (ROOT / "shared/plugins/testdata/manifests/valid/minimal.toml").read_bytes(), _files
)
STAGE = pm.queue_name(PID, "find_silences")


@dataclass
class FakeDB:
    executed: list[tuple[str, tuple[Any, ...]]] = field(default_factory=list)
    #: the row's data_version (slot 0202) and the manifest's, with its migrate flag
    data_version: int | None = None
    doc: dict[str, Any] = field(default_factory=lambda: DOC)

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        return [
            {
                "plugin_id": PID,
                "version": "1.4.0",
                "path": "/data/plugins/x",
                "manifest": self.doc,
                "review_level": "none",
                "data_version": self.data_version,
            }
        ]

    async def fetchrow(self, sql: str, *args: Any) -> dict[str, Any] | None:
        if "app_settings" in sql:
            return {"value": '{"threshold":3}'}
        return None

    async def execute(self, sql: str, *args: Any) -> str:
        self.executed.append((sql, args))
        return "UPDATE 1"


@dataclass
class Subject:
    kind: str
    id: Any


@dataclass
class Job:
    id: int = 7
    stage: str = STAGE
    payload: dict[str, Any] | None = None
    attempts: int = 1
    subject: Subject = field(default_factory=lambda: Subject("video", uuid4()))


@dataclass
class Stage:
    extension: str = f"plugin:{PID}"


@dataclass
class Ctx:
    stage: Stage = field(default_factory=Stage)
    progressed: list[int] = field(default_factory=list)

    async def progress(self, step: str, done: int, total: int, *a: Any, **k: Any) -> None:
        self.progressed.append(done)


@pytest.fixture
def api(monkeypatch: pytest.MonkeyPatch) -> list[tuple[str, str]]:
    calls: list[tuple[str, str]] = []

    def fake(
        method: str, path: str, body: dict[str, Any] | None = None
    ) -> tuple[int, dict[str, Any]]:
        calls.append((method, path))
        if method == "POST":
            return 200, {"invocation_id": "inv_1", "token": "t", "grants": ["chapters.write"]}
        return 200, {"ended": True}

    monkeypatch.setattr(dispatch, "_api", fake)
    return calls


class FakeWriter:
    def __init__(self) -> None:
        self.buf = bytearray()

    def write(self, b: bytes) -> None:
        self.buf += b

    async def drain(self) -> None:
        return None

    def close(self) -> None:
        return None


def _host(
    monkeypatch: pytest.MonkeyPatch,
    frames: list[dict[str, Any]],
    seen: list[dict[str, Any]],
    eof: bool = True,
) -> None:
    """A fake maktaba-plugind: answers ``frames`` and records the Run it was sent."""

    async def connect(path: str) -> tuple[Any, Any]:
        r = asyncio.StreamReader()
        for f in frames:
            body = json.dumps(f).encode()
            r.feed_data(struct.pack(">I", len(body)) + body)
        r.feed_eof()
        w = FakeWriter()

        class Recording(FakeWriter):
            def write(self, b: bytes) -> None:
                w.write(b)
                (n,) = struct.unpack(">I", bytes(w.buf[:4]))
                if len(w.buf) >= 4 + n:
                    seen.append(json.loads(bytes(w.buf[4 : 4 + n])))

        return r, Recording()

    monkeypatch.setattr(dispatch, "_connect", connect)


def _no_host(monkeypatch: pytest.MonkeyPatch) -> None:
    async def connect(path: str) -> Any:
        raise FileNotFoundError(path)

    monkeypatch.setattr(dispatch, "_connect", connect)


def _result(outcome: str, **kw: Any) -> dict[str, Any]:
    return {"type": "result", "invocation_id": "inv_1", "outcome": outcome, **kw}


@pytest.mark.parametrize(
    ("result", "want"),
    [
        (_result("trap", error_code="plugin_trap"), ("plugin_trap", True)),
        (_result("timeout", error_code="plugin_timeout"), ("plugin_timeout", True)),
        (_result("memory", error_code="plugin_memory"), ("plugin_memory", False)),
        (_result("refused", error_code="plugin_capability"), ("plugin_capability", False)),
        (_result("refused", error_code="plugin_scope"), ("plugin_scope", False)),
        (_result("refused", error_code="plugin_quota"), ("plugin_quota", False)),
        (
            _result("failed", error_code="plugin_failed", detail="bad_input"),
            ("plugin_failed", False),
        ),
        (
            _result("refused", error_code="plugin_forbidden_import"),
            ("plugin_forbidden_import", False),
        ),
    ],
)
async def test_outcomes_map_to_causes(
    monkeypatch: pytest.MonkeyPatch, api: Any, result: dict[str, Any], want: Any
) -> None:
    _host(monkeypatch, [result], [])
    with pytest.raises(StageFailure) as exc:
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    assert (exc.value.kind, exc.value.retryable) == want
    assert api[-1] == ("DELETE", "/internal/plugins/invocations/inv_1")


async def test_ok_with_progress_checkpoint_and_requests(
    monkeypatch: pytest.MonkeyPatch, api: Any
) -> None:
    vid = str(uuid4())
    frames = [
        {"type": "progress", "invocation_id": "inv_1", "pct": 40},
        {"type": "progress", "invocation_id": "inv_1", "pct": 0, "checkpoint": "aGFsZg=="},
        _result(
            "ok",
            output_hash="h1",
            summary='{"chapters":3}',
            requests=[
                {"stage": "find_silences", "subject": {"kind": "video", "id": vid}, "payload": "{}"}
            ],
        ),
    ]
    seen: list[dict[str, Any]] = []
    _host(monkeypatch, frames, seen)
    db, ctx = FakeDB(), Ctx()
    res = await dispatch.run_plugin_stage(db, Job(), ctx)
    assert res.output_hash == "h1" and res.summary["chapters"] == 3
    assert [r.stage for r in res.requests] == [STAGE]
    assert ctx.progressed == [40]
    assert any("plugin_checkpoint" in sql for sql, _ in db.executed)
    run = seen[0]
    assert run["type"] == "run" and run["token"] == "t" and run["plugin_id"] == PID
    assert run["job"]["stage"] == "find_silences" and run["settings"] == '{"threshold":3}'
    assert run["grants"] == ["chapters.write"]


async def test_host_gone_or_lost_gives_the_job_back(
    monkeypatch: pytest.MonkeyPatch, api: Any
) -> None:
    """T04: no host, or a host that closes mid-job: deferred, no attempt spent."""
    _no_host(monkeypatch)
    with pytest.raises(JobDeferred, match="plugin_host_unavailable"):
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    _host(monkeypatch, [{"type": "progress", "invocation_id": "inv_1", "pct": 10}], [])
    with pytest.raises(JobDeferred, match="plugin_host_lost"):
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    assert api.count(("DELETE", "/internal/plugins/invocations/inv_1")) == 2


async def test_a_refused_mint_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(dispatch, "_api", lambda *a, **k: (409, {"code": "plugin_not_installed"}))
    with pytest.raises(JobDeferred, match="plugin_held"):
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())


async def test_cancel_goes_back_to_the_queue(monkeypatch: pytest.MonkeyPatch, api: Any) -> None:
    _host(monkeypatch, [_result("refused", error_code="cancelled", checkpoint="Y3A=")], [])
    db = FakeDB()
    with pytest.raises(JobDeferred, match="plugin_cancelled"):
        await dispatch.run_plugin_stage(db, Job(), Ctx())
    assert any("plugin_checkpoint" in sql for sql, _ in db.executed)


async def test_each_outcome_reaches_the_breaker(monkeypatch: pytest.MonkeyPatch, api: Any) -> None:
    """54.09 §2: the breaker hears every result (its cause, "" for ok) and the refusals the
    API counted when the token ended; a job given back unrun is not counted."""
    from maktaba_pipeline.plugins import breaker

    heard: list[tuple[str, str, int]] = []

    async def record(db: Any, pid: str, cause: str, **kw: Any) -> str:
        heard.append((pid, cause, int(kw.get("refusals", 0))))
        return ""

    monkeypatch.setattr(breaker, "record", record)
    _host(monkeypatch, [_result("trap", error_code="plugin_trap")], [])
    with pytest.raises(StageFailure):
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    _host(monkeypatch, [_result("ok", output_hash="h", summary="{}")], [])
    await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    _no_host(monkeypatch)
    with pytest.raises(JobDeferred):
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    assert heard == [(PID, "plugin_trap", 0), (PID, "", 0)]


async def test_a_drain_cancels_the_job_in_the_host(
    monkeypatch: pytest.MonkeyPatch, api: Any
) -> None:
    """54.07 §1: past the drain's grace the job's task is cancelled; the host is sent
    Cancel for its invocation (it keeps the checkpoint) and the token ends."""
    sent: list[dict[str, Any]] = []
    first = True

    async def connect(path: str) -> tuple[Any, Any]:
        nonlocal first
        r = asyncio.StreamReader()
        if not first:
            body = json.dumps({"type": "ack", "ok": True}).encode()
            r.feed_data(struct.pack(">I", len(body)) + body)
        first = False

        class W(FakeWriter):
            def write(self, b: bytes) -> None:
                sent.append(json.loads(b[4:]))

        return r, W()  # the run's reader never answers: the job runs until cancelled

    monkeypatch.setattr(dispatch, "_connect", connect)
    task = asyncio.ensure_future(dispatch.run_plugin_stage(FakeDB(), Job(), Ctx()))
    for _ in range(50):
        await asyncio.sleep(0.01)
        if sent:
            break
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert [m["type"] for m in sent] == ["run", "cancel"]
    assert sent[1]["invocation_id"] == "inv_1"
    assert api[-1] == ("DELETE", "/internal/plugins/invocations/inv_1")


def test_frames_are_length_prefixed_json() -> None:
    body = json.dumps({"type": "health"}).encode()
    assert len(body) < dispatch.MAX_FRAME


async def test_the_guest_never_sees_the_lineage(monkeypatch: pytest.MonkeyPatch, api: Any) -> None:
    """54.40 §3: the payload the host gets has no _lineage."""
    seen: list[dict[str, Any]] = []
    _host(monkeypatch, [_result("ok", output_hash="h")], seen)
    job = Job(payload={"a": 1, "_lineage": {"hops": 2, "path": []}})
    await dispatch.run_plugin_stage(FakeDB(), job, Ctx())
    assert json.loads(seen[0]["job"]["payload"]) == {"a": 1}


async def test_a_mint_refused_as_a_loop_fails_the_job(monkeypatch: pytest.MonkeyPatch) -> None:
    """54.40 §3: a job a core event started that closes a loop through data."""
    monkeypatch.setattr(
        dispatch,
        "_api",
        lambda *a, **k: (409, {"code": "plugin_loop", "detail": "a loop through data"}),
    )
    with pytest.raises(StageFailure) as f:
        await dispatch.run_plugin_stage(FakeDB(), Job(), Ctx())
    assert f.value.kind == "plugin_loop" and not f.value.retryable


# story 54.07 §5 step 4 (#712): a version whose data_version grew runs one migrate job first


def _migrating(migrate: bool) -> FakeDB:
    doc = dict(DOC, data_version=3, migrate=migrate) if migrate else dict(DOC, data_version=3)
    return FakeDB(data_version=2, doc=doc)


async def test_a_stage_waits_for_the_migrate_job(
    monkeypatch: pytest.MonkeyPatch, api: list[tuple[str, str]]
) -> None:
    enqueued: list[dict[str, Any]] = []

    async def enqueue(db: Any, **kw: Any) -> None:
        enqueued.append(kw)

    import maktaba_pipeline.engine.enqueue as enq

    monkeypatch.setattr(enq, "enqueue_job", enqueue)
    seen: list[dict[str, Any]] = []
    _host(monkeypatch, [_result("ok", output_hash="h")], seen)
    with pytest.raises(JobDeferred) as d:
        await dispatch.run_plugin_stage(_migrating(True), Job(), Ctx())
    assert "plugin_migrating" in d.value.reason
    assert [e["stage"] for e in enqueued] == [pm.queue_name(PID, "_migrate")]
    assert enqueued[0]["subject_kind"] == "catalog"
    assert seen == [] and api == []  # no token, no host


async def test_a_migrate_job_runs_the_guests_migrate_and_records_it(
    monkeypatch: pytest.MonkeyPatch, api: list[tuple[str, str]]
) -> None:
    db = _migrating(True)
    seen: list[dict[str, Any]] = []
    _host(monkeypatch, [_result("ok", output_hash="h")], seen)
    job = Job(stage=pm.queue_name(PID, "_migrate"), subject=Subject("catalog", uuid4()))
    out = await dispatch.run_plugin_stage(db, job, Ctx())
    assert out.output_hash == "data-3"
    assert seen[0]["job"]["stage"] == "__migrate"
    assert json.loads(seen[0]["job"]["payload"]) == {"from": 2, "to": 3}
    sql, args = db.executed[-1]
    assert "SET data_version = NULL" in sql and args == (PID, "1.4.0", 2)


async def test_a_failed_migrate_leaves_the_data_behind(
    monkeypatch: pytest.MonkeyPatch, api: list[tuple[str, str]]
) -> None:
    db = _migrating(True)
    _host(monkeypatch, [_result("failed", error_code="plugin_failed", detail="bad")], [])
    job = Job(stage=pm.queue_name(PID, "_migrate"), subject=Subject("catalog", uuid4()))
    with pytest.raises(StageFailure):
        await dispatch.run_plugin_stage(db, job, Ctx())
    assert not any("data_version" in sql for sql, _ in db.executed)


async def test_with_no_migrate_step_the_data_is_taken_as_it_is(
    monkeypatch: pytest.MonkeyPatch, api: list[tuple[str, str]]
) -> None:
    db = _migrating(False)
    _no_host(monkeypatch)
    job = Job(stage=pm.queue_name(PID, "_migrate"), subject=Subject("catalog", uuid4()))
    out = await dispatch.run_plugin_stage(db, job, Ctx())
    assert out.output_hash == "data-3" and api == []
    assert "SET data_version = NULL" in db.executed[-1][0]


async def test_a_migrate_job_with_nothing_behind_does_nothing(
    monkeypatch: pytest.MonkeyPatch, api: list[tuple[str, str]]
) -> None:
    db = FakeDB(data_version=None, doc=dict(DOC, data_version=3, migrate=True))
    _no_host(monkeypatch)
    job = Job(stage=pm.queue_name(PID, "_migrate"), subject=Subject("catalog", uuid4()))
    out = await dispatch.run_plugin_stage(db, job, Ctx())
    assert out.output_hash == "data-3" and db.executed == [] and api == []
