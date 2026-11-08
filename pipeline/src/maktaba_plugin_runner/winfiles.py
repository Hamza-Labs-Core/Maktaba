"""A runner's library on a share, through its launcher's broker (Epic 54, story 54.23).

An AppContainer with no capabilities cannot reach SMB at all, so on Windows the launcher
(``maktaba-server runner-sandbox-run``, outside the sandbox) brokers the group's share
folders: it serves a pipe only this runner's process may use (``MAKTABA_RUNNER_FILES_PIPE``)
and answers for the folders it names (``MAKTABA_RUNNER_FILES_ROOTS``). For one file the
runner asks, it checks the path is in those folders and spelled one way, opens it, and
duplicates the handle into this process: read, or write for a subtitle name in a folder the
group writes by name. The bytes then go through the handle to the share, never through the
launcher (cmd/maktaba-server/internal/appcontainer/broker.go).

:func:`install` routes the calls a stage makes on such a path to the broker: ``open``,
``os.stat`` (and so ``os.path.exists`` and the like), ``os.listdir``, ``os.replace`` /
``os.rename`` and ``os.remove`` / ``os.unlink``. Each raises the audit event the plain call
would, so the runner's guards (subtitle names, the job's scope) see the path as before. A
refusal is a ``PermissionError``, so a job refused there runs in the worker
(``sandbox_refused``), as a job does that a tool runs on a share: a tool (ffmpeg) cannot use
a handle of the runner's, so a share path in a tool's arguments is refused up front.

Standard library only.
"""

from __future__ import annotations

import builtins
import errno
import json
import os
import stat as stat_mod
import sys
import threading
from collections.abc import Callable
from typing import Any

__all__ = ["PIPE_ENV", "ROOTS_ENV", "Broker", "install", "install_from_env"]

PIPE_ENV = "MAKTABA_RUNNER_FILES_PIPE"
ROOTS_ENV = "MAKTABA_RUNNER_FILES_ROOTS"

Request = Callable[[dict[str, Any]], dict[str, Any]]


def _key(path: str) -> str:
    return path.replace("/", "\\").rstrip("\\").lower()


class Broker:
    """The pipe to the launcher's broker: one request line, one answer line, one at a time."""

    def __init__(self, pipe: str, opener: Callable[[str], Any] | None = None) -> None:
        self._f = (opener or (lambda p: open(p, "r+b", buffering=0)))(pipe)  # noqa: SIM115
        self._buf = b""
        self._lock = threading.Lock()

    def __call__(self, req: dict[str, Any]) -> dict[str, Any]:
        with self._lock:
            self._f.write(json.dumps(req).encode() + b"\n")
            while b"\n" not in self._buf:
                chunk = self._f.read(65536)
                if not chunk:
                    raise PermissionError(errno.EACCES, "the file broker closed its pipe")
                self._buf += chunk
            line, self._buf = self._buf.split(b"\n", 1)
        out: dict[str, Any] = json.loads(line)
        return out


class _Shim:
    def __init__(
        self, request: Request, roots: list[str], fdopen: Callable[[int, int], int]
    ) -> None:
        self.request = request
        self.roots = [_key(r) for r in roots if r]
        self.fdopen = fdopen
        self.open = builtins.open
        self.stat = os.stat
        self.lstat = os.lstat
        self.listdir = os.listdir
        self.replace = os.replace
        self.rename = os.rename
        self.remove = os.remove
        self.unlink = os.unlink
        self.exists = os.path.exists
        self.isfile = os.path.isfile
        self.isdir = os.path.isdir

    def under(self, path: object) -> str | None:
        """``path`` as the broker is asked for it when it is in a brokered folder."""
        if path is None or isinstance(path, int):
            return None
        try:
            p = os.fsdecode(path)  # type: ignore[arg-type]
        except (TypeError, ValueError):
            return None
        if not (p.startswith("\\\\") or p.startswith("//")):
            return None
        k = _key(p)
        if any(k == r or k.startswith(r + "\\") for r in self.roots):
            return p
        return None

    def ask(self, req: dict[str, Any]) -> dict[str, Any]:
        out = self.request(req)
        if out.get("error"):
            code = int(out.get("errno") or errno.EACCES)
            if code == errno.ENOENT:
                raise FileNotFoundError(code, str(out["error"]), req.get("path"))
            raise PermissionError(code, str(out["error"]), req.get("path"))
        return out

    # -- the calls routed -------------------------------------------------------------

    def open_(self, file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        p = self.under(file)
        if p is None:
            return self.open(file, mode, *args, **kwargs)
        write = any(c in mode for c in "wax+")
        sys.audit("open", p, mode, 0)
        if write and (mode.replace("b", "").replace("t", "") != "w"):
            # appending, exclusive creation and read-write are not brokered: refused
            raise PermissionError(
                errno.EACCES, f"{p}: only a whole subtitle file is written on a share"
            )
        out = self.ask({"op": "open", "path": p, "write": write})
        flags = (os.O_WRONLY if write else os.O_RDONLY) | getattr(os, "O_BINARY", 0)
        fd = self.fdopen(int(out["handle"]), flags)
        return self.open(fd, mode, *args, **kwargs)

    def stat_(self, path: Any, *args: Any, **kwargs: Any) -> os.stat_result:
        p = self.under(path)
        if p is None:
            return self.stat(path, *args, **kwargs)
        return self._stat(p)

    def lstat_(self, path: Any, *args: Any, **kwargs: Any) -> os.stat_result:
        p = self.under(path)
        if p is None:
            return self.lstat(path, *args, **kwargs)
        return self._stat(p)

    def _stat(self, p: str) -> os.stat_result:
        sys.audit("os.stat", p)
        out = self.ask({"op": "stat", "path": p})
        mode = (stat_mod.S_IFDIR | 0o555) if out.get("dir") else (stat_mod.S_IFREG | 0o444)
        ns = int(out.get("mtime_ns") or 0)
        size = int(out.get("size") or 0)
        sec = ns // 1_000_000_000
        return os.stat_result((mode, 0, 0, 1, 0, 0, size, sec, sec, sec))

    # Python 3.12 on Windows answers these without os.stat (nt._path_exists and the like)
    def exists_(self, path: Any) -> bool:
        if self.under(path) is None:
            return self.exists(path)
        try:
            self.stat_(path)
        except (OSError, ValueError):
            return False
        return True

    def isfile_(self, path: Any) -> bool:
        if self.under(path) is None:
            return self.isfile(path)
        try:
            return stat_mod.S_ISREG(self.stat_(path).st_mode)
        except (OSError, ValueError):
            return False

    def isdir_(self, path: Any) -> bool:
        if self.under(path) is None:
            return self.isdir(path)
        try:
            return stat_mod.S_ISDIR(self.stat_(path).st_mode)
        except (OSError, ValueError):
            return False

    def listdir_(self, path: Any = ".") -> list[Any]:
        p = self.under(path)
        if p is None:
            return self.listdir(path)
        sys.audit("os.listdir", p)
        names = [str(n) for n in self.ask({"op": "list", "path": p}).get("names") or []]
        return [os.fsencode(n) for n in names] if isinstance(path, bytes) else names

    def _move(
        self, plain: Callable[..., None], src: Any, dst: Any, *args: Any, **kwargs: Any
    ) -> None:
        s, d = self.under(src), self.under(dst)
        if s is None and d is None:
            plain(src, dst, *args, **kwargs)
            return
        sys.audit("os.rename", os.fsdecode(src), os.fsdecode(dst), -1, -1)
        if s is None or d is None:
            raise PermissionError(
                errno.EACCES, f"{src} -> {dst}: a file is not moved on or off a share here"
            )
        self.ask({"op": "replace", "path": s, "to": d})

    def replace_(self, src: Any, dst: Any, *args: Any, **kwargs: Any) -> None:
        self._move(self.replace, src, dst, *args, **kwargs)

    def rename_(self, src: Any, dst: Any, *args: Any, **kwargs: Any) -> None:
        self._move(self.rename, src, dst, *args, **kwargs)

    def _remove(self, plain: Callable[..., None], path: Any, *args: Any, **kwargs: Any) -> None:
        p = self.under(path)
        if p is None:
            plain(path, *args, **kwargs)
            return
        sys.audit("os.remove", p, -1)
        self.ask({"op": "remove", "path": p})

    def remove_(self, path: Any, *args: Any, **kwargs: Any) -> None:
        self._remove(self.remove, path, *args, **kwargs)

    def unlink_(self, path: Any, *args: Any, **kwargs: Any) -> None:
        self._remove(self.unlink, path, *args, **kwargs)

    def exec_hook(self, event: str, args: tuple[object, ...]) -> None:
        """A tool cannot use the runner's handles: one handed a share path is refused, so
        that job runs in the worker."""
        if event != "subprocess.Popen":
            return
        argv = args[1]
        items = [argv] if isinstance(argv, (str, bytes, os.PathLike)) else list(argv or ())  # type: ignore[call-overload]
        for a in items:
            text = os.fsdecode(a) if isinstance(a, (str, bytes, os.PathLike)) else ""
            if any(self.under(part) for part in [text, *text.split()]):
                raise PermissionError(
                    errno.EACCES, f"{text}: a tool cannot reach the share; the worker runs this job"
                )


_installed: list[_Shim] = []


def install(
    request: Request,
    roots: list[str],
    *,
    fdopen: Callable[[int, int], int] | None = None,
) -> None:
    """Route the calls a stage makes on a path in ``roots`` to the broker (``request``).
    Installed once per process (an audit hook is for good)."""
    if _installed:
        return
    if fdopen is None:
        import msvcrt  # Windows only: the launcher brokers there alone

        fdopen = msvcrt.open_osfhandle  # type: ignore[attr-defined]
    shim = _Shim(request, roots, fdopen)
    _installed.append(shim)
    builtins.open = shim.open_
    import io

    io.open = shim.open_
    os.stat = shim.stat_
    os.lstat = shim.lstat_
    os.listdir = shim.listdir_
    os.replace = shim.replace_
    os.rename = shim.rename_
    os.remove = shim.remove_
    os.unlink = shim.unlink_
    os.path.exists = shim.exists_
    os.path.isfile = shim.isfile_
    os.path.isdir = shim.isdir_  # type: ignore[assignment]
    sys.addaudithook(shim.exec_hook)


def install_from_env(environ: dict[str, str] | None = None) -> bool:
    """:func:`install` with the launcher's pipe and folders, when it named any."""
    env = os.environ if environ is None else environ
    pipe = env.get(PIPE_ENV, "")
    if not pipe:
        return False
    roots = [str(r.get("path") or "") for r in json.loads(env.get(ROOTS_ENV) or "[]")]
    install(Broker(pipe), roots)
    return True
