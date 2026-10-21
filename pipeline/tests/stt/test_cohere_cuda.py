"""The second listen on Windows and Linux (story 38.9, stt/cohere_cuda.py): whether this
machine runs it, whether its GPU has room for it now, how it hears, and what the
transcript and ``models`` say when it cannot. torch and transformers are stood in for: no
GPU is asked, no model is loaded."""

from __future__ import annotations

import sys
import types
from collections.abc import Iterator
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.classify.quran import verse_runs
from maktaba_pipeline.engine.stages import transcribe as stage
from maktaba_pipeline.stt import cohere, cohere_cuda, engine, model_share
from maktaba_pipeline.stt.settings import SAMPLE_RATE, Sentence, Transcript, WhisperSettings
from tests.conftest import NO_TEST_GPU

pytestmark = pytest.mark.unit

verse_runs(["قال"] * 4)  # the Quran's index, read once here, not inside a timed test

#: the machine check itself (the tests' conftest stands in for it everywhere else)
REAL_GPU_PROBLEM = cohere_cuda._gpu_problem
GIB = 1 << 30
MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"


class OutOfMemory(RuntimeError):
    """torch.cuda.OutOfMemoryError"""


@dataclass
class FakeCuda:
    """torch.cuda on one machine: its GPUs (total GiB, compute capability) and free memory."""

    gpus: list[tuple[str, float, tuple[int, int]]] = field(
        default_factory=lambda: [("NVIDIA GeForce RTX 3080 Ti", 12.0, (8, 6))]
    )
    free_gb: float = 10.0
    archs: list[str] = field(default_factory=lambda: ["sm_75", "sm_80", "sm_86", "sm_90"])
    broken: Exception | None = None
    emptied: int = 0
    OutOfMemoryError: type[Exception] = OutOfMemory

    def is_available(self) -> bool:
        if self.broken is not None:
            raise self.broken
        return bool(self.gpus)

    def device_count(self) -> int:
        return len(self.gpus)

    def get_device_properties(self, i: int) -> Any:
        name, total, (major, minor) = self.gpus[i]
        return types.SimpleNamespace(
            name=name, total_memory=int(total * GIB), major=major, minor=minor
        )

    def get_device_capability(self, i: int) -> tuple[int, int]:
        return self.gpus[i][2]

    def get_arch_list(self) -> list[str]:
        return self.archs

    def mem_get_info(self, device: Any = 0) -> tuple[int, int]:
        return int(self.free_gb * GIB), int(self.gpus[0][1] * GIB)

    def empty_cache(self) -> None:
        self.emptied += 1


class _InferenceMode:
    def __enter__(self) -> None:
        return None

    def __exit__(self, *_a: object) -> None:
        return None


def fake_torch(
    cuda: FakeCuda, *, version: str = "2.14.1+cu130", cuda_version: str | None = "13.0"
) -> Any:
    return types.SimpleNamespace(
        __version__=version,
        version=types.SimpleNamespace(cuda=cuda_version),
        cuda=cuda,
        float16="torch.float16",
        bfloat16="torch.bfloat16",
        device=lambda kind, index: f"{kind}:{index}",
        inference_mode=_InferenceMode,
    )


@pytest.fixture
def gpu(monkeypatch: pytest.MonkeyPatch) -> Iterator[FakeCuda]:
    """This machine: CTranslate2's engine, the stt extra installed, one NVIDIA GPU."""
    import importlib.util

    cuda = FakeCuda()
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(cohere_cuda, "_gpu_problem", REAL_GPU_PROBLEM)
    monkeypatch.setitem(sys.modules, "torch", fake_torch(cuda))
    real_find = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: (
            object()
            if name in ("torch", "transformers", "accelerate", "librosa")
            else real_find(name, *a)
        ),
    )
    monkeypatch.setattr(cohere, "_REC", {})
    monkeypatch.setattr(cohere, "_PUBLISHED", set())
    # a model is on this machine only as a folder (never the real Hugging Face cache)
    monkeypatch.setattr(
        cohere_cuda, "_local_folder", lambda m: Path(m) if Path(m).is_dir() else None
    )
    monkeypatch.delenv("MAKTABA_STT_DEVICE", raising=False)
    REAL_GPU_PROBLEM.cache_clear()
    yield cuda
    REAL_GPU_PROBLEM.cache_clear()


# --- which GPU ----------------------------------------------------------------------------


def test_the_second_listen_uses_whisper_s_gpu() -> None:
    assert cohere_cuda.device_index("auto") == 0
    assert cohere_cuda.device_index("") == 0
    assert cohere_cuda.device_index("CUDA") == 0
    assert cohere_cuda.device_index("cuda:1") == 1
    assert cohere_cuda.device_index("cpu") is None
    assert cohere_cuda.device_index("cuda:x") is None


def test_a_gpu_runs_the_builds_for_its_generation_or_ptx() -> None:
    archs = ["sm_75", "sm_80", "sm_86", "sm_90", "sm_120"]
    assert cohere_cuda._runs_on((7, 5), archs)  # Turing: the RTX 2080 SUPER
    assert cohere_cuda._runs_on((8, 9), archs)  # Ada runs sm_86's code
    assert cohere_cuda._runs_on((12, 0), archs)
    assert not cohere_cuda._runs_on((6, 1), archs)  # Pascal: CUDA 13 dropped it
    assert not cohere_cuda._runs_on((7, 0), archs)  # Volta
    assert cohere_cuda._runs_on((9, 0), ["sm_80", "compute_80"])  # PTX: any later GPU
    assert not cohere_cuda._runs_on((7, 5), ["sm_80", "compute_80"])


# --- whether this machine has a second listen at all ---------------------------------------


def test_a_gpu_with_room_runs_it(gpu: FakeCuda) -> None:
    assert cohere.unavailable() is None


def test_on_apple_silicon_it_always_runs(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cohere_cuda, "problem", lambda _d: pytest.fail("asked the GPU"))
    assert cohere.unavailable() is None  # the tests' engine is MLX


def test_transcription_on_the_cpu_has_none(gpu: FakeCuda, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "cpu")
    why = cohere.unavailable()
    assert why is not None and "MAKTABA_STT_DEVICE=cpu" in why and "NVIDIA GPU only" in why


def test_a_cpu_build_of_torch_says_so(gpu: FakeCuda, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setitem(
        sys.modules, "torch", fake_torch(gpu, version="2.14.1+cpu", cuda_version=None)
    )
    why = cohere.unavailable()
    assert why is not None and "torch 2.14.1+cpu is a CPU build" in why
    assert "far too slow" in why  # never the 2 B model on the CPU


def test_no_gpu_or_a_broken_driver(gpu: FakeCuda) -> None:
    gpu.gpus = []
    assert "no CUDA device torch can use" in str(cohere.unavailable())
    REAL_GPU_PROBLEM.cache_clear()
    gpu.broken = RuntimeError("CUDA driver version is insufficient")
    assert cohere.unavailable() == "CUDA: RuntimeError: CUDA driver version is insufficient"


def test_the_gpu_asked_for_must_exist(gpu: FakeCuda) -> None:
    assert cohere.unavailable("cuda:1") == "this machine has no CUDA device 1"


def test_a_gpu_older_than_the_torch_build(gpu: FakeCuda) -> None:
    gpu.gpus = [("NVIDIA GeForce GTX 1080 Ti", 11.0, (6, 1))]
    why = str(cohere.unavailable())
    assert "GTX 1080 Ti (compute capability 6.1) is older than" in why
    assert "CUDA 13.0" in why


def test_a_gpu_too_small_to_hold_it_beside_whisper(gpu: FakeCuda) -> None:
    gpu.gpus = [("NVIDIA GeForce RTX 3050", cohere_cuda.MIN_GPU_GB - 1.5, (8, 6))]
    why = str(cohere.unavailable())
    assert "RTX 3050 has" in why and f"{cohere_cuda.MIN_GPU_GB:g} GB or more" in why


def test_a_missing_package(gpu: FakeCuda, monkeypatch: pytest.MonkeyPatch) -> None:
    import importlib.util

    monkeypatch.setattr(
        importlib.util, "find_spec", lambda name, *a: None if name == "librosa" else object()
    )
    assert cohere.unavailable() == "librosa is not installed (the stt extra)"


def test_the_windows_threshold_leaves_out_an_8_gb_card() -> None:
    """Measured on an RTX 2080 SUPER under Windows: the driver's budget leaves 5.8 GiB
    once Whisper is loaded, short of the 6.4 the second listen needs beside it."""
    need = cohere_cuda.need_gb(cohere_cuda.WEIGHTS_GB, 4)
    if sys.platform == "win32":
        assert cohere_cuda.MIN_GPU_GB > 8.0 and need > 5.8
    else:
        assert cohere_cuda.MIN_GPU_GB <= 7.6  # an 8 GB card's reported size


# --- room on the GPU now ------------------------------------------------------------------


def test_room_needs_the_weights_a_batch_and_whisper_s_files(gpu: FakeCuda, tmp_path: Path) -> None:
    folder = tmp_path / "cohere"
    folder.mkdir()
    (folder / "model.safetensors").write_bytes(b"\0" * (1 << 20))
    weights = cohere_cuda.weights_gb(str(folder))
    assert weights == pytest.approx(1 / 1024)
    need = cohere_cuda.need_gb(weights, 8)
    assert need == pytest.approx(
        weights
        + cohere_cuda.LOAD_EXTRA_GB
        + cohere_cuda.MIN_BATCH * cohere_cuda.STRETCH_GB
        + 8 * cohere_cuda.WHISPER_FILE_GB
    )
    gpu.free_gb = need + 0.01
    assert cohere_cuda.room(str(folder), WhisperSettings(max_batch=8)) is None
    gpu.free_gb = need - 0.01
    why = str(cohere_cuda.room(str(folder), WhisperSettings(max_batch=8)))
    assert why.startswith("too little free GPU memory for the second listen:")
    assert "room for Whisper's 8 files" in why


def test_a_model_not_downloaded_yet_counts_as_the_default_s_size(gpu: FakeCuda) -> None:
    assert cohere_cuda.weights_gb("someone/not-here", None) == cohere_cuda.WEIGHTS_GB


# --- hearing ------------------------------------------------------------------------------


@dataclass
class FakeModel:
    """AutoModelForSpeechSeq2Seq: each stretch "heard" as its length in seconds."""

    oom_above: int = 99
    batches: list[int] = field(default_factory=list)

    def eval(self) -> FakeModel:
        return self

    def generate(self, input_features: Any, max_new_tokens: int, **_k: Any) -> list[int]:
        if len(input_features) > self.oom_above:
            raise OutOfMemory("CUDA out of memory")
        self.batches.append(len(input_features))
        assert max_new_tokens == cohere_cuda.MAX_TOKENS
        return list(input_features)


class FakeInputs(dict[str, Any]):
    def to(self, device: str, dtype: Any) -> FakeInputs:
        self["moved"] = (device, dtype)
        return self


class FakeProcessor:
    """AutoProcessor: a stretch longer than 30 s comes back in two parts, rejoined."""

    def __call__(self, audio: list[Any], sampling_rate: int, language: str, **_k: Any) -> Any:
        assert sampling_rate == SAMPLE_RATE and language == "ar"
        parts: list[int] = []
        index: list[tuple[int, int | None]] = []
        for i, a in enumerate(audio):
            sec = round(len(a) / SAMPLE_RATE)
            if sec > 30:
                parts += [30, sec - 30]
                index += [(i, 0), (i, 1)]
            else:
                parts.append(sec)
                index.append((i, None))
        return FakeInputs(input_features=parts, audio_chunk_index=index)

    def decode(self, ids: list[int], audio_chunk_index: Any, language: str, **_k: Any) -> list[str]:
        texts = [f" {n}s " for n in ids]
        out: dict[int, str] = {}
        for (i, _part), t in zip(audio_chunk_index, texts, strict=True):
            out[i] = (out.get(i, "") + " " + t.strip()).strip()
        return [out[i] for i in sorted(out)]


@pytest.fixture
def transformers(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """transformers' Auto classes, loading a cohere_asr (or another) model."""
    state: dict[str, Any] = {"model_type": "cohere_asr", "model": FakeModel(), "loads": []}

    def config(folder: Any) -> Any:
        return types.SimpleNamespace(model_type=state["model_type"])

    def model(folder: Any, dtype: Any, device_map: str) -> FakeModel:
        state["loads"].append((Path(folder), dtype, device_map))
        return state["model"]  # type: ignore[no-any-return]

    fake = types.SimpleNamespace(
        AutoConfig=types.SimpleNamespace(from_pretrained=config),
        AutoProcessor=types.SimpleNamespace(from_pretrained=lambda _f: FakeProcessor()),
        AutoModelForSpeechSeq2Seq=types.SimpleNamespace(from_pretrained=model),
    )
    bars = types.SimpleNamespace(disable_progress_bar=lambda: state.update(no_bars=True))
    monkeypatch.setitem(sys.modules, "transformers", fake)
    monkeypatch.setitem(sys.modules, "transformers.utils", types.SimpleNamespace(logging=bars))
    import importlib.metadata

    real_version = importlib.metadata.version
    monkeypatch.setattr(
        importlib.metadata,
        "version",
        lambda name: "5.17.0" if name == "transformers" else real_version(name),
    )
    return state


def secs(*seconds: float) -> list[np.ndarray]:
    return [np.zeros(int(s * SAMPLE_RATE), np.float32) for s in seconds]


def test_it_hears_stretches_in_batches_the_gpu_holds(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path
) -> None:
    gpu.free_gb = (
        cohere_cuda.MARGIN_GB + 4 * cohere_cuda.WHISPER_FILE_GB + 3.5 * cohere_cuda.STRETCH_GB
    )
    rec = cohere_cuda.CudaRecogniser(
        "m", tmp_path, settings=WhisperSettings(max_batch=4, device="cuda:0")
    )
    assert rec.batch == 3  # what is left once loaded, Whisper's room kept
    assert transformers["loads"] == [(tmp_path, "torch.bfloat16", "cuda:0")]  # Ampere
    assert rec.runtime == "transformers 5.17.0, torch 2.14.1+cu130, cuda:0 bfloat16"
    assert transformers["no_bars"]  # nothing drawn into the worker's log
    out = rec(secs(5, 12, 40, 3, 28, 9, 20))
    assert out == ["5s", "12s", "30s 10s", "3s", "28s", "9s", "20s"]
    assert transformers["model"].batches == [4, 3, 1]  # the 40 s stretch is two parts
    assert gpu.emptied == 1  # its memory given back to Whisper


def test_an_older_gpu_hears_in_float16(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path
) -> None:
    gpu.gpus = [("NVIDIA GeForce RTX 2080 Ti", 11.0, (7, 5))]
    rec = cohere_cuda.CudaRecogniser("m", tmp_path)
    assert transformers["loads"][0][1] == "torch.float16"
    assert rec.runtime.endswith("cuda:0 float16")
    assert rec.batch == cohere_cuda.MAX_BATCH  # 10 GB free: the most refine hands it


def test_a_batch_that_does_not_fit_is_halved(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path
) -> None:
    transformers["model"].oom_above = 2
    rec = cohere_cuda.CudaRecogniser("m", tmp_path)
    assert rec(secs(1, 2, 3, 4, 5)) == ["1s", "2s", "3s", "4s", "5s"]
    assert rec.batch == 2
    assert transformers["model"].batches == [2, 2, 1]


def test_out_of_memory_with_one_stretch_is_an_error(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path
) -> None:
    transformers["model"].oom_above = 0
    rec = cohere_cuda.CudaRecogniser("m", tmp_path)
    with pytest.raises(OutOfMemory):
        rec(secs(1, 2))
    assert rec.batch == 1 and gpu.emptied > 0


def test_only_a_cohere_asr_model(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path
) -> None:
    transformers["model_type"] = "whisper"
    with pytest.raises(
        RuntimeError, match="is a whisper model: the second listen loads a cohere_asr"
    ):
        cohere_cuda.CudaRecogniser("openai/whisper-large-v3", tmp_path)


# --- the process's recogniser -------------------------------------------------------------


def test_the_recogniser_loads_once_from_hugging_face(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fetched: list[str] = []

    def snapshot(model: str) -> Path:
        fetched.append(model)
        return tmp_path

    monkeypatch.setattr(cohere_cuda, "model_dir", snapshot)
    monkeypatch.setattr(model_share, "publish_from_cache", lambda *_a: None)
    settings = WhisperSettings(max_batch=4)
    rec = cohere.recogniser(MODEL, None, settings)
    assert isinstance(rec, cohere_cuda.CudaRecogniser) and rec.source == "hf"
    assert cohere.loaded(MODEL) and cohere.recogniser(MODEL, None, settings) is rec
    assert fetched == [MODEL]


def test_no_room_now_is_asked_again_next_time(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(cohere_cuda, "model_dir", lambda _m: tmp_path)
    gpu.free_gb = 2.0
    with pytest.raises(RuntimeError, match="too little free GPU memory"):
        cohere.recogniser(MODEL, None, WhisperSettings(max_batch=4))
    assert not cohere.loaded(MODEL) and transformers["loads"] == []
    gpu.free_gb = 10.0  # another program let go of the GPU
    assert cohere.recogniser(MODEL, None, WhisperSettings(max_batch=4)).source == "hf"


def test_without_a_login_the_home_s_copy_on_the_share(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    def refused(_model: str) -> Path:
        raise RuntimeError("401 gated repo")

    copy = tmp_path / "copy"
    monkeypatch.setattr(cohere_cuda, "model_dir", refused)
    monkeypatch.setattr(model_share, "from_share", lambda _m, _s: copy)
    rec = cohere.recogniser(MODEL, tmp_path / "share", WhisperSettings(max_batch=8))
    assert rec.source == "share" and transformers["loads"][0][0] == copy


def test_a_model_that_does_not_load_is_not_fetched_from_the_share(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    def too_big(*_a: Any, **_k: Any) -> Any:
        raise OutOfMemory("CUDA out of memory")

    monkeypatch.setattr(cohere_cuda, "model_dir", lambda _m: tmp_path)
    monkeypatch.setattr(
        sys.modules["transformers"].AutoModelForSpeechSeq2Seq, "from_pretrained", too_big
    )
    monkeypatch.setattr(model_share, "from_share", lambda *_a: pytest.fail("copied the share's"))
    with pytest.raises(cohere_cuda.LoadError, match="OutOfMemory: CUDA out of memory"):
        cohere.recogniser(MODEL, tmp_path / "share", WhisperSettings(max_batch=8))
    assert gpu.emptied == 1 and not cohere.loaded(MODEL)


def test_no_gpu_never_loads_anything(gpu: FakeCuda, monkeypatch: pytest.MonkeyPatch) -> None:
    gpu.gpus = []
    monkeypatch.setattr(cohere_cuda, "room", lambda *_a: pytest.fail("asked for room"))
    with pytest.raises(RuntimeError, match="no CUDA device torch can use"):
        cohere.recogniser(MODEL, None, WhisperSettings())


# --- the transcript says why ---------------------------------------------------------------


def transcript() -> Transcript:
    text = "قال الشيخ رحمه الله"
    words = [(0.5 * i, 0.5 * i + 0.5, w) for i, w in enumerate(text.split(" "))]
    return Transcript([Sentence(0.0, 2.0, text, -0.1, 0.01, words)], 2.0, "ar", "m", 1.0)


def test_the_transcript_records_why_there_was_no_second_listen(
    gpu: FakeCuda, monkeypatch: pytest.MonkeyPatch
) -> None:
    gpu.free_gb = 1.0
    t = transcript()
    out = stage._refine(t, np.zeros(3 * SAMPLE_RATE, np.float32), MODEL, None, WhisperSettings())
    assert out["source"] == "skipped" and out["model"] == MODEL
    assert out["skipped"].startswith("RuntimeError: too little free GPU memory")
    assert t.sentences[0].text == "قال الشيخ رحمه الله"


def test_the_transcript_records_what_heard_it(
    gpu: FakeCuda, transformers: dict[str, Any], tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(cohere_cuda, "model_dir", lambda _m: tmp_path)
    monkeypatch.setattr(model_share, "publish_from_cache", lambda *_a: None)
    monkeypatch.setattr(cohere, "dictionary", lambda: None)
    out = stage._refine(
        transcript(),
        np.zeros(3 * SAMPLE_RATE, np.float32),
        MODEL,
        None,
        WhisperSettings(max_batch=8),
    )
    assert out["source"] == "hf" and out["stretches"] == 1
    assert out["runtime"] == "transformers 5.17.0, torch 2.14.1+cu130, cuda:0 bfloat16"


def test_the_tests_never_ask_this_machine_s_gpu(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    assert cohere.unavailable() == NO_TEST_GPU
