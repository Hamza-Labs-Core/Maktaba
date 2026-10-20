"""The first-run venv bootstrap (story 36.3): real venvs and a real pip, offline, from
tiny wheels built here (``PIP_NO_INDEX`` + ``PIP_FIND_LINKS``: nothing is downloaded).

Seconds each (a venv per test), so in no tier: the node-packaging CI gate runs this file
(``.github/workflows/_node-packaging.yml``), and the real wheel on macOS after it."""

from __future__ import annotations

import base64
import hashlib
import json
import subprocess
import sys
import zipfile
from pathlib import Path

import pytest

from maktaba_pipeline import venv_bootstrap as vb


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
        self.index.mkdir()
        self.payload.mkdir()

    def payload_files(
        self, version: str, deps: list[Path], requires: list[str], extra_lines: str = ""
    ) -> None:
        for old in self.payload.iterdir():
            old.unlink()
        _wheel(self.payload, "maktaba_pipeline", version, requires)
        _constraints(self.payload / "maktaba-pipeline-constraints.txt", deps, extra_lines)

    def run(
        self, capsys: pytest.CaptureFixture[str], *extra: str
    ) -> tuple[int, list[dict[str, object]]]:
        code = vb.main(
            [
                "--python",
                sys.executable,
                "--payload",
                str(self.payload),
                "--venv",
                str(self.venv),
                *extra,
            ]
        )
        out = capsys.readouterr().out
        return code, [json.loads(line) for line in out.splitlines() if line.strip()]

    def status(self) -> dict[str, object]:
        data = json.loads((self.venv.parent / "venv-status.json").read_text(encoding="utf-8"))
        assert isinstance(data, dict)
        return data

    def stamp(self) -> dict[str, object]:
        data = vb.read_stamp(self.venv)
        assert data is not None
        return data

    def installed(self, module: str) -> str:
        out = subprocess.run(
            [
                str(self.venv / "bin" / "python"),
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
