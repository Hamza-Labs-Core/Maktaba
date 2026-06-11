"""Minimal HTTP endpoint exposing the pipeline's recent logs.

The pipeline is otherwise a gRPC + queue-worker daemon with no HTTP
surface, so rather than pull in a web framework we run a stdlib
``ThreadingHTTPServer`` on a daemon thread. It serves a single route:

    GET /logs/recent

with the same query params the Go ``RecentHandler`` accepts
(``since``/``level``/``services``/``q``/``limit``/``format``). The API's
diagnostics-export endpoint proxies this into the bundle's
``pipeline-logs.jsonl``.

Bind it on an internal/admin port only — it carries operational logs.
"""

from __future__ import annotations

import json
import threading
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

from .ring import LogRingBuffer, get_ring

__all__ = ["build_recent_response", "start_log_server"]


def build_recent_response(path: str, ring: LogRingBuffer | None) -> tuple[int, str, bytes]:
    """Pure request handler — returns (status, content_type, body).

    Extracted from the socket layer so the routing + filtering logic is
    unit-testable without opening a port. ``path`` is the raw request
    target (path + query); ``ring`` is the buffer to read.
    """
    parsed = urlparse(path)
    if parsed.path != "/logs/recent":
        return 404, "text/plain", b"not found\n"
    if ring is None:
        return 503, "text/plain", b"log ring buffer not enabled\n"

    params = parse_qs(parsed.query)
    lines = ring.recent(
        since=_first_ts(params.get("since")),
        min_level=_first(params.get("level")),
        services=_csv_set(_first(params.get("services"))),
        search=_first(params.get("q")),
        limit=_first_int(params.get("limit")),
    )

    if _first(params.get("format")) == "json":
        payload = json.dumps({"entries": [json.loads(x) for x in lines], "count": len(lines)})
        return 200, "application/json", payload.encode("utf-8")
    # Default: newline-delimited JSON, matching the Go RecentHandler.
    ndjson = "\n".join(lines) + ("\n" if lines else "")
    return 200, "application/x-ndjson", ndjson.encode("utf-8")


class _Handler(BaseHTTPRequestHandler):
    # Silence the default stderr access log — the daemon already emits
    # structured logs and per-request noise would pollute them.
    def log_message(self, *_args: object) -> None:  # noqa: D401
        return

    def do_GET(self) -> None:  # noqa: N802 — BaseHTTPRequestHandler API
        status, content_type, body = build_recent_response(self.path, get_ring())
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def _first(values: list[str] | None) -> str | None:
    return values[0] if values else None


def _first_int(values: list[str] | None) -> int | None:
    raw = _first(values)
    if not raw:
        return None
    try:
        return int(raw)
    except ValueError:
        return None


def _first_ts(values: list[str] | None) -> datetime | None:
    raw = _first(values)
    if not raw:
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return None


def _csv_set(raw: str | None) -> frozenset[str] | None:
    if not raw:
        return None
    items = {s.strip() for s in raw.split(",") if s.strip()}
    return frozenset(items) if items else None


def start_log_server(addr: str, ring: LogRingBuffer | None = None) -> ThreadingHTTPServer:
    """Start the recent-logs HTTP server on a daemon thread.

    ``addr`` is ``host:port`` (an empty host binds all interfaces, e.g.
    ``":9102"``). The returned server can be ``shutdown()`` on graceful
    exit. The ``ring`` argument is accepted for symmetry/tests; the
    handler reads the process-global ring so the worker loop and the
    server share one buffer.
    """
    host, _, port_s = addr.rpartition(":")
    port = int(port_s)
    server = ThreadingHTTPServer((host, port), _Handler)
    thread = threading.Thread(target=server.serve_forever, name="log-http", daemon=True)
    thread.start()
    return server
