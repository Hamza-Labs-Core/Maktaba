"""A Windows NVIDIA worker's capability report and stage selection (the Windows node: an RTX
2080 SUPER that reported ``"gpus": []`` and no ``stt`` extra, and never transcribed).

Runs on Windows too (``_unit.yml`` pipeline-cluster-windows): nothing here needs a GPU.
"""

from __future__ import annotations

import subprocess
import types

import pytest

from maktaba_pipeline import gpu as gpu_probe
from maktaba_pipeline import venv_bootstrap as vb
from maktaba_pipeline.cluster import capabilities as caps

pytestmark = pytest.mark.unit

#: what the PC's nvidia-smi prints (Windows: CRLF), and a second card
WINDOWS_SMI = (
    "NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 581.57\r\nNVIDIA RTX A2000, 6138, 8.6, 581.57\r\n"
)
RTX = vb.Gpu("NVIDIA GeForce RTX 2080 SUPER", 8.0, (7, 5), "581.57")


def _smi(monkeypatch: pytest.MonkeyPatch, stdout: str, returncode: int = 0) -> list[list[str]]:
    asked: list[list[str]] = []

    def run(cmd: list[str], **_k: object) -> object:
        asked.append(cmd)
        return types.SimpleNamespace(returncode=returncode, stdout=stdout, stderr="")

    monkeypatch.setattr(vb, "_nvidia_smi", lambda: r"C:\Windows\System32\nvidia-smi.exe")
    monkeypatch.setattr(subprocess, "run", run)
    return asked


# --- nvidia-smi: the fallback, and its Windows output -------------------------------------


def test_windows_nvidia_smi_output_is_read_line_by_line() -> None:
    assert vb.parse_gpus(WINDOWS_SMI) == [
        RTX,
        vb.Gpu("NVIDIA RTX A2000", 6138 / 1024, (8, 6), "581.57"),
    ]
    # without nounits nvidia-smi says "8192 MiB"; an older one has no compute_cap
    assert vb.parse_gpu("NVIDIA GeForce RTX 2080 SUPER, 8192 MiB, 7.5, 581.57\r") == RTX
    assert vb.parse_gpu("NVIDIA GeForce GTX 1080 Ti, 11264, 472.12") == vb.Gpu(
        "NVIDIA GeForce GTX 1080 Ti", 11.0, None, "472.12"
    )
    assert vb.parse_gpus("No devices were found\r\n") == []


def test_the_probe_falls_back_to_nvidia_smi_without_nvml(monkeypatch: pytest.MonkeyPatch) -> None:
    asked = _smi(monkeypatch, WINDOWS_SMI)
    found = gpu_probe.probe(lambda: ([], "NVML is not installed"), vb.nvidia_gpus)
    assert found.source == "nvidia-smi" and found.problem is None
    assert found.gpus[0] == RTX and len(found.gpus) == 2
    assert asked[0][0].endswith("nvidia-smi.exe")
    assert asked[0][1] == "--query-gpu=name,memory.total,compute_cap,driver_version"


def test_nvml_answers_first(monkeypatch: pytest.MonkeyPatch) -> None:
    _smi(monkeypatch, "")
    found = gpu_probe.probe(lambda: ([RTX], None), vb.nvidia_gpus)
    assert found.source == "nvml" and found.gpus == [RTX]


def test_no_gpu_says_why(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(vb, "_nvidia_smi", lambda: None)
    found = gpu_probe.probe(lambda: ([], "NVML is not installed"), vb.nvidia_gpus)
    assert found.gpus == [] and found.source is None
    assert found.problem is not None
    assert "NVML is not installed" in found.problem and "nvidia-smi was not found" in found.problem
    _smi(monkeypatch, "", returncode=9)
    why = gpu_probe.probe(lambda: ([], "no NVML"), vb.nvidia_gpus).problem
    assert why is not None and "exited 9" in why


def test_nvml_without_a_driver_is_a_reason_not_an_error() -> None:
    gpus, why = gpu_probe.nvml_gpus(lambda: None)
    assert gpus == [] and why is not None and "NVML" in why

    class Broken:
        def nvmlInit_v2(self) -> int:  # noqa: N802 - NVML's name
            return 9  # NVML_ERROR_DRIVER_NOT_LOADED

    gpus, why = gpu_probe.nvml_gpus(Broken)
    assert gpus == [] and why is not None and "error 9" in why


# --- the capability report -----------------------------------------------------------------


@pytest.fixture
def windows_pc(monkeypatch: pytest.MonkeyPatch) -> None:
    """The Windows node: Windows x86_64, the app's venv (every pin of the constraints: the
    stt extra's faster-whisper, CTranslate2 and cuBLAS wheels), no pynvml, no NVML that
    loads, an RTX 2080 SUPER nvidia-smi sees."""
    installed = {"faster_whisper", "ctranslate2", "nvidia", "sentence_transformers", "yt_dlp"}
    monkeypatch.setattr(caps, "has_module", lambda name: name in installed)
    monkeypatch.setattr(caps, "is_apple_silicon", lambda: False)
    monkeypatch.setattr(caps, "_cuda_wheels", lambda: True)
    monkeypatch.setattr(caps, "_ffmpeg", lambda: None)
    monkeypatch.setattr(gpu_probe, "nvml_gpus", lambda load=None: ([], "NVML did not load"))
    _smi(monkeypatch, WINDOWS_SMI)


def test_the_windows_pc_reports_its_gpu_and_ct2_cuda(windows_pc: None) -> None:
    report = caps.capabilities()
    assert report["gpus"][0] == {
        "name": "NVIDIA GeForce RTX 2080 SUPER",
        "memory_gb": 8.0,
        "compute_capability": "7.5",
        "driver": "581.57",
        "source": "nvidia-smi",
    }
    assert report["gpu_problem"] is None
    assert report["stt_backend"] == "ct2-cuda"
    assert report["extras"] == ["mediaintel", "search", "stt"]
    assert report["mlx"] is False
    # the keys the Machines page reads are all there
    for key in (
        "os",
        "arch",
        "cpu_count",
        "memory_gb",
        "apple_silicon",
        "python",
        "pipeline_version",
    ):
        assert key in report


def test_stt_on_the_cpu_without_a_gpu(windows_pc: None, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(vb, "_nvidia_smi", lambda: None)
    report = caps.capabilities()
    assert report["gpus"] == []
    assert "nvidia-smi was not found" in str(report["gpu_problem"])
    assert report["stt_backend"] == "ct2-cpu"
    assert "stt" in report["extras"]


def test_no_whisper_engine_is_no_stt(windows_pc: None, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(caps, "has_module", lambda name: name in {"yt_dlp"})
    report = caps.capabilities()
    assert report["stt_backend"] is None and report["extras"] == ["mediaintel"]


def test_apple_silicon_is_mlx(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(caps, "is_apple_silicon", lambda: True)
    monkeypatch.setattr(caps, "has_module", lambda name: name == "mlx_whisper")
    assert caps.stt_backend([]) == "mlx"
    monkeypatch.setattr(caps, "has_module", lambda name: False)
    assert caps.stt_backend([]) is None


def test_a_broken_probe_is_logged_not_raised(monkeypatch: pytest.MonkeyPatch) -> None:
    def boom() -> object:
        raise RuntimeError("driver wedged")

    monkeypatch.setattr(gpu_probe, "probe", boom)
    gpus, why = caps._nvidia()
    assert gpus == [] and why is not None and "driver wedged" in why
