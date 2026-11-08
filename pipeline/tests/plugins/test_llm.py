"""maktaba_pipeline.plugins.llm (story 54.31 §2): in-process a question goes to the model
server itself; in a runner it goes to compute.generate with the same messages, format and
options; every failure is an OSError, the stages' model failure."""

from __future__ import annotations

import asyncio
import json
import threading
import urllib.error
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any

import pytest

from maktaba_pipeline.plugins import llm


class _Model(BaseHTTPRequestHandler):
    asked: list[tuple[str, dict[str, Any]]] = []

    def do_POST(self) -> None:  # noqa: N802 - http.server's name
        n = int(self.headers["Content-Length"])
        _Model.asked.append((self.path, json.loads(self.rfile.read(n))))
        out = json.dumps({"message": {"role": "assistant", "content": '{"ok": true}'}}).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *_: Any) -> None:
        pass


class _Transport:
    def __init__(self, answer: Any) -> None:
        self.answer = answer
        self.calls: list[tuple[str, dict[str, Any]]] = []

    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
        self.calls.append((fn, body))
        if isinstance(self.answer, BaseException):
            raise self.answer
        return dict(self.answer)


class _DB:
    def __init__(self, transport: Any) -> None:
        self.transport = transport


class _Ctx:
    model_via = "api"


def test_the_body_is_the_classify_question() -> None:
    b = llm.chat_body("qwen", "about?")
    assert b == {
        "model": "qwen",
        "stream": False,
        "format": "json",
        "options": {"temperature": 0, "num_ctx": 4096},
        "messages": [{"role": "user", "content": "about?"}],
    }


def test_in_process_asks_the_model_server() -> None:
    srv = HTTPServer(("127.0.0.1", 0), _Model)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{srv.server_port}/"
        got = asyncio.run(llm.chat(object(), object(), url, llm.chat_body("m", "q"), 5))
    finally:
        srv.shutdown()
    assert got == '{"ok": true}'
    path, body = _Model.asked[-1]
    assert path == "/api/chat" and body == llm.chat_body("m", "q")


def test_in_the_runner_it_asks_compute_generate_unchanged() -> None:
    t = _Transport({"text": '{"topics": []}', "model": "m"})
    got = asyncio.run(llm.chat(_DB(t), _Ctx(), "http://unused", llm.chat_body("m", "q"), 5))
    assert got == '{"topics": []}'
    fn, body = t.calls[0]
    want = llm.chat_body("m", "q")
    assert fn == "compute/generate"
    assert body == {k: want[k] for k in ("messages", "format", "options")}


@pytest.mark.parametrize("err", [RuntimeError("plugin_compute_unavailable"), ConnectionError()])
def test_a_failure_in_the_runner_is_an_oserror(err: BaseException) -> None:
    with pytest.raises(OSError):
        asyncio.run(llm.chat(_DB(_Transport(err)), _Ctx(), "", llm.chat_body("m", "q"), 5))


def test_a_down_server_in_process_is_an_oserror() -> None:
    with pytest.raises(urllib.error.URLError):
        asyncio.run(llm.chat(object(), object(), "http://127.0.0.1:9", llm.chat_body("m", "q"), 2))
