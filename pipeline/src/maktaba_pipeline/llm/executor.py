"""Running one LLM request on a local runtime (design-agent.md section 8.4).

What travels is OpenAI's wire format, untouched: ``body`` is an OpenAI request body
(``chat`` → ``/v1/chat/completions``, ``generate`` → ``/v1/completions``, ``embed`` →
``/v1/embeddings``), sent to the runtime's OpenAI-compatible endpoint (Ollama serves one
under ``/v1``) with the runtime's own model name. Streaming, each chunk is one OpenAI
stream chunk (the JSON of one ``data:`` line); otherwise one chunk is the whole response.
The usage — ``{prompt_tokens, completion_tokens, ms}`` — is read from the response (a
streamed one is asked to include it).

:func:`text_of` and :func:`embeddings_of` read those chunks for the door.
"""

from __future__ import annotations

import contextlib
import json
import time
from collections.abc import AsyncIterator, Mapping
from dataclasses import dataclass, field
from typing import Any

from . import http
from .errors import RuntimeFailed
from .runtimes import Runtime

__all__ = ["OPS", "PATHS", "Execution", "embeddings_of", "execute", "text_of"]

OPS = ("chat", "generate", "embed")
PATHS = {"chat": "/v1/chat/completions", "generate": "/v1/completions", "embed": "/v1/embeddings"}


@dataclass
class Execution:
    usage: dict[str, int] = field(
        default_factory=lambda: {"prompt_tokens": 0, "completion_tokens": 0, "ms": 0}
    )


def openai_body(op: str, model: str, body: Mapping[str, Any], stream: bool) -> dict[str, Any]:
    out = {k: v for k, v in body.items() if k not in ("model", "stream")}
    out["model"] = model
    if op != "embed":
        out["stream"] = stream
        if stream:
            out["stream_options"] = {**(out.get("stream_options") or {}), "include_usage": True}
    return out


def text_of(op: str, data: Mapping[str, Any]) -> str:
    """The text in one chunk (a stream chunk or a whole response)."""
    parts: list[str] = []
    for choice in data.get("choices") or []:
        if not isinstance(choice, Mapping):
            continue
        if op == "chat":
            piece = (choice.get("delta") or choice.get("message") or {}).get("content")
        else:
            piece = choice.get("text")
        if isinstance(piece, str):
            parts.append(piece)
    return "".join(parts)


def embeddings_of(data: Mapping[str, Any]) -> list[list[float]]:
    rows = [d for d in data.get("data") or [] if isinstance(d, Mapping)]
    rows.sort(key=lambda d: int(d.get("index", 0)))
    return [list(d.get("embedding") or []) for d in rows]


def _usage(run: Execution, usage: Any) -> None:
    if isinstance(usage, Mapping):
        run.usage["prompt_tokens"] = int(usage.get("prompt_tokens") or 0)
        run.usage["completion_tokens"] = int(usage.get("completion_tokens") or 0)


async def _post(url: str, body: Any) -> http.Response:
    resp = await http.open_request("POST", url, body=body)
    if resp.status >= 400:
        try:
            text = (await resp.read()).decode("utf-8", "replace")
        finally:
            await resp.aclose()
        message: Any = text
        with contextlib.suppress(ValueError, AttributeError):
            err = json.loads(text).get("error")
            message = err.get("message") if isinstance(err, Mapping) else (err or text)
        host = url.split("/")[2] if "//" in url else url
        raise RuntimeFailed(f"{host} {resp.status}: {str(message)[:500]}")
    return resp


async def execute(
    rt: Runtime,
    op: str,
    model: str,
    body: Mapping[str, Any],
    *,
    stream: bool,
    run: Execution,
) -> AsyncIterator[dict[str, Any]]:
    """Yield the answer's chunks (OpenAI JSON); ``run.usage`` is filled by the end."""
    if op not in OPS:
        raise ValueError(f"unknown op {op!r}")
    t0 = time.monotonic()
    streaming = stream and op != "embed"
    resp = await _post(rt.base_url + PATHS[op], openai_body(op, model, body, streaming))
    pieces = 0
    try:
        if not streaming:
            data = await resp.json()
            if not isinstance(data, dict):
                raise RuntimeFailed("the runtime's answer is not a JSON object")
            _usage(run, data.get("usage"))
            yield data
            return
        async for line in resp.lines():
            if not line.startswith(b"data:"):
                continue
            payload = line[5:].strip()
            if payload == b"[DONE]":
                break
            try:
                chunk = json.loads(payload)
            except ValueError as exc:
                raise RuntimeFailed(f"a stream chunk is not JSON: {payload[:100]!r}") from exc
            if not isinstance(chunk, dict):
                continue
            if chunk.get("error"):
                raise RuntimeFailed(str(chunk["error"])[:500])
            _usage(run, chunk.get("usage"))
            if text_of(op, chunk):
                pieces += 1
            yield chunk
        if not run.usage["completion_tokens"]:
            run.usage["completion_tokens"] = pieces  # a runtime that reports no usage
    finally:
        run.usage["ms"] = int((time.monotonic() - t0) * 1000)
        await resp.aclose()
