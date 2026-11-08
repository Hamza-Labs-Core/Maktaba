"""A runner's tool starts on Windows, through anonymous pipes (Epic 54, story 54.23).

asyncio's Windows event loop gives a child's captured output named pipes it creates
(``\\\\.\\pipe\\python-pid-...``); an AppContainer may not create a named pipe, so every
``asyncio.create_subprocess_exec(..., stdout=PIPE)`` a stage makes (``run_media``, the
thumbnail, probe, audio and transcription helpers) fails inside a runner's AppContainer
(``PermissionError``, ``WinError 5``). :func:`create_subprocess_exec` keeps asyncio's API and
starts the tool with ``subprocess.Popen``, whose pipes are anonymous (``CreatePipe``, which
an AppContainer may use): a thread per captured pipe feeds an ``asyncio.StreamReader``, a
thread waits for the exit. The tool is put in a Job Object of its own (nested in the
runner's), so a kill (the caller's after a timeout, or a cancelled ``communicate`` or
``wait``) ends what it started with it. It is put there once ``Popen`` returns, so a process
it starts in its first instant escapes that job (``Popen`` keeps no thread handle to start it
suspended and resume it); such a process is still in the runner's own Job Object, and ends
with the runner.

:func:`install` makes it ``asyncio.create_subprocess_exec`` (a Windows runner, after its
sandbox). ``subprocess.Popen`` is what it starts with, so the runner's exec guard and its
share routing (a brokered handle as a tool's standard input) see each start as before.

Standard library only.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import subprocess
import sys
import threading
from typing import Any

__all__ = ["Process", "create_subprocess_exec", "install"]

_LIMIT = 2**16  # asyncio's default stream limit


class _Job:
    """A Job Object holding one tool (Windows): terminating it ends the tool and what it
    started. Best effort: without one, a kill ends the tool alone."""

    def __init__(self, popen: subprocess.Popen[bytes]) -> None:
        self._handle = 0
        self._k32: Any = None
        # terminate (the loop's kill) and close (the waiter thread, once the tool exited)
        # never overlap: a closed handle's value may be a new tool's job by then
        self._lock = threading.Lock()
        if sys.platform != "win32":
            return
        import ctypes
        from ctypes import wintypes

        k32 = ctypes.WinDLL("kernel32", use_last_error=True)
        k32.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
        k32.CreateJobObjectW.restype = wintypes.HANDLE
        k32.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        k32.AssignProcessToJobObject.restype = wintypes.BOOL
        k32.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
        k32.TerminateJobObject.restype = wintypes.BOOL
        k32.CloseHandle.argtypes = [wintypes.HANDLE]
        k32.CloseHandle.restype = wintypes.BOOL
        self._k32 = k32
        job = k32.CreateJobObjectW(None, None)
        if not job:
            return
        if not k32.AssignProcessToJobObject(job, int(popen._handle)):  # type: ignore[attr-defined,unused-ignore]
            k32.CloseHandle(job)
            return
        self._handle = job

    def terminate(self) -> bool:
        with self._lock:
            if not self._handle:
                return False
            return bool(self._k32.TerminateJobObject(self._handle, 1))

    def close(self) -> None:
        with self._lock:
            handle, self._handle = self._handle, 0
            if handle:
                self._k32.CloseHandle(handle)


def _on_loop(loop: asyncio.AbstractEventLoop, fn: Any, *args: Any) -> bool:
    """Run ``fn`` on the loop from a thread; False once the loop is gone."""
    try:
        loop.call_soon_threadsafe(fn, *args)
    except RuntimeError:  # the loop closed: no one reads any more
        return False
    return True


def _pump(loop: asyncio.AbstractEventLoop, fd: int, reader: asyncio.StreamReader) -> None:
    try:
        while chunk := os.read(fd, 65536):
            if not _on_loop(loop, reader.feed_data, chunk):
                return
    except OSError:
        pass
    finally:
        with contextlib.suppress(OSError):
            os.close(fd)
        _on_loop(loop, reader.feed_eof)


class _Writer:
    """The tool's standard input when it is a pipe: what ``communicate(input)`` and a
    stage's ``write``/``drain`` need, the writes in a thread."""

    def __init__(self, f: Any) -> None:
        self._f = f
        self._pending = bytearray()

    def write(self, data: bytes) -> None:
        self._pending += data

    async def drain(self) -> None:
        data, self._pending = bytes(self._pending), bytearray()
        if data:
            await asyncio.to_thread(self._f.write, data)
            await asyncio.to_thread(self._f.flush)

    def close(self) -> None:
        with contextlib.suppress(OSError, ValueError):
            if self._pending:
                self._f.write(bytes(self._pending))
                self._pending = bytearray()
            self._f.close()

    async def wait_closed(self) -> None:
        return None

    def is_closing(self) -> bool:
        return bool(self._f.closed)


class Process:
    """asyncio's ``Process`` for a tool started through anonymous pipes."""

    def __init__(self, popen: subprocess.Popen[bytes], limit: int) -> None:
        loop = asyncio.get_running_loop()
        self._popen = popen
        self._job = _Job(popen)
        self.pid = popen.pid
        self._returncode: int | None = None
        self._exited: asyncio.Future[int] = loop.create_future()
        self.stdin = _Writer(popen.stdin) if popen.stdin is not None else None
        self.stdout = self._reader(loop, popen.stdout, limit)
        self.stderr = self._reader(loop, popen.stderr, limit)
        threading.Thread(target=self._wait_thread, args=(loop,), daemon=True).start()

    @staticmethod
    def _reader(loop: asyncio.AbstractEventLoop, f: Any, limit: int) -> asyncio.StreamReader | None:
        if f is None:
            return None
        reader = asyncio.StreamReader(limit=limit, loop=loop)
        fd = os.dup(f.fileno())
        f.close()
        threading.Thread(target=_pump, args=(loop, fd, reader), daemon=True).start()
        return reader

    def _wait_thread(self, loop: asyncio.AbstractEventLoop) -> None:
        code = self._popen.wait()
        self._job.close()
        _on_loop(loop, self._set_exit, code)

    def _set_exit(self, code: int) -> None:
        self._returncode = code
        if not self._exited.done():
            self._exited.set_result(code)

    @property
    def returncode(self) -> int | None:
        return self._returncode

    async def wait(self) -> int:
        try:
            return await asyncio.shield(self._exited)
        except asyncio.CancelledError:
            self.kill()
            raise

    async def communicate(self, input: bytes | None = None) -> tuple[bytes | None, bytes | None]:  # noqa: A002
        async def feed() -> None:
            if self.stdin is None:
                return
            if input:
                self.stdin.write(input)
                await self.stdin.drain()
            self.stdin.close()

        async def read(r: asyncio.StreamReader | None) -> bytes | None:
            return None if r is None else await r.read()

        try:
            _, out, err = await asyncio.gather(feed(), read(self.stdout), read(self.stderr))
            await self.wait()
        except asyncio.CancelledError:
            self.kill()
            raise
        return out, err

    def send_signal(self, sig: int) -> None:
        if self._returncode is None:
            with contextlib.suppress(OSError):
                self._popen.send_signal(sig)

    def terminate(self) -> None:
        self.kill()

    def kill(self) -> None:
        """End the tool (and on Windows what it started, its Job Object)."""
        if self._returncode is not None:
            return
        if not self._job.terminate():
            with contextlib.suppress(OSError):
                self._popen.kill()


async def create_subprocess_exec(
    program: Any,
    *args: Any,
    stdin: Any = None,
    stdout: Any = None,
    stderr: Any = None,
    limit: int = _LIMIT,
    **kwds: Any,
) -> Process:
    """``asyncio.create_subprocess_exec`` through anonymous pipes (the same arguments)."""
    popen = subprocess.Popen(  # noqa: S603 - the stage's tool, as asyncio would start it
        [program, *args], stdin=stdin, stdout=stdout, stderr=stderr, bufsize=0, **kwds
    )
    return Process(popen, limit)


def install() -> None:
    """Make :func:`create_subprocess_exec` asyncio's (a Windows runner, after its sandbox)."""
    asyncio.create_subprocess_exec = create_subprocess_exec  # type: ignore[assignment]
    asyncio.subprocess.create_subprocess_exec = create_subprocess_exec  # type: ignore[assignment]
