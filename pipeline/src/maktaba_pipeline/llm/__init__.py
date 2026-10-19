"""LLMs across the cluster: the pipeline's door (design-agent.md section 6).

::

    from maktaba_pipeline.llm import cluster_llm

    llm = cluster_llm()
    result = await llm.chat("llama3.1:8b", [{"role": "user", "content": "Hi"}])
    print(result.text, result.usage)

    async for piece in llm.chat("llama3.1:8b", messages, stream=True, temperature=0.2):
        ...

    vectors = (await llm.embed("nomic-embed-text", ["a", "b"])).embeddings

``opts`` are OpenAI request fields (``temperature``, ``max_tokens``, ``stop``, …): the
body travels in OpenAI's wire format (section 8.4). A model a runtime on this machine has
runs here directly (waiting its turn under ``MAKTABA_LLM_MAX_PARALLEL``); any other goes
to the home's router over this process's agent channel (``llm.request``), which runs it
on a machine that has it. Errors: :class:`NoModel`, :class:`NoCapacity`,
:class:`MachineGone`, :class:`ClusterUnavailable` (``no_channel``: no channel to the
home), :class:`RuntimeFailed`, :class:`Cancelled`, :class:`TooLarge` — all
:class:`LLMError`.
"""

from __future__ import annotations

import contextlib
from collections.abc import AsyncIterator, Callable, Generator, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, Protocol

from .errors import (
    Cancelled,
    ClusterUnavailable,
    LLMError,
    MachineGone,
    NoCapacity,
    NoModel,
    RuntimeFailed,
    TooLarge,
)
from .executor import Execution, embeddings_of, text_of
from .service import LocalLLM

__all__ = [
    "Cancelled",
    "ClusterLLM",
    "ClusterUnavailable",
    "EmbedResult",
    "LLMCall",
    "LLMError",
    "LLMResult",
    "MachineGone",
    "NoCapacity",
    "NoModel",
    "RemoteTransport",
    "RuntimeFailed",
    "TooLarge",
    "attach",
    "cluster_llm",
    "detach",
]


class RemoteTransport(Protocol):
    """What the agent offers the door: ``llm.request`` over its channel."""

    def llm_available(self) -> bool: ...

    def llm_request(
        self,
        op: str,
        model: str,
        body: Mapping[str, Any],
        *,
        stream: bool,
        run: Execution,
    ) -> AsyncIterator[dict[str, Any]]: ...


@dataclass
class LLMResult:
    text: str
    model: str
    #: ``local`` (a runtime on this machine) or ``cluster`` (routed by the home)
    via: str
    usage: dict[str, int] = field(default_factory=dict)


@dataclass
class EmbedResult:
    embeddings: list[list[float]]
    model: str
    via: str
    usage: dict[str, int] = field(default_factory=dict)


class LLMCall:
    """A chat or generate call: ``await`` it for the whole answer, or ``async for`` over
    it for the pieces as they come (with ``stream=True`` the runtime streams them)."""

    def __init__(self, door: ClusterLLM, op: str, model: str, body: dict[str, Any], stream: bool):
        self._door = door
        self.op = op
        self.model = model
        self.body = body
        self.stream = stream
        self.run = Execution()
        self.via = ""

    async def _data(self) -> AsyncIterator[dict[str, Any]]:
        async for data in self._door._chunks(self):
            yield data

    def __aiter__(self) -> AsyncIterator[str]:
        return self._pieces()

    async def _pieces(self) -> AsyncIterator[str]:
        async for data in self._data():
            piece = text_of(self.op, data)
            if piece:
                yield piece

    async def _collect(self) -> LLMResult:
        parts = [p async for p in self._pieces()]
        return LLMResult("".join(parts), self.model, self.via, dict(self.run.usage))

    def __await__(self) -> Generator[Any, None, LLMResult]:
        return self._collect().__await__()

    @property
    def usage(self) -> dict[str, int]:
        return dict(self.run.usage)


class ClusterLLM:
    def __init__(
        self,
        local: LocalLLM,
        transport: Callable[[], RemoteTransport | None] = lambda: None,
        *,
        refresh_local: bool = True,
    ) -> None:
        self.local = local
        self.transport = transport
        self.refresh_local = refresh_local

    def chat(
        self, model: str, messages: Sequence[Mapping[str, Any]], stream: bool = False, **opts: Any
    ) -> LLMCall:
        return LLMCall(self, "chat", model, {**opts, "messages": list(messages)}, stream)

    def generate(self, model: str, prompt: str, stream: bool = False, **opts: Any) -> LLMCall:
        return LLMCall(self, "generate", model, {**opts, "prompt": prompt}, stream)

    async def embed(self, model: str, input: str | Sequence[str]) -> EmbedResult:  # noqa: A002
        call = LLMCall(
            self, "embed", model, {"input": input if isinstance(input, str) else list(input)}, False
        )
        vectors: list[list[float]] = []
        async for data in call._data():
            vectors.extend(embeddings_of(data))
        return EmbedResult(vectors, model, call.via, dict(call.run.usage))

    async def _chunks(self, call: LLMCall) -> AsyncIterator[dict[str, Any]]:
        if self.refresh_local:
            with contextlib.suppress(Exception):  # failing detection: nothing local
                await self.local.runtimes.ensure_fresh()
        if self.local.has(call.model):
            call.via = "local"
            async for data in self.local.run(
                call.op, call.model, call.body, stream=call.stream, wait=True, run=call.run
            ):
                yield data
            return
        t = self.transport()
        if t is None or not t.llm_available():
            raise ClusterUnavailable(
                f"{call.model} is not on this machine and there is no channel to the home"
            )
        call.via = "cluster"
        async for data in t.llm_request(
            call.op, call.model, call.body, stream=call.stream, run=call.run
        ):
            yield data


_LOCAL: LocalLLM | None = None
_TRANSPORT: RemoteTransport | None = None


def attach(local: LocalLLM, transport: RemoteTransport | None) -> None:
    """The agent shares its runtimes (it keeps them fresh) and its channel."""
    global _LOCAL, _TRANSPORT
    _LOCAL, _TRANSPORT = local, transport


def detach() -> None:
    global _TRANSPORT
    _TRANSPORT = None


def cluster_llm() -> ClusterLLM:
    """The door for pipeline code in this process."""
    global _LOCAL
    if _LOCAL is None:
        _LOCAL = LocalLLM()
    # the agent re-detects every 30 s; without it the door checks at most that often
    return ClusterLLM(_LOCAL, lambda: _TRANSPORT, refresh_local=True)
