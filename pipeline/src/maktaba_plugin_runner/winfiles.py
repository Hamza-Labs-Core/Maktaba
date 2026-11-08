"""A runner's library on a share, through its launcher's broker (Epic 54, story 54.23).

An AppContainer with no capabilities cannot reach SMB at all, so on Windows the launcher
(``maktaba-server runner-sandbox-run``, outside the sandbox) brokers the group's share
folders: it serves a pipe only this runner's process may use (``MAKTABA_RUNNER_FILES_PIPE``)
and answers for the folders it names (``MAKTABA_RUNNER_FILES_ROOTS``). For one file the
runner asks to read, it checks the path is in those folders and spelled one way, opens it,
and duplicates the handle into this process with read access: the bytes then go through the
handle to the share, never through the launcher. A subtitle (a subtitle name in a folder the
group writes by name) is written by the launcher from the bytes this process sends when the
file closes: the runner holds no handle it could write with
(cmd/maktaba-server/internal/appcontainer/broker.go).

:func:`install` routes the calls a stage makes on such a path to the broker: ``open``,
``os.stat`` (and so ``os.path.exists`` and the like), ``os.listdir``, ``os.replace`` /
``os.rename`` and ``os.remove`` / ``os.unlink``. Each raises the audit event the plain call
would, so the runner's guards (subtitle names, the job's scope) see the path as before. A
refusal is a ``PermissionError``, so a job refused there runs in the worker
(``sandbox_refused``).

A tool (ffmpeg, ffprobe) reaches no share either, being a child in the same AppContainer and
Job Object. One given a single file of a brokered folder as its input reads it through a
handle the broker opened for that file: :meth:`_Shim.popen_init` asks the broker to open it
(the same checks as ``open``), starts the tool with that handle as its standard input and
the argument as ``fd:`` (ffmpeg's protocol for a descriptor, which seeks a file as it seeks
any other, so an mp4 whose index is at its end is read as from a path), and closes the
runner's copy once the tool has its. Any other share path in a tool's arguments (a second
share file, an output, a path inside another argument, another share) is refused up front.

Standard library only.
"""

from __future__ import annotations

import base64
import builtins
import errno
import io
import json
import os
import stat as stat_mod
import subprocess
import sys
import threading
from collections.abc import Callable
from typing import Any

__all__ = ["PIPE_ENV", "ROOTS_ENV", "Broker", "install", "install_from_env"]

PIPE_ENV = "MAKTABA_RUNNER_FILES_PIPE"
ROOTS_ENV = "MAKTABA_RUNNER_FILES_ROOTS"

Request = Callable[[dict[str, Any]], dict[str, Any]]

#: the broker's cap on a subtitle it writes (MaxBrokeredWrite)
MAX_WRITE = 16 << 20

#: the tools that read an input through a descriptor (``fd:``): the input a brokered handle
#: is handed to; a share path given to any other program is refused
HANDLE_TOOLS = frozenset({"ffmpeg", "ffprobe"})

#: inputs that are the tool's standard input already: one brokered input would collide
_STDIN_INPUTS = ("-", "pipe:", "pipe:0", "fd:")


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
        if write:
            if mode.replace("b", "").replace("t", "") != "w":
                # appending, exclusive creation and read-write are not brokered: refused
                raise PermissionError(
                    errno.EACCES, f"{p}: only a whole subtitle file is written on a share"
                )
            buf = _BrokeredWrite(self, p)
            if "b" in mode:
                return buf
            return io.TextIOWrapper(
                buf,
                encoding=kwargs.get("encoding") or "utf-8",
                errors=kwargs.get("errors"),
                newline=kwargs.get("newline"),
            )
        out = self.ask({"op": "open", "path": p})
        fd = self.fdopen(int(out["handle"]), os.O_RDONLY | getattr(os, "O_BINARY", 0))
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

    # -- tools ----------------------------------------------------------------------

    def mentions(self, text: str) -> bool:
        """``text`` names a path in a brokered folder anywhere in it (``subtitles=\\\\nas\\...``,
        ``file:\\\\nas\\...``)."""
        for lead in ("\\\\", "//"):
            at = text.find(lead)
            while at >= 0:
                if self.under(text[at:]):
                    return True
                at = text.find(lead, at + 1)
        return False

    def tool_args(self, argv: list[Any]) -> tuple[list[str], str] | None:
        """How a tool started with ``argv`` reads a share: ``None`` when no argument names
        one (the start is left alone); else its arguments with the one share input as
        ``fd:`` and that input's path, for the broker to open. Refused (PermissionError):
        a program that does not read a descriptor, more than one share path, a share path
        that is not a whole input argument, or a share outside the brokered folders."""
        texts = [
            os.fsdecode(a) if isinstance(a, (str, bytes, os.PathLike)) else str(a) for a in argv
        ]

        def refuse(why: str) -> PermissionError:
            return PermissionError(
                errno.EACCES, f"{' '.join(texts)}: {why}; the worker runs this job"
            )

        unc = [
            t
            for t in texts[1:]
            if (t.startswith("\\\\") or t.startswith("//"))
            and not t.startswith(("\\\\?\\", "\\\\.\\", "//?/", "//./"))
        ]
        hits = [i for i, t in enumerate(texts) if self.mentions(t)]
        if not hits:
            if unc:
                raise refuse("a share the runner is not brokered")
            return None
        name = os.path.basename(texts[0].replace("\\", "/")).lower()
        name = name.removesuffix(".exe")
        if name not in HANDLE_TOOLS:
            raise refuse("only ffmpeg and ffprobe read a share file, through a handle")
        if len(hits) > 1 or any(not self.under(t) for t in unc):
            raise refuse("a tool reads one share file, its one input")
        at = hits[0]
        path = texts[at]
        if self.under(path) != path:
            raise refuse("a share path inside another argument")
        positional_last = name == "ffprobe" and at == len(texts) - 1
        if not (at >= 2 and texts[at - 1] == "-i") and not positional_last:
            raise refuse("a share path that is not the tool's input")
        others = [t for i, t in enumerate(texts) if i != at and i > 0 and texts[i - 1] == "-i"]
        if any(t in _STDIN_INPUTS or t.startswith(("pipe:", "fd:")) for t in others):
            raise refuse("the tool's standard input is another input already")
        new = list(texts)
        new[at] = "fd:"
        return new, path

    def popen_init(self, orig: Callable[..., None]) -> Callable[..., None]:
        """``subprocess.Popen.__init__`` with a tool's share input routed (:meth:`tool_args`):
        the broker opens the file, the tool gets the handle as its standard input, and the
        runner's copy is closed once the tool has its (or the start failed)."""
        shim = self

        def __init__(popen: Any, args: Any, *rest: Any, **kwargs: Any) -> None:
            if rest or kwargs.get("shell") or not isinstance(args, (list, tuple)):
                orig(popen, args, *rest, **kwargs)  # the audit hook refuses a share path here
                return
            routed = shim.tool_args(list(args))
            if routed is None:
                orig(popen, args, **kwargs)
                return
            new, path = routed
            stdin = kwargs.get("stdin")
            # winproc's empty pipe in place of DEVNULL (the nul device an AppContainer may
            # not open) is a standard input no caller needs, as DEVNULL is
            if stdin not in (None, subprocess.DEVNULL) and not getattr(stdin, "empty_stdin", False):
                raise PermissionError(
                    errno.EACCES,
                    f"{path}: the tool's standard input is taken; the worker runs this job",
                )
            sys.audit("open", path, "rb", 0)
            out = shim.ask({"op": "open", "path": path})
            fd = shim.fdopen(int(out["handle"]), os.O_RDONLY | getattr(os, "O_BINARY", 0))
            try:
                orig(popen, new, **{**kwargs, "stdin": fd})
            finally:
                os.close(fd)

        return __init__

    def exec_hook(self, event: str, args: tuple[object, ...]) -> None:
        """The backstop: a share path that reaches a process start (one not routed by
        :meth:`popen_init`: ``os.spawn``, a shell, a string command) is refused, so that job
        runs in the worker."""
        if event != "subprocess.Popen":
            return
        argv = args[1]
        items = [argv] if isinstance(argv, (str, bytes, os.PathLike)) else list(argv or ())  # type: ignore[call-overload]
        for a in items:
            text = os.fsdecode(a) if isinstance(a, (str, bytes, os.PathLike)) else ""
            if self.mentions(text) or any(self.under(part) for part in text.split()):
                raise PermissionError(
                    errno.EACCES, f"{text}: a tool cannot reach the share; the worker runs this job"
                )


class _BrokeredWrite(io.BytesIO):
    """A subtitle being written on a share: its bytes go to the broker when it closes."""

    def __init__(self, shim: _Shim, path: str) -> None:
        super().__init__()
        self._shim, self._path, self._sent = shim, path, False

    def close(self) -> None:
        if self.closed or self._sent:
            super().close()
            return
        self._sent = True
        try:
            data = self.getvalue()
            if len(data) > MAX_WRITE:
                raise PermissionError(
                    errno.EACCES, f"{self._path}: a subtitle over {MAX_WRITE} bytes"
                )
            self._shim.ask(
                {"op": "write", "path": self._path, "data": base64.b64encode(data).decode()}
            )
        finally:
            super().close()


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
    subprocess.Popen.__init__ = shim.popen_init(subprocess.Popen.__init__)  # type: ignore[method-assign]
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
