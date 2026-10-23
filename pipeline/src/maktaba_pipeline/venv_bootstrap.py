"""Build the pipeline's Python environment from a release's wheel (Epic 36, story 36.3;
Windows: Epic 38, story 38.7).

Maktaba.app and Maktaba for Windows ship a Python (python-build-standalone 3.12), the
pipeline's wheel and its constraints file (every dependency pinned with its hashes); the
torch and MLX wheels are too big to ship, so the environment is built on first run::

    python -m maktaba_pipeline.venv_bootstrap --payload <server/pipeline>

This module runs **before the environment exists**, with that bare Python: it imports
nothing but the standard library, so it runs from the wheel itself (a wheel is a zip:
``PYTHONPATH=<the .whl> python -m maktaba_pipeline.venv_bootstrap``) or as a plain file.

What it does, in order (the contract other parts of the app rely on is
``specs/epics/36-mac-app/contract-venv.md``):

1. decide (:func:`plan`) from the stamp the last run left in the venv
   (``maktaba-venv.json``): nothing to do, build afresh, resume an interrupted build,
   repoint the venv at a moved Python, or reinstall the wheel alone;
2. ``python -m venv``; install every pin of the constraints file, each checked against
   its ``--hash`` (``--require-hashes --no-deps``: a venv's pip takes no hashes from a
   constraints file, so the pins are installed as requirements); then the wheel, checked
   against its own SHA-256;
3. check that the wheel's requirements with the asked extras are all installed, then
   write the stamp as ``ready``.

Which constraints (story 38.9): a payload may hold a second set,
``*constraints-cuda.txt``, with torch's CUDA build for Windows (2.0 GB, from PyTorch's
index) where the default set has PyPI's CPU build (124 MB). On Windows x86_64 the CUDA set
is taken when the machine's NVIDIA GPU runs the second listen (:func:`torch_build`: the
pipeline's own thresholds, asked of ``nvidia-smi``), else the default; ``--torch`` says
otherwise. Elsewhere the default set always (Linux's PyPI torch has CUDA already). A GPU
added later changes the choice, and so the constraints: the next run builds again.

Progress goes to stdout as one JSON object per line (UTF-8) and to a status file
(rewritten whole on every change) that a process that did not start this one can read.
Two runs never build at once: the second waits on a lock, then finds nothing to do.

The venv goes to ``~/Library/Application Support/Maktaba/venv`` on macOS,
``%LOCALAPPDATA%\\Maktaba\\venv`` on Windows and ``$XDG_DATA_HOME/maktaba/venv`` on Linux
(unless told otherwise); its interpreter is ``Scripts\\python.exe`` on Windows, else
``bin/python``. The lock is ``flock`` (``msvcrt.locking`` on Windows).

Stopping: SIGTERM or Ctrl-C; on Windows CTRL_BREAK (``SIGBREAK``), which is how the
supervisor asks a child to stop there (no SIGTERM ever arrives from another process). The
stamp stays ``building`` and the run exits 130; pip is stopped first (on Windows it runs in
its own process group and gets a CTRL_BREAK, else it is terminated). When the console is
closed or the user signs out, Windows ends the process as soon as its handler returns, so
the status may still say ``building``: the stamp says so too and the next run resumes.
"""

from __future__ import annotations

import argparse
import contextlib
import errno
import hashlib
import io
import json
import os
import queue
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import time
from collections.abc import Callable, Iterator
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import IO, Any, TextIO

if sys.platform == "win32":
    import msvcrt
else:
    import fcntl

__all__ = [
    "EXIT_BAD_INPUT",
    "EXIT_FAILED",
    "EXIT_INTERRUPTED",
    "EXIT_NEEDS_WORK",
    "EXIT_OK",
    "Plan",
    "Target",
    "main",
    "plan",
]

STAMP = "maktaba-venv.json"
SCHEMA = 1

EXIT_OK = 0
EXIT_FAILED = 1
EXIT_BAD_INPUT = 2
EXIT_NEEDS_WORK = 10  #: --check only: the venv is not ready for these inputs
EXIT_INTERRUPTED = 130

DEFAULT_EXTRAS = "stt,search"

#: what the second listen needs of an NVIDIA GPU, as the pipeline checks it
#: (stt/cohere_cuda.py: MIN_GPU_GB on Windows, MIN_COMPUTE, MIN_DRIVER; a test keeps them
#: equal): this module runs before the venv exists and imports nothing of the pipeline.
#: Torch's CUDA build is worth its 2 GB only where the second listen runs.
SECOND_LISTEN_MIN_GIB = 9.5
SECOND_LISTEN_MIN_COMPUTE = (7, 5)
SECOND_LISTEN_MIN_DRIVER = 580
TORCH_CHOICES = ("auto", "cpu", "cuda")
#: nvidia-smi's answer, all it is asked (before R510 it knows no compute_cap)
_SMI_QUERIES = ("name,memory.total,compute_cap,driver_version", "name,memory.total,driver_version")

#: one requirement of a constraints file starts at the line's first column
_REQ_LINE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)")

#: the signals that ask a run to stop (Ctrl-C is KeyboardInterrupt, on every platform)
_STOP_SIGNALS: tuple[signal.Signals, ...]
#: how pip and ``python -m venv`` are started
_POPEN_FLAGS: int
if sys.platform == "win32":
    # CTRL_BREAK (SIGBREAK) is the supervisor's ask on Windows, and what a console's
    # close or a sign-out becomes; SIGTERM only comes from signal.raise_signal there
    _STOP_SIGNALS = (signal.SIGTERM, signal.SIGBREAK)
    # their own process group: a CTRL_BREAK can be sent to them (and what they start)
    # alone, and one sent to this run's group does not reach them first
    _POPEN_FLAGS = subprocess.CREATE_NEW_PROCESS_GROUP
else:
    _STOP_SIGNALS = (signal.SIGTERM,)
    _POPEN_FLAGS = 0


class BootstrapError(RuntimeError):
    """A step failed; the message says which and why."""


class _Interrupted(BaseException):
    """A stop signal (or Ctrl-C) while building: the stamp stays ``building`` to resume."""


# --- inputs ------------------------------------------------------------------------------


def default_venv() -> Path:
    """``%LOCALAPPDATA%\\Maktaba\\venv`` on Windows, ``~/Library/Application
    Support/Maktaba/venv`` on macOS, else under XDG data."""
    if sys.platform == "win32":
        base = os.environ.get("LOCALAPPDATA", "").strip() or str(Path.home() / "AppData" / "Local")
        return Path(base) / "Maktaba" / "venv"
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Application Support" / "Maktaba" / "venv"
    base = os.environ.get("XDG_DATA_HOME", "").strip() or str(Path.home() / ".local" / "share")
    return Path(base) / "maktaba" / "venv"


if sys.platform == "win32":
    _DEFAULT_VENV_HELP = r"%%LOCALAPPDATA%%\Maktaba\venv"  # argparse %-formats help
elif sys.platform == "darwin":
    _DEFAULT_VENV_HELP = "~/Library/Application Support/Maktaba/venv"
else:
    _DEFAULT_VENV_HELP = "$XDG_DATA_HOME/maktaba/venv"


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        while chunk := fh.read(1 << 20):
            h.update(chunk)
    return h.hexdigest()


def named_wheel(payload: Path) -> Path | None:
    """The wheel the ``payload.json`` beside ``payload`` names (``pipeline.wheel``), when it
    names one: an app installed over an older one may keep the older wheel too (the
    Windows installer's update deletes nothing)."""
    try:
        doc = json.loads((payload.parent / "payload.json").read_text(encoding="utf-8"))
        name = doc["pipeline"]["wheel"]
    except (OSError, ValueError, KeyError, TypeError):
        return None
    if not isinstance(name, str) or not name:
        return None
    return payload / Path(name).name


def find_payload(payload: Path, torch: str = "cpu") -> tuple[Path, Path]:
    """The ``maktaba_pipeline-*.whl`` the payload.json beside ``payload`` names, else the
    one in ``payload``, and the one ``*constraints.txt``, or with ``torch="cuda"`` its
    ``*constraints-cuda.txt`` (torch's CUDA build)."""
    named = named_wheel(payload)
    if named is not None and not named.is_file():
        raise BootstrapError(
            f"payload.json names the wheel {named.name}, which {payload} does not hold"
        )
    wheels = [named] if named is not None else sorted(payload.glob("maktaba_pipeline-*.whl"))
    cons = sorted(payload.glob("*constraints.txt"))
    if len(wheels) != 1 or len(cons) != 1:
        raise BootstrapError(
            f"{payload} must hold one maktaba_pipeline wheel and one constraints file "
            f"(found {len(wheels)} and {len(cons)})"
        )
    if torch != "cuda":
        return wheels[0], cons[0]
    cuda = sorted(payload.glob("*constraints-cuda.txt"))
    if len(cuda) != 1:
        raise BootstrapError(
            f"{payload} has no constraints-cuda.txt (torch's CUDA build): this payload"
            " installs the CPU build only"
        )
    return wheels[0], cuda[0]


@dataclass(frozen=True)
class Gpu:
    """The first NVIDIA GPU, as ``nvidia-smi`` reports it (None: it does not say)."""

    name: str
    memory_gib: float
    compute: tuple[int, int] | None
    driver: str | None

    @property
    def driver_major(self) -> int | None:
        head = (self.driver or "").split(".", 1)[0]
        return int(head) if head.isdigit() else None


def _nvidia_smi() -> str | None:
    found = shutil.which("nvidia-smi")
    if found or sys.platform != "win32":
        return found
    # a service's PATH may lack it: where the driver puts it
    for base in (os.environ.get("SYSTEMROOT"), os.environ.get("PROGRAMFILES")):
        if not base:
            continue
        for p in (Path(base) / "System32", Path(base) / "NVIDIA Corporation" / "NVSMI"):
            if (p / "nvidia-smi.exe").is_file():
                return str(p / "nvidia-smi.exe")
    return None


def parse_gpu(line: str) -> Gpu | None:
    """One line of :data:`_SMI_QUERIES`' answer ("NVIDIA GeForce RTX 3080 Ti, 12288, 8.6,
    591.44", or the fallback's "name, MiB, driver"), or None."""
    f = [x.strip() for x in line.split(",")]
    if len(f) < 2 or not f[1].isdigit():
        return None
    compute = None
    driver = f[-1] if len(f) >= 3 and f[-1] not in ("", "[N/A]") else None
    if len(f) >= 4:
        major, _, minor = f[2].partition(".")
        if major.isdigit() and minor.isdigit():
            compute = (int(major), int(minor))
    return Gpu(f[0], int(f[1]) / 1024, compute, driver)


def nvidia_gpu() -> Gpu | None:
    """The first NVIDIA GPU (CUDA device 0), or None without one or its driver."""
    exe = _nvidia_smi()
    if exe is None:
        return None
    flags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    for query in _SMI_QUERIES:
        try:
            out = subprocess.run(
                [exe, f"--query-gpu={query}", "--format=csv,noheader,nounits"],
                capture_output=True,
                text=True,
                timeout=30,
                check=False,
                creationflags=flags,
            )
        except (OSError, subprocess.SubprocessError):
            return None
        if out.returncode == 0:
            lines = out.stdout.strip().splitlines()
            return parse_gpu(lines[0]) if lines else None
    return None


def second_listen_gpu(gpu: Gpu | None) -> str | None:
    """Why this GPU does not run the second listen (so torch's CPU build is enough), or
    None: its memory, compute capability and driver; one nvidia-smi does not report is
    not held against it."""
    if gpu is None:
        return "no NVIDIA GPU"
    if gpu.memory_gib < SECOND_LISTEN_MIN_GIB:
        return f"{gpu.name} has {gpu.memory_gib:.1f} GiB, under {SECOND_LISTEN_MIN_GIB:g}"
    if gpu.compute is not None and gpu.compute < SECOND_LISTEN_MIN_COMPUTE:
        c, m = gpu.compute, SECOND_LISTEN_MIN_COMPUTE
        return f"{gpu.name} is compute capability {c[0]}.{c[1]}, under {m[0]}.{m[1]}"
    if gpu.driver_major is not None and gpu.driver_major < SECOND_LISTEN_MIN_DRIVER:
        return f"the NVIDIA driver {gpu.driver} is older than R{SECOND_LISTEN_MIN_DRIVER}"
    return None


def torch_build(
    want: str, payload: Path | None, probe: Callable[[], Gpu | None] = nvidia_gpu
) -> tuple[str, str]:
    """``(cpu | cuda, why)``: torch's build for this machine. ``want`` is ``--torch``:
    ``cpu`` or ``cuda`` as asked; ``auto`` the CUDA build on Windows x86_64 when the payload
    has it and the GPU runs the second listen, else the CPU build."""
    if want in ("cpu", "cuda"):
        return want, f"--torch {want}"
    if not _windows_x86_64():
        return "cpu", "PyPI's torch (torch's CUDA build is Windows' choice)"
    if payload is None or not any(payload.glob("*constraints-cuda.txt")):
        return "cpu", "the payload has no constraints-cuda.txt"
    gpu = probe()
    why = second_listen_gpu(gpu)
    if why is not None:
        return "cpu", f"no second listen here ({why})"
    assert gpu is not None
    return "cuda", f"{gpu.name} runs the second listen"


def _windows_x86_64() -> bool:
    """Windows on x86_64: where torch's CUDA build is a choice (PyPI's is the CPU one)."""
    import platform

    return sys.platform == "win32" and platform.machine() in ("AMD64", "x86_64")


def count_requirements(constraints: Path) -> int:
    with open(constraints, encoding="utf-8") as fh:
        return sum(1 for line in fh if _REQ_LINE.match(line))


@dataclass(frozen=True)
class Target:
    """What the venv must be built from."""

    python: Path  #: the base interpreter, resolved
    python_version: str  #: its ``major.minor.micro``
    wheel: Path
    wheel_sha256: str
    constraints: Path
    constraints_sha256: str
    extras: tuple[str, ...]

    @property
    def python_minor(self) -> str:
        return ".".join(self.python_version.split(".")[:2])

    def stamp(self, state: str) -> dict[str, Any]:
        return {
            "schema": SCHEMA,
            "state": state,
            "python": str(self.python),
            "python_version": self.python_version,
            "wheel": self.wheel.name,
            "wheel_sha256": self.wheel_sha256,
            "constraints_sha256": self.constraints_sha256,
            "extras": list(self.extras),
        }


def python_version(python: Path) -> str:
    try:
        out = subprocess.run(
            [str(python), "-c", "import platform; print(platform.python_version())"],
            capture_output=True,
            text=True,
            check=True,
            timeout=60,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise BootstrapError(f"cannot run {python}: {exc}") from exc
    return out.stdout.strip()


def make_target(python: Path, wheel: Path, constraints: Path, extras: str) -> Target:
    for label, p in (("python", python), ("wheel", wheel), ("constraints", constraints)):
        if not p.is_file():
            raise BootstrapError(f"{label} {p} is not a file")
    if "--hash=sha256:" not in constraints.read_text(encoding="utf-8"):
        raise BootstrapError(f"{constraints} pins no hashes (--hash=sha256:...)")
    names = tuple(sorted({e.strip() for e in extras.split(",") if e.strip()}))
    for e in names:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", e):
            raise BootstrapError(f"not an extra: {e!r}")
    resolved = python.resolve()
    return Target(
        python=resolved,
        python_version=python_version(resolved),
        wheel=wheel.resolve(),
        wheel_sha256=sha256_file(wheel),
        constraints=constraints.resolve(),
        constraints_sha256=sha256_file(constraints),
        extras=names,
    )


# --- the decision ------------------------------------------------------------------------


@dataclass(frozen=True)
class Plan:
    """``action``: ``none`` | ``build`` (afresh, anything there removed) | ``resume`` (an
    interrupted build of the same inputs) | ``repair`` (repoint the venv at a moved or
    patched Python, then reinstall the wheel if it changed) | ``wheel`` (reinstall the
    wheel alone)."""

    action: str
    reason: str


def read_stamp(venv: Path) -> dict[str, Any] | None:
    try:
        data = json.loads((venv / STAMP).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    return data if isinstance(data, dict) and data.get("schema") == SCHEMA else None


def venv_python(venv: Path) -> Path:
    """The venv's interpreter: ``Scripts\\python.exe`` on Windows, else ``bin/python``."""
    if sys.platform == "win32":
        return venv / "Scripts" / "python.exe"
    return venv / "bin" / "python"


def _runs(python: Path) -> bool:
    try:
        return (
            subprocess.run(
                [str(python), "-c", "pass"], capture_output=True, timeout=60, check=False
            ).returncode
            == 0
        )
    except (OSError, subprocess.SubprocessError):
        return False


def plan(venv: Path, target: Target) -> Plan:
    if not venv.exists():
        return Plan("build", "no venv yet")
    stamp = read_stamp(venv)
    if stamp is None:
        return Plan("build", "a venv without a readable stamp (not built by this tool)")
    if stamp.get("constraints_sha256") != target.constraints_sha256:
        return Plan("build", "the constraints changed")
    if ".".join(str(stamp.get("python_version", "")).split(".")[:2]) != target.python_minor:
        return Plan("build", f"Python {target.python_minor} replaces {stamp.get('python_version')}")
    if stamp.get("state") != "ready":
        return Plan("resume", "a build of these constraints was interrupted")
    # a venv's interpreter starts the base Python it was made from (a symlink to it; on
    # Windows a launcher that runs pyvenv.cfg's `home`): it stops running when that moved
    if (
        stamp.get("python") != str(target.python)
        or stamp.get("python_version") != target.python_version
        or not _runs(venv_python(venv))
    ):
        return Plan("repair", "the bundled Python moved or was updated")
    if stamp.get("wheel_sha256") != target.wheel_sha256 or sorted(
        stamp.get("extras") or []
    ) != list(target.extras):
        return Plan("wheel", "the pipeline wheel changed")
    return Plan("none", "up to date")


# --- reporting ---------------------------------------------------------------------------


def _now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def _refused(exc: Exception) -> bool:
    return isinstance(exc, PermissionError)


def _while_open_elsewhere(
    op: Callable[[], None], held: Callable[[Exception], bool] = _refused
) -> None:
    """Run ``op`` (a replace or a removal); on Windows, again for up to 5 s while another
    process has the file open (a reader of the status file, a virus scanner, the search
    indexer, a program of the venv still exiting): Windows refuses to replace or remove a
    file that is open (WinError 5 / 32), where POSIX does not mind. ``held`` says whether
    a failure is that refusal."""
    delay, deadline = 0.01, time.monotonic() + 5.0
    while True:
        try:
            op()
            return
        except Exception as exc:
            if not held(exc) or sys.platform != "win32" or time.monotonic() >= deadline:
                raise
        time.sleep(delay)
        delay = min(delay * 2, 0.25)


def _removal_held(exc: Exception) -> bool:
    """A removal refused while something is open: the file itself, or (a folder) a file
    in it whose deletion waits for its last handle to close."""
    return isinstance(exc, PermissionError) or (
        isinstance(exc, OSError) and exc.errno == errno.ENOTEMPTY
    )


def _remove_tree(path: Path) -> None:
    """``shutil.rmtree``; on Windows each removal refused is tried again
    (:func:`_while_open_elsewhere`), a read-only file made writable first."""

    def again(func: Callable[[str], None], name: str, exc: BaseException) -> None:
        if (
            sys.platform != "win32"
            or not isinstance(exc, OSError)
            or not _removal_held(exc)
            or func not in (os.unlink, os.rmdir)
        ):
            raise exc
        with contextlib.suppress(OSError):
            os.chmod(name, stat.S_IWRITE)  # Windows: clears the read-only attribute
        _while_open_elsewhere(lambda: func(name), _removal_held)

    shutil.rmtree(path, onexc=again)


def write_json_atomic(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            json.dump(data, fh, indent=1)
            fh.write("\n")
        _while_open_elsewhere(lambda: os.replace(tmp, path))
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise


class Reporter:
    """Each event: a JSON line on ``out`` and the status file, rewritten whole (not while
    ``writes_file`` is off: a run waiting on the lock leaves the file to the running one)."""

    def __init__(self, status_file: Path, out: IO[str], base: dict[str, Any]) -> None:
        self.status_file = status_file
        self.out = out
        self.base = base
        self.state: dict[str, Any] = {}
        self.writes_file = True
        self._last_write = 0.0

    def emit(
        self,
        state: str,
        step: str,
        progress: float,
        message: str,
        *,
        error: str | None = None,
        throttle: bool = False,
        counts: tuple[int, int] | None = None,
    ) -> None:
        now = time.monotonic()
        if throttle and now - self._last_write < 0.5:
            return
        self._last_write = now
        self.state = {
            **self.base,
            "state": state,
            "step": step,
            "progress": round(max(0.0, min(1.0, progress)), 3),
            "message": message,
            "pid": os.getpid(),
            "updated_at": _now(),
        }
        if counts is not None:
            self.state["done"], self.state["total"] = counts
        if error is not None:
            self.state["error"] = error
        self.out.write(json.dumps({"event": "status", **self.state}, ensure_ascii=False) + "\n")
        self.out.flush()
        if self.writes_file:
            with contextlib.suppress(OSError):
                write_json_atomic(self.status_file, self.state)


def _utf8(stream: TextIO, errors: str) -> TextIO:
    """``stream`` (stdout, stderr), writing UTF-8: on Windows a pipe's default is the ANSI
    code page, which cannot write every path or pip line a message carries."""
    enc = (stream.encoding or "").lower().replace("-", "").replace("_", "")
    if enc != "utf8" and isinstance(stream, io.TextIOWrapper):
        stream.reconfigure(encoding="utf-8", errors=errors)
    return stream


# --- running pip -------------------------------------------------------------------------

#: the share of the whole run each step takes, for one progress figure
_SPAN = {"venv": (0.0, 0.05), "deps": (0.05, 0.92), "wheel": (0.92, 0.97), "verify": (0.97, 1.0)}

_PIP_SEEN = re.compile(r"^(Collecting|Requirement already satisfied:|Ignoring) ([A-Za-z0-9._-]+)")

#: how long the main thread waits for pip's next line before it looks for a stop signal
_POLL = 0.25


def _child_env() -> dict[str, str]:
    env = dict(os.environ)
    env.pop("PYTHONPATH", None)  # the wheel this may run from is not the venv's
    env.pop("PYTHONHOME", None)
    env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    env["PIP_NO_INPUT"] = "1"
    env["PYTHONUNBUFFERED"] = "1"
    # pip writes its lines (paths among them) as UTF-8 into the pipe, whatever the
    # Windows code page is, and they are read back as UTF-8
    env["PYTHONIOENCODING"] = "utf-8"
    return env


_CHILD: list[subprocess.Popen[str]] = []


def _lines(proc: subprocess.Popen[str]) -> Iterator[str]:
    """``proc``'s output lines, read by a thread: the main thread waits for each in short
    slices, so a stop signal is taken within ``_POLL`` even while pip prints nothing for
    minutes (a large download). On Windows a signal does not interrupt a blocking read."""
    assert proc.stdout is not None
    stdout = proc.stdout
    got: queue.SimpleQueue[str | None] = queue.SimpleQueue()

    def pump() -> None:
        try:
            for raw in stdout:
                got.put(raw)
        except (OSError, ValueError):
            pass  # the pipe went away under it: the process is gone
        finally:
            got.put(None)

    threading.Thread(target=pump, name="venv-bootstrap-output", daemon=True).start()
    while True:
        try:
            raw = got.get(timeout=_POLL)
        except queue.Empty:
            continue
        if raw is None:
            return
        yield raw


def run_step(
    cmd: list[str],
    rep: Reporter,
    step: str,
    message: str,
    *,
    total: int = 0,
    cwd: Path | None = None,
) -> None:
    """Run ``cmd``; its output becomes progress for ``step`` (requirements seen out of
    ``total``). Raises :class:`BootstrapError` with the output's tail on failure."""
    lo, hi = _SPAN[step]
    rep.emit("building", step, lo, message)
    tail: list[str] = []
    seen: set[str] = set()
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        encoding="utf-8",
        errors="replace",
        env=_child_env(),
        cwd=cwd,
        creationflags=_POPEN_FLAGS,
    )
    _CHILD.append(proc)
    try:
        for raw in _lines(proc):
            line = raw.rstrip()
            if not line:
                continue
            tail = [*tail[-39:], line]
            m = _PIP_SEEN.match(line)
            if m:
                seen.add(m.group(2).lower())
            frac = min(len(seen) / total, 1.0) * 0.85 if total else 0.0
            if line.startswith("Installing collected packages"):
                frac = 0.9
            rep.emit(
                "building",
                step,
                lo + (hi - lo) * frac,
                line[:200],
                throttle=True,
                counts=(min(len(seen), total), total) if total else None,
            )
        code = proc.wait()
    except BaseException:
        _stop(proc)  # a stop signal / Ctrl-C / a failure here: pip must not go on writing
        raise
    finally:
        _CHILD.remove(proc)
    if code != 0:
        raise BootstrapError(f"{step} failed (exit {code}):\n" + "\n".join(tail[-15:]))
    rep.emit("building", step, hi, f"{step} done", counts=(total, total) if total else None)


def _stop(proc: subprocess.Popen[str]) -> None:
    """Stop ``proc`` and wait for it; kill it when it does not stop in time.

    POSIX: SIGTERM, then SIGKILL after 30 s. Windows: CTRL_BREAK to its process group
    (it leads one), which also reaches what it started; when there is no console to send
    that through, TerminateProcess. Then TerminateProcess after 10 s."""
    if proc.poll() is not None:
        return
    grace = 30.0
    if sys.platform == "win32":
        grace = 10.0
        try:
            proc.send_signal(signal.CTRL_BREAK_EVENT)
        except OSError:
            with contextlib.suppress(OSError):
                proc.terminate()
    else:
        with contextlib.suppress(OSError):
            proc.terminate()
    try:
        proc.wait(timeout=grace)
    except subprocess.TimeoutExpired:
        with contextlib.suppress(OSError):
            proc.kill()
        with contextlib.suppress(subprocess.SubprocessError):
            proc.wait(timeout=30)


def _pip(venv: Path, *args: str) -> list[str]:
    return [str(venv_python(venv)), "-m", "pip", "install", "--no-color", *args]


#: run in the venv: every requirement of the wheel, with the extras asked, is installed
#: at a version it allows (pip's vendored ``packaging`` evaluates the markers)
_VERIFY = r"""
import json, sys
from importlib import metadata
from pip._vendor.packaging.requirements import Requirement
extras = json.loads(sys.argv[1])
dist = metadata.distribution("maktaba-pipeline")
missing = []
for raw in dist.requires or []:
    req = Requirement(raw)
    envs = [{"extra": e} for e in extras] + [{"extra": ""}]
    if req.marker is not None and not any(req.marker.evaluate(e) for e in envs):
        continue
    try:
        have = metadata.version(req.name)
    except metadata.PackageNotFoundError:
        missing.append(f"{req} (not installed)")
        continue
    if req.specifier and not req.specifier.contains(have, prereleases=True):
        missing.append(f"{req} (have {have})")
import maktaba_pipeline
print(json.dumps({"version": dist.version, "missing": missing}))
"""


def verify(venv: Path, target: Target, rep: Reporter) -> str:
    lo, _hi = _SPAN["verify"]
    rep.emit("building", "verify", lo, "checking the installed packages")
    try:
        out = subprocess.run(
            [str(venv_python(venv)), "-c", _VERIFY, json.dumps(list(target.extras))],
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            env=_child_env(),
            timeout=300,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise BootstrapError(f"verify failed: {exc}") from exc
    if out.returncode != 0:
        raise BootstrapError(f"verify failed: {out.stderr.strip()[-800:]}")
    try:
        result = json.loads(out.stdout.strip().splitlines()[-1])
    except (IndexError, ValueError) as exc:
        raise BootstrapError(f"verify printed no result: {out.stdout.strip()[-400:]}") from exc
    if result["missing"]:
        raise BootstrapError(
            "verify failed: the constraints do not cover "
            + ", ".join(result["missing"][:10])
            + f" (extras {','.join(target.extras) or 'none'})"
        )
    return str(result["version"])


# --- the steps ---------------------------------------------------------------------------


#: ``python -m venv`` refused to write a file of the venv that is open elsewhere (on
#: Windows ``Scripts\python.exe`` while a program of the venv still exits, or a virus
#: scanner reads it): access denied or a sharing violation
_VENV_HELD = re.compile(r"\[WinError (?:5|32)\]|\[Errno 13\]")


def _venv_held(exc: Exception) -> bool:
    return isinstance(exc, BootstrapError) and _VENV_HELD.search(str(exc)) is not None


def create_venv(venv: Path, target: Target, rep: Reporter, *, upgrade: bool = False) -> None:
    args = [str(target.python), "-m", "venv"]
    if upgrade:
        args.append("--upgrade")
        if sys.platform != "win32":
            # `venv --upgrade` keeps a symlink that is there, even one to a Python that
            # has moved away: remove them, and it links the Python it runs with. (On
            # Windows it copies its launcher over Scripts\python.exe and rewrites
            # pyvenv.cfg's `home`, which the launcher starts.)
            for link in (venv / "bin").glob("python*"):
                if link.is_symlink():
                    link.unlink()
    _while_open_elsewhere(
        lambda: run_step(
            [*args, str(venv)],
            rep,
            "venv",
            ("repointing" if upgrade else "creating")
            + f" the venv with Python {target.python_version}",
        ),
        _venv_held,
    )


def install_deps(venv: Path, target: Target, rep: Reporter) -> None:
    total = count_requirements(target.constraints)
    run_step(
        _pip(venv, "--require-hashes", "--no-deps", "-r", str(target.constraints)),
        rep,
        "deps",
        f"installing {total} pinned packages (the first run downloads ~1-2 GB)",
        total=total,
    )


def install_wheel(venv: Path, target: Target, rep: Reporter) -> None:
    extras = f"[{','.join(target.extras)}]" if target.extras else ""
    req = venv / ".maktaba-wheel-requirements.txt"
    req.write_text(
        f"maktaba-pipeline{extras} @ {target.wheel.as_uri()} --hash=sha256:{target.wheel_sha256}\n",
        encoding="utf-8",
    )
    try:
        run_step(
            _pip(venv, "--require-hashes", "--no-deps", "--force-reinstall", "-r", str(req)),
            rep,
            "wheel",
            f"installing {target.wheel.name}",
        )
    finally:
        req.unlink(missing_ok=True)


def execute(venv: Path, target: Target, p: Plan, rep: Reporter) -> str:
    """Carry out ``p``; returns the installed pipeline version."""
    if p.action == "build" and (venv.exists() or venv.is_symlink()):
        rep.emit("building", "venv", 0.0, f"removing the old venv ({p.reason})")
        if venv.is_symlink() or venv.is_file():
            venv.unlink()
        else:
            # the stamp first: a removal cut short leaves no "ready" venv behind
            _while_open_elsewhere(lambda: (venv / STAMP).unlink(missing_ok=True))
            _remove_tree(venv)
    if p.action in ("build", "resume"):
        # resuming: the venv is there, maybe made by a Python that has moved since
        py = venv_python(venv)
        create_venv(venv, target, rep, upgrade=py.exists() or py.is_symlink())
        write_json_atomic(venv / STAMP, target.stamp("building"))
        install_deps(venv, target, rep)
        install_wheel(venv, target, rep)
    elif p.action == "repair":
        create_venv(venv, target, rep, upgrade=True)
        stamp = read_stamp(venv) or {}
        if stamp.get("wheel_sha256") != target.wheel_sha256:
            install_wheel(venv, target, rep)
    elif p.action == "wheel":
        install_wheel(venv, target, rep)
    version = verify(venv, target, rep)
    write_json_atomic(venv / STAMP, {**target.stamp("ready"), "built_at": _now()})
    return version


# --- the lock ----------------------------------------------------------------------------

#: how often a run waiting on the lock tries it again (Windows)
_LOCK_POLL = 0.25

if sys.platform == "win32":

    def _try_lock(fh: IO[str]) -> bool:
        fh.seek(0)  # the lock is on the file's first byte, wherever the handle is
        try:
            msvcrt.locking(fh.fileno(), msvcrt.LK_NBLCK, 1)
        except OSError as exc:
            if exc.errno not in (errno.EACCES, errno.EDEADLOCK):
                raise
            return False
        return True

    def _wait_lock(fh: IO[str]) -> None:
        # not LK_LOCK: it gives up after ~10 s, and sleeps where a stop signal is not taken
        while not _try_lock(fh):
            time.sleep(_LOCK_POLL)

    def _unlock(fh: IO[str]) -> None:
        fh.seek(0)
        msvcrt.locking(fh.fileno(), msvcrt.LK_UNLCK, 1)

else:

    def _try_lock(fh: IO[str]) -> bool:
        try:
            fcntl.flock(fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return False
        return True

    def _wait_lock(fh: IO[str]) -> None:
        fcntl.flock(fh, fcntl.LOCK_EX)  # a signal interrupts it

    def _unlock(fh: IO[str]) -> None:
        fcntl.flock(fh, fcntl.LOCK_UN)


@contextlib.contextmanager
def locked(path: Path, rep: Reporter) -> Iterator[None]:
    """Hold ``path``'s lock (``flock``; on Windows ``msvcrt.locking``), waiting for another
    run to finish first. While it waits (and when it is stopped waiting) it reports on its
    own stdout only: the status file is the running build's."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a+") as fh:
        if not _try_lock(fh):
            rep.writes_file = False
            rep.emit("waiting", "lock", 0.0, "another build of this venv is running: waiting")
            _wait_lock(fh)
            rep.writes_file = True
        try:
            yield
        finally:
            with contextlib.suppress(OSError):  # closing the file lets go of it too
                _unlock(fh)


# --- command line ------------------------------------------------------------------------


def _parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="python -m maktaba_pipeline.venv_bootstrap",
        description="Build (or bring up to date) the pipeline's venv from a wheel and its "
        "hash-pinned constraints. Progress: JSON lines on stdout and --status-file.",
    )
    env = os.environ.get
    p.add_argument(
        "--python",
        default=env("MAKTABA_PYTHON") or sys.executable,
        help="the base interpreter (default: $MAKTABA_PYTHON, else this one)",
    )
    p.add_argument(
        "--payload",
        default=env("MAKTABA_PIPELINE_PAYLOAD"),
        help="a folder with one maktaba_pipeline-*.whl and one *constraints.txt "
        "(default: $MAKTABA_PIPELINE_PAYLOAD)",
    )
    p.add_argument("--wheel", help="the wheel (instead of --payload)")
    p.add_argument("--constraints", help="the constraints file (instead of --payload)")
    p.add_argument(
        "--venv",
        help=f"the venv to build (default: $MAKTABA_VENV, else {_DEFAULT_VENV_HELP})",
    )
    p.add_argument(
        "--extras",
        default=env("MAKTABA_PIPELINE_EXTRAS") or DEFAULT_EXTRAS,
        help=f"extras the venv must have complete (default: {DEFAULT_EXTRAS})",
    )
    p.add_argument(
        "--torch",
        choices=TORCH_CHOICES,
        default=env("MAKTABA_PIPELINE_TORCH") or "auto",
        help="with --payload, which constraints: auto (default: on Windows torch's CUDA build "
        "when the NVIDIA GPU runs the second listen, else the CPU build), cpu or cuda "
        "(default: $MAKTABA_PIPELINE_TORCH)",
    )
    p.add_argument(
        "--status-file",
        help="where the status JSON goes (default: venv-status.json beside the venv)",
    )
    p.add_argument(
        "--check",
        action="store_true",
        help="change nothing: print the plan; exit 0 when up to date, 10 when not",
    )
    p.add_argument("--force", action="store_true", help="rebuild from scratch")
    return p


def _on_stop_signal(_signum: int, _frame: Any) -> None:
    raise _Interrupted()


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    out = _utf8(sys.stdout, "strict")
    _utf8(sys.stderr, "backslashreplace")
    try:
        # the default only now: a Windows environment without a profile folder has none
        venv = Path(args.venv or os.environ.get("MAKTABA_VENV") or default_venv())
        venv = venv.expanduser().absolute()
    except RuntimeError as exc:  # "Could not determine home directory."
        sys.stderr.write(f"error: {exc} (give --venv)\n")
        return EXIT_BAD_INPUT
    status_file = (
        Path(args.status_file).expanduser()
        if args.status_file
        else venv.parent / "venv-status.json"
    )
    rep = Reporter(status_file, out, {"venv": str(venv)})
    try:
        if args.wheel or args.constraints:
            if not (args.wheel and args.constraints):
                raise BootstrapError("--wheel and --constraints go together")
            wheel, constraints = Path(args.wheel), Path(args.constraints)
        elif args.payload:
            payload = Path(args.payload).expanduser()
            build, why = torch_build(args.torch, payload)
            wheel, constraints = find_payload(payload, build)
            rep.base["torch"] = build
            rep.base["torch_why"] = why
        else:
            raise BootstrapError("give --payload, or --wheel and --constraints")
        target = make_target(Path(args.python).expanduser(), wheel, constraints, args.extras)
    except BootstrapError as exc:
        sys.stderr.write(f"error: {exc}\n")
        if not args.check:
            rep.emit("failed", "inputs", 0.0, str(exc), error=str(exc))
        return EXIT_BAD_INPUT
    rep.base.update({"wheel": target.wheel.name, "constraints_sha256": target.constraints_sha256})

    if args.check:
        p = plan(venv, target)
        if args.force and p.action == "none":
            p = Plan("build", "--force")
        out.write(json.dumps({"event": "plan", "action": p.action, "reason": p.reason}) + "\n")
        out.flush()
        return EXIT_OK if p.action == "none" else EXIT_NEEDS_WORK

    old = {sig: signal.signal(sig, _on_stop_signal) for sig in _STOP_SIGNALS}
    try:
        with locked(venv.parent / f".{venv.name}.lock", rep):
            # decided under the lock: a run that waited sees what the other one built
            p = Plan("build", "--force") if args.force else plan(venv, target)
            rep.emit("building", "plan", 0.0, f"{p.action}: {p.reason}")
            version = execute(venv, target, p, rep) if p.action != "none" else None
            done = "up to date" if version is None else f"maktaba-pipeline {version} ready"
            rep.emit("ready", "done", 1.0, done)
            return EXIT_OK
    except BootstrapError as exc:
        sys.stderr.write(f"error: {exc}\n")
        rep.emit(
            "failed",
            rep.state.get("step", "?"),
            rep.state.get("progress", 0.0),
            str(exc).splitlines()[0],
            error=str(exc),
        )
        return EXIT_FAILED
    except (_Interrupted, KeyboardInterrupt):
        for sig in _STOP_SIGNALS:
            signal.signal(sig, signal.SIG_IGN)  # asked again: we are stopping
        for proc in list(_CHILD):
            _stop(proc)
        rep.emit(
            "interrupted",
            rep.state.get("step", "?"),
            rep.state.get("progress", 0.0),
            "stopped; the next run resumes",
        )
        return EXIT_INTERRUPTED
    except Exception as exc:  # noqa: BLE001 - disk full, a pip that cannot start: reported
        sys.stderr.write(f"error: {type(exc).__name__}: {exc}\n")
        rep.emit(
            "failed",
            rep.state.get("step", "?"),
            rep.state.get("progress", 0.0),
            f"{type(exc).__name__}: {exc}"[:200],
            error=f"{type(exc).__name__}: {exc}",
        )
        return EXIT_FAILED
    finally:
        for sig, handler in old.items():
            signal.signal(sig, signal.SIG_DFL if handler is None else handler)


if __name__ == "__main__":
    raise SystemExit(main())
