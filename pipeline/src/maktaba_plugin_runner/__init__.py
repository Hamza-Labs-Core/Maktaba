"""maktaba-plugin-runner: Maktaba's own stages as first-party plugins (Epic 54, story 54.23).

One process per runner group (``light``, ``ml``, ``index``, ``net``, ``service``, and the
fingerprint runner ``fp``), started by the supervisor as role ``plugin-runner`` with no
database login and no secrets in its environment. At start it

1. checks that the first-party bundle and every plugin package of its group are the ones
   the release installed (:mod:`.trust`), refusing to start otherwise
   (``plugin_trusted_unsigned``);
2. applies its OS sandbox to itself from the union of its group's ``[trusted]`` tables
   (:mod:`.sandbox`), before any plugin is imported;
3. imports its group's plugin modules, only now;
4. serves the queue worker on ``<runner dir>/runner-<group>.sock``: ``hello`` (what it
   serves here and why a stage is unavailable), ``run`` (one job, with its invocation
   token, the job's progress and log relayed to the worker), ``fingerprints`` and
   ``cancel`` (:mod:`.server`).

A plugin's code reaches the database only through this machine's plugin API, by the
names of its registered statements (``maktaba_plugin_sdk.native``).
"""

from __future__ import annotations

__all__ = ["GROUPS", "socket_path"]

import hashlib
import os
from pathlib import Path

#: the runner groups (README §15.4); a manifest may also ask for its own (``own``)
GROUPS = ("light", "ml", "index", "net", "service", "fp")

#: the longest unix socket path macOS takes (sun_path is 104 bytes)
_MAX_SOCKET = 100


def runner_dir() -> Path:
    """Where the runners' sockets are: ``MAKTABA_RUNNER_DIR`` (the supervisor sets it for
    the worker and every runner), else ``<data>/run/runners``, else a short folder under
    /tmp named after the data folder when that path is too long for a socket."""
    explicit = os.environ.get("MAKTABA_RUNNER_DIR", "").strip()
    if explicit:
        return Path(explicit)
    data = os.environ.get("MAKTABA_DATA_DIR", "").strip() or "."
    d = Path(data) / "run" / "runners"
    if len(str(d / "runner-service.sock")) > _MAX_SOCKET:
        d = Path("/tmp") / (
            "maktaba-r-" + hashlib.sha256(str(Path(data).resolve()).encode()).hexdigest()[:12]
        )
    return d


def socket_path(group: str) -> Path:
    return runner_dir() / f"runner-{group}.sock"
