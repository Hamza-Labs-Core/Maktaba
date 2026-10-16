"""Subprocess access for the media stages (ffmpeg / ffprobe), injectable for tests.

The analysis stages call :func:`run_media`; a test installs a fake with
:func:`set_runner`, so the stage logic is exercised without ffmpeg or a NAS.
"""

from __future__ import annotations

import asyncio
import os
import shutil
from collections.abc import Awaitable, Callable
from dataclasses import dataclass

__all__ = [
    "FFMPEG",
    "FFPROBE",
    "ProcResult",
    "reset_runner",
    "run_media",
    "set_runner",
]

FFMPEG: str = os.environ.get("MAKTABA_FFMPEG") or shutil.which("ffmpeg") or "ffmpeg"
FFPROBE: str = os.environ.get("MAKTABA_FFPROBE") or shutil.which("ffprobe") or "ffprobe"


@dataclass(frozen=True, slots=True)
class ProcResult:
    stdout: bytes
    stderr: bytes
    returncode: int

    @property
    def text_err(self) -> str:
        return self.stderr.decode("utf-8", errors="replace")


Runner = Callable[[list[str], float], Awaitable[ProcResult]]


async def _real_runner(args: list[str], timeout: float) -> ProcResult:
    try:
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
    except OSError as exc:
        return ProcResult(b"", str(exc).encode(), 127)
    try:
        out, err = await asyncio.wait_for(proc.communicate(), timeout=timeout)
    except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
        proc.kill()
        await proc.wait()
        return ProcResult(b"", b"timed out", 124)
    return ProcResult(out, err, proc.returncode or 0)


_runner: Runner = _real_runner


def set_runner(runner: Runner) -> None:
    global _runner
    _runner = runner


def reset_runner() -> None:
    global _runner
    _runner = _real_runner


async def run_media(args: list[str], *, timeout: float = 120.0) -> ProcResult:
    """Run ``args`` (argv[0] is ffmpeg / ffprobe) and capture output; never raises."""
    return await _runner(args, timeout)
