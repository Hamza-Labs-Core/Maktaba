"""A tiny fake Ollama for the cluster e2e harness (stdlib only).

It holds one model, ``e2e-fake:latest``, answers Ollama's native API (``/api/tags``,
``/api/ps``, ``/api/show``, ``/api/version``, ``/api/chat``, ``/api/generate``,
``/api/embed``, ``/api/embeddings``) and its OpenAI-compatible one (``/v1/models``,
``/v1/chat/completions``, ``/v1/completions``, ``/v1/embeddings``), streaming or not.
Every answer names the machine it runs for (``--machine``), so a caller can tell which
machine served it; every request is appended to ``--requests`` (JSON lines: path,
model, stream; never the prompt).

    python fake_ollama.py --port 11500 --machine e2e-x-b --requests reqs.jsonl
"""

from __future__ import annotations

import argparse
import contextlib
import json
import threading
import time
from datetime import UTC, datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

MODEL = "e2e-fake:latest"
DIGEST = "e2e0" * 16
_lock = threading.Lock()


def _now() -> str:
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def _reply(machine: str) -> str:
    return f"cluster-e2e reply from {machine}"


def _model_entry() -> dict[str, Any]:
    return {
        "name": MODEL,
        "model": MODEL,
        "modified_at": _now(),
        "size": 1_234_567,
        "digest": DIGEST,
        "details": {
            "format": "gguf",
            "family": "e2e",
            "families": ["e2e"],
            "parameter_size": "1M",
            "quantization_level": "Q4_0",
        },
    }


class Handler(BaseHTTPRequestHandler):
    server_version = "fake-ollama/0.1"
    protocol_version = "HTTP/1.1"
    machine = "unknown"
    requests_path = ""

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
        print(f"{self.address_string()} {format % args}", flush=True)

    # ---- plumbing ---------------------------------------------------------------

    def _body(self) -> dict[str, Any]:
        n = int(self.headers.get("Content-Length") or 0)
        if n <= 0:
            return {}
        try:
            data = json.loads(self.rfile.read(n))
        except json.JSONDecodeError:
            return {}
        return data if isinstance(data, dict) else {}

    def _record(self, body: dict[str, Any], stream: bool) -> None:
        if not self.requests_path:
            return
        line = json.dumps(
            {
                "ts": _now(),
                "method": self.command,
                "path": self.path,
                "model": body.get("model"),
                "stream": stream,
                "machine": self.machine,
            }
        )
        with _lock, open(self.requests_path, "a", encoding="utf-8") as fh:
            fh.write(line + "\n")

    def _json(self, status: int, obj: Any) -> None:
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _stream(self, content_type: str, chunks: list[bytes]) -> None:
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Transfer-Encoding", "chunked")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for c in chunks:
            self.wfile.write(f"{len(c):x}\r\n".encode() + c + b"\r\n")
            self.wfile.flush()
            time.sleep(0.01)
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

    def _model_ok(self, body: dict[str, Any]) -> bool:
        model = str(body.get("model") or "")
        if model in (MODEL, MODEL.removesuffix(":latest")):
            return True
        self._json(404, {"error": f"model '{model}' not found"})
        return False

    # ---- routes -----------------------------------------------------------------

    def do_GET(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0].rstrip("/")
        if path in ("", "/"):
            self._json(200, {"status": "Ollama is running"})
        elif path == "/api/version":
            self._json(200, {"version": "0.9.0-e2e"})
        elif path == "/api/tags":
            self._json(200, {"models": [_model_entry()]})
        elif path == "/api/ps":
            entry = {**_model_entry(), "size_vram": 1_234_567, "expires_at": "2099-01-01T00:00:00Z"}
            self._json(200, {"models": [entry]})
        elif path == "/v1/models":
            self._json(
                200,
                {
                    "object": "list",
                    "data": [{"id": MODEL, "object": "model", "created": 0, "owned_by": "e2e"}],
                },
            )
        else:
            self._json(404, {"error": "not found"})

    def do_HEAD(self) -> None:  # noqa: N802
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_POST(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0].rstrip("/")
        body = self._body()
        reply = _reply(self.machine)
        words = reply.split(" ")
        if path == "/api/show":
            self._json(200, {"details": _model_entry()["details"], "model_info": {}, "modelfile": ""})
            return
        if path in ("/api/chat", "/api/generate"):
            stream = bool(body.get("stream", True))
            self._record(body, stream)
            if not self._model_ok(body):
                return
            key = "message" if path == "/api/chat" else "response"

            def piece(text: str, done: bool) -> dict[str, Any]:
                out: dict[str, Any] = {"model": MODEL, "created_at": _now(), "done": done}
                out[key] = {"role": "assistant", "content": text} if key == "message" else text
                if done:
                    out.update(
                        done_reason="stop",
                        total_duration=5_000_000,
                        load_duration=1_000,
                        prompt_eval_count=7,
                        prompt_eval_duration=1_000_000,
                        eval_count=len(words),
                        eval_duration=4_000_000,
                    )
                return out

            if not stream:
                self._json(200, piece(reply, True))
                return
            parts = [(w if i == 0 else " " + w) for i, w in enumerate(words)]
            lines = [json.dumps(piece(p, False)).encode() + b"\n" for p in parts]
            lines.append(json.dumps(piece("", True)).encode() + b"\n")
            self._stream("application/x-ndjson", lines)
            return
        if path in ("/api/embed", "/api/embeddings", "/v1/embeddings"):
            self._record(body, False)
            if not self._model_ok(body):
                return
            vec = [0.1, 0.2, 0.3, 0.4]
            if path == "/api/embed":
                inputs = body.get("input")
                n = len(inputs) if isinstance(inputs, list) else 1
                self._json(200, {"model": MODEL, "embeddings": [vec] * n, "prompt_eval_count": 3})
            elif path == "/api/embeddings":
                self._json(200, {"embedding": vec})
            else:
                inputs = body.get("input")
                n = len(inputs) if isinstance(inputs, list) else 1
                self._json(
                    200,
                    {
                        "object": "list",
                        "model": MODEL,
                        "data": [{"object": "embedding", "index": i, "embedding": vec} for i in range(n)],
                        "usage": {"prompt_tokens": 3, "total_tokens": 3},
                    },
                )
            return
        if path in ("/v1/chat/completions", "/v1/completions"):
            stream = bool(body.get("stream", False))
            self._record(body, stream)
            if not self._model_ok(body):
                return
            chat = path == "/v1/chat/completions"
            created = int(time.time())
            usage = {"prompt_tokens": 7, "completion_tokens": len(words), "total_tokens": 7 + len(words)}
            if not stream:
                choice: dict[str, Any] = {"index": 0, "finish_reason": "stop"}
                if chat:
                    choice["message"] = {"role": "assistant", "content": reply}
                else:
                    choice["text"] = reply
                self._json(
                    200,
                    {
                        "id": "chatcmpl-e2e",
                        "object": "chat.completion" if chat else "text_completion",
                        "created": created,
                        "model": MODEL,
                        "choices": [choice],
                        "usage": usage,
                    },
                )
                return
            events: list[bytes] = []
            for i, w in enumerate(words):
                text = w if i == 0 else " " + w
                delta: dict[str, Any] = (
                    {"index": 0, "delta": {"role": "assistant", "content": text}, "finish_reason": None}
                    if chat
                    else {"index": 0, "text": text, "finish_reason": None}
                )
                events.append(
                    b"data: "
                    + json.dumps(
                        {
                            "id": "chatcmpl-e2e",
                            "object": "chat.completion.chunk" if chat else "text_completion",
                            "created": created,
                            "model": MODEL,
                            "choices": [delta],
                        }
                    ).encode()
                    + b"\n\n"
                )
            final: dict[str, Any] = (
                {"index": 0, "delta": {}, "finish_reason": "stop"}
                if chat
                else {"index": 0, "text": "", "finish_reason": "stop"}
            )
            events.append(
                b"data: "
                + json.dumps(
                    {
                        "id": "chatcmpl-e2e",
                        "object": "chat.completion.chunk" if chat else "text_completion",
                        "created": created,
                        "model": MODEL,
                        "choices": [final],
                        "usage": usage,
                    }
                ).encode()
                + b"\n\n"
            )
            events.append(b"data: [DONE]\n\n")
            self._stream("text/event-stream", events)
            return
        self._json(404, {"error": "not found"})


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--port", type=int, required=True)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--machine", required=True, help="named in every answer")
    ap.add_argument("--requests", default="", help="append each request here (JSON lines)")
    args = ap.parse_args()
    Handler.machine = args.machine
    Handler.requests_path = args.requests
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    srv.daemon_threads = True
    print(f"fake ollama for {args.machine} on {args.host}:{args.port}", flush=True)
    with contextlib.suppress(KeyboardInterrupt):
        srv.serve_forever()


if __name__ == "__main__":
    main()
