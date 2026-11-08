"""A job of a first-party stage whose runner starts on demand (plugins/native.py
``serving``): the shared groups' runners stop when idle, so the first job after an update
(every process restarted, ``job_stages.impl`` flipped to ``plugin``) finds its runner down.
The worker asks the supervisor for it and the job waits for it to come up, a bounded while,
instead of running in-process beside a runner that serves seconds later (update rehearsal
16.8: both jobs queued after the flip ran in the worker, the runner up 9 s later)."""

from __future__ import annotations

import asyncio
import time
import urllib.error
import urllib.request
from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.plugins import firstparty, native

pytestmark = pytest.mark.asyncio

STAGE = "plex_livetv"  # tv.maktaba.plex's Live TV stage, in the on-demand service group


class _Res:
    def __enter__(self) -> _Res:
        return self

    def __exit__(self, *a: object) -> None:
        return None

    def read(self) -> bytes:
        return b""


class _Supervisor:
    """The supervisor's POST /runners/{group}/start, and the runner it starts: up
    ``start_after`` seconds after the ask (never, with None); ``refuse`` answers 409."""

    def __init__(self, start_after: float | None, refuse: bool = False) -> None:
        self.start_after = start_after
        self.refuse = refuse
        self.asks: list[str] = []
        self.up_at: float | None = None
        self.probes = 0

    def urlopen(self, req: urllib.request.Request, timeout: float = 0) -> _Res:
        self.asks.append(req.full_url)
        if self.refuse:
            raise urllib.error.HTTPError(req.full_url, 409, "component_off", {}, None)  # type: ignore[arg-type]
        if self.start_after is not None and self.up_at is None:
            self.up_at = time.monotonic() + self.start_after
        return _Res()

    async def probe(self, group: str) -> dict[str, Any] | None:
        self.probes += 1
        if self.up_at is None or time.monotonic() < self.up_at:
            return None
        return {"type": "hello", "bundle": firstparty.bundle_digest(), "stages": {STAGE: None}}


@pytest.fixture(autouse=True)
def _clean(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    assert firstparty.runner_instance(STAGE) == "service"
    monkeypatch.setenv("MAKTABA_SUPERVISOR_URL", "http://127.0.0.1:9/")
    monkeypatch.setattr(native, "RUNNER_POLL_SEC", 0.02)
    monkeypatch.setattr(native, "RUNNER_START_WAIT_SEC", 1.0)
    native.forget_hello()
    native._ASKED.clear()
    native._START_FAILED.clear()
    native._ASK_REFUSED.clear()
    yield
    native.forget_hello()
    native._ASKED.clear()
    native._START_FAILED.clear()
    native._ASK_REFUSED.clear()


def _sup(monkeypatch: pytest.MonkeyPatch, sup: _Supervisor) -> _Supervisor:
    monkeypatch.setattr(urllib.request, "urlopen", sup.urlopen)
    monkeypatch.setattr(native, "_probe", sup.probe)
    return sup


async def test_jobs_wait_for_the_runner_they_asked_for(monkeypatch: pytest.MonkeyPatch) -> None:
    """Two jobs at once, the runner down: one ask, and both go to the runner once it is up."""
    sup = _sup(monkeypatch, _Supervisor(start_after=0.2))
    got = await asyncio.gather(native.serving(STAGE), native.serving(STAGE))
    assert list(got) == [(True, None), (True, None)]
    assert sup.asks == ["http://127.0.0.1:9/runners/service/start"]


async def test_a_runner_that_never_comes_up_costs_one_wait(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The window ends: that job runs in the worker, and the next ones do at once (no wait
    per job) until the runner answers again."""
    _sup(monkeypatch, _Supervisor(start_after=None))
    t0 = time.monotonic()
    assert await native.serving(STAGE) == (False, None)
    assert time.monotonic() - t0 >= 1.0
    native._ASKED.clear()  # the next minute's ask
    t1 = time.monotonic()
    assert await native.serving(STAGE) == (False, None)
    assert time.monotonic() - t1 < 0.5


async def test_a_refused_ask_is_not_waited_for(monkeypatch: pytest.MonkeyPatch) -> None:
    """The supervisor will not start it (its component is off): the job runs in the worker."""
    _sup(monkeypatch, _Supervisor(start_after=None, refuse=True))
    t0 = time.monotonic()
    assert await native.serving(STAGE) == (False, None)
    assert time.monotonic() - t0 < 0.5


async def test_no_supervisor_no_wait(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_SUPERVISOR_URL")
    sup = _sup(monkeypatch, _Supervisor(start_after=0.0))
    assert await native.serving(STAGE) == (False, None)
    assert sup.asks == []


async def test_a_runner_that_did_not_answer_is_seen_once_it_does(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """ "Did not answer" is believed only a moment: a runner up since is used by the next job,
    not after the hello cache's 15 seconds."""
    monkeypatch.delenv("MAKTABA_SUPERVISOR_URL")
    sup = _sup(monkeypatch, _Supervisor(start_after=None))
    assert await native.hello("service") is None
    sup.up_at = time.monotonic()
    await asyncio.sleep(native.HELLO_DOWN_TTL + 0.05)
    assert await native.hello("service") is not None
    probes = sup.probes
    assert await native.hello("service") is not None  # an answer is kept
    assert sup.probes == probes
