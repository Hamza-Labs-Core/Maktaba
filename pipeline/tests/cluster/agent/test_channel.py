"""The agent against a fake hub (websockets on 127.0.0.1): handshake v2, sealed frames,
streams, close codes and reconnects, commands, and LLM work both ways."""

from __future__ import annotations

import asyncio
import contextlib
import json
import random
import time
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster import crypto
from maktaba_pipeline.cluster.agent.client import (
    AgentIdentity,
    ChannelAgent,
    Commands,
    agent_url,
    agent_urls,
)
from maktaba_pipeline.cluster.agent.files import ClusterFiles
from maktaba_pipeline.cluster.agent.jobs import JobTracker
from maktaba_pipeline.cluster.agent.logship import LogShipper
from maktaba_pipeline.cluster.agent.metrics import MetricsCollector, PipelineView
from maktaba_pipeline.llm import ClusterLLM, ClusterUnavailable, MachineGone, NoModel
from maktaba_pipeline.llm.runtimes import LLMRuntimes
from maktaba_pipeline.llm.service import LocalLLM
from tests.cluster.agent.fakes import (
    FakeHub,
    FakeRuntimeState,
    HubSession,
    fake_runtime,
    running_hub,
)

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]

NO_RUNTIMES = {"MAKTABA_OLLAMA_URL": "", "MAKTABA_LLM_ENDPOINTS": ""}


def ev(level: str, event: str) -> dict[str, Any]:
    return {"level": level, "event": event, "ts": "2026-10-19T00:00:01Z"}


def make_agent(
    hub: FakeHub,
    *,
    kind: str = "node",
    node_key: crypto.KeyPair | None = None,
    home_static: bytes | None = None,
    key_path: Path | None = None,
    shipper: LogShipper | None = None,
    llm: LocalLLM | None = None,
    **kw: Any,
) -> ChannelAgent:
    ident = AgentIdentity(
        kind=kind,
        url=hub.url,
        worker_id="node-ab12cd34-77" if kind == "node" else "home-w1",
        host="mac-mini",
        role=kind,
        node_id="ab12cd34-0000-0000-0000-000000000000" if kind == "node" else None,
        node_private=node_key.private if node_key else None,
        home_static=home_static if home_static is not None else hub.home_static.public,
        home_key_path=key_path,
        ip="192.168.50.31",
        os="linux",
        version="0.0.1",
    )
    kw.setdefault("backoff_min", 0.02)
    kw.setdefault("backoff_max", 0.1)
    return ChannelAgent(
        ident,
        shipper=shipper if shipper is not None else LogShipper("info"),
        llm=llm if llm is not None else LocalLLM(LLMRuntimes(env=NO_RUNTIMES)),
        rng=random.Random(7),
        **kw,
    )


@contextlib.asynccontextmanager
async def running(
    agent: ChannelAgent, shutdown: asyncio.Event | None = None
) -> AsyncIterator[asyncio.Task[None]]:
    task = asyncio.create_task(agent.run(shutdown))
    try:
        yield task
    finally:
        agent.stop()
        with contextlib.suppress(Exception):
            await asyncio.wait_for(task, 3)


async def until(cond: Any, timeout: float = 3.0) -> None:
    async def wait() -> None:
        while not cond():
            await asyncio.sleep(0.01)

    await asyncio.wait_for(wait(), timeout)


def node_hub() -> tuple[FakeHub, crypto.KeyPair]:
    key = crypto.generate_keypair()
    return FakeHub(node_pub=key.public), key


async def test_agent_url() -> None:
    assert agent_url("http://192.168.1.20:8080") == "ws://192.168.1.20:8080/api/cluster/agent"
    assert agent_url("https://home.example/") == "wss://home.example/api/cluster/agent"
    assert agent_url("http://h:8080/api") == "ws://h:8080/api/cluster/agent"
    assert agent_url("h:8080") == "ws://h:8080/api/cluster/agent"


async def test_node_handshake_streams_and_settings(tmp_path: Path) -> None:
    hub, key = node_hub()
    hub.settings = {"log_level": "debug", "metrics_every": 0.2, "unknown_key": 1}
    shipper = LogShipper("info")
    files = ClusterFiles(tmp_path / "share", "192.168.50.31", "node-ab12cd34-77",
                         fallback_dir=tmp_path / "local")  # fmt: skip
    files.start()
    tracker = JobTracker()
    collector = MetricsCollector(
        PipelineView(role="node", stages=lambda: ("probe",)),
        files_status=files.status,
        health=lambda final: {"cpu.pct": 1.0, "stt.audio_sec:pid1": 9.0 if final else 5.0},
        tracker=tracker,
    )
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, shipper=shipper, files=files, collector=collector)
        tracker.add_sink(agent.job_sink)
        async with running(agent):
            s = await hub.wait_session(1)
            hello = s.hello
            assert hello["t"] == "hello" and hello["v"] == 2 and hello["kind"] == "node"
            assert hello["node_id"].startswith("ab12cd34") and hello["host"] == "mac-mini"
            assert hello["worker_id"] == "node-ab12cd34-77" and isinstance(hello["pid"], int)
            assert len(crypto.b64d(hello["nonce"])) == 16 and len(crypto.b64d(hello["eph"])) == 32
            assert hello["ip"] == "192.168.50.31" and hello["os"] and hello["version"]
            await s.next("llm.models")
            m = await s.next("metrics")
            assert m["pipeline"]["role"] == "node" and m["pipeline"]["stages"] == ["probe"]
            assert m["health"] == {"cpu.pct": 1.0, "stt.audio_sec:pid1": 5.0}
            assert m["files"] == {
                "ip": "192.168.50.31",
                "dir": str(tmp_path / "share" / "192.168.50.31"),
                "on_share": True,
                "reason": None,
                "dropped": 0,
            }
            # welcome settings applied: the shipper now takes debug, metrics every 0.2 s
            await until(lambda: agent.connected)
            assert shipper.level == "debug" and agent.metrics_every == 0.2
            shipper.sink(ev("debug", "fine detail"))
            logs = await s.next("logs")
            assert [r["event"] for r in logs["records"]] == ["fine detail"]
            job: Any = type("J", (), {"id": 5, "stage": "probe", "subject_kind": "video",
                                      "subject_id": "v", "attempts": 1})()  # fmt: skip
            tracker.claimed(job)
            tracker.finished(5, "done", output={"segments": 2})
            claimed = await s.next("job")
            done = await s.next("job")
            assert (claimed["event"], done["event"]) == ("claimed", "done")
            assert done["output"] == {"segments": 2} and claimed["duration_ms"] is None
            await s.send({"t": "ping", "ts": "x"})
            assert (await s.next("pong"))["ts"] == "x"
            await s.send({"t": "something.new", "x": 1})  # ignored, the counter still moves
            await s.send({"t": "ping", "ts": "y"})
            assert (await s.next("pong"))["ts"] == "y"
            assert agent.state == "connected" and agent.status()["session"] == "s1"
    files.stop()
    m_dir = tmp_path / "share" / "192.168.50.31"
    lines = [json.loads(x) for f in (m_dir / "metrics").glob("*/node-ab12cd34-77.jsonl")
             for x in f.read_text().splitlines()]  # fmt: skip
    assert lines and "t" not in lines[0] and "system" in lines[0]  # the frame without t


async def test_home_handshake_with_the_key_file(tmp_path: Path) -> None:
    home_key = bytes(range(32))
    key_path = tmp_path / "home-agent.key"
    hub = FakeHub(home_key=home_key)
    async with running_hub(hub):
        agent = make_agent(hub, kind="home", key_path=key_path)
        async with running(agent):
            await until(lambda: bool(agent.last_error))
            assert "not found" in str(agent.last_error)  # no key yet: retried later
            key_path.write_bytes(home_key)
            s = await hub.wait_session(1)
            assert s.hello["kind"] == "home" and "node_id" not in s.hello
            await s.next("llm.models")


async def test_a_hub_that_is_not_the_home_is_refused() -> None:
    hub, key = node_hub()
    hub.wrong_home = True
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key)
        async with running(agent):
            s = await hub.wait_session(1)
            await asyncio.wait_for(s.closed.wait(), 3)
            assert "not the home I joined" in str(agent.last_error)
            assert s.frames == []  # nothing was sent to it
    # a node config without the home's cluster key never dials
    hub2, key2 = node_hub()
    async with running_hub(hub2):
        agent = make_agent(hub2, node_key=key2, home_static=b"")
        agent.identity.home_static = None
        async with running(agent):
            await until(lambda: agent.state == "no_channel")
            assert "re-join" in str(agent.last_error) and hub2.hellos == []


async def test_wrong_pairing_key_waits_the_longest_delay() -> None:
    hub, _ = node_hub()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=crypto.generate_keypair(), backoff_max=0.3)
        async with running(agent):
            await until(lambda: hub.refused >= 2)
            assert "4004" in str(agent.last_error)
            n = hub.refused
            await asyncio.sleep(0.15)
            assert hub.refused <= n + 1  # waits backoff_max, not the short delay


async def test_backoff_doubles_with_jitter_and_caps() -> None:
    hub, key = node_hub()
    agent = make_agent(hub, node_key=key, backoff_min=1.0, backoff_max=30.0)
    for n, base in ((1, 1.0), (2, 2.0), (3, 4.0), (5, 16.0)):
        for _ in range(20):
            assert 0.8 * base <= agent._backoff(n) <= 1.2 * base
    assert all(24.0 <= agent._backoff(n) <= 30.0 for n in (6, 9, 20))


@pytest.mark.parametrize("code", [4003, 4010])
async def test_a_close_code_alone_never_stops_the_worker(code: int) -> None:
    """A close frame is not authenticated (4003 comes before any proof; any close travels
    outside the sealed stream): the agent asks the database and keeps retrying."""
    hub, key = node_hub()
    reasons: list[str] = []
    checks: list[float] = []

    async def check_membership() -> None:
        checks.append(asyncio.get_running_loop().time())

    commands = Commands(revoked=reasons.append, check_membership=check_membership)
    async with running_hub(hub):
        if code == 4003:  # refused before any proof
            hub.refuse, hub.refuse_code = "not_member", 4003
        agent = make_agent(hub, node_key=key, commands=commands, backoff_max=0.2)
        async with running(agent) as task:
            if code == 4010:  # after welcome, with no sealed disconnect before it
                s = await hub.wait_session(1)
                await s.ws.close(4010, "revoked")
            await until(lambda: len(checks) >= 1)  # such a close: the database, at once
            await until(lambda: len(hub.hellos) >= 2)  # and it came back (longest delay)
            assert not task.done() and not agent.revoked and reasons == []
            if code == 4003:
                await until(lambda: len(checks) >= 2)  # every time
                hub.refuse = None  # the home knows it again: the channel works
                await hub.wait_session(1)
            else:
                await hub.wait_session(2)


async def test_the_sealed_revocation_stops_the_worker_with_77() -> None:
    from maktaba_pipeline.cluster import exitcodes
    from maktaba_pipeline.cluster.agent.service import _refused

    hub, key = node_hub()
    shutdown = asyncio.Event()
    checks: list[int] = []

    async def check_membership() -> None:
        checks.append(1)

    commands = Commands(revoked=_refused(shutdown), check_membership=check_membership)
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, commands=commands)
        async with running(agent) as task:
            s = await hub.wait_session(1)
            await s.send({"t": "cmd", "id": "x", "name": "disconnect",
                          "args": {"reason": "revoked"}})  # fmt: skip
            assert (await s.next("cmd.result"))["ok"]
            await s.ws.close(4010, "revoked")
            await asyncio.wait_for(task, 3)  # no reconnect
    assert agent.revoked and shutdown.is_set()
    assert exitcodes.exit_code(0) == exitcodes.EX_NOPERM == 77
    assert len(hub.hellos) == 1


async def test_a_gap_in_the_counter_closes_the_channel() -> None:
    hub, key = node_hub()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key)
        async with running(agent):
            s = await hub.wait_session(1)
            s.sealer.seal({"t": "ping"})  # counter 1 is skipped: the next frame is out of order
            await s.send({"t": "ping", "ts": "late"})
            await asyncio.wait_for(s.closed.wait(), 3)
            assert s.ws.close_code == 4011
            s2 = await hub.wait_session(2)  # reconnected, new keys, counters from 0
            await s2.send({"t": "ping", "ts": "fresh"})
            assert (await s2.next("pong"))["ts"] == "fresh"


async def test_logs_buffer_while_disconnected_and_drops_are_reported() -> None:
    hub, key = node_hub()
    shipper = LogShipper("info", capacity=3)
    tracker = JobTracker()
    async with running_hub(hub):
        hub.refuse, hub.refuse_code = "going away", 1001
        agent = make_agent(hub, node_key=key, shipper=shipper)
        tracker.add_sink(agent.job_sink)
        async with running(agent):
            await until(lambda: hub.refused > 0)
            for i in range(5):
                shipper.sink(ev("warning", f"while down {i}"))
            job: Any = type("J", (), {"id": 9, "stage": "probe"})()
            tracker.claimed(job)  # not resent later: the files have it
            hub.refuse = None
            s = await hub.wait_session(1)
            dropped = await s.next("dropped")
            assert dropped["logs"] == 2 and dropped["since"] and dropped["until"]
            logs = await s.next("logs")
            assert [r["event"] for r in logs["records"]] == [f"while down {i}" for i in (2, 3, 4)]
            await asyncio.sleep(0.3)
            assert not [f for f in s.frames if f["t"] == "job"]


async def test_silence_closes_and_reconnects() -> None:
    hub, key = node_hub()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, ping_sec=0.05, silence_sec=0.2)
        async with running(agent):
            s = await hub.wait_session(1)
            assert (await s.next("ping"))["t"] == "ping"
            await asyncio.wait_for(s.closed.wait(), 3)  # the hub never answered
            assert s.ws.close_code == 4008
            await hub.wait_session(2)


async def test_commands() -> None:
    hub, key = node_hub()
    calls: list[Any] = []

    async def drain(hold: bool) -> str:
        calls.append(("drain", hold))
        return "held" if hold else "released"

    async def reload_stages() -> str:
        calls.append("reload")
        return "serving 3 stage(s)"

    shipper = LogShipper("info")
    commands = Commands(drain=drain, reload_stages=reload_stages, revoked=calls.append)
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, shipper=shipper, commands=commands)
        async with running(agent) as task:
            s = await hub.wait_session(1)

            async def cmd(cid: str, name: str, args: dict[str, Any] | None = None) -> Any:
                await s.send({"t": "cmd", "id": cid, "name": name, "args": args or {}})
                return await s.next("cmd.result", lambda f: f["id"] == cid)

            assert await cmd("1", "drain") == {"t": "cmd.result", "id": "1", "ok": True,
                                               "detail": "held"}  # fmt: skip
            assert (await cmd("2", "undrain"))["detail"] == "released"
            assert (await cmd("3", "reload_stages"))["detail"] == "serving 3 stage(s)"
            r = await cmd("4", "log_level", {"level": "warning"})
            assert r["ok"] and r["detail"] == "warning" and shipper.level == "warning"
            assert not (await cmd("4b", "log_level", {"level": "loud"}))["ok"]
            r = await cmd("5", "bogus")
            assert not r["ok"] and "unknown command" in r["detail"]
            assert calls == [("drain", True), ("drain", False), "reload"]
            r = await cmd("6", "disconnect", {"reason": "maintenance"})
            assert r["ok"]
            s2 = await hub.wait_session(2)  # a plain disconnect: it comes back
            await s2.send({"t": "cmd", "id": "7", "name": "disconnect",
                           "args": {"reason": "revoked"}})  # fmt: skip
            assert (await s2.next("cmd.result"))["ok"]
            await asyncio.wait_for(task, 3)  # revoked: stops, no reconnect
            assert agent.revoked and len(hub.sessions) == 2
            assert "revoked" in calls[-1]


async def test_hub_routes_an_llm_request_here() -> None:
    hub, key = node_hub()
    records: list[dict[str, Any]] = []
    with fake_runtime(FakeRuntimeState(delay=0.05)) as (rt_url, _):
        local = LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": rt_url}), record=records.append)
        async with running_hub(hub):
            agent = make_agent(hub, node_key=key, llm=local)
            async with running(agent):
                s = await hub.wait_session(1)
                models = await s.next("llm.models", lambda f: bool(f["runtimes"]))
                rt = models["runtimes"][0]
                assert rt["provider"] == "ollama" and rt["max_parallel"] == 1
                names = {m["name"]: m for m in rt["models"]}
                assert names["llama3:latest"]["loaded"] is True
                req = {"t": "llm.request", "id": "r1", "model": "llama3", "op": "chat",
                       "body": {"messages": [{"role": "user", "content": "hi"}]},
                       "stream": True, "from": "192.168.50.40"}  # fmt: skip
                await s.send(req)
                await asyncio.sleep(0.02)
                await s.send({**req, "id": "r2"})  # max_parallel 1: refused at once
                busy = await s.next("llm.done", lambda f: f["id"] == "r2")
                assert busy["error"]["code"] == "no_capacity"
                done = await s.next("llm.done", lambda f: f["id"] == "r1")
                chunks = [f["data"] for f in s.frames
                          if f["t"] == "llm.chunk" and f["id"] == "r1"]  # fmt: skip
                assert chunks[0] == {"choices": [{"index": 0, "delta": {"content": "Hel"}}]}
                assert len(chunks) == 4 and "error" not in done
                assert done["usage"]["prompt_tokens"] == 5
                assert done["usage"]["completion_tokens"] == 3
                await s.send({**req, "id": "r3", "model": "missing"})
                missing = await s.next("llm.done", lambda f: f["id"] == "r3")
                assert missing["error"]["code"] == "no_model"
                # the requester gave up: the hub cancels, the runner still answers
                await s.send({**req, "id": "r4"})
                await s.next("llm.chunk", lambda f: f["id"] == "r4")
                await s.send({"t": "llm.cancel", "id": "r4"})
                cancelled = await s.next("llm.done", lambda f: f["id"] == "r4")
                assert cancelled["error"]["code"] == "cancelled"
    ran = [r for r in records if r["id"] == "r1"]
    assert ran and ran[0]["from"] == "192.168.50.40" and ran[0]["error"] is None
    assert [r["error"] for r in records if r["id"] == "r4"] == ["cancelled"]


async def test_a_runtime_that_dies_mid_stream_is_answered() -> None:
    """Whatever fails (here the runtime closes inside a chunk: IncompleteReadError), the
    hub gets llm.done with runtime_error, and the runtime's slot is free again."""
    hub, key = node_hub()
    with fake_runtime(FakeRuntimeState(die_after=1)) as (rt_url, _):
        local = LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": rt_url}))
        async with running_hub(hub):
            agent = make_agent(hub, node_key=key, llm=local)
            async with running(agent):
                s = await hub.wait_session(1)
                await s.next("llm.models", lambda f: bool(f["runtimes"]))
                req = {"t": "llm.request", "id": "d1", "model": "llama3", "op": "chat",
                       "body": {"messages": []}, "stream": True}  # fmt: skip
                await s.send(req)
                done = await s.next("llm.done", lambda f: f["id"] == "d1")
                assert done["error"]["code"] == "runtime_error"
                assert "IncompleteReadError" in done["error"]["detail"]
                assert local.runtimes.stats()["in_flight"] == 0
                await s.send({**req, "id": "d2"})  # the slot is free: not no_capacity
                again = await s.next("llm.done", lambda f: f["id"] == "d2")
                assert again["error"]["code"] == "runtime_error"


async def test_the_door_asks_the_cluster() -> None:
    hub, key = node_hub()

    def chunk(text: str) -> dict[str, Any]:
        return {"choices": [{"index": 0, "delta": {"content": text}}]}

    async def answer(s: HubSession, f: dict[str, Any]) -> None:
        if f.get("t") != "llm.request":
            return
        rid, model = f["id"], f["model"]
        if model == "big-model":
            for piece in ("Hi", " there"):
                await s.send({"t": "llm.chunk", "id": rid, "data": chunk(piece)})
            usage = {"prompt_tokens": 4, "completion_tokens": 2, "ms": 9}
            await s.send({"t": "llm.done", "id": rid, "usage": usage})
        elif model == "embedder":
            data = {"data": [{"index": 0, "embedding": [1.0, 2.0]}]}
            await s.send({"t": "llm.chunk", "id": rid, "data": data})
            await s.send({"t": "llm.done", "id": rid, "usage": {}})
        elif model == "flaky":
            await s.send({"t": "llm.chunk", "id": rid, "data": chunk("par")})
            await s.send({"t": "llm.done", "id": rid, "usage": {},
                          "error": {"code": "machine_gone", "detail": "b left"}})  # fmt: skip
        elif model == "slow":
            for i in range(50):
                await s.send({"t": "llm.chunk", "id": rid, "data": chunk(str(i))})
                await asyncio.sleep(0.02)
        elif model == "hang":
            await s.ws.close()
        else:
            await s.send({"t": "llm.done", "id": rid, "usage": {},
                          "error": {"code": "no_model", "detail": ""}})  # fmt: skip

    hub.on_frame = answer
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key)
        door = ClusterLLM(LocalLLM(LLMRuntimes(env=NO_RUNTIMES)), lambda: agent)
        with pytest.raises(ClusterUnavailable):
            await door.chat("big-model", [])
        async with running(agent):
            s = await hub.wait_session(1)
            await until(lambda: agent.connected)
            result = await door.chat("big-model", [{"role": "user", "content": "q"}], top_p=0.9)
            assert result.text == "Hi there" and result.via == "cluster"
            assert result.usage == {"prompt_tokens": 4, "completion_tokens": 2, "ms": 9}
            sent = await s.next("llm.request")
            assert sent["op"] == "chat" and sent["stream"] is False
            assert sent["body"] == {"top_p": 0.9, "messages": [{"role": "user", "content": "q"}]}
            assert [p async for p in door.chat("big-model", [], stream=True)] == ["Hi", " there"]
            assert (await door.embed("embedder", "x")).embeddings == [[1.0, 2.0]]
            with pytest.raises(NoModel):
                await door.generate("nobody-has-it", "p")
            with pytest.raises(MachineGone, match="b left"):
                await door.chat("flaky", [])
            # giving up early sends llm.cancel
            call = door.chat("slow", [], stream=True)
            it = call.__aiter__()
            assert await it.__anext__() == "0"
            await it.aclose()  # type: ignore[attr-defined]
            cancel = await s.next("llm.cancel")
            slow = await s.next("llm.request", lambda f: f["model"] == "slow")
            assert cancel["id"] == slow["id"]
            with pytest.raises(ClusterUnavailable):
                await door.chat("hang", [])


async def test_files_continue_while_the_home_is_down_and_a_final_frame(tmp_path: Path) -> None:
    hub, key = node_hub()
    files = ClusterFiles(tmp_path, "10.1.1.1", "w", fallback_dir=tmp_path / "l", flush_sec=0.05)
    files.start()
    collector = MetricsCollector(
        files_status=files.status, health=lambda final: {"stt.audio_lost:x": 3.0 if final else 1.0}
    )
    async with running_hub(hub):
        port = hub.port
    hub.port = port  # nothing listens there any more
    shutdown = asyncio.Event()
    agent = make_agent(hub, node_key=key, files=files, collector=collector, metrics_every=0.05,
                       metrics_file_sec=0.1)  # fmt: skip
    began = time.monotonic()
    async with running(agent, shutdown):
        # a refused connection is reported at once on Linux and macOS, after ~2 s on
        # Windows (it retries the SYN): wait for the first failure, and for a few
        # metrics lines, not a fixed time
        await asyncio.sleep(0.35)
        for _ in range(200):
            if agent.last_error:
                break
            await asyncio.sleep(0.05)
        assert not agent.connected and agent.last_error
        shutdown.set()  # the worker stops: one last frame, final values
        await asyncio.sleep(0.2)
    ran = time.monotonic() - began
    files.stop()
    path = next((tmp_path / "10.1.1.1" / "metrics").glob("*/w.jsonl"))
    lines = [json.loads(x) for x in path.read_text().splitlines()]
    assert len(lines) >= 3 and "system" in lines[0]
    # a line every 0.1 s (plus the first and the final), not one per 0.05 s frame
    assert len(lines) <= ran / 0.1 + 2
    assert lines[-1]["final"] is True and lines[-1]["health"] == {"stt.audio_lost:x": 3.0}
    assert all(x["health"] == {"stt.audio_lost:x": 1.0} for x in lines[:-1])


async def test_the_metrics_file_gets_a_line_every_10_s_the_channel_every_frame(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("MAKTABA_CLUSTER_METRICS_FILE_SEC", raising=False)
    hub, key = node_hub()
    files = ClusterFiles(tmp_path, "10.1.1.2", "w", fallback_dir=tmp_path / "l")
    collector = MetricsCollector(files_status=files.status)
    shutdown = asyncio.Event()
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, files=files, collector=collector, metrics_every=0.05)
        assert agent.metrics_file_sec == 10.0
        async with running(agent, shutdown):
            s = await hub.wait_session(1)
            await until(lambda: len([f for f in s.frames if f["t"] == "metrics"]) >= 5)
            shutdown.set()
            await s.next("metrics", lambda f: bool(f.get("final")))
    files.flush()
    path = next((tmp_path / "10.1.1.2" / "metrics").glob("*/w.jsonl"))
    lines = [json.loads(x) for x in path.read_text().splitlines()]
    assert len(lines) == 2 and lines[-1]["final"] is True  # the first frame, then the final
    monkeypatch.setenv("MAKTABA_CLUSTER_METRICS_FILE_SEC", "30")
    assert make_agent(hub, node_key=key).metrics_file_sec == 30.0


async def test_the_welcome_names_the_folder_and_the_share(tmp_path: Path) -> None:
    from maktaba_pipeline.cluster.agent.service import _settings_hook

    hub, key = node_hub()
    (tmp_path / "nas").mkdir()  # the share root exists: only folders below it are made
    hub.settings = {"ip": "192.168.50.20", "share_root": str(tmp_path / "nas"),
                    "retention_days": 3, "file_mb": 7}  # fmt: skip
    files = ClusterFiles(None, "10.0.0.5", "w", fallback_dir=tmp_path / "local")
    assert not files.on_share
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, files=files, on_settings=_settings_hook(files))
        async with running(agent):
            await hub.wait_session(1)
            await until(lambda: files.ip == "192.168.50.20" and files.on_share)
    assert files.status()["dir"] == str(tmp_path / "nas" / "maktaba" / "cluster" / "192.168.50.20")
    assert files.retention_days == 3 and files.file_cap == 7 * 1024 * 1024
    files.stop()


async def test_agent_via_local_link() -> None:
    """A full node's process dials its node API's loopback route to the home's channel
    over the machine's cluster link first (story 35.16), its v2 sealing unchanged; with
    the link not up (nothing answers there) it reaches the home straight."""
    assert agent_urls("http://h:8080", env={}) == ["ws://h:8080/api/cluster/agent"]
    local = "ws://127.0.0.1:1/api/cluster/link/local/agent"
    assert agent_urls("http://h:8080", env={"MAKTABA_CLUSTER_AGENT_URL": local}) == [
        local,
        "ws://h:8080/api/cluster/agent",
    ]
    hub, key = node_hub()
    async with running_hub(hub):
        # the "link" route is the hub itself: the session goes through it, sealed as ever
        import os

        os.environ["MAKTABA_CLUSTER_AGENT_URL"] = f"ws://127.0.0.1:{hub.port}/api/cluster/agent"
        try:
            agent = make_agent(hub, node_key=key)
            async with running(agent):
                await until(lambda: agent.connects >= 1)
        finally:
            os.environ["MAKTABA_CLUSTER_AGENT_URL"] = local  # nothing listens: straight to the home
        try:
            agent = make_agent(hub, node_key=key)
            async with running(agent):
                await until(lambda: agent.connects >= 1)
        finally:
            del os.environ["MAKTABA_CLUSTER_AGENT_URL"]


async def test_a_worker_without_the_share_keeps_its_files_here(tmp_path: Path) -> None:
    """The index worker (``--no-share``): the hub's share root does not move its files
    to the share; the rest of the welcome still applies."""
    from maktaba_pipeline.cluster.agent.service import _settings_hook

    hub, key = node_hub()
    (tmp_path / "nas").mkdir()
    hub.settings = {"ip": "192.168.50.20", "share_root": str(tmp_path / "nas"),
                    "retention_days": 3, "file_mb": 7}  # fmt: skip
    files = ClusterFiles(None, "10.0.0.5", "w", fallback_dir=tmp_path / "local")
    hook = _settings_hook(files, on_share=False)
    async with running_hub(hub):
        agent = make_agent(hub, node_key=key, files=files, on_settings=hook)
        async with running(agent):
            await hub.wait_session(1)
            await until(lambda: files.ip == "192.168.50.20" and files.retention_days == 3)
    assert not files.on_share and not (tmp_path / "nas" / "maktaba").exists()
    assert files.status()["dir"] == str(tmp_path / "local" / "192.168.50.20")
    files.stop()
