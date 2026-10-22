"""Where Whisper loads on CTranslate2 (review of story 38.8, items 2 and 3): a GPU without
the memory free for it leaves it to the CPU, a load that fails on the GPU is not tried
again, and the default models download at a pinned commit, their files checked."""

from __future__ import annotations

import hashlib
import os
import sys
import types
from pathlib import Path
from typing import Any

import numpy as np
import pytest
from huggingface_hub import snapshot_download  # noqa: F401 - loaded here, not in a timed test

from maktaba_pipeline.models import fetch_cli
from maktaba_pipeline.stt import ct2, engine
from maktaba_pipeline.stt.settings import WhisperSettings

pytestmark = pytest.mark.unit

GPU_TYPES = ["float32", "float16", "int8_float16", "int8_float32", "int8"]
CPU_TYPES = ["float32", "int8_float32", "int8"]


class FakeWhisperModel:
    """faster-whisper's ``WhisperModel``: records where it was asked to load."""

    made: list[tuple[str, int, str, int]] = []
    cuda_fails = False

    def __init__(self, folder: str, **kw: Any) -> None:
        FakeWhisperModel.made.append(
            (kw["device"], kw["device_index"], kw["compute_type"], kw["cpu_threads"])
        )
        if kw["device"] == "cuda" and FakeWhisperModel.cuda_fails:
            raise RuntimeError("CUDA failed with error out of memory")
        self.hf_tokenizer = object()
        self.model = types.SimpleNamespace(is_multilingual=True)
        self.feature_extractor = types.SimpleNamespace(mel_filters=np.zeros((128, 201)))
        self.find_alignment: Any = lambda *a: []


@pytest.fixture
def fake_engine(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Path:
    """CTranslate2 and faster-whisper stand-ins, CUDA usable, a model folder here."""
    c2 = types.ModuleType("ctranslate2")
    c2.get_cuda_device_count = lambda: 1  # type: ignore[attr-defined]
    c2.get_supported_compute_types = (  # type: ignore[attr-defined]
        lambda device, index=0: GPU_TYPES if device == "cuda" else CPU_TYPES
    )
    c2.StorageView = types.SimpleNamespace(from_array=lambda a: a)  # type: ignore[attr-defined]
    c2.__version__ = "4.6.3"  # type: ignore[attr-defined]
    fw = types.ModuleType("faster_whisper")
    fw.WhisperModel = FakeWhisperModel  # type: ignore[attr-defined]
    tok = types.ModuleType("faster_whisper.tokenizer")
    tok.Tokenizer = lambda *a, **k: object()  # type: ignore[attr-defined]
    tr = types.ModuleType("faster_whisper.transcribe")
    tr.get_suppressed_tokens = lambda t, s: []  # type: ignore[attr-defined]
    for name, mod in (
        ("ctranslate2", c2),
        ("faster_whisper", fw),
        ("faster_whisper.tokenizer", tok),
        ("faster_whisper.transcribe", tr),
    ):
        monkeypatch.setitem(sys.modules, name, mod)
    import importlib.metadata

    monkeypatch.setattr(importlib.metadata, "version", lambda name: "1.2.1")
    folder = tmp_path / "model"
    folder.mkdir()
    monkeypatch.setattr(ct2, "model_dir", lambda model: str(folder))
    monkeypatch.setattr(ct2, "cuda_problem", lambda: None)
    monkeypatch.setattr(ct2, "_GPU_FAILED", [])
    monkeypatch.setattr(ct2, "_LOGGED", set())
    monkeypatch.setattr(FakeWhisperModel, "made", [])
    monkeypatch.setattr(FakeWhisperModel, "cuda_fails", False)
    return folder


def _free(monkeypatch: pytest.MonkeyPatch, gb: float | None) -> None:
    monkeypatch.setattr(ct2, "gpu_free_gb", lambda index: gb)


def test_whisper_goes_on_a_gpu_with_its_memory_free(
    fake_engine: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _free(monkeypatch, 4.8)  # this PC's RTX 2080 SUPER, the desktop on it
    ld = ct2.load(WhisperSettings(max_batch=8, device="auto"))
    assert (ld.device, ld.compute_type) == ("cuda", "float16")
    _free(monkeypatch, None)  # no NVML to ask: as before, the GPU
    assert ct2.load(WhisperSettings(max_batch=8, device="auto")).device == "cuda"


def test_a_gpu_without_it_leaves_whisper_to_the_cpu(
    fake_engine: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _free(monkeypatch, 2.5)  # a 3 GB card with the desktop on it: float16, 8 files need 3.7
    logged: list[dict[str, Any]] = []
    monkeypatch.setattr(ct2._log, "warning", lambda event, **kw: logged.append(kw))
    ld = ct2.load(WhisperSettings(max_batch=8, device="auto"))
    assert (ld.device, ld.compute_type) == ("cpu", "int8")
    assert FakeWhisperModel.made == [("cpu", 0, "int8", ct2.cpu_threads_default())]
    assert "2.5 GiB free" in logged[0]["reason"] and "needs 3.7 GiB" in logged[0]["reason"]
    # int8_float16 and four files fit in it
    ld = ct2.load(WhisperSettings(max_batch=4, device="auto", compute_type="int8_float16"))
    assert (ld.device, ld.compute_type) == ("cuda", "int8_float16")
    # the operator asked for the GPU: the GPU
    ld = ct2.load(WhisperSettings(max_batch=8, device="cuda"))
    assert ld.device == "cuda"
    # the operator's CPU threads stand
    _free(monkeypatch, 1.0)
    ct2.load(WhisperSettings(max_batch=8, device="auto", cpu_threads=6))
    assert FakeWhisperModel.made[-1] == ("cpu", 0, "int8", 6)


def test_a_failed_load_on_the_gpu_is_remembered(
    fake_engine: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _free(monkeypatch, 6.0)
    FakeWhisperModel.cuda_fails = True
    ld = ct2.load(WhisperSettings(max_batch=8, device="auto"))
    assert ld.device == "cpu"
    assert [m[0] for m in FakeWhisperModel.made] == ["cuda", "cpu"]
    assert "out of memory" in ct2._GPU_FAILED[0]
    # the next model of this process goes to the CPU without trying the GPU again
    ct2.load(WhisperSettings(max_batch=4, device="auto"))
    assert [m[0] for m in FakeWhisperModel.made] == ["cuda", "cpu", "cpu"]
    assert ct2.choose_device("auto") == ("cpu", 0)
    # asked for the GPU, a failure is the operator's to see
    with pytest.raises(RuntimeError, match="out of memory"):
        ct2.load(WhisperSettings(max_batch=4, device="cuda"))


def test_what_whisper_needs_on_the_gpu() -> None:
    # the measured turbo: 2.3 GB in float16, 1.2 in int8_float16, 0.14 a file, 0.25 spare
    assert ct2.gpu_need_gb("float16", 8) == pytest.approx(2.3 + 8 * 0.14 + 0.25)
    assert ct2.gpu_need_gb("int8_float16", 4) == pytest.approx(1.2 + 4 * 0.14 + 0.25)
    # large-v3's weights are 3.09 GB against turbo's 1.62
    assert ct2.gpu_need_gb("float16", 8, 3.09) == pytest.approx(2.3 * 3.09 / 1.62 + 1.37)
    assert ct2.gpu_need_gb("bfloat16", 1) == ct2.gpu_need_gb("float32", 1)  # the most


def test_the_model_s_weights_size_the_check(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _free(monkeypatch, 3.0)
    assert ct2.gpu_short(0, "float16", 8) is not None  # turbo's 3.7 GiB
    (tmp_path / "model.bin").write_bytes(b"\0" * 1000)  # a tiny model
    assert ct2.gpu_short(0, "float16", 8, str(tmp_path)) is None


def test_the_driver_s_count_of_free_memory() -> None:
    """NVML through ctypes, where this machine has an NVIDIA driver."""
    free = ct2.gpu_free_gb(0)
    if free is None:
        pytest.skip("no NVIDIA driver here")
    assert 0 <= free < 1024
    assert ct2.gpu_free_gb(64) is None  # no such GPU


def test_gpus_are_numbered_as_nvidia_smi_numbers_them() -> None:
    assert os.environ.get("CUDA_DEVICE_ORDER") == "PCI_BUS_ID"


# --- the pinned downloads ----------------------------------------------------------------


def test_the_default_models_are_pinned() -> None:
    import fnmatch

    repos = {engine.DEFAULT_MODEL[engine.CT2], *engine._CT2_OF_MLX.values()}
    assert repos <= set(engine.CT2_PINS)
    for repo, pin in engine.CT2_PINS.items():
        assert len(pin.revision) == 40 and int(pin.revision, 16) >= 0, repo
        for pattern in engine.CT2_FILES:  # every file the engine loads has its hash
            assert [n for n in pin.sha256 if fnmatch.fnmatch(n, pattern)], (repo, pattern)
        assert all(len(h) == 64 and int(h, 16) >= 0 for h in pin.sha256.values()), repo


def _pinned(tmp_path: Path, files: dict[str, bytes]) -> tuple[Path, engine.Pin]:
    snap = tmp_path / "snapshots" / ("a" * 40)
    snap.mkdir(parents=True)
    for name, data in files.items():
        (snap / name).write_bytes(data)
    pin = engine.Pin("a" * 40, {n: hashlib.sha256(d).hexdigest() for n, d in files.items()})
    return snap, pin


def test_a_pinned_snapshot_is_checked_once(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    snap, pin = _pinned(tmp_path, {"model.bin": b"weights", "config.json": b"{}"})
    engine.verify(snap, pin)
    assert (snap / engine.VERIFIED).read_text(encoding="utf-8").strip() == pin.revision
    (snap / "model.bin").write_bytes(b"changed after the check")
    engine.verify(snap, pin)  # checked already: not hashed again


def test_a_file_that_is_not_the_pinned_one_is_removed_and_refused(tmp_path: Path) -> None:
    snap, pin = _pinned(tmp_path, {"model.bin": b"weights", "config.json": b"{}"})
    (snap / "model.bin").write_bytes(b"something else")
    (snap / "config.json").unlink()
    with pytest.raises(engine.PinMismatch, match="model.bin has SHA-256") as e:
        engine.verify(snap, pin)
    assert "config.json is missing" in str(e.value)
    assert not (snap / "model.bin").exists()  # the next download fetches it again
    assert not (snap / engine.VERIFIED).exists()


def test_a_mismatch_names_the_hashes_before_the_folder(tmp_path: Path) -> None:
    """A model's state and the logs cut a message at 300 characters: the hashes come
    first, so a long folder path (on Windows) cuts only itself."""
    snap, pin = _pinned(tmp_path, {"model.bin": b"weights"})
    (snap / "model.bin").write_bytes(b"something else")
    with pytest.raises(engine.PinMismatch) as e:
        engine.verify(snap, pin)
    got = hashlib.sha256(b"something else").hexdigest()
    message = str(e.value)
    assert message.startswith(f"model.bin has SHA-256 {got}, not {pin.sha256['model.bin']}")
    assert message.endswith(f"in {snap}")


def test_the_model_is_fetched_at_its_pin(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    import huggingface_hub

    repo = engine.DEFAULT_MODEL[engine.CT2]
    snap, pin = _pinned(tmp_path, {"model.bin": b"weights"})
    monkeypatch.setitem(engine.CT2_PINS, repo, pin)
    asked: list[dict[str, Any]] = []

    def download(repo_id: str, **kw: Any) -> str:
        asked.append({"repo": repo_id, **kw})
        return str(snap)

    monkeypatch.setattr(huggingface_hub, "snapshot_download", download)
    assert ct2.model_dir(repo) == str(snap)
    assert asked == [{"repo": repo, "revision": pin.revision, "allow_patterns": engine.CT2_FILES}]
    assert (snap / engine.VERIFIED).is_file()
    # a repo of the operator's own: as its repo has it
    assert ct2.model_dir("someone/faster-whisper-tiny") == str(snap)
    assert asked[-1]["revision"] is None


def test_models_download_fetches_whisper_at_its_pin_and_checks_it(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import huggingface_hub

    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    for var in ("MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE"):
        monkeypatch.delenv(var, raising=False)
    repo = engine.DEFAULT_MODEL[engine.CT2]
    snap, pin = _pinned(tmp_path, {"model.bin": b"weights"})
    monkeypatch.setitem(engine.CT2_PINS, repo, pin)
    whisper = fetch_cli.items({"whisper"})[0]
    assert whisper.model == repo and whisper.pin is pin
    asked: list[Any] = []

    def download(repo_id: str, **kw: Any) -> str:
        asked.append(kw.get("revision"))
        return str(snap)

    monkeypatch.setattr(huggingface_hub, "snapshot_download", download)
    out = fetch_cli._Out(as_json=True)
    fetch_cli._download(whisper, out, None)
    assert whisper.state == "ready" and asked == [pin.revision]
    assert (snap / engine.VERIFIED).is_file() and (snap / fetch_cli.COMPLETE).is_file()

    (snap / engine.VERIFIED).unlink()
    (snap / fetch_cli.COMPLETE).unlink()
    (snap / "model.bin").write_bytes(b"tampered")
    whisper = fetch_cli.items({"whisper"})[0]
    fetch_cli._download(whisper, out, None)
    assert whisper.state == "failed" and pin.sha256["model.bin"] in whisper.message
    assert not (snap / fetch_cli.COMPLETE).exists()
