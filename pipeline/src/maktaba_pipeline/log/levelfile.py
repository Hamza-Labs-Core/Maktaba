"""The level file (Epic 53 story 53.11): "Detailed logs for one hour".

The supervisor writes ``<level> <until RFC3339>`` into a file per role
(``MAKTABA_LOG_LEVEL_FILE``); the pipeline reads it every few seconds and logs at that
level until then, and at the level it started with after (as the Go roles do:
shared/log/go/levelfile.go).
"""

from __future__ import annotations

import logging
import os
import threading
from collections.abc import Callable
from datetime import UTC, datetime

ENV_LEVEL_FILE = "MAKTABA_LOG_LEVEL_FILE"
LEVEL_EVERY_S = 5.0

_NAMES = {
    "debug": logging.DEBUG,
    "info": logging.INFO,
    "warn": logging.WARNING,
    "error": logging.ERROR,
}


def read_level_file(path: str, now: datetime) -> int | None:
    """The level the file asks for at ``now`` (None: no file, unreadable, or its time passed)."""
    try:
        with open(path, encoding="utf-8") as f:
            parts = f.read().split()
    except OSError:
        return None
    if len(parts) != 2 or parts[0].lower() not in _NAMES:
        return None
    try:
        until = datetime.fromisoformat(parts[1].replace("Z", "+00:00"))
    except ValueError:
        return None
    if until.tzinfo is None or now >= until:
        return None
    return _NAMES[parts[0].lower()]


def level_from(path: str, base: int, now: datetime) -> int:
    """The level to log at: the file's while it lasts, else ``base``."""
    got = read_level_file(path, now)
    return base if got is None else got


def watch(
    base: int, apply: Callable[[int], None], path: str | None = None
) -> threading.Thread | None:
    """Apply the file every LEVEL_EVERY_S on a daemon thread (None without a file to watch)."""
    path = path or os.environ.get(ENV_LEVEL_FILE, "").strip()
    if not path:
        return None

    def run() -> None:
        cur = base
        stop = threading.Event()
        while not stop.wait(LEVEL_EVERY_S):
            nxt = level_from(path, base, datetime.now(UTC))
            if nxt != cur:
                cur = nxt
                apply(nxt)

    t = threading.Thread(target=run, name="log-level-file", daemon=True)
    t.start()
    return t
