"""tools/ci/pipeline-deps.sh: CI's pipeline environment, without torch's CUDA tree.

On Linux x86_64 the stt and search extras pull PyPI's torch, the CUDA 13 build, with triton,
cuda-toolkit and the nvidia-* wheels: about 3.7 GB of the 4.1 GB `uv sync --all-extras`
downloaded, which no test runs (a cold download ran #1163's unit job past 25 minutes). The
script syncs every extra but those; lint adds torch's CPU build for mypy. Each test runs the
script against the real uv.lock with a stand-in `uv` that records what it was asked.
"""

from __future__ import annotations

import os
import re
import subprocess
import tomllib
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/pipeline-deps.sh"
LOCK = ROOT / "pipeline/uv.lock"
WORKFLOWS = ROOT / ".github/workflows"

# torch's dependencies that are small and pure Python: CI keeps them
TORCH_PLAIN_DEPS = {"filelock", "fsspec", "jinja2", "networkx", "setuptools", "sympy",
                    "typing-extensions"}  # fmt: skip


def run(tmp_path: Path, *args: str, lock: str | None = None) -> tuple[int, list[list[str]]]:
    """Runs the script in a folder holding the lock, with a `uv` that logs its arguments."""
    work = tmp_path / "pipeline"
    work.mkdir()
    (work / "uv.lock").write_text(LOCK.read_text() if lock is None else lock)
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    log = tmp_path / "uv.log"
    uv = bin_dir / "uv"
    uv.write_text(f'#!/bin/sh\nprintf "%s\\037" "$@" >> {log}\necho >> {log}\n')
    uv.chmod(0o755)
    env = {**os.environ, "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}"}
    done = subprocess.run(
        ["sh", str(SCRIPT), *args], cwd=work, env=env, capture_output=True, check=False
    )
    calls = (
        [line.split("\x1f")[:-1] for line in log.read_text().splitlines()] if log.exists() else []
    )
    return done.returncode, calls


def left_out(sync: list[str]) -> set[str]:
    return {sync[i + 1] for i, a in enumerate(sync) if a == "--no-install-package"}


def pypi_torch() -> dict[str, object]:
    for pkg in tomllib.loads(LOCK.read_text())["package"]:
        if pkg["name"] == "torch" and "pypi.org" in pkg["source"].get("registry", ""):
            return dict(pkg)
    raise AssertionError("uv.lock: no PyPI torch")


def test_it_syncs_every_extra_but_torch_and_its_gpu_wheels(tmp_path: Path) -> None:
    code, calls = run(tmp_path)
    assert code == 0
    [sync] = calls
    assert sync[:3] == ["sync", "--frozen", "--all-extras"]
    skipped = left_out(sync)
    assert {"torch", "triton"} <= skipped
    gpu = {n for n in re.findall(r'^name = "([^"]+)"$', LOCK.read_text(), re.MULTILINE)
           if n.startswith(("nvidia-", "cuda-"))}  # fmt: skip
    assert gpu and gpu <= skipped
    # what the tests import stays
    assert not skipped & {"transformers", "chromadb", "ctranslate2", "yt-dlp", "numpy"}


def test_torch_keeps_only_its_plain_dependencies(tmp_path: Path) -> None:
    """A torch upgrade that adds a GPU wheel under a new name fails here, not in a slow job."""
    _, [sync] = run(tmp_path)
    deps = pypi_torch()["dependencies"]
    assert isinstance(deps, list)
    kept = {d["name"] for d in deps} - left_out(sync)
    assert kept <= TORCH_PLAIN_DEPS, (
        f"torch now depends on {sorted(kept - TORCH_PLAIN_DEPS)}: a GPU wheel belongs in"
        " pipeline-deps.sh's pattern, a small pure-Python one in TORCH_PLAIN_DEPS"
    )


def test_cpu_torch_installs_the_locked_version_from_the_cpu_index(tmp_path: Path) -> None:
    code, calls = run(tmp_path, "--cpu-torch")
    assert code == 0
    [_, pip] = calls
    assert pip == [
        "pip", "install", "--no-deps", f"torch=={pypi_torch()['version']}",
        "--index-url", "https://download.pytorch.org/whl/cpu",
    ]  # fmt: skip


def test_a_lock_without_torch_stops_it(tmp_path: Path) -> None:
    code, calls = run(tmp_path, lock='[[package]]\nname = "numpy"\nversion = "2.0.0"\n')
    assert code != 0
    assert calls == []


@pytest.mark.parametrize("path", sorted(WORKFLOWS.glob("*.yml")), ids=lambda p: p.name)
def test_no_workflow_installs_every_extra_itself(path: Path) -> None:
    """`uv sync --all-extras`, or `uv run --all-extras` (which syncs them), would bring the CUDA
    tree back: CI installs through the script, and runs with --no-sync after it."""
    found = [
        f"{path.name}:{n}"
        for n, line in enumerate(path.read_text().splitlines(), 1)
        if re.search(r"\buv (sync|run)\b.*--all-extras", line)
    ]
    assert found == [], "use tools/ci/pipeline-deps.sh"
