"""Build the pipeline's Python environment from a release's wheel (Epic 36, story 36.3).

Maktaba.app ships a Python (python-build-standalone 3.12), the pipeline's wheel and its
constraints file (every dependency pinned with its hashes); the MLX and torch wheels are
too big to ship, so the environment is built on the Mac on first run::

    python -m maktaba_pipeline.venv_bootstrap --payload <Resources/server/pipeline>

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

Progress goes to stdout as one JSON object per line and to a status file (rewritten
whole on every change) that a process that did not start this one can read. Two runs
never build at once: the second waits on a lock, then finds nothing to do.
"""

from __future__ import annotations

import argparse
import contextlib
import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Iterator
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import IO, Any

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

#: one requirement of a constraints file starts at the line's first column
_REQ_LINE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)")


class BootstrapError(RuntimeError):
    """A step failed; the message says which and why."""


class _Interrupted(BaseException):
    """SIGTERM (or Ctrl-C) while building: the stamp stays ``building`` to resume."""


# --- inputs ------------------------------------------------------------------------------


def default_venv() -> Path:
    """``~/Library/Application Support/Maktaba/venv`` on macOS, else under XDG data."""
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Application Support" / "Maktaba" / "venv"
    base = os.environ.get("XDG_DATA_HOME", "").strip() or str(Path.home() / ".local" / "share")
    return Path(base) / "maktaba" / "venv"


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        while chunk := fh.read(1 << 20):
            h.update(chunk)
    return h.hexdigest()


def find_payload(payload: Path) -> tuple[Path, Path]:
    """The one ``maktaba_pipeline-*.whl`` and the one ``*constraints.txt`` in ``payload``."""
    wheels = sorted(payload.glob("maktaba_pipeline-*.whl"))
    cons = sorted(payload.glob("*constraints.txt"))
    if len(wheels) != 1 or len(cons) != 1:
        raise BootstrapError(
            f"{payload} must hold one maktaba_pipeline wheel and one constraints file "
            f"(found {len(wheels)} and {len(cons)})"
        )
    return wheels[0], cons[0]


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


def write_json_atomic(path: Path, data: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            json.dump(data, fh, indent=1)
            fh.write("\n")
        os.replace(tmp, path)
    except BaseException:
        with contextlib.suppress(OSError):
            os.unlink(tmp)
        raise


class Reporter:
    """Each event: a JSON line on ``out`` and the status file, rewritten whole."""

    def __init__(self, status_file: Path, out: IO[str], base: dict[str, Any]) -> None:
        self.status_file = status_file
        self.out = out
        self.base = base
        self.state: dict[str, Any] = {}
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
        with contextlib.suppress(OSError):
            write_json_atomic(self.status_file, self.state)


# --- running pip -------------------------------------------------------------------------

#: the share of the whole run each step takes, for one progress figure
_SPAN = {"venv": (0.0, 0.05), "deps": (0.05, 0.92), "wheel": (0.92, 0.97), "verify": (0.97, 1.0)}

_PIP_SEEN = re.compile(r"^(Collecting|Requirement already satisfied:|Ignoring) ([A-Za-z0-9._-]+)")


def _child_env() -> dict[str, str]:
    env = dict(os.environ)
    env.pop("PYTHONPATH", None)  # the wheel this may run from is not the venv's
    env.pop("PYTHONHOME", None)
    env["PIP_DISABLE_PIP_VERSION_CHECK"] = "1"
    env["PIP_NO_INPUT"] = "1"
    env["PYTHONUNBUFFERED"] = "1"
    return env


_CHILD: list[subprocess.Popen[str]] = []


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
        text=True,
        env=_child_env(),
        cwd=cwd,
    )
    _CHILD.append(proc)
    try:
        assert proc.stdout is not None
        for raw in proc.stdout:
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
    finally:
        _CHILD.remove(proc)
    if code != 0:
        raise BootstrapError(f"{step} failed (exit {code}):\n" + "\n".join(tail[-15:]))
    rep.emit("building", step, hi, f"{step} done", counts=(total, total) if total else None)


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
    out = subprocess.run(
        [str(venv_python(venv)), "-c", _VERIFY, json.dumps(list(target.extras))],
        capture_output=True,
        text=True,
        env=_child_env(),
        timeout=300,
        check=False,
    )
    if out.returncode != 0:
        raise BootstrapError(f"verify failed: {out.stderr.strip()[-800:]}")
    result = json.loads(out.stdout.strip().splitlines()[-1])
    if result["missing"]:
        raise BootstrapError(
            "verify failed: the constraints do not cover "
            + ", ".join(result["missing"][:10])
            + f" (extras {','.join(target.extras) or 'none'})"
        )
    return str(result["version"])


# --- the steps ---------------------------------------------------------------------------


def create_venv(venv: Path, target: Target, rep: Reporter, *, upgrade: bool = False) -> None:
    args = [str(target.python), "-m", "venv"]
    if upgrade:
        args.append("--upgrade")
    run_step(
        [*args, str(venv)],
        rep,
        "venv",
        ("repointing" if upgrade else "creating")
        + f" the venv with Python {target.python_version}",
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
            shutil.rmtree(venv)
    if p.action in ("build", "resume"):
        # resuming: the venv is there, maybe made by a Python that has moved since
        create_venv(venv, target, rep, upgrade=venv_python(venv).exists())
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


@contextlib.contextmanager
def locked(path: Path, rep: Reporter) -> Iterator[None]:
    """Hold ``path``'s lock (``flock``), waiting for another run to finish first."""
    import fcntl

    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a+") as fh:
        try:
            fcntl.flock(fh, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            rep.emit("waiting", "lock", 0.0, "another build of this venv is running: waiting")
            fcntl.flock(fh, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(fh, fcntl.LOCK_UN)


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
        default=env("MAKTABA_VENV") or str(default_venv()),
        help="the venv to build (default: $MAKTABA_VENV, else "
        "~/Library/Application Support/Maktaba/venv)",
    )
    p.add_argument(
        "--extras",
        default=env("MAKTABA_PIPELINE_EXTRAS") or DEFAULT_EXTRAS,
        help=f"extras the venv must have complete (default: {DEFAULT_EXTRAS})",
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


def _on_sigterm(_signum: int, _frame: Any) -> None:
    raise _Interrupted()


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    venv = Path(args.venv).expanduser()
    if not venv.is_absolute():
        venv = Path.cwd() / venv
    status_file = (
        Path(args.status_file).expanduser()
        if args.status_file
        else venv.parent / "venv-status.json"
    )
    rep = Reporter(status_file, sys.stdout, {"venv": str(venv)})
    try:
        if args.wheel or args.constraints:
            if not (args.wheel and args.constraints):
                raise BootstrapError("--wheel and --constraints go together")
            wheel, constraints = Path(args.wheel), Path(args.constraints)
        elif args.payload:
            wheel, constraints = find_payload(Path(args.payload).expanduser())
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
        sys.stdout.write(
            json.dumps({"event": "plan", "action": p.action, "reason": p.reason}) + "\n"
        )
        return EXIT_OK if p.action == "none" else EXIT_NEEDS_WORK

    old = signal.signal(signal.SIGTERM, _on_sigterm)
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
        for proc in list(_CHILD):
            with contextlib.suppress(OSError):
                proc.terminate()
                proc.wait(timeout=30)
        rep.emit(
            "interrupted",
            rep.state.get("step", "?"),
            rep.state.get("progress", 0.0),
            "stopped; the next run resumes",
        )
        return EXIT_INTERRUPTED
    finally:
        signal.signal(signal.SIGTERM, old)


if __name__ == "__main__":
    raise SystemExit(main())
