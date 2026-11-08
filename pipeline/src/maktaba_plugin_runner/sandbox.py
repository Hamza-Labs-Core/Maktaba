"""The runner's OS sandbox, applied to itself before any plugin is imported (story 54.23 §3).

Least privilege from the union of the group's ``[trusted]`` tables (:class:`Policy`):

- **macOS**: a Seatbelt profile (the ``plugind.sb`` approach) given to
  ``sandbox_init_with_parameters``: nothing by default; read the Python installation and
  the ``files.read`` areas; read and write the run folder and the ``files.write`` areas;
  run only the ``exec`` tools; the network only to the plugin API's and the runner's own
  unix sockets, plus outbound for a group with ``net``.
- **Linux**: Landlock for files (read, write and execute rules per path) and, from ABI 4,
  TCP; seccomp-bpf refusing ``socket(AF_INET/AF_INET6)`` unless the group has ``net``, and
  ``ptrace``, ``bpf``, ``mount`` and ``keyctl`` always. Without Landlock the runner runs
  *Degraded* ``sandbox_partial`` when seccomp is in force (as 54.03).
- **Windows**: a process cannot put itself in an AppContainer, so the supervisor's
  launcher (``maktaba-server runner-sandbox-run``) applies the policy from outside: it runs
  ``--policy`` (:func:`policy_doc`), grants the group's own profile
  (``Maktaba.Runner.<group>``) read on the interpreter, the ``files.read`` areas and the
  tools' installations and full control on the run folder and the ``files.write`` areas,
  and starts the runner in that AppContainer with no capability (the Windows Filtering
  Platform blocks every network path) or, for a group with ``net``, internetClient alone,
  in a Job Object that allows no child process to a group without ``exec``. Here the
  runner checks that its token is that AppContainer's with no other capability
  (:func:`_apply_windows`), and since every AppContainer may run the system's programs, a
  group with ``exec`` runs only its own tools (:func:`guard_exec`). ``library.subtitles``
  is kept to subtitle names by :func:`guard_names`, as on Linux. Its sockets are the
  supervisor's pipes (:mod:`.winpipe`).

A runner that cannot apply its sandbox does not run (:class:`SandboxUnavailable`).
"""

from __future__ import annotations

import contextvars
import ctypes
import ctypes.util
import importlib.util
import os
import platform
import re
import shlex
import shutil
import struct
import sys
import sysconfig
import threading
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from maktaba_plugin_sdk.native.stmtdb import PluginRefused

__all__ = [
    "JOB_SCOPE",
    "Applied",
    "Policy",
    "SandboxUnavailable",
    "apply",
    "guard_exec",
    "guard_scope",
    "policy_doc",
    "seatbelt_profile",
]


class SandboxUnavailable(RuntimeError):
    """The sandbox could not be applied; ``step`` says which part."""

    def __init__(self, step: str, detail: str = "") -> None:
        super().__init__(
            f"sandbox_unavailable: {step}: {detail}" if detail else f"sandbox_unavailable: {step}"
        )
        self.step = step


@dataclass
class Policy:
    read: list[str] = field(default_factory=list)
    write: list[str] = field(default_factory=list)
    exec: list[str] = field(default_factory=list)
    net: bool = False
    gpu: bool = False
    #: the unix sockets: the plugin API's (out) and the runner's own (in)
    api_socket: str = ""
    own_socket: str = ""
    run_dir: str = ""
    #: library.subtitles (story 54.29 §3): folders where only a subtitle file Maktaba writes
    #: (SUBTITLE_NAME) may be written: Seatbelt matches the name; Landlock cannot, so the
    #: runner's own guard (guard_names) refuses any other name there, on both systems
    write_named: list[str] = field(default_factory=list)


#: a subtitle file Maktaba writes next to a video, ``<video stem>.<lang>.srt|vtt``, and its
#: temporary file while it is written (one pattern for Python and Seatbelt, whose bracket
#: expressions need the ``-`` first and which has no ``{m,n}``)
SUBTITLE_NAME = r"[^/]+\.[-A-Za-z0-9_]{1,35}\.(srt|vtt)(\.maktaba-tmp)?"


def _rx(path: str) -> str:
    """A path as a literal in a Seatbelt regex."""
    return "".join("\\" + c if c in ".^$*+?()[]{}|\\" else c for c in path)


def _names_check(
    roots: list[str], normcase: Callable[[str], str] = os.path.normcase
) -> Callable[[object], None]:
    """The check of :func:`guard_names`: a path under ``roots`` (compared as the OS names
    paths: any case on Windows) must have a subtitle name, as it was written."""
    name = re.compile("^" + SUBTITLE_NAME + "$")
    real = [normcase(os.path.realpath(r)) for r in roots]

    def check(path: object) -> None:
        if path is None or isinstance(path, int):
            return
        try:
            a = os.path.abspath(os.fsdecode(path))  # type: ignore[arg-type]
        except (TypeError, ValueError):
            return
        k = normcase(a)
        if any(k == r or k.startswith(r.rstrip(os.sep) + os.sep) for r in real) and not name.match(
            os.path.basename(a)
        ):
            raise PluginRefused(403, "plugin_scope", f"{a}: only subtitle files are written here")

    return check


def guard_names(roots: list[str]) -> None:
    """Refuse (``plugin_scope``) a write under ``roots`` to any name but SUBTITLE_NAME: an
    audit hook on every open for writing, rename, remove and link (story 54.29 §3). Landlock
    and an AppContainer's folder grants cannot match a name; this does, on both."""
    if not roots:
        return
    check = _names_check(roots)
    writes = os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND

    def hook(event: str, args: tuple[object, ...]) -> None:
        if event == "open":
            path, mode, flags = args
            if (isinstance(mode, str) and any(c in mode for c in "wax+")) or (
                isinstance(flags, int) and flags & writes
            ):
                check(path)
        elif event in ("os.rename", "os.link", "os.symlink"):
            check(args[0] if event == "os.rename" else None)
            check(args[1])
        elif event in ("os.remove", "os.rmdir", "os.mkdir", "os.truncate", "os.utime"):
            check(args[0])

    sys.addaudithook(hook)


#: the running job's :class:`~maktaba_pipeline.library_mgmt.scope.JobScope`, or None: set
#: by the runner around a job of a scoped stage (asyncio tasks and ``to_thread`` copy it)
JOB_SCOPE: contextvars.ContextVar[Any] = contextvars.ContextVar("maktaba_job_scope", default=None)

_in_scope_hook = threading.local()
_scope_guarded: list[bool] = []


def guard_scope() -> None:
    """Refuse (``plugin_scope``) a change to a library file that the running job's scope
    (:data:`JOB_SCOPE`) does not allow: an audit hook on every open for writing, rename,
    remove, link, truncate, chmod, utime and tree removal (story 54.32 §2). A job with no
    scope is not checked, nor a path outside the scope's library roots or in their trash.
    Each refusal is also kept on the scope, so the job is refused even when its code caught
    the error. Installed once per process (an audit hook is for good)."""
    if _scope_guarded:
        return
    _scope_guarded.append(True)
    writes = os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND

    def check(path: object, dir_fd: object = None) -> None:
        scope = JOB_SCOPE.get()
        if scope is None or path is None or isinstance(path, int):
            return
        try:
            p = os.fsdecode(path)  # type: ignore[arg-type]
        except (TypeError, ValueError):
            return
        if dir_fd is not None and not os.path.isabs(p):
            return  # relative to an open folder: shutil.rmtree's own steps, checked whole
        if getattr(_in_scope_hook, "on", False):
            return
        _in_scope_hook.on = True
        try:
            from maktaba_pipeline.library_mgmt.scope import key

            k = key(p)
            if not scope.governs(k) or scope.allows(k):
                return
            scope.refused.append(k)
        finally:
            _in_scope_hook.on = False
        raise PluginRefused(
            403, "plugin_scope", f"{k}: not a file of this {scope.stage} job's subject"
        )

    def hook(event: str, args: tuple[object, ...]) -> None:
        if event == "open":
            path, mode, flags = args
            if (isinstance(mode, str) and any(c in mode for c in "wax+")) or (
                isinstance(flags, int) and flags & writes
            ):
                check(path)
        elif event == "os.rename":
            check(args[0], args[2])
            check(args[1], args[3])
        elif event in ("os.link", "os.symlink"):
            check(args[1], args[-1])
        elif event in ("os.remove", "os.chmod", "os.utime"):
            check(args[0], args[-1])
        elif event == "os.truncate":
            check(args[0])
        elif event == "shutil.rmtree":
            check(args[0], args[1])

    sys.addaudithook(hook)


@dataclass(frozen=True)
class Applied:
    level: str  # "full" | "partial"
    missing: tuple[str, ...] = ()


def python_paths() -> list[str]:
    """What the interpreter itself reads: its installation, its packages, sys.path."""
    out = {
        sys.prefix,
        sys.base_prefix,
        sys.exec_prefix,
        os.path.dirname(os.path.realpath(sys.executable)),
    }
    for k in ("stdlib", "platstdlib", "purelib", "platlib"):
        with _ignore():
            out.add(sysconfig.get_path(k))
    for p in sys.path:
        if p and os.path.isdir(p):
            out.add(os.path.realpath(p))
    return sorted(x for x in out if x)


class _ignore:
    def __enter__(self) -> None:
        return None

    def __exit__(self, *exc: object) -> bool:
        return True


#: package managers whose tools load libraries from elsewhere in their prefix
_TOOL_PREFIXES = ("/opt/homebrew", "/usr/local", "/opt/local")


def tool_prefixes(tool: str) -> list[str]:
    """What a tool reads to start: its installation (the folder above its ``bin``), or the
    whole prefix of a package manager that links its libraries from other packages."""
    real = os.path.realpath(tool)
    for pm in _TOOL_PREFIXES:
        if real.startswith(pm + "/"):
            return [pm]
    here = os.path.dirname(real)
    up = os.path.dirname(here) if os.path.basename(here) == "bin" else here
    # never a filesystem root (a tool at /ffmpeg, or C:\bin\ffmpeg.exe on Windows)
    return [up] if up not in ("/", "/usr", "") and os.path.dirname(up) != up else [real]


def tool_path(name: str) -> str | None:
    """A tool the group may run, as this machine has it (Epic 51 assets on PATH)."""
    env = {
        "ffmpeg": "MAKTABA_FFMPEG",
        "ffprobe": "MAKTABA_FFPROBE",
        "fpcalc": "MAKTABA_FPCALC",
        "yt-dlp": "MAKTABA_YTDLP",
    }
    explicit = os.environ.get(env.get(name, ""), "").strip()
    if name == "yt-dlp":
        # what the stages run (integrations.youtube_download.ytdlp_command): MAKTABA_YTDLP
        # is a command line, else this Python's yt_dlp module (``python -m yt_dlp``), so the
        # tool is the command's program, else this interpreter when the module is there; a
        # yt-dlp on PATH only when neither is. Its children keep this sandbox.
        if explicit:
            argv = shlex.split(explicit)
            found = (shutil.which(argv[0]) or argv[0]) if argv else None
        elif importlib.util.find_spec("yt_dlp") is not None:
            found = sys.executable
        else:
            found = shutil.which(name)
        return os.path.realpath(found) if found else None
    found = explicit or shutil.which(name)
    return os.path.realpath(found) if found else None


def _q(s: str) -> str:
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def seatbelt_profile(p: Policy) -> str:
    """The macOS profile of a group (tools/ci/tests check it names no TCP or host)."""
    lines = [
        ";; maktaba-plugin-runner (Epic 54, story 54.23 §3): generated from the group's manifests",
        "(version 1)",
        "(deny default)",
        "(allow process-info* (target self))",
        "(allow signal (target self))",
        "(allow sysctl-read)",
        "(allow file-read-metadata)",
        '(allow file-read* (literal "/dev/urandom") (literal "/dev/random") (literal "/dev/null"))',
        '(allow file-write-data (literal "/dev/null"))',
        ";; the interpreter, its libraries and the system's shared libraries",
        '(allow file-read* (subpath "/usr/lib") (subpath "/System") (subpath "/Library/Preferences")'  # noqa: E501
        ' (subpath "/private/var/db/timezone") (subpath "/usr/share") (subpath "/private/etc"))',
        '(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))',
    ]
    for path in python_paths():
        lines.append(f"(allow file-read* (subpath {_q(path)}))")
    for path in p.read:
        lines.append(f"(allow file-read* (subpath {_q(path)}))")
    for path in [p.run_dir, *p.write]:
        if path:
            lines.append(f"(allow file-read* file-write* (subpath {_q(path)}))")
    for path in p.write_named:
        # Seatbelt's regex has no bounded repeat: the guard checks the language's length
        rx = "^" + _rx(path.rstrip("/")) + "/(.*/)?" + SUBTITLE_NAME.replace("{1,35}", "+") + "$"
        lines.append(f'(allow file-write* (regex #"{rx}"))')
    if p.exec:
        lines.append("(allow process-fork)")
        # a child's loader reads the root folder's entry (not what is under it) and maps the
        # system's libraries; a tool's own libraries are in its installation
        lines.append('(allow file-read-data (literal "/"))')
        lines.append('(allow file-map-executable (subpath "/usr/lib") (subpath "/System"))')
        for tool in p.exec:
            lines.append(f"(allow process-exec (literal {_q(tool)}))")
            for prefix in tool_prefixes(tool):
                lines.append(f"(allow file-read* file-map-executable (subpath {_q(prefix)}))")
    else:
        lines.append("(deny process-fork)")
    if p.gpu:
        lines += [
            '(allow iokit-open (iokit-user-client-class "AGXDeviceUserClient") (iokit-user-client-class "IOSurfaceRootUserClient"))',  # noqa: E501
            '(allow mach-lookup (global-name "com.apple.MTLCompilerService"))',
            "(allow iokit-get-properties)",
        ]
    lines.append(";; the network: the plugin API's socket and this runner's own")
    if p.api_socket:
        lines.append(
            f"(allow network-outbound (remote unix-socket (path-literal {_q(p.api_socket)})))"
        )
    if p.own_socket:
        lines.append(
            f"(allow network-bind network-inbound (local unix-socket (path-literal {_q(p.own_socket)})))"  # noqa: E501
        )
        lines.append(f"(allow file-read* file-write* (literal {_q(p.own_socket)}))")
    if p.api_socket:
        lines.append(f"(allow file-read* file-write* (literal {_q(p.api_socket)}))")
    if p.net:
        lines.append("(allow network-outbound)")
        lines.append('(allow mach-lookup (global-name "com.apple.dnssd.service"))')
    return "\n".join(lines) + "\n"


def _apply_darwin(p: Policy) -> Applied:
    lib = ctypes.CDLL(ctypes.util.find_library("sandbox") or "/usr/lib/libsandbox.1.dylib")
    fn = lib.sandbox_init_with_parameters
    fn.argtypes = [
        ctypes.c_char_p,
        ctypes.c_uint64,
        ctypes.POINTER(ctypes.c_char_p),
        ctypes.POINTER(ctypes.c_char_p),
    ]
    fn.restype = ctypes.c_int
    err = ctypes.c_char_p()
    params = (ctypes.c_char_p * 1)(None)
    if fn(seatbelt_profile(p).encode(), 0, params, ctypes.byref(err)) != 0:
        raise SandboxUnavailable("seatbelt", (err.value or b"").decode(errors="replace"))
    return Applied("full")


# --- Linux: Landlock and seccomp ---------------------------------------------------------

_LL_CREATE, _LL_ADD, _LL_RESTRICT = 444, 445, 446
_FS_EXECUTE, _FS_WRITE, _FS_READ_FILE, _FS_READ_DIR = 1, 2, 4, 8
_FS_REMOVE_DIR, _FS_REMOVE_FILE, _FS_MAKE_CHAR, _FS_MAKE_DIR = 16, 32, 64, 128
_FS_MAKE_REG, _FS_MAKE_SOCK, _FS_MAKE_FIFO, _FS_MAKE_BLOCK, _FS_MAKE_SYM = (
    256,
    512,
    1024,
    2048,
    4096,
)
_FS_REFER, _FS_TRUNCATE = 8192, 16384
_READ = _FS_READ_FILE | _FS_READ_DIR
_WRITE = (
    _FS_WRITE
    | _FS_REMOVE_DIR
    | _FS_REMOVE_FILE
    | _FS_MAKE_DIR
    | _FS_MAKE_REG
    | _FS_MAKE_SOCK
    | _FS_MAKE_FIFO
    | _FS_MAKE_SYM
)

_SECCOMP = {
    # arch: (AUDIT_ARCH, socket, ptrace, bpf, mount, keyctl)
    "x86_64": (0xC000003E, 41, 101, 321, 165, 250),
    "aarch64": (0xC00000B7, 198, 117, 280, 40, 219),
}


def _syscall() -> ctypes._CFuncPtr:  # type: ignore[name-defined]
    libc = ctypes.CDLL(None, use_errno=True)
    f = libc.syscall
    f.restype = ctypes.c_long
    return f


def _landlock(p: Policy) -> tuple[bool, list[str]]:
    sc = _syscall()
    abi = sc(_LL_CREATE, None, ctypes.c_size_t(0), ctypes.c_uint32(1))
    if abi < 1:
        return False, ["landlock"]
    handled = _READ | _WRITE | _FS_EXECUTE | _FS_MAKE_CHAR | _FS_MAKE_BLOCK
    if abi >= 2:
        handled |= _FS_REFER
    if abi >= 3:
        handled |= _FS_TRUNCATE
    handled_net = 3 if abi >= 4 and not p.net else 0  # bind and connect TCP: none allowed
    attr = struct.pack("=QQ", handled, handled_net)
    buf = ctypes.create_string_buffer(attr)
    fd = sc(_LL_CREATE, buf, ctypes.c_size_t(len(attr) if abi >= 4 else 8), ctypes.c_uint32(0))
    if fd < 0:
        return False, ["landlock"]
    missing: list[str] = [] if abi >= 4 or p.net else ["landlock_net"]

    def rule(path: str, access: int) -> None:
        try:
            pfd = os.open(path, os.O_PATH | os.O_CLOEXEC)  # type: ignore[attr-defined,unused-ignore]
        except OSError:
            return  # a path this machine does not have
        try:
            allowed = access & handled
            if not os.path.isdir(path):
                allowed &= _FS_EXECUTE | _FS_WRITE | _FS_READ_FILE | _FS_TRUNCATE
            b = ctypes.create_string_buffer(struct.pack("=Qi", allowed, pfd))
            if sc(_LL_ADD, ctypes.c_int(fd), ctypes.c_int(1), b, ctypes.c_uint32(0)) != 0:
                raise SandboxUnavailable("landlock_rule", path)
        finally:
            os.close(pfd)

    for path in [
        "/usr",
        "/lib",
        "/lib64",
        "/etc",
        "/proc/self",
        "/sys/devices/system/cpu",
        *python_paths(),
        *p.read,
    ]:
        # read, never run: only the group's own tools (and the dynamic loader they need)
        # may be executed, so a plugin cannot start a shell
        rule(path, _READ)
    if p.exec:
        import glob

        for ld in sorted(set(glob.glob("/lib*/ld-linux*") + glob.glob("/usr/lib*/ld-linux*"))):
            rule(os.path.realpath(ld), _FS_EXECUTE | _FS_READ_FILE)
    for path in ["/dev/null", "/dev/urandom"]:
        rule(path, _FS_READ_FILE | _FS_WRITE)
    # Landlock matches folders, not names: guard_names keeps library.subtitles to its names
    for path in [x for x in (p.run_dir, *p.write, *p.write_named) if x]:
        rule(
            path,
            _READ | _WRITE | (_FS_REFER if abi >= 2 else 0) | (_FS_TRUNCATE if abi >= 3 else 0),
        )
    for path in p.exec:
        for prefix in tool_prefixes(path):
            rule(prefix, _READ)  # its own libraries (a tool outside /usr)
        rule(path, _FS_EXECUTE | _FS_READ_FILE)
    if p.gpu:
        for d in ("/dev/dri", "/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia0"):
            rule(d, _READ | _FS_WRITE)
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(38, 1, 0, 0, 0) != 0:  # PR_SET_NO_NEW_PRIVS
        raise SandboxUnavailable("no_new_privs")
    if sc(_LL_RESTRICT, ctypes.c_int(fd), ctypes.c_uint32(0)) != 0:
        raise SandboxUnavailable("landlock_restrict")
    os.close(fd)
    return True, missing


def _seccomp(p: Policy) -> bool:
    arch = platform.machine()
    if arch not in _SECCOMP:
        return False
    audit, nr_socket, *deny = _SECCOMP[arch]
    ld_w_abs, jeq_k, ret_k = 0x20, 0x15, 0x06
    allow, eperm = 0x7FFF0000, 0x00050000 | 1
    prog: list[tuple[int, int, int, int]] = [
        (ld_w_abs, 0, 0, 4),  # seccomp_data.arch
        (jeq_k, 1, 0, audit),
        (ret_k, 0, 0, allow),
        (ld_w_abs, 0, 0, 0),  # seccomp_data.nr
    ]
    for nr in deny:
        prog.append((jeq_k, 0, 1, nr))
        prog.append((ret_k, 0, 0, eperm))
    if not p.net:
        prog += [
            (jeq_k, 0, 4, nr_socket),
            (ld_w_abs, 0, 0, 16),  # args[0], low word: the address family
            (jeq_k, 1, 0, 2),  # AF_INET
            (jeq_k, 0, 1, 10),  # AF_INET6
            (ret_k, 0, 0, eperm),
        ]
    prog.append((ret_k, 0, 0, allow))
    raw = b"".join(struct.pack("=HBBI", *x) for x in prog)
    filt = ctypes.create_string_buffer(raw)

    class Fprog(ctypes.Structure):
        _fields_ = [("len", ctypes.c_ushort), ("filter", ctypes.c_void_p)]

    fp = Fprog(len(prog), ctypes.cast(filt, ctypes.c_void_p))
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl(38, 1, 0, 0, 0)
    return int(libc.prctl(22, 2, ctypes.byref(fp), 0, 0)) == 0  # PR_SET_SECCOMP, FILTER


def _apply_linux(p: Policy) -> Applied:
    seccomp = _seccomp(p)
    landlock, missing = _landlock(p)
    if not seccomp:
        raise SandboxUnavailable("seccomp")
    if not landlock:
        return Applied("partial", tuple(missing))
    return Applied("full" if not missing else "partial", tuple(missing))


# --- Windows: the AppContainer the launcher started this runner in -----------------------

#: the internetClient capability (outbound internet; never loopback or the local network)
INTERNET_CLIENT = "S-1-15-3-1"
_TOKEN_IS_APPCONTAINER, _TOKEN_CAPABILITIES, _TOKEN_QUERY = 29, 30, 0x0008


def _token_appcontainer() -> tuple[bool, list[str]]:  # pragma: no cover - Windows only
    """Whether this process's token is an AppContainer's, and its capability SIDs."""
    from ctypes import wintypes

    advapi = ctypes.WinDLL("advapi32", use_last_error=True)  # type: ignore[attr-defined]
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)  # type: ignore[attr-defined]
    kernel.GetCurrentProcess.restype = wintypes.HANDLE
    token = wintypes.HANDLE()
    if not advapi.OpenProcessToken(kernel.GetCurrentProcess(), _TOKEN_QUERY, ctypes.byref(token)):
        err = ctypes.get_last_error()  # type: ignore[attr-defined]
        raise SandboxUnavailable("appcontainer", f"OpenProcessToken: {err}")
    try:
        flag, n = wintypes.DWORD(), wintypes.DWORD()
        if not advapi.GetTokenInformation(
            token, _TOKEN_IS_APPCONTAINER, ctypes.byref(flag), 4, ctypes.byref(n)
        ):
            err = ctypes.get_last_error()  # type: ignore[attr-defined]
            raise SandboxUnavailable("appcontainer", f"GetTokenInformation: {err}")
        if not flag.value:
            return False, []
        advapi.GetTokenInformation(token, _TOKEN_CAPABILITIES, None, 0, ctypes.byref(n))
        buf = ctypes.create_string_buffer(n.value or 4)
        if not advapi.GetTokenInformation(token, _TOKEN_CAPABILITIES, buf, n, ctypes.byref(n)):
            err = ctypes.get_last_error()  # type: ignore[attr-defined]
            raise SandboxUnavailable("appcontainer", f"capabilities: {err}")

        class SidAndAttributes(ctypes.Structure):
            _fields_ = [("sid", ctypes.c_void_p), ("attributes", wintypes.DWORD)]

        count = ctypes.cast(buf, ctypes.POINTER(wintypes.DWORD))[0]
        offset = ctypes.sizeof(ctypes.c_void_p)  # GroupCount, padded to the array's alignment
        groups = (SidAndAttributes * count).from_buffer(buf, offset)
        caps: list[str] = []
        for g in groups:
            text = wintypes.LPWSTR()
            if advapi.ConvertSidToStringSidW(ctypes.c_void_p(g.sid), ctypes.byref(text)):
                caps.append(str(text.value))
                kernel.LocalFree(text)
        return True, caps
    finally:
        kernel.CloseHandle(token)


def exec_allowed(program: object, allowed: list[str]) -> bool:
    """Whether ``program`` (as named, or found on PATH) is one of the group's tools, by real
    path, compared as the OS names paths."""
    if program is None:
        return False
    try:
        name = os.fsdecode(program)  # type: ignore[arg-type]
    except (TypeError, ValueError):
        return False
    found = shutil.which(name) or name
    real = os.path.normcase(os.path.realpath(found))
    return real in {os.path.normcase(os.path.realpath(a)) for a in allowed}


def _program(argv: object) -> object:
    """The program a command line runs (its first word)."""
    if isinstance(argv, (str, bytes)):
        text = os.fsdecode(argv).strip()
        if text.startswith('"'):
            return text[1:].split('"', 1)[0]
        return text.split(" ", 1)[0] if text else None
    if isinstance(argv, (list, tuple)) and argv:
        return argv[0]
    return None


def guard_exec(allowed: list[str]) -> None:
    """Refuse (``plugin_exec``) any program but the group's tools: an audit hook on every
    process start (subprocess, os.exec*, os.spawn*, os.posix_spawn), and os.system and
    os.startfile always (a shell). On Windows every AppContainer may read and so run the
    system's programs; this keeps a group with ``exec`` to its own tools."""

    def refuse(what: object) -> None:
        raise PluginRefused(403, "plugin_exec", f"{what}: not a tool of this runner group")

    def hook(event: str, args: tuple[object, ...]) -> None:
        if event == "subprocess.Popen":
            executable, argv = args[0], args[1]
            prog = executable if executable is not None else _program(argv)
            if not exec_allowed(prog, allowed):
                refuse(prog)
        elif event in ("os.exec", "os.spawn", "os.posix_spawn"):
            if not exec_allowed(args[0], allowed):
                refuse(args[0])
        elif event in ("os.system", "os.startfile"):
            refuse(args[0])

    sys.addaudithook(hook)


def _apply_windows(p: Policy) -> Applied:
    """The launcher applied the sandbox; this checks it: the token is an AppContainer's, it
    holds no capability but internetClient for a group with ``net``, and the group's tools
    are the only programs it runs."""
    inside, caps = _token_appcontainer()
    if not inside:
        raise SandboxUnavailable("appcontainer", "not started in its AppContainer")
    extra = [c for c in caps if not (p.net and c == INTERNET_CLIENT)]
    if extra:
        raise SandboxUnavailable("appcontainer_capabilities", ",".join(extra))
    guard_exec(list(p.exec))
    return Applied("full")


def policy_doc(p: Policy) -> dict[str, Any]:
    """The policy as the Windows launcher reads it (``--policy``): the group's folders and
    tools, the tools' installations, what the interpreter reads, and the Job Object's
    process limit: the runner, its interpreter when a venv's launcher starts it (Windows),
    and room for the tools only when the group may run any."""
    processes = 1
    base = getattr(sys, "_base_executable", sys.executable)
    if sys.platform == "win32" and os.path.normcase(base) != os.path.normcase(sys.executable):
        processes += 1  # Scripts\python.exe starts the base interpreter as its child
    if p.exec:
        processes += 32
    return {
        "read": list(p.read),
        "write": list(p.write),
        "write_named": list(p.write_named),
        "exec": list(p.exec),
        "net": p.net,
        "gpu": p.gpu,
        "run_dir": p.run_dir,
        "python": python_paths(),
        "tool_dirs": sorted({d for t in p.exec for d in tool_prefixes(t)}),
        "processes": processes,
    }


def apply(p: Policy) -> Applied:
    """Apply the group's sandbox to this process, for good (on Windows: check the one the
    launcher applied)."""
    if sys.platform == "darwin":
        return _apply_darwin(p)
    if sys.platform.startswith("linux"):
        return _apply_linux(p)
    if sys.platform == "win32":
        return _apply_windows(p)
    raise SandboxUnavailable("platform", sys.platform)


def policy_for(
    trusted: list[dict[str, object]],
    *,
    areas: dict[str, str],
    api_socket: str,
    own_socket: str,
    run_dir: str,
) -> Policy:
    """The union of a group's ``[trusted]`` tables; ``areas`` maps an area name
    (``library``, ``data.thumbnails`` …) to this machine's folders (``;``-separated).

    The sockets and the run folder are named by their real paths, as the areas are: a
    runner's socket outside a long data folder is under ``/tmp``, which is ``/private/tmp``
    on macOS, and Seatbelt matches the real path (a bind there was refused, so the runner
    exited 1)."""

    def real(path: str) -> str:
        return os.path.realpath(path) if path else path

    p = Policy(api_socket=real(api_socket), own_socket=real(own_socket), run_dir=real(run_dir))

    def paths(area: str) -> list[str]:
        return [x for x in areas.get(area, "").split(";") if x]

    for t in trusted:
        files = t.get("files") or {}
        assert isinstance(files, dict)
        for a in files.get("read") or ():
            p.read += paths(str(a))
        for a in files.get("write") or ():
            if str(a) == "library.subtitles":
                p.write_named += paths("library")
            else:
                p.write += paths(str(a))
        for tool in t.get("exec") or ():  # type: ignore[attr-defined]
            found = tool_path(str(tool))
            if found:
                p.exec.append(found)
        p.net |= bool(t.get("net"))
        p.gpu |= str(t.get("gpu") or "none") != "none"
    p.read, p.write, p.exec = sorted(set(p.read)), sorted(set(p.write)), sorted(set(p.exec))
    # a folder the group may write in full needs no name rule
    p.write_named = sorted(
        x
        for x in set(p.write_named)
        if not any(x == w or x.startswith(w.rstrip("/") + "/") for w in p.write)
    )
    return p
