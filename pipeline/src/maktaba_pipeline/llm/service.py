"""This process's LLM work on its own runtimes: capacity, load and the ``llm`` file.

Both doors use it: requests the hub routes here (``llm.request`` S→C) and pipeline code
on this machine (:func:`maktaba_pipeline.llm.cluster_llm`) when a local runtime has the
model. Every request it ran is recorded — ``{ts, id, model, op, from, prompt_tokens,
completion_tokens, ms, error}``, never the prompt or the answer.
"""

from __future__ import annotations

import contextlib
import uuid
from collections.abc import AsyncIterator, Callable, Mapping
from datetime import UTC, datetime
from typing import Any

from .errors import LLMError, NoCapacity, NoModel
from .executor import Execution, execute
from .runtimes import LLMRuntimes

__all__ = ["LocalLLM"]

Record = Callable[[dict[str, Any]], None]


def _now() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


class LocalLLM:
    def __init__(self, runtimes: LLMRuntimes | None = None, record: Record | None = None) -> None:
        self.runtimes = runtimes or LLMRuntimes()
        self.record = record

    def has(self, model: str) -> bool:
        return self.runtimes.find(model) is not None

    async def run(
        self,
        op: str,
        model: str,
        body: Mapping[str, Any],
        *,
        stream: bool,
        wait: bool,
        run: Execution,
        req_id: str | None = None,
        origin: str = "local",
    ) -> AsyncIterator[dict[str, Any]]:
        """Yield the answer's chunks. ``wait=False`` fails at once with
        :class:`NoCapacity` when the runtime is at ``max_parallel`` (the hub then routes
        elsewhere); pipeline code on this machine waits its turn."""
        rt = self.runtimes.find(model)
        if rt is None:
            raise NoModel(f"no runtime on this machine has {model}")
        info = rt.model(model)
        local_name = info.name if info is not None else model  # "llama3" -> "llama3:latest"
        if wait:
            await self.runtimes.acquire(rt)
        elif not self.runtimes.try_acquire(rt):
            raise NoCapacity(f"{rt.base_url} is running {rt.max_parallel} request(s)")
        error: str | None = None
        try:
            async for data in execute(rt, op, local_name, body, stream=stream, run=run):
                yield data
        except BaseException as exc:
            error = "cancelled" if not isinstance(exc, Exception) else _short(exc)
            raise
        finally:
            self.runtimes.release(rt)
            self.runtimes.add_tokens(run.usage.get("completion_tokens", 0))
            if self.record is not None:
                with contextlib.suppress(Exception):  # never fails a request
                    self.record(
                        {
                            "ts": _now(),
                            "id": req_id or uuid.uuid4().hex,
                            "model": model,
                            "op": op,
                            "from": origin,
                            "prompt_tokens": run.usage.get("prompt_tokens", 0),
                            "completion_tokens": run.usage.get("completion_tokens", 0),
                            "ms": run.usage.get("ms", 0),
                            "error": error,
                        }
                    )


def _short(exc: BaseException) -> str:
    if isinstance(exc, LLMError):
        return f"{exc.code}: {exc}"[:300]
    return f"{type(exc).__name__}: {exc}"[:300]
