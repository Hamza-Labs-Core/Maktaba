"""The first-run venv bootstrap (story 36.3; Windows: 38.7): real venvs and a real pip,
offline, from tiny wheels built here (``PIP_NO_INDEX`` + ``PIP_FIND_LINKS``: nothing is
downloaded; one test serves a wheel itself, on 127.0.0.1).

Seconds each (a venv per test), so in no tier: the node-packaging CI gate runs this file
on Linux and Windows (``.github/workflows/_node-packaging.yml``), and the real wheel on
macOS and Windows after it."""

from __future__ import annotations

import base64
import hashlib
import http.server
import json
import os
import signal
import subprocess
import sys
import threading
import time
import zipfile
from pathlib import Path

import psutil
import pytest

from maktaba_pipeline import venv_bootstrap as vb

WINDOWS = sys.platform == "win32"


def _wheel(folder: Path, name: str, version: str, requires: list[str] | None = None) -> Path:
    """A pure-Python wheel of ``name`` (a package of that name, ``__version__`` set)."""
    folder.mkdir(parents=True, exist_ok=True)
    dist = f"{name}-{version}.dist-info"
    meta = ["Metadata-Version: 2.1", f"Name: {name}", f"Version: {version}"]
    meta += ["Provides-Extra: stt", "Provides-Extra: search"]
    meta += [f"Requires-Dist: {r}" for r in requires or []]
    files = {
        f"{name}/__init__.py": f'__version__ = "{version}"\n',
        f"{dist}/METADATA": "\n".join(meta) + "\n",
        f"{dist}/WHEEL": "Wheel-Version: 1.0\nGenerator: test\nRoot-Is-Purelib: true\n"
        "Tag: py3-none-any\n",
    }
    path = folder / f"{name}-{version}-py3-none-any.whl"
    record = []
    with zipfile.ZipFile(path, "w") as z:
        for p, content in files.items():
            z.writestr(p, content)
            digest = hashlib.sha256(content.encode()).digest()
            b64 = base64.urlsafe_b64encode(digest).rstrip(b"=").decode()
            record.append(f"{p},sha256={b64},{len(content)}")
        record.append(f"{dist}/RECORD,,")
        z.writestr(f"{dist}/RECORD", "\n".join(record) + "\n")
    return path


def _constraints(path: Path, wheels: list[Path], extra_lines: str = "") -> Path:
    lines = []
    for w in wheels:
        name, version = w.name.split("-")[:2]
        lines.append(f"{name}=={version} \\\n    --hash=sha256:{vb.sha256_file(w)}")
    path.write_text("\n".join(lines) + "\n" + extra_lines, encoding="utf-8")
    return path


class Env:
    """A payload folder (the app's ``Resources/server/pipeline``), an index of
    dependency wheels, and a venv path."""

    def __init__(self, root: Path) -> None:
        self.root = root
        self.index = root / "index"
        self.payload = root / "payload"
        self.venv = root / "data" / "venv"
        self.status_file = self.venv.parent / "venv-status.json"
        self.lock = self.venv.parent / f".{self.venv.name}.lock"
        self.index.mkdir()
        self.payload.mkdir()

    def payload_files(
        self, version: str, deps: list[Path], requires: list[str], extra_lines: str = ""
    ) -> None:
        for old in self.payload.iterdir():
            old.unlink()
        _wheel(self.payload, "maktaba_pipeline", version, requires)
        _constraints(self.payload / "maktaba-pipeline-constraints.txt", deps, extra_lines)

    def args(self, *extra: str) -> list[str]:
        return [
            "--python",
            sys.executable,
            "--payload",
            str(self.payload),
            "--venv",
            str(self.venv),
            *extra,
        ]

    def run(
        self, capsys: pytest.CaptureFixture[str], *extra: str
    ) -> tuple[int, list[dict[str, object]]]:
        code = vb.main(self.args(*extra))
        out = capsys.readouterr().out
        return code, [json.loads(line) for line in out.splitlines() if line.strip()]

    def command(self, *extra: str) -> list[str]:
        """The run as the app starts it: another process."""
        return [sys.executable, "-m", "maktaba_pipeline.venv_bootstrap", *self.args(*extra)]

    def status(self) -> dict[str, object]:
        data = json.loads(self.status_file.read_text(encoding="utf-8"))
        assert isinstance(data, dict)
        return data

    def stamp(self) -> dict[str, object]:
        data = vb.read_stamp(self.venv)
        assert data is not None
        return data

    def installed(self, module: str) -> str:
        out = subprocess.run(
            [
                str(vb.venv_python(self.venv)),
                "-c",
                f"import {module}; print({module}.__version__)",
            ],
            capture_output=True,
            text=True,
            check=True,
        )
        return out.stdout.strip()

    def mark(self) -> Path:
        """A file inside the venv: gone when the venv is rebuilt from scratch."""
        m = self.venv / "kept-marker"
        m.write_text("x", encoding="utf-8")
        return m


@pytest.fixture
def env(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Env:
    e = Env(tmp_path)
    monkeypatch.setenv("PIP_NO_INDEX", "1")
    monkeypatch.setenv("PIP_FIND_LINKS", str(e.index))
    monkeypatch.setenv("PIP_NO_CACHE_DIR", "1")
    return e


def _deps(env: Env) -> list[Path]:
    a = _wheel(env.index, "fakedep_a", "1.0")
    b = _wheel(env.index, "fakedep_b", "2.0")
    return [a, b]


REQUIRES = ["fakedep_a==1.0", 'fakedep_b==2.0; extra == "stt"']


def test_fresh_build_installs_wheel_and_pins(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK
    assert env.installed("maktaba_pipeline") == "0.1.0"
    assert env.installed("fakedep_b") == "2.0"
    assert env.stamp()["state"] == "ready"
    assert env.stamp()["extras"] == ["search", "stt"]
    want = env.venv / "Scripts" / "python.exe" if WINDOWS else env.venv / "bin" / "python"
    assert vb.venv_python(env.venv) == want and want.is_file()
    steps = [e["step"] for e in events]
    for step in ("plan", "venv", "deps", "wheel", "verify", "done"):
        assert step in steps
    assert events[-1]["state"] == "ready" and events[-1]["progress"] == 1.0
    progress = [float(str(e["progress"])) for e in events]
    assert progress == sorted(progress)
    counted = [e for e in events if e["step"] == "deps" and "total" in e]
    assert counted and all(e["total"] == 2 for e in counted)
    assert env.status()["state"] == "ready"
    assert env.status()["wheel"] == "maktaba_pipeline-0.1.0-py3-none-any.whl"


def test_noop_when_nothing_changed(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_OK and events == [
        {"event": "plan", "action": "none", "reason": "up to date"}
    ]
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK
    assert [e["step"] for e in events] == ["plan", "done"]
    assert events[-1]["message"] == "up to date"
    assert marker.exists()


def test_rebuilds_when_constraints_change(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    deps = _deps(env)
    env.payload_files("0.1.0", deps, REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    newer = _wheel(env.index, "fakedep_a", "1.1")
    env.payload_files("0.1.0", [newer, deps[1]], ["fakedep_a==1.1", REQUIRES[1]])
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "build"
    assert events[0]["reason"] == "the constraints changed"
    code, _ = env.run(capsys)
    assert code == vb.EXIT_OK
    assert not marker.exists()  # built afresh
    assert env.installed("fakedep_a") == "1.1"


def test_new_wheel_same_constraints_reinstalls_wheel_only(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    deps = _deps(env)
    env.payload_files("0.1.0", deps, REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    env.payload_files("0.2.0", deps, REQUIRES)
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK
    assert events[0]["message"] == "wheel: the pipeline wheel changed"
    assert "deps" not in [e["step"] for e in events]
    assert marker.exists()
    assert env.installed("maktaba_pipeline") == "0.2.0"


def test_resumes_after_a_failed_install(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    a = _wheel(env.index, "fakedep_a", "1.0")
    # fakedep_b is pinned but not downloadable yet: the network dropped mid-install
    b = _wheel(env.root / "later", "fakedep_b", "2.0")
    env.payload_files("0.1.0", [a, b], REQUIRES)
    code, events = env.run(capsys)
    assert code == vb.EXIT_FAILED
    assert events[-1]["state"] == "failed" and events[-1]["step"] == "deps"
    assert "fakedep" in str(events[-1]["error"])
    assert env.status()["state"] == "failed"
    assert env.stamp()["state"] == "building"
    marker = env.mark()

    (env.index / b.name).write_bytes(b.read_bytes())
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "resume"
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK
    assert marker.exists()  # the same venv, carried on
    assert env.stamp()["state"] == "ready"
    assert env.installed("fakedep_b") == "2.0"


def test_repairs_venv_when_python_moved(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    stamp = env.stamp()
    stamp["python"] = "/Applications/Old Place/Maktaba.app/python3"
    vb.write_json_atomic(env.venv / vb.STAMP, stamp)
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK
    assert str(events[0]["message"]).startswith("repair:")
    assert marker.exists()
    assert env.stamp()["python"] == str(Path(sys.executable).resolve())


def test_verify_fails_when_constraints_miss_an_extra(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    a, _b = _deps(env)
    env.payload_files("0.1.0", [a], REQUIRES)  # fakedep_b (the stt extra) not pinned
    code, events = env.run(capsys)
    assert code == vb.EXIT_FAILED
    assert events[-1]["step"] == "verify"
    assert "fakedep_b" in str(events[-1]["error"])
    # without the extra asked for, the same files make a complete venv
    code, _ = env.run(capsys, "--extras", "")
    assert code == vb.EXIT_OK


def test_refuses_constraints_without_hashes(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    _wheel(env.payload, "maktaba_pipeline", "0.1.0")
    (env.payload / "maktaba-pipeline-constraints.txt").write_text(
        "fakedep_a==1.0\n", encoding="utf-8"
    )
    code, events = env.run(capsys)
    assert code == vb.EXIT_BAD_INPUT
    assert events[-1]["state"] == "failed" and "hashes" in str(events[-1]["message"])
    assert not env.venv.exists()


def test_payload_needs_exactly_one_wheel(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    _wheel(env.payload, "maktaba_pipeline", "0.2.0")
    code, _ = env.run(capsys)
    assert code == vb.EXIT_BAD_INPUT


def test_runs_from_the_wheel_with_only_the_stdlib(env: Env) -> None:
    """The app runs it before any venv exists: from the wheel itself on PYTHONPATH."""
    src = Path(vb.__file__).resolve().parents[1]
    whl = env.root / "maktaba_pipeline-9.9.9-py3-none-any.whl"
    with zipfile.ZipFile(whl, "w") as z:
        for name in ("__init__.py", "model_dirs.py", "venv_bootstrap.py"):
            z.write(src / "maktaba_pipeline" / name, f"maktaba_pipeline/{name}")
    out = subprocess.run(
        [sys.executable, "-S", "-m", "maktaba_pipeline.venv_bootstrap", "--help"],
        capture_output=True,
        text=True,
        env={"PYTHONPATH": str(whl), "PATH": "/usr/bin:/bin"},
        check=False,
    )
    assert out.returncode == 0, out.stderr
    assert "--payload" in out.stdout


def test_an_interrupt_stops_pip(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """SIGTERM while pip runs: pip is stopped before the bootstrap lets go of the lock."""
    started: list[subprocess.Popen[str]] = []
    real = subprocess.Popen

    def popen(*a: object, **k: object) -> subprocess.Popen[str]:
        proc: subprocess.Popen[str] = real(*a, **k)  # type: ignore[call-overload]
        started.append(proc)
        return proc

    monkeypatch.setattr("subprocess.Popen", popen)

    class Stopping(vb.Reporter):
        def emit(self, state: str, step: str, *a: object, **k: object) -> None:
            if state == "building" and started:
                raise vb._Interrupted()

    rep = Stopping(tmp_path / "status.json", sys.stdout, {})
    slow = "import time; print('Collecting fakedep_a==1.0', flush=True); time.sleep(60)"
    with pytest.raises(vb._Interrupted):
        vb.run_step([sys.executable, "-c", slow], rep, "deps", "x", total=1)
    assert started and started[0].poll() is not None
    assert not vb._CHILD


def test_a_removal_cut_short_leaves_no_ready_stamp(
    env: Env, capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK

    def cut_short(_path: object, **_kw: object) -> None:
        raise OSError("disk went away")

    monkeypatch.setattr("shutil.rmtree", cut_short)
    code, events = env.run(capsys, "--force")
    assert code == vb.EXIT_FAILED  # reported, not a traceback
    assert events[-1]["state"] == "failed" and "disk went away" in str(events[-1]["error"])
    assert env.status()["state"] == "failed"
    assert vb.read_stamp(env.venv) is None
    monkeypatch.undo()
    monkeypatch.setenv("PIP_NO_INDEX", "1")
    monkeypatch.setenv("PIP_FIND_LINKS", str(env.index))
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "build"


# --- where the venv goes ----------------------------------------------------------------


@pytest.mark.skipif(not WINDOWS, reason="LOCALAPPDATA: where Windows keeps a user's local data")
def test_the_default_venv_on_windows_is_under_localappdata(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local"))
    assert vb.default_venv() == tmp_path / "Local" / "Maktaba" / "venv"
    # not set (or blank): the profile's own AppData\Local
    monkeypatch.setenv("USERPROFILE", str(tmp_path / "me"))
    monkeypatch.setenv("LOCALAPPDATA", " ")
    assert vb.default_venv() == tmp_path / "me" / "AppData" / "Local" / "Maktaba" / "venv"
    monkeypatch.delenv("LOCALAPPDATA")
    assert vb.default_venv() == tmp_path / "me" / "AppData" / "Local" / "Maktaba" / "venv"


def test_a_run_without_venv_uses_the_platform_default(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.delenv("MAKTABA_VENV", raising=False)
    if WINDOWS:
        monkeypatch.setenv("LOCALAPPDATA", str(tmp_path / "Local App Data"))
        want = tmp_path / "Local App Data" / "Maktaba" / "venv"
    elif sys.platform == "darwin":
        monkeypatch.setenv("HOME", str(tmp_path / "home"))
        want = tmp_path / "home" / "Library" / "Application Support" / "Maktaba" / "venv"
    else:
        monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path / "data"))
        want = tmp_path / "data" / "maktaba" / "venv"
    # an empty payload: the run stops at its inputs, having said where the venv is
    assert vb.main(["--payload", str(tmp_path)]) == vb.EXIT_BAD_INPUT
    event = json.loads(capsys.readouterr().out.splitlines()[-1])
    assert event["venv"] == str(want)
    status = json.loads((want.parent / "venv-status.json").read_text(encoding="utf-8"))
    assert status["venv"] == str(want) and status["state"] == "failed"


def test_a_path_with_spaces_and_arabic_letters(tmp_path: Path) -> None:
    """A profile folder can be named in any script: the run (another process, as the app
    starts it) builds there, and every path stays exact in the status, the stamp and on
    stdout, which is UTF-8 whatever the console's code page (Windows: the ANSI one)."""
    root = tmp_path / "App Data" / "مكتبة"  # "maktaba", in Arabic
    root.mkdir(parents=True)
    env = Env(root)
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    child = {k: v for k, v in os.environ.items() if k not in ("PYTHONIOENCODING", "PYTHONUTF8")}
    # (a URI: pip splits PIP_FIND_LINKS at spaces)
    child.update(PIP_NO_INDEX="1", PIP_FIND_LINKS=env.index.as_uri(), PIP_NO_CACHE_DIR="1")
    missing = root / "nothing here"
    cmd = [*env.command()[:3], "--payload", str(missing), "--venv", str(env.venv)]
    out = subprocess.run(cmd, capture_output=True, env=child, timeout=60, check=False)
    assert out.returncode == vb.EXIT_BAD_INPUT
    assert str(missing) in out.stderr.decode("utf-8")
    assert str(missing) in json.loads(out.stdout.decode("utf-8"))["message"]
    out = subprocess.run(env.command(), capture_output=True, env=child, timeout=600, check=False)
    assert out.returncode == vb.EXIT_OK, out.stderr.decode("utf-8")
    events = [json.loads(line) for line in out.stdout.decode("utf-8").splitlines()]
    assert events[-1]["state"] == "ready"
    assert {e["venv"] for e in events} == {str(env.venv)}
    assert env.status()["venv"] == str(env.venv)
    assert env.stamp()["state"] == "ready"
    assert env.installed("maktaba_pipeline") == "0.1.0"


# --- a moved Python -----------------------------------------------------------------------


def test_repairs_a_venv_whose_python_is_gone(env: Env, capsys: pytest.CaptureFixture[str]) -> None:
    """The app was moved: the venv's interpreter no longer finds the Python it was made
    from (a dangling symlink; on Windows the launcher in Scripts reads pyvenv.cfg's
    ``home``, which names a folder that is gone). A repair repoints it, in place."""
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    gone = env.root / "Old Place" / "python"
    if WINDOWS:
        cfg = env.venv / "pyvenv.cfg"
        lines = cfg.read_text(encoding="utf-8").splitlines()
        moved = [f"home = {gone}" if ln.partition("=")[0].strip() == "home" else ln for ln in lines]
        assert moved != lines
        cfg.write_text("\n".join(moved) + "\n", encoding="utf-8")
    else:
        py = env.venv / "bin" / "python"
        py.unlink()
        py.symlink_to(gone / "bin" / "python3")
    assert not vb._runs(vb.venv_python(env.venv))
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "repair"
    code, events = env.run(capsys)
    assert code == vb.EXIT_OK, events[-1]
    assert str(events[0]["message"]).startswith("repair:")
    assert marker.exists()
    assert vb._runs(vb.venv_python(env.venv))
    assert env.installed("maktaba_pipeline") == "0.1.0"
    assert env.run(capsys, "--check")[0] == vb.EXIT_OK


# --- the lock -----------------------------------------------------------------------------

#: holds the venv's lock (the module's own) in another process until its stdin closes
_HOLD = """
import sys
from pathlib import Path
from maktaba_pipeline import venv_bootstrap as vb
with vb.locked(Path(sys.argv[1]), vb.Reporter(Path(sys.argv[2]), sys.stderr, {})):
    print("held", flush=True)
    sys.stdin.read()
"""


class Holder:
    """Another run, holding the lock until :meth:`release`."""

    def __init__(self, lock: Path, root: Path) -> None:
        self.proc = subprocess.Popen(
            [sys.executable, "-c", _HOLD, str(lock), str(root / "holder-status.json")],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            text=True,
        )
        assert self.proc.stdout is not None
        assert self.proc.stdout.readline().strip() == "held"

    def release(self) -> None:
        assert self.proc.stdin is not None
        if not self.proc.stdin.closed:
            self.proc.stdin.close()
        self.proc.wait(timeout=30)


def test_a_second_run_waits_for_the_first_then_goes_on(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    holder = Holder(env.lock, env.root)
    status_while_waiting: list[bool] = []

    def release() -> None:
        status_while_waiting.append(env.status_file.exists())
        holder.release()

    timer = threading.Timer(1.5, release)
    timer.start()
    try:
        code, events = env.run(capsys)
    finally:
        timer.join()
        holder.release()
    assert code == vb.EXIT_OK
    assert (events[0]["state"], events[0]["step"]) == ("waiting", "lock")
    assert events[1]["step"] == "plan" and events[-1]["state"] == "ready"
    assert status_while_waiting == [False]  # the file is the running build's, not the waiter's
    assert env.status()["state"] == "ready"


def _ask_the_main_thread_to_stop() -> None:
    """What the supervisor's stop does to a run, from another thread."""
    if sys.platform == "win32":
        signal.raise_signal(signal.SIGBREAK)  # CTRL_BREAK arrives as SIGBREAK
    else:
        main = threading.main_thread().ident
        assert main is not None
        signal.pthread_kill(main, signal.SIGTERM)


def test_a_run_stopped_while_waiting_leaves_the_status_file_alone(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    holder = Holder(env.lock, env.root)
    running = {"state": "building", "step": "deps", "pid": holder.proc.pid}
    vb.write_json_atomic(env.status_file, running)
    timer = threading.Timer(1.0, _ask_the_main_thread_to_stop)
    timer.start()
    try:
        code, events = env.run(capsys)
    finally:
        timer.join()
        holder.release()
    assert code == vb.EXIT_INTERRUPTED
    assert [e["state"] for e in events] == ["waiting", "interrupted"]
    assert json.loads(env.status_file.read_text(encoding="utf-8")) == running
    assert not env.venv.exists()


class _Lines:
    """A stdout that keeps what was written, line by line."""

    def __init__(self) -> None:
        self.lines: list[str] = []

    def write(self, s: str) -> int:
        self.lines += [ln for ln in s.splitlines() if ln]
        return len(s)

    def flush(self) -> None:
        pass


@pytest.mark.skipif(not WINDOWS, reason="msvcrt.locking(LK_LOCK) gives up after ~10 s")
def test_the_lock_waits_longer_than_msvcrt_would(tmp_path: Path) -> None:
    lock = tmp_path / ".venv.lock"
    holder = Holder(lock, tmp_path)
    out = _Lines()
    rep = vb.Reporter(tmp_path / "status.json", out, {})  # type: ignore[arg-type]
    got = threading.Event()

    def wait() -> None:
        with vb.locked(lock, rep):
            got.set()

    waiter = threading.Thread(target=wait, daemon=True)
    waiter.start()
    try:
        assert not got.wait(11)  # past LK_LOCK's ten tries a second apart
    finally:
        holder.release()
    assert got.wait(10)
    waiter.join(10)
    assert json.loads(out.lines[0])["state"] == "waiting"
    assert not (tmp_path / "status.json").exists()


def test_the_status_file_is_replaced_while_another_process_reads_it(tmp_path: Path) -> None:
    """A reader (the supervisor, the app) holds the status file open for a moment: on
    Windows that refuses a replace, so it is tried again until the reader lets go."""
    status = tmp_path / "venv-status.json"
    vb.write_json_atomic(status, {"state": "building"})
    reader = open(status, encoding="utf-8")  # noqa: SIM115 - closed by the timer
    timer = threading.Timer(0.5, reader.close)
    timer.start()
    try:
        vb.write_json_atomic(status, {"state": "ready"})
    finally:
        timer.join()
        reader.close()
    assert json.loads(status.read_text(encoding="utf-8")) == {"state": "ready"}
    assert [p.name for p in tmp_path.iterdir()] == [status.name]  # no temp file left


@pytest.mark.skipif(not WINDOWS, reason="POSIX removes a file that is open, or read-only")
def test_a_venv_is_removed_while_a_file_in_it_is_open(tmp_path: Path) -> None:
    """A virus scanner or the search indexer holds a file of the venv a moment: its
    removal is refused (WinError 32) until it lets go, and tried again till then; a
    read-only file (WinError 5 for ever) is made writable."""
    venv = tmp_path / "venv"
    (venv / "Lib" / "site-packages").mkdir(parents=True)
    held = venv / "Lib" / "site-packages" / "scanned.pyd"
    held.write_bytes(b"x")
    readonly = venv / "Lib" / "readonly.txt"
    readonly.write_bytes(b"x")
    readonly.chmod(0o444)
    scanner = open(held, "rb")  # noqa: SIM115 - closed by the timer
    with pytest.raises(PermissionError):
        os.unlink(held)  # what rmtree meets
    timer = threading.Timer(0.5, scanner.close)
    timer.start()
    try:
        vb._remove_tree(venv)
    finally:
        timer.join()
        scanner.close()
    assert not venv.exists()


@pytest.mark.skipif(not WINDOWS, reason="Windows refuses to remove or write a running program")
def test_the_venv_is_repaired_and_rebuilt_while_its_python_still_runs(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    """A program of the venv still exiting (the pipeline, stopped for the update) holds
    ``Scripts\\python.exe``: ``venv --upgrade`` writing over it (Errno 13) and the
    removal (WinError 5) are refused until it exits, and tried again till then."""
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    assert env.run(capsys)[0] == vb.EXIT_OK
    marker = env.mark()
    stamp = env.stamp()
    stamp["python"] = str(env.root / "Old Place" / "python.exe")
    vb.write_json_atomic(env.venv / vb.STAMP, stamp)
    for extra, action, kept in (((), "repair", True), (("--force",), "build", False)):
        exiting = subprocess.Popen(
            [str(vb.venv_python(env.venv)), "-c", "import time; time.sleep(1.5)"]
        )
        try:
            code, events = env.run(capsys, *extra)
        finally:
            exiting.kill()
            exiting.wait()
        assert code == vb.EXIT_OK, events[-1]
        assert str(events[0]["message"]).startswith(f"{action}:")
        assert marker.exists() is kept  # a repair in place; a rebuild from scratch
        assert env.stamp()["python"] == str(Path(sys.executable).resolve())
    assert env.installed("maktaba_pipeline") == "0.1.0"


# --- stopping -----------------------------------------------------------------------------


@pytest.mark.parametrize("name", [s.name for s in vb._STOP_SIGNALS])
def test_a_stop_signal_mid_build_stops_pip_and_leaves_a_resumable_venv(
    env: Env, capsys: pytest.CaptureFixture[str], monkeypatch: pytest.MonkeyPatch, name: str
) -> None:
    """SIGTERM (Windows: SIGBREAK, what CTRL_BREAK and a console's close become) while
    pip installs the pins: pip is stopped, the stamp stays ``building``, exit 130."""
    sig = signal.Signals[name]
    env.payload_files("0.1.0", _deps(env), REQUIRES)
    started: list[subprocess.Popen[str]] = []
    real_popen = subprocess.Popen

    def popen(*a: object, **k: object) -> subprocess.Popen[str]:
        proc: subprocess.Popen[str] = real_popen(*a, **k)  # type: ignore[call-overload]
        started.append(proc)
        return proc

    real_emit = vb.Reporter.emit
    fired: list[bool] = []

    def emit(self: vb.Reporter, state: str, step: str, *a: object, **k: object) -> None:
        real_emit(self, state, step, *a, **k)  # type: ignore[arg-type]
        pip_runs = any(isinstance(p.args, list) and "pip" in p.args for p in started)
        if state == "building" and step == "deps" and pip_runs and not fired:
            fired.append(True)
            signal.raise_signal(sig)

    monkeypatch.setattr("subprocess.Popen", popen)
    monkeypatch.setattr(vb.Reporter, "emit", emit)
    before = signal.getsignal(sig)
    code, events = env.run(capsys)
    monkeypatch.setattr(vb.Reporter, "emit", real_emit)
    monkeypatch.setattr("subprocess.Popen", real_popen)

    assert fired and code == vb.EXIT_INTERRUPTED
    assert (events[-1]["state"], events[-1]["step"]) == ("interrupted", "deps")
    assert env.status()["state"] == "interrupted"
    assert env.stamp()["state"] == "building"
    assert all(p.poll() is not None for p in started)  # pip is not left running
    assert not vb._CHILD
    assert signal.getsignal(sig) == before
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "resume"


class _StallingLinks:
    """A find-links page on 127.0.0.1 whose one wheel starts downloading and never ends."""

    def __init__(self, wheel: Path) -> None:
        data, name = wheel.read_bytes(), wheel.name
        self.requested = threading.Event()
        self.release = threading.Event()
        links = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, format: str, *args: object) -> None:
                pass

            def do_GET(self) -> None:
                if self.path == "/":
                    body = f'<a href="/{name}">{name}</a>'.encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "text/html")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                elif self.path == f"/{name}":
                    self.send_response(200)
                    self.send_header("Content-Type", "application/octet-stream")
                    self.send_header("Content-Length", str(len(data)))
                    self.end_headers()
                    self.wfile.write(data[:16])
                    self.wfile.flush()
                    links.requested.set()
                    links.release.wait(120)
                else:
                    self.send_error(404)

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}/"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self) -> None:
        self.release.set()
        self.server.shutdown()
        self.server.server_close()


def test_a_stop_request_mid_download_stops_the_run_and_pip(
    env: Env, capsys: pytest.CaptureFixture[str]
) -> None:
    """What the supervisor does to the run it started (Windows: CTRL_BREAK to the run's
    process group; else SIGTERM) while pip waits on a download and prints nothing. The run
    stops at once (on Windows a signal does not interrupt the read of pip's output), stops
    pip and whatever pip started, and exits 130 with the venv resumable."""
    a = _wheel(env.root / "served", "fakedep_a", "1.0")
    env.payload_files("0.1.0", [a], ["fakedep_a==1.0"])
    links = _StallingLinks(a)
    child = dict(os.environ)
    child.update(
        PIP_FIND_LINKS=links.url,
        PIP_NO_INDEX="1",
        PIP_TIMEOUT="120",
        PIP_RETRIES="0",
        NO_PROXY="127.0.0.1",
        no_proxy="127.0.0.1",
    )
    log = env.root / "run.jsonl"
    with open(log, "wb") as out:
        run = subprocess.Popen(
            env.command("--extras", ""),
            stdout=out,
            stderr=subprocess.STDOUT,
            env=child,
            creationflags=vb._POPEN_FLAGS,  # its own process group, as the supervisor does
        )
    try:
        assert links.requested.wait(180), log.read_text(encoding="utf-8", errors="replace")
        time.sleep(0.5)
        started = psutil.Process(run.pid).children(recursive=True)
        assert started  # pip (on Windows: behind its venv launcher)
        asked = time.monotonic()
        try:
            if sys.platform == "win32":
                run.send_signal(signal.CTRL_BREAK_EVENT)
            else:
                run.send_signal(signal.SIGTERM)
        except OSError as exc:  # a Windows session without a console
            pytest.skip(f"no console to send CTRL_BREAK through: {exc}")
        code = run.wait(timeout=60)
        took = time.monotonic() - asked
        _gone, alive = psutil.wait_procs(started, timeout=30)
    finally:
        if run.poll() is None:
            for proc in psutil.Process(run.pid).children(recursive=True):
                proc.kill()
            run.kill()
        links.close()
    text = log.read_text(encoding="utf-8")
    assert code == vb.EXIT_INTERRUPTED, text
    assert took < 15, f"took {took:.1f} s to stop"
    assert not alive, f"left running: {alive}"
    events = [json.loads(line) for line in text.splitlines() if line.startswith("{")]
    assert (events[-1]["state"], events[-1]["step"]) == ("interrupted", "deps")
    assert env.status()["state"] == "interrupted"
    assert env.stamp()["state"] == "building"
    code, events = env.run(capsys, "--check")
    assert code == vb.EXIT_NEEDS_WORK and events[0]["action"] == "resume"
