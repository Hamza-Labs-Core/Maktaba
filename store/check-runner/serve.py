#!/usr/bin/env python3
"""The isolated runner's server inside the Cloudflare Container (story 54.16 §1, #756).

The store's CheckRunner Durable Object starts the container with its internet off, then:
PUT /package and PUT /source (the bodies are the files), POST /run (runs run.sh, answers its
report JSON). GET /health answers once listening. One submission per container: nothing is
kept between containers, and the server takes no input but these files. The CI fallback runs
run.sh directly instead.
"""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
WORK = Path(tempfile.mkdtemp(prefix="runner-"))
# each path's file and the store's own size limit: the file names are fixed here, never taken
# from the request
FILES = {"/package": ("package", 52_428_800), "/source": ("source", 209_715_200)}
RUN_SECONDS = 18 * 60


class Handler(BaseHTTPRequestHandler):
    def _answer(self, status: int, body: bytes, kind: str = "text/plain") -> None:
        self.send_response(status)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 (http.server's name)
        if self.path == "/health":
            self._answer(200, b"ok")
        else:
            self._answer(404, b"not found")

    def do_PUT(self) -> None:  # noqa: N802
        entry = FILES.get(self.path)
        if entry is None:
            self._answer(404, b"not found")
            return
        name, limit = entry
        size = int(self.headers.get("Content-Length") or "-1")
        if size < 0 or size > limit:
            self._answer(413, b"too large or no length")
            return
        with open(WORK / name, "wb") as f:
            left = size
            while left > 0:
                chunk = self.rfile.read(min(left, 1 << 20))
                if not chunk:
                    break
                f.write(chunk)
                left -= len(chunk)
        self._answer(204 if left == 0 else 400, b"")

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/run":
            self._answer(404, b"not found")
            return
        pkg, src, out = WORK / "package", WORK / "source", WORK / "report.json"
        if not pkg.is_file() or not src.is_file():
            self._answer(409, b"the package and the source come first")
            return
        try:
            subprocess.run(
                [str(HERE / "run.sh"), str(pkg), str(src), str(out)],
                check=True,
                timeout=RUN_SECONDS,
                env={"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(WORK)},
            )
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as e:
            self._answer(500, f"run.sh failed: {e}".encode())
            return
        self._answer(200, out.read_bytes(), "application/json")

    def log_message(self, *_args: object) -> None:
        return  # nothing about a plugin is logged outside its report


def main() -> None:
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8080
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
