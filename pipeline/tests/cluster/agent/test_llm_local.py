"""LLM runtimes on this machine: detection, the executor (OpenAI wire format), capacity,
the usage records, and the local door."""

from __future__ import annotations

import asyncio
from typing import Any

import pytest

from maktaba_pipeline.llm import ClusterLLM, ClusterUnavailable, NoCapacity, NoModel
from maktaba_pipeline.llm.errors import RuntimeFailed
from maktaba_pipeline.llm.executor import Execution, embeddings_of, execute, text_of
from maktaba_pipeline.llm.runtimes import LLMRuntimes, Runtime, detect
from maktaba_pipeline.llm.service import LocalLLM
from tests.cluster.agent.fakes import FakeRuntimeState, fake_runtime

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]


async def test_detects_ollama_and_openai_runtimes() -> None:
    with fake_runtime() as (url, _):
        env = {
            "MAKTABA_OLLAMA_URL": url,
            "MAKTABA_LLM_ENDPOINTS": f"{url}/v1, http://127.0.0.1:9",
            "MAKTABA_LLM_MAX_PARALLEL": "2",
        }
        rts = await detect(env, timeout=1.0)
    assert [(r.provider, r.base_url, r.max_parallel) for r in rts] == [
        ("ollama", url, 2),
        ("openai", url, 2),
    ]
    llama = rts[0].model("llama3")
    assert llama is not None and llama.loaded and llama.family == "llama"
    assert llama.quant == "Q4_K_M" and llama.size == 100
    assert rts[0].as_json()["models"][0]["name"] == "llama3:latest"


async def test_registry_reports_changes() -> None:
    with fake_runtime() as (url, st):
        reg = LLMRuntimes(env={"MAKTABA_OLLAMA_URL": url})
        assert await reg.refresh()
        assert not await reg.refresh()
        st.loaded = []
        assert await reg.refresh()
        assert reg.frame()["t"] == "llm.models"
        assert reg.find("llama3") is not None and reg.find("nope") is None


async def _collect(
    rt: Runtime, op: str, body: dict[str, Any], stream: bool, model: str = "llama3:latest"
) -> tuple[list[dict[str, Any]], Execution]:
    run = Execution()
    out = [d async for d in execute(rt, op, model, body, stream=stream, run=run)]
    return out, run


@pytest.mark.parametrize("chunked", [True, False])
async def test_openai_passthrough(chunked: bool) -> None:
    with fake_runtime(FakeRuntimeState(chunked=chunked)) as (url, st):
        rt = Runtime("ollama", url)  # Ollama answers on /v1 too
        body = {"messages": [{"role": "user", "content": "hi"}], "temperature": 0.1}
        out, run = await _collect(rt, "chat", body, True)
        assert "".join(text_of("chat", c) for c in out) == "Hello!"
        assert out[0] == {"choices": [{"index": 0, "delta": {"content": "Hel"}}]}  # untouched
        assert run.usage["prompt_tokens"] == 5 and run.usage["completion_tokens"] == 3
        path, sent = st.requests[-1]
        assert path == "/v1/chat/completions" and sent["model"] == "llama3:latest"
        assert sent["temperature"] == 0.1 and sent["stream_options"] == {"include_usage": True}
        out, run = await _collect(rt, "chat", body, False)
        assert len(out) == 1 and text_of("chat", out[0]) == "Hello!"
        out, _ = await _collect(rt, "generate", {"prompt": "p"}, True)
        assert "".join(text_of("generate", c) for c in out) == "Hello!"
        assert st.requests[-1][0] == "/v1/completions"
        out, run = await _collect(
            rt, "embed", {"input": ["a", "b"]}, True, "nomic-embed-text:latest"
        )
        assert embeddings_of(out[0]) == [[0.0, 0.5], [1.0, 0.5]]  # back in input order
        assert run.usage["prompt_tokens"] == 2
        assert "stream" not in st.requests[-1][1]
        with pytest.raises(RuntimeFailed, match="not found"):
            await _collect(rt, "chat", body, True, "missing")


async def test_a_runtime_without_usage_counts_pieces() -> None:
    with fake_runtime(FakeRuntimeState(usage=False)) as (url, _):
        _, run = await _collect(Runtime("openai", url), "chat", {"messages": []}, True)
        assert run.usage["completion_tokens"] == 3


async def test_capacity_and_records() -> None:
    with fake_runtime(FakeRuntimeState(delay=0.05)) as (url, _):
        records: list[dict[str, Any]] = []
        local = LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": url}), record=records.append)
        await local.runtimes.refresh()
        body = {"messages": [{"role": "user", "content": "secret prompt"}]}

        async def one(wait: bool) -> list[Any]:
            run = Execution()
            return [
                d
                async for d in local.run(
                    "chat", "llama3", body, stream=True, wait=wait, run=run, req_id="r1"
                )
            ]

        first = asyncio.create_task(one(True))
        await asyncio.sleep(0.02)
        assert local.runtimes.stats()["in_flight"] == 1
        with pytest.raises(NoCapacity):
            await one(False)  # the hub's request: refused at once
        waiting = asyncio.create_task(one(True))  # this machine's code: waits its turn
        assert len(await first) == 4  # three pieces and the usage chunk
        assert len(await waiting) == 4
        with pytest.raises(NoModel):
            run = Execution()
            async for _ in local.run("chat", "nope", body, stream=True, wait=True, run=run):
                pass
    assert local.runtimes.stats()["in_flight"] == 0
    assert local.runtimes.stats()["tokens_per_s"] > 0
    ok = [r for r in records if r["error"] is None]
    assert len(ok) == 2 and ok[0]["model"] == "llama3" and ok[0]["completion_tokens"] == 3
    assert {"ts", "id", "model", "op", "from", "prompt_tokens", "completion_tokens", "ms",
            "error"} == set(ok[0])  # fmt: skip
    assert "secret prompt" not in repr(records)  # usage only, never the text


async def test_cancelling_frees_the_slot() -> None:
    with fake_runtime(FakeRuntimeState(delay=0.5)) as (url, _):
        local = LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": url}))
        await local.runtimes.refresh()

        async def go() -> None:
            run = Execution()
            async for _ in local.run("chat", "llama3", {"messages": []}, stream=True,
                                     wait=True, run=run):  # fmt: skip
                pass

        t = asyncio.create_task(go())
        await asyncio.sleep(0.1)
        t.cancel()
        with pytest.raises(asyncio.CancelledError):
            await t
        assert local.runtimes.stats()["in_flight"] == 0


async def test_door_uses_the_local_runtime() -> None:
    with fake_runtime() as (url, _):
        door = ClusterLLM(LocalLLM(LLMRuntimes(env={"MAKTABA_OLLAMA_URL": url})))
        result = await door.chat("llama3", [{"role": "user", "content": "hi"}])
        assert result.text == "Hello!" and result.via == "local"
        assert result.usage["completion_tokens"] == 3
        pieces = [p async for p in door.chat("llama3", [], stream=True)]
        assert pieces == ["Hel", "lo", "!"]
        gen = await door.generate("llama3", "p")
        assert gen.text == "Hello!"
        emb = await door.embed("nomic-embed-text", ["a"])
        assert emb.embeddings == [[0.0, 0.5]] and emb.via == "local"
        with pytest.raises(ClusterUnavailable) as err:
            await door.chat("not-here", [])
        assert err.value.code == "no_channel"
