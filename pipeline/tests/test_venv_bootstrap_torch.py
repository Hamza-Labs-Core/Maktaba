"""Which torch build the venv bootstrap installs (review of #209, item 2): on Windows
x86_64 the CUDA build only where the NVIDIA GPU runs the second listen, else PyPI's CPU
build; elsewhere PyPI's always. ``nvidia-smi`` is stood in for."""

from __future__ import annotations

import subprocess
import sys
import types
from pathlib import Path

import pytest

from maktaba_pipeline import venv_bootstrap as vb
from maktaba_pipeline.stt import cohere_cuda

pytestmark = pytest.mark.unit

AMPERE = vb.Gpu("NVIDIA GeForce RTX 3080 Ti", 12.0, (8, 6), "591.44")


def _payload(tmp_path: Path, cuda: bool = True) -> Path:
    (tmp_path / "maktaba_pipeline-1.0.0-py3-none-any.whl").write_bytes(b"w")
    (tmp_path / "maktaba-pipeline-1.0.0-constraints.txt").write_text("x", encoding="utf-8")
    if cuda:
        (tmp_path / "maktaba-pipeline-1.0.0-constraints-cuda.txt").write_text("y", encoding="utf-8")
    return tmp_path


@pytest.fixture
def windows(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(vb, "_windows_x86_64", lambda: True)


def test_the_thresholds_are_the_pipelines() -> None:
    """The bootstrap runs before the venv exists and cannot import them: kept equal here."""
    assert vb.SECOND_LISTEN_MIN_COMPUTE == cohere_cuda.MIN_COMPUTE
    assert vb.SECOND_LISTEN_MIN_DRIVER == cohere_cuda.MIN_DRIVER
    assert vb.SECOND_LISTEN_MIN_GIB == 9.5  # MIN_GPU_GB on Windows, the cuda set's platform
    if sys.platform == "win32":
        assert vb.SECOND_LISTEN_MIN_GIB == cohere_cuda.MIN_GPU_GB


def test_a_gpu_that_runs_the_second_listen_gets_the_cuda_build(
    windows: None, tmp_path: Path
) -> None:
    payload = _payload(tmp_path)
    build, why = vb.torch_build("auto", payload, probe=lambda: AMPERE)
    assert build == "cuda" and "RTX 3080 Ti runs the second listen" in why
    wheel, cons = vb.find_payload(payload, build)
    assert cons.name == "maktaba-pipeline-1.0.0-constraints-cuda.txt" and wheel.suffix == ".whl"


@pytest.mark.parametrize(
    ("gpu", "why"),
    [
        (None, "no NVIDIA GPU"),
        (vb.Gpu("NVIDIA GeForce RTX 2080 SUPER", 8.0, (7, 5), "616.64"), "8.0 GiB, under 9.5"),
        (vb.Gpu("NVIDIA GeForce GTX 1080 Ti", 11.0, (6, 1), "591.44"), "compute capability 6.1"),
        (vb.Gpu("NVIDIA GeForce RTX 3080 Ti", 12.0, (8, 6), "572.16"), "older than R580"),
    ],
)
def test_anything_else_gets_the_cpu_build(
    windows: None, tmp_path: Path, gpu: vb.Gpu | None, why: str
) -> None:
    payload = _payload(tmp_path)
    build, said = vb.torch_build("auto", payload, probe=lambda: gpu)
    assert build == "cpu" and why in said
    assert vb.find_payload(payload, build)[1].name == "maktaba-pipeline-1.0.0-constraints.txt"


def test_what_nvidia_smi_does_not_say_is_not_held_against_it() -> None:
    assert vb.second_listen_gpu(vb.Gpu("NVIDIA RTX A4000", 16.0, None, None)) is None


def test_off_windows_and_without_the_cuda_set_the_default(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    probe_never = lambda: pytest.fail("asked nvidia-smi")  # noqa: E731
    monkeypatch.setattr(vb, "_windows_x86_64", lambda: False)  # Linux, macOS, Windows on Arm
    assert vb.torch_build("auto", _payload(tmp_path), probe_never)[0] == "cpu"
    monkeypatch.setattr(vb, "_windows_x86_64", lambda: True)
    older = tmp_path / "older"
    older.mkdir()
    assert vb.torch_build("auto", _payload(older, cuda=False), probe_never)[0] == "cpu"


def test_asked_for_is_asked_for(windows: None, tmp_path: Path) -> None:
    payload = _payload(tmp_path)
    assert vb.torch_build("cuda", payload, lambda: None) == ("cuda", "--torch cuda")
    assert vb.torch_build("cpu", payload, lambda: AMPERE) == ("cpu", "--torch cpu")
    older = tmp_path / "older"
    older.mkdir()
    with pytest.raises(vb.BootstrapError, match="no constraints-cuda.txt"):
        vb.find_payload(_payload(older, cuda=False), "cuda")


def test_nvidia_smi_is_read_with_its_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    asked: list[str] = []

    def run(cmd: list[str], **_k: object) -> object:
        asked.append(cmd[1])
        if "compute_cap" in cmd[1]:  # an nvidia-smi before R510
            return types.SimpleNamespace(returncode=6, stdout="")
        return types.SimpleNamespace(
            returncode=0, stdout="NVIDIA GeForce GTX 1080 Ti, 11264, 472.12\n"
        )

    monkeypatch.setattr(vb, "_nvidia_smi", lambda: "nvidia-smi")
    monkeypatch.setattr(subprocess, "run", run)
    gpu = vb.nvidia_gpu()
    assert gpu == vb.Gpu("NVIDIA GeForce GTX 1080 Ti", 11.0, None, "472.12")
    assert asked == [f"--query-gpu={q}" for q in vb._SMI_QUERIES]
    assert "older than R580" in str(vb.second_listen_gpu(gpu))
    assert vb.parse_gpu("NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 616.64") == vb.Gpu(
        "NVIDIA GeForce RTX 2080 SUPER", 8.0, (7, 5), "616.64"
    )
    assert vb.parse_gpu("No devices were found") is None
    monkeypatch.setattr(vb, "_nvidia_smi", lambda: None)
    assert vb.nvidia_gpu() is None


def test_the_windows_installer_reads_the_same() -> None:
    """install-node.ps1 picks the release's cuda set by the same thresholds
    (deploy/packaging/node/windows/node-common.ps1)."""
    import re

    ps1 = Path(__file__).parents[2] / "deploy/packaging/node/windows/node-common.ps1"
    if not ps1.is_file():
        pytest.skip("the installer is not in this checkout")
    text = ps1.read_text(encoding="utf-8-sig")

    def value(name: str) -> str:
        m = re.search(rf"^\$script:{name} = '?([0-9.]+)'?", text, re.M)
        assert m is not None, name
        return m.group(1)

    assert int(value("NodeSecondListenMinMiB")) / 1024 == vb.SECOND_LISTEN_MIN_GIB
    assert value("NodeSecondListenMinCompute") == "{}.{}".format(*vb.SECOND_LISTEN_MIN_COMPUTE)
    assert int(value("NodeSecondListenMinDriver")) == vb.SECOND_LISTEN_MIN_DRIVER
