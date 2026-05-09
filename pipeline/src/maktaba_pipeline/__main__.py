"""Stub PID 1 for the pipeline container (Story 22.3).

The real CLI (`maktaba-pipeline run`, `maktaba-pipeline doctor`, ...)
lands with Epic 03. Until then, `python -m maktaba_pipeline` parks in a
sleep loop so compose has a long-lived container to attach a
healthcheck to. `python -m maktaba_pipeline doctor` short-circuits to a
zero-exit status so the compose healthcheck passes.
"""

from __future__ import annotations

import os
import signal
import sys
import time
from typing import Any

from . import __version__
from .log import init as init_log


def _doctor(log: Any) -> int:
    """Stub doctor probe — always healthy until Epic 03 wires real checks.

    Story 22.3 plan §2.7 specifies the real probe set (DB reach, Chroma
    reach, ffmpeg presence, MLX bind on Mac). Returning 0 here lets the
    compose healthcheck succeed so the rest of the stack can be
    exercised end-to-end before the pipeline service is real.
    """
    log.info("doctor stub OK", version=__version__)
    return 0


def _bootstrap_log() -> Any:
    return init_log(
        service="pipeline",
        env=os.getenv("MAKTABA_ENV", "dev"),
        version=__version__,
    )


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    if args and args[0] in {"--version", "-V"}:
        sys.stdout.write(f"{__version__}\n")
        sys.stdout.flush()
        return 0

    log = _bootstrap_log()
    if args and args[0] == "doctor":
        return _doctor(log)

    log.info("pipeline stub started (Epic 03 will replace this)", version=__version__)
    sys.stdout.flush()

    # Park until SIGTERM/SIGINT. This keeps the compose container alive
    # so it has somewhere for the healthcheck to land. The real CLI
    # replaces this loop with the worker pool.
    stop = False

    def _shutdown(_signum: int, _frame: object) -> None:
        nonlocal stop
        stop = True

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)
    while not stop:
        time.sleep(1)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
