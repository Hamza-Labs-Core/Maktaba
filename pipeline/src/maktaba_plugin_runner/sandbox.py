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
- **Windows**: no runner (the stages run in the worker there, as before); see
  :mod:`maktaba_pipeline.plugins.native`.

A runner that cannot apply its sandbox does not run (:class:`SandboxUnavailable`).
"""

from __future__ import annotations

import ctypes
import ctypes.util
import os
import platform
import shutil
import struct
import sys
import sysconfig
from dataclasses import dataclass, field

__all__ = ["Applied", "Policy", "SandboxUnavailable", "apply", "seatbelt_profile"]


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


def tool_path(name: str) -> str | None:
    """A tool the group may run, as this machine has it (Epic 51 assets on PATH)."""
    env = {
        "ffmpeg": "MAKTABA_FFMPEG",
        "ffprobe": "MAKTABA_FFPROBE",
        "fpcalc": "MAKTABA_FPCALC",
        "yt-dlp": "MAKTABA_YTDLP",
    }
    explicit = os.environ.get(env.get(name, ""), "").strip()
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
    if p.exec:
        lines.append("(allow process-fork)")
        for tool in p.exec:
            lines.append(f"(allow process-exec (literal {_q(tool)}))")
            lines.append(f"(allow file-read* (literal {_q(tool)}))")
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
            pfd = os.open(path, os.O_PATH | os.O_CLOEXEC)  # type: ignore[attr-defined]
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
    for path in [x for x in (p.run_dir, *p.write) if x]:
        rule(
            path,
            _READ | _WRITE | (_FS_REFER if abi >= 2 else 0) | (_FS_TRUNCATE if abi >= 3 else 0),
        )
    for path in p.exec:
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


def apply(p: Policy) -> Applied:
    """Apply the group's sandbox to this process, for good."""
    if sys.platform == "darwin":
        return _apply_darwin(p)
    if sys.platform.startswith("linux"):
        return _apply_linux(p)
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
    (``library``, ``data.thumbnails`` …) to this machine's folders (``;``-separated)."""
    p = Policy(api_socket=api_socket, own_socket=own_socket, run_dir=run_dir)

    def paths(area: str) -> list[str]:
        return [x for x in areas.get(area, "").split(";") if x]

    for t in trusted:
        files = t.get("files") or {}
        assert isinstance(files, dict)
        for a in files.get("read") or ():
            p.read += paths(str(a))
        for a in files.get("write") or ():
            p.write += paths(str(a))
        for tool in t.get("exec") or ():  # type: ignore[attr-defined]
            found = tool_path(str(tool))
            if found:
                p.exec.append(found)
        p.net |= bool(t.get("net"))
        p.gpu |= str(t.get("gpu") or "none") != "none"
    p.read, p.write, p.exec = sorted(set(p.read)), sorted(set(p.write)), sorted(set(p.exec))
    return p
