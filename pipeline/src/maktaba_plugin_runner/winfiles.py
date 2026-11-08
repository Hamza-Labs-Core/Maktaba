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
(``sandbox_refused``). The lookups Windows answers without ``os.stat`` (``lexists``,
``islink``, ``isjunction``, ``os.access``) go through the broker too, and any other file call
on a brokered path (``os.scandir``, ``os.open``, ``glob``, a ``shutil`` copy...) is refused
by the audit hook: inside the AppContainer it would fail as if the file were not there. So is
every call on a share path outside the brokered folders (another share, or the long
``\\\\?\\UNC\\`` form), naming the path. What
the broker answered for each job is counted (:func:`take_counts`, the runner's job line).

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


def share_path(path: object) -> bool:
    """Whether ``path`` names a file on a share (``\\\\server\\share\\...``, either slash, or
    ``\\\\?\\UNC\\...``), never a device or a local path in the long form (``\\\\.\\pipe\\...``,
    ``\\\\?\\C:\\...``)."""
    if path is None or isinstance(path, int):
        return False
    try:
        p = os.fsdecode(path).replace("/", "\\")  # type: ignore[arg-type]
    except (TypeError, ValueError):
        return False
    if p[:8].upper() == "\\\\?\\UNC\\":
        return True
    return p.startswith("\\\\") and p[2:3] not in ("?", ".")


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
        self.lexists = os.path.lexists
        self.islink = os.path.islink
        self.isjunction = getattr(os.path, "isjunction", None)
        self.access = os.access
        #: what the broker answered since :func:`take_counts` last read it: ``<op>`` for an
        #: answer, ``<op>_missing`` for a file not there, ``<op>_refused`` for a refusal,
        #: ``unrouted_refused`` for a call on a share path the shim does not route
        self.counts: dict[str, int] = {}
        #: the first paths a look-up found missing since :func:`take_missing` (at most 3)
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

    def _route(self, path: object) -> str | None:
        """``path`` when the broker answers for it (:meth:`under`), None for a path the plain
        call serves. A share path outside the brokered folders (another share, or a brokered
        one spelled otherwise: ``\\\\?\\UNC\\...``) is refused: the AppContainer reaches no
        share, so the plain call would answer it as absent."""
        p = self.under(path)
        if p is None and share_path(path):
            self.count("unbrokered_refused")
            name = os.fsdecode(path)  # type: ignore[arg-type]
            raise PermissionError(
                errno.EACCES,
                f"{name}: a share path outside this runner's brokered folders"
                "; the worker runs this job",
                name,
            )
        return p

    def count(self, key: str) -> None:
        self.counts[key] = self.counts.get(key, 0) + 1

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

    # -- the calls routed -------------------------------------------------------------

    def open_(self, file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        p = self._route(file)
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
        p = self._route(path)
        if p is None:
            return self.stat(path, *args, **kwargs)
        return self._stat(p)

    def lstat_(self, path: Any, *args: Any, **kwargs: Any) -> os.stat_result:
        p = self._route(path)
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
    def _plain_look(
        self, look: Callable[[os.stat_result], bool], path: Any, *, follow: bool = True
    ) -> bool:
        """A look-up the plain call serves, answered as the plain one is but for a refusal,
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

    def note_missing(self, path: Any) -> None:
        if len(self.missing) < 3:
            with contextlib.suppress(TypeError, ValueError):
                self.missing.append(os.fsdecode(path))

    def exists_(self, path: Any) -> bool:
        if self._route(path) is None:
            return self._plain_look(lambda _st: True, path)
        try:
            self.stat_(path)
        except PermissionError:
            raise  # refused, not absent: the job runs in the worker (sandbox_refused)
        except (OSError, ValueError):
            return False
        return True

    def isfile_(self, path: Any) -> bool:
        if self._route(path) is None:
            return self._plain_look(lambda st: stat_mod.S_ISREG(st.st_mode), path)
        try:
            return stat_mod.S_ISREG(self.stat_(path).st_mode)
        except PermissionError:
            raise
        except (OSError, ValueError):
            return False

    def isdir_(self, path: Any) -> bool:
        if self._route(path) is None:
            return self._plain_look(lambda st: stat_mod.S_ISDIR(st.st_mode), path)
        try:
            return stat_mod.S_ISDIR(self.stat_(path).st_mode)
        except PermissionError:
            raise
        except (OSError, ValueError):
            return False

    # these answer without os.stat on Windows too: a share path goes through the broker (a
    # brokered file is never a link: the broker refuses one, which raises here)
    def lexists_(self, path: Any) -> bool:
        if self._route(path) is None:
            return self._plain_look(lambda _st: True, path, follow=False)
        return self.exists_(path)

    def islink_(self, path: Any) -> bool:
        if self._route(path) is None:
            return self.islink(path)
        self.exists_(path)
        return False

    def isjunction_(self, path: Any) -> bool:
        if self._route(path) is None:
            return bool(self.isjunction and self.isjunction(path))
        self.exists_(path)
        return False

    def access_(self, path: Any, mode: int, *args: Any, **kwargs: Any) -> bool:
        if self._route(path) is None:
            return self.access(path, mode, *args, **kwargs)
        if mode & (os.W_OK | os.X_OK):
            self.count("unrouted_refused")
            raise PermissionError(
                errno.EACCES, f"{path}: only a read is asked of the broker", os.fsdecode(path)
            )
        return self.exists_(path)

    def listdir_(self, path: Any = ".") -> list[Any]:
        p = self._route(path)
        if p is None:
            return self.listdir(path)
        self._audit("os.listdir", p)
        names = [str(n) for n in self.ask({"op": "list", "path": p}).get("names") or []]
        return [os.fsencode(n) for n in names] if isinstance(path, bytes) else names

    def _move(
        self, plain: Callable[..., None], src: Any, dst: Any, *args: Any, **kwargs: Any
    ) -> None:
        s, d = self._route(src), self._route(dst)
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
        p = self._route(path)
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
        runs in the worker; and so is a file call on a brokered path that the shim does not
        route (``os.scandir`` and so ``os.walk``/``glob``, ``os.open``, ``os.mkdir``, a
        ``shutil`` copy...): inside the AppContainer it would fail as if the file were not
        there, and a stage would go on with the wrong answer."""
        if event in _FILE_EVENTS:
            if getattr(self._own, "on", False):
                return
            for a in args[: _FILE_EVENTS[event]]:
                if isinstance(a, (str, bytes, os.PathLike)) and (
                    self.under(a) is not None or share_path(a)
                ):
                    self.count("unrouted_refused")
                    raise PermissionError(
                        errno.EACCES,
                        f"{event} on a share path is not brokered; the worker runs this job",
                        os.fsdecode(a),
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
#: the shim does not route itself: on a brokered path each is refused (:meth:`_Shim.exec_hook`)
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
    os.replace = shim.replace_
    os.rename = shim.rename_
    os.remove = shim.remove_
    os.unlink = shim.unlink_
    os.path.exists = shim.exists_
    os.path.isfile = shim.isfile_
    os.path.isdir = shim.isdir_  # type: ignore[assignment]
    os.path.lexists = shim.lexists_
    os.path.islink = shim.islink_
    if shim.isjunction is not None:
        os.path.isjunction = shim.isjunction_
    os.access = shim.access_
    subprocess.Popen.__init__ = shim.popen_init(subprocess.Popen.__init__)  # type: ignore[method-assign]
    sys.addaudithook(shim.exec_hook)


def take_counts() -> dict[str, int]:
    """What the broker answered since the last call (the runner's job line), emptied; {}
    when no broker is installed. Jobs that run at once in one runner share the counts."""
    if not _installed:
        return {}
    shim = _installed[0]
    out, shim.counts = shim.counts, {}
    return out


def take_missing() -> list[str]:
    """The first paths a look-up found missing since the last call (at most 3), emptied:
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
