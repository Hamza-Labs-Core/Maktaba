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
``os.stat`` (and so ``os.path.exists``, ``lexists``, ``isfile``, ``isdir`` and the like),
``os.listdir``, ``os.replace`` / ``os.rename`` and ``os.remove`` / ``os.unlink``; ``os.scandir``
(and so ``os.walk`` and ``Path.glob``) is refused there. Each raises the audit event the plain
call would, so the runner's guards (subtitle names, the job's scope) see the path as before. A
refusal is a ``PermissionError``, never a missing file: a lookup that answered False or empty
for a refused file would let a stage end done with the wrong result, where the refusal makes
the job run in the worker (``sandbox_refused``). The lookups Windows answers without
``os.stat`` (``islink``, ``isjunction``, ``os.access``) go through the broker too; a local path
the AppContainer has no grant for (a share this machine serves, mapped back to its folder)
raises from the lookups instead of answering absent; and any other file call on a share path
(``os.open``, ``glob``, a mkdir, a ``shutil`` copy...) is refused by the audit hook. What the
broker and the plain lookups answered for each job is counted, with the first paths a lookup
found missing (:func:`take_counts`, :func:`take_missing`: the runner's job line).

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
import contextlib
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

__all__ = [
    "PIPE_ENV",
    "ROOTS_ENV",
    "Broker",
    "install",
    "install_from_env",
    "install_unbrokered",
    "is_share",
    "take_counts",
    "take_missing",
]

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


def is_share(path: str) -> bool:
    """``path`` names a file on a share (``\\\\server\\share\\...``, also as ``\\\\?\\UNC\\...``),
    not a device or a local path in the long form (``\\\\?\\C:\\...``, ``\\\\.\\pipe\\...``)."""
    p = path.replace("/", "\\")
    if p[:8].upper() == "\\\\?\\UNC\\":
        return True
    return p.startswith("\\\\") and not p.startswith(("\\\\?\\", "\\\\.\\"))


def _key(path: str) -> str:
    return path.replace("/", "\\").rstrip("\\").lower()


class Broker:
    """The pipe to the launcher's broker: one request line, one answer line, one at a time."""

    def __init__(self, pipe: str, opener: Callable[[str], Any] | None = None) -> None:
        self._f = (opener or (lambda p: open(p, "r+b", buffering=0)))(pipe)  # noqa: SIM115
        self._buf = b""
        self._lock = threading.Lock()

    def __call__(self, req: dict[str, Any]) -> dict[str, Any]:
        # any failure of the pipe is a refusal, never an absence: a broken pipe is an OSError
        # a caller's ``isfile`` would answer False to, a share file seen as missing
        try:
            with self._lock:
                self._f.write(json.dumps(req).encode() + b"\n")
                while b"\n" not in self._buf:
                    chunk = self._f.read(65536)
                    if not chunk:
                        raise PermissionError(errno.EACCES, "the file broker closed its pipe")
                    self._buf += chunk
                line, self._buf = self._buf.split(b"\n", 1)
            out = json.loads(line)
        except PermissionError:
            raise
        except (OSError, ValueError) as exc:
            raise PermissionError(errno.EACCES, f"the file broker's pipe failed: {exc}") from exc
        if not isinstance(out, dict):
            raise PermissionError(errno.EACCES, "the file broker's answer is not an object")
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
        self.scandir = os.scandir
        self.exists = os.path.exists
        self.lexists = os.path.lexists
        self.isfile = os.path.isfile
        self.isdir = os.path.isdir
        self.islink = os.path.islink
        self.isjunction = getattr(os.path, "isjunction", None)
        self.access = os.access
        #: what the broker and the plain lookups answered since :func:`take_counts`: ``<op>``
        #: for an answer, ``<op>_missing`` for a file not there, ``<op>_refused`` for a
        #: refusal, ``plain``/``plain_missing`` for a lookup the plain call served,
        #: ``unbrokered_refused``/``unrouted_refused`` for a share path refused here
        self.counts: dict[str, int] = {}
        #: the first paths a lookup found missing since :func:`take_missing` (at most 3)
        self.missing: list[str] = []
        self._own = threading.local()

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

    def route(self, path: object) -> str | None:
        """:meth:`under`, and a refusal for any other share path: an AppContainer reaches
        no share by itself, so a plain call there could only fail, and the plain lookups
        (``os.path.exists`` and the like, which answer False on any error) would read that
        failure as a missing file. A runner with no broker (one that writes its libraries
        in full) refuses every share path this way, so its job runs in the worker."""
        p = self.under(path)
        if p is not None:
            return p
        if path is None or isinstance(path, int):
            return None
        try:
            text = os.fsdecode(path)  # type: ignore[arg-type]
        except (TypeError, ValueError):
            return None
        if is_share(text):
            self.count("unbrokered_refused")
            raise PermissionError(
                errno.EACCES,
                f"{text}: a share this runner is not brokered; the worker runs this job",
            )
        return None

    def count(self, key: str) -> None:
        self.counts[key] = self.counts.get(key, 0) + 1

    def note_missing(self, path: Any) -> None:
        if len(self.missing) < 3:
            with contextlib.suppress(TypeError, ValueError):
                self.missing.append(os.fsdecode(path))

    def ask(self, req: dict[str, Any]) -> dict[str, Any]:
        op = str(req.get("op"))
        try:
            out = self.request(req)
        except PermissionError:
            self.count(op + "_refused")
            raise
        if out.get("error"):
            code = int(out.get("errno") or errno.EACCES)
            if code == errno.ENOENT:
                self.count(op + "_missing")
                self.note_missing(str(req.get("path")))
                raise FileNotFoundError(code, str(out["error"]), req.get("path"))
            self.count(op + "_refused")
            raise PermissionError(code, str(out["error"]), req.get("path"))
        self.count(op)
        return out

    def _audit(self, event: str, *args: object) -> None:
        """The audit event the plain call raises, marked as the shim's own so
        :meth:`exec_hook` lets it through (the runner's guards still see it)."""
        self._own.on = True
        try:
            sys.audit(event, *args)
        finally:
            self._own.on = False

    def _plain_look(
        self, look: Callable[[os.stat_result], bool], path: Any, *, follow: bool = True
    ) -> bool:
        """A lookup the plain call serves, answered as the plain one is but for a refusal,
        which is raised: inside the AppContainer a folder without a grant answers
        ``PermissionError`` (a share this machine serves, mapped back to its folder, is one),
        and ``os.path.isfile`` and the like would answer False, as if the file were not there."""
        try:
            st = (self.stat if follow else self.lstat)(path)
        except PermissionError:
            raise
        except (OSError, ValueError):
            self.count("plain_missing")
            self.note_missing(path)
            return False
        self.count("plain")
        return look(st)

    # -- the calls routed -------------------------------------------------------------

    def open_(self, file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        p = self.route(file)
        if p is None:
            return self.open(file, mode, *args, **kwargs)
        write = any(c in mode for c in "wax+")
        self._audit("open", p, mode, 0)
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
        p = self.route(path)
        if p is None:
            return self.stat(path, *args, **kwargs)
        return self._stat(p)

    def lstat_(self, path: Any, *args: Any, **kwargs: Any) -> os.stat_result:
        p = self.route(path)
        if p is None:
            return self.lstat(path, *args, **kwargs)
        return self._stat(p)

    def _stat(self, p: str) -> os.stat_result:
        self._audit("os.stat", p)
        out = self.ask({"op": "stat", "path": p})
        mode = (stat_mod.S_IFDIR | 0o555) if out.get("dir") else (stat_mod.S_IFREG | 0o444)
        ns = int(out.get("mtime_ns") or 0)
        size = int(out.get("size") or 0)
        sec = ns // 1_000_000_000
        return os.stat_result((mode, 0, 0, 1, 0, 0, size, sec, sec, sec))

    # Python 3.12 on Windows answers these without os.stat (nt._path_exists and the like).
    # A missing file is False, as the plain calls answer; a refusal is raised: answered
    # False, a stage would go on as if the file were absent (a thumbnail not found, a job
    # done with the wrong result) instead of running in the worker (sandbox_refused).
    def exists_(self, path: Any) -> bool:
        if self.route(path) is None:
            return self._plain_look(lambda _st: True, path)
        try:
            self.stat_(path)
        except PermissionError:
            raise  # refused, not absent: the job runs in the worker (sandbox_refused)
        except (OSError, ValueError):
            return False
        return True

    def lexists_(self, path: Any) -> bool:
        if self.route(path) is None:
            return self._plain_look(lambda _st: True, path, follow=False)
        try:
            self.lstat_(path)
        except PermissionError:
            raise  # refused, not absent: the job runs in the worker (sandbox_refused)
        except (OSError, ValueError):
            return False
        return True

    def isfile_(self, path: Any) -> bool:
        if self.route(path) is None:
            return self._plain_look(lambda st: stat_mod.S_ISREG(st.st_mode), path)
        try:
            return stat_mod.S_ISREG(self.stat_(path).st_mode)
        except PermissionError:
            raise
        except (OSError, ValueError):
            return False

    def isdir_(self, path: Any) -> bool:
        if self.route(path) is None:
            return self._plain_look(lambda st: stat_mod.S_ISDIR(st.st_mode), path)
        try:
            return stat_mod.S_ISDIR(self.stat_(path).st_mode)
        except PermissionError:
            raise
        except (OSError, ValueError):
            return False

    # these answer without os.stat on Windows too: a share path goes through the broker (a
    # brokered file is never a link: the broker refuses one, which raises here)
    def islink_(self, path: Any) -> bool:
        if self.route(path) is None:
            return self.islink(path)
        self.exists_(path)
        return False

    def isjunction_(self, path: Any) -> bool:
        if self.route(path) is None:
            return bool(self.isjunction and self.isjunction(path))
        self.exists_(path)
        return False

    def access_(self, path: Any, mode: int, *args: Any, **kwargs: Any) -> bool:
        if self.route(path) is None:
            return self.access(path, mode, *args, **kwargs)
        if mode & (os.W_OK | os.X_OK):
            self.count("unrouted_refused")
            raise PermissionError(
                errno.EACCES, f"{path}: only a read is asked of the broker", os.fsdecode(path)
            )
        return self.exists_(path)

    def listdir_(self, path: Any = ".") -> list[Any]:
        p = self.route(path)
        if p is None:
            return self.listdir(path)
        self._audit("os.listdir", p)
        names = [str(n) for n in self.ask({"op": "list", "path": p}).get("names") or []]
        return [os.fsencode(n) for n in names] if isinstance(path, bytes) else names

    def scandir_(self, path: Any = ".") -> Any:
        """Not brokered (its entries would stat behind the broker's back): a brokered folder
        is refused, so the job runs in the worker instead of seeing an empty or missing one."""
        p = self.route(path)
        if p is None:
            return self.scandir(path)
        self._audit("os.scandir", p)
        raise PermissionError(errno.EACCES, f"{p}: a share folder is listed with os.listdir")

    def _move(
        self, plain: Callable[..., None], src: Any, dst: Any, *args: Any, **kwargs: Any
    ) -> None:
        s, d = self.route(src), self.route(dst)
        if s is None and d is None:
            plain(src, dst, *args, **kwargs)
            return
        self._audit("os.rename", os.fsdecode(src), os.fsdecode(dst), -1, -1)
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
        p = self.route(path)
        if p is None:
            plain(path, *args, **kwargs)
            return
        self._audit("os.remove", p, -1)
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
            shim._audit("open", path, "rb", 0)
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
        runs in the worker; and so is a file call on a share path that the shim does not
        route (``os.open``, ``glob``, ``os.mkdir``, a ``shutil`` copy...): inside the
        AppContainer it would fail as if the file were not there, and a stage would go on
        with the wrong answer."""
        if event in _FILE_EVENTS:
            if getattr(self._own, "on", False):
                return
            for a in args[: _FILE_EVENTS[event]]:
                if not isinstance(a, (str, bytes, os.PathLike)):
                    continue
                try:
                    text = os.fsdecode(a)
                except (TypeError, ValueError):
                    continue
                if self.under(text) is not None or is_share(text):
                    self.count("unrouted_refused")
                    raise PermissionError(
                        errno.EACCES,
                        f"{text}: {event} on a share path is not brokered"
                        "; the worker runs this job",
                        text,
                    )
            return
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


#: the audit events of file calls (and how many of their first arguments are paths) that
#: the shim does not route itself: on a share path each is refused (:meth:`_Shim.exec_hook`)
_FILE_EVENTS: dict[str, int] = {
    "open": 1,  # os.open, io.open_code, and any open the shim did not route
    "os.scandir": 1,
    "os.listdir": 1,
    "os.mkdir": 1,
    "os.rmdir": 1,
    "os.remove": 1,
    "os.rename": 2,
    "os.chmod": 1,
    "os.chown": 1,
    "os.utime": 1,
    "os.truncate": 1,
    "os.link": 2,
    "os.symlink": 2,
    "os.startfile": 1,
    "os.chdir": 1,
    "glob.glob": 1,
    "glob.glob/2": 1,
    "shutil.copyfile": 2,
    "shutil.copymode": 2,
    "shutil.copystat": 2,
    "shutil.copytree": 2,
    "shutil.rmtree": 1,
    "shutil.move": 2,
}


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
    os.scandir = shim.scandir_
    os.replace = shim.replace_
    os.rename = shim.rename_
    os.remove = shim.remove_
    os.unlink = shim.unlink_
    os.path.exists = shim.exists_
    os.path.lexists = shim.lexists_
    os.path.isfile = shim.isfile_
    os.path.isdir = shim.isdir_  # type: ignore[assignment]
    os.path.islink = shim.islink_
    if shim.isjunction is not None:
        os.path.isjunction = shim.isjunction_
    os.access = shim.access_
    subprocess.Popen.__init__ = shim.popen_init(subprocess.Popen.__init__)  # type: ignore[method-assign]
    sys.addaudithook(shim.exec_hook)


def _no_broker(req: dict[str, Any]) -> dict[str, Any]:
    return {"error": "this runner has no broker", "errno": errno.EACCES}


def install_unbrokered(*, fdopen: Callable[[int, int], int] | None = None) -> None:
    """:func:`install` with no broker and no folders: every share path is refused
    (:meth:`_Shim.route`), so a runner whose launcher brokers nothing (it writes its
    libraries in full) hands a share job to the worker instead of reading the share as
    empty or a file there as missing. A no-op once a broker is installed."""
    install(_no_broker, [], fdopen=fdopen)


def take_counts() -> dict[str, int]:
    """What the broker and the plain lookups answered since the last call (the runner's job
    line), emptied; {} when no shim is installed. Jobs that run at once in one runner share
    the counts."""
    if not _installed:
        return {}
    shim = _installed[0]
    out, shim.counts = shim.counts, {}
    return out


def take_missing() -> list[str]:
    """The first paths a lookup found missing since the last call (at most 3), emptied:
    the runner's job line names them, so a stage that found nothing says where it looked."""
    if not _installed:
        return []
    shim = _installed[0]
    out, shim.missing = shim.missing, []
    return out


def install_from_env(environ: dict[str, str] | None = None) -> bool:
    """:func:`install` with the launcher's pipe and folders, when it named any."""
    env = os.environ if environ is None else environ
    pipe = env.get(PIPE_ENV, "")
    if not pipe:
        return False
    roots = [str(r.get("path") or "") for r in json.loads(env.get(ROOTS_ENV) or "[]")]
    install(Broker(pipe), roots)
    return True
