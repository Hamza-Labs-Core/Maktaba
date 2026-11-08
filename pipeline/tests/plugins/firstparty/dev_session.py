"""One test session's plugin API dev server, for test_dev_api_sessions.py: run as
``python -m tests.plugins.firstparty.dev_session`` from pipeline/, it builds the binary the
harness's way, starts the server, prints one JSON line (where it listens, which binary, its
pid, its session's folder) and keeps it serving until its stdin closes; then it ends as a
session ends (its exit handlers stop the server and remove the folder)."""

from __future__ import annotations

import json
import os
import sys

from tests.plugins.firstparty import harness


def main() -> None:
    binary = harness.plugin_api_dev()
    if binary is None:
        sys.exit("no go here")
    folder = harness.session_dir()
    server = harness.start_dev_api(
        binary,
        # the server opens no connection until a route needs one: the test asks none
        dsn="postgres://127.0.0.1:1/none?sslmode=disable",
        socket=folder / "api.sock",
    )
    info = {
        "mint": server.mint,
        "socket": str(server.socket),
        "binary": binary,
        "server_pid": server.proc.pid,
        "pid": os.getpid(),
        "dir": str(folder),
    }
    sys.stdout.write(json.dumps(info) + "\n")
    sys.stdout.flush()
    sys.stdin.read()


if __name__ == "__main__":
    main()
