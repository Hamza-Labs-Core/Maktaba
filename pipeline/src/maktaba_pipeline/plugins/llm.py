"""One chat question to a language model, the same way in-process and in a plugin's runner
(story 54.31 §2).

In-process a stage asks the model server at its ``rules.<section>.url`` itself. In a
first-party plugin's runner it has no socket beyond the plugin API's, so it asks
``compute.generate`` with the same messages, format and options; the API forwards them to
the stage's ``rules.<model_rules>`` server and model unchanged, so recorded answers match.

Either way a failure is an :class:`OSError` (``urllib.error.URLError``), the error the
stages already map to their model failure.
"""

from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from typing import Any

__all__ = ["chat", "chat_body", "in_runner"]


def chat_body(model: str, text: str, *, num_ctx: int = 4096) -> dict[str, Any]:
    """The request every classify question sends: JSON mode, temperature 0."""
    return {
        "model": model,
        "stream": False,
        "format": "json",
        "options": {"temperature": 0, "num_ctx": num_ctx},
        "messages": [{"role": "user", "content": text}],
    }


def in_runner(db: Any, ctx: Any) -> bool:
    """A job in a plugin's runner: its context says its model questions go to the plugin
    API (``model_via``) and its database is the API's (a transport)."""
    return getattr(ctx, "model_via", None) == "api" and getattr(db, "transport", None) is not None


def _direct(url: str, body: dict[str, Any], timeout_s: float) -> str:
    req = urllib.request.Request(
        url.rstrip("/") + "/api/chat",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout_s) as r:  # noqa: S310 - the owner's model server
        return str(json.loads(r.read().decode("utf-8"))["message"]["content"])


async def chat(db: Any, ctx: Any, url: str, body: dict[str, Any], timeout_s: float) -> str:
    """The model's answer to ``body`` (an Ollama chat request)."""
    if not in_runner(db, ctx):
        return await asyncio.to_thread(_direct, url, body, timeout_s)
    ask = {k: body[k] for k in ("messages", "format", "options") if k in body}
    try:
        got = await asyncio.wait_for(
            db.transport.call("compute/generate", ask), timeout=timeout_s + 30.0
        )
    except TimeoutError as e:
        raise urllib.error.URLError("the model did not answer in time") from e
    except OSError:
        raise
    except Exception as e:  # noqa: BLE001 - the API's refusal: the model failed or is off
        raise urllib.error.URLError(str(e)) from e
    return str(got["text"])
