"""The CTranslate2 engine (story 38.8) with a fake model: nothing is downloaded, no GPU.

The fake stands in for ``ctranslate2.models.Whisper`` and faster-whisper's tokenizer and
alignment; the spectrogram, the windows, the batching, the seek and the word placement
are the engine's own.
"""

from __future__ import annotations

import asyncio
import importlib.util
import sys
import types
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import huggingface_hub  # noqa: F401 - imported once here, not inside a timed test
import numpy as np
import pytest

from maktaba_pipeline.engine.stages import transcribe as stage
from maktaba_pipeline.stt import batched, ct2, engine
from maktaba_pipeline.stt.settings import SAMPLE_RATE, FileProgress, WhisperSettings

TS = 1000  # timestamp_begin of the fake tokenizer
EOT = 999
SOT = [900, 901, 902]  # <|startoftranscript|> <|ar|> <|transcribe|>
SOT_PREV = 903


def ts(sec: float) -> int:
    return TS + round(sec / 0.02)


KNOBS = (
    "MAKTABA_STT_MODEL",
    "MAKTABA_STT_BATCH",
    "MAKTABA_STT_DEVICE",
    "MAKTABA_STT_COMPUTE_TYPE",
    "MAKTABA_STT_CPU_THREADS",
    "HF_HUB_OFFLINE",
)


@pytest.fixture
def on_ct2(monkeypatch: pytest.MonkeyPatch) -> None:
    """This machine runs the CTranslate2 engine (Windows, Linux)."""
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    for var in KNOBS:
        monkeypatch.delenv(var, raising=False)


# --- the engine of a machine, and its model -------------------------------------------


@pytest.mark.unit
def test_apple_silicon_runs_mlx_and_everything_else_ctranslate2() -> None:
    assert engine.for_machine("Darwin", "arm64") == engine.MLX
    assert engine.for_machine("Windows", "AMD64") == engine.CT2
    assert engine.for_machine("Linux", "x86_64") == engine.CT2
    assert engine.for_machine("Darwin", "x86_64") == engine.CT2


@pytest.mark.unit
def test_each_engine_has_turbo_by_default_and_the_same_fingerprint(on_ct2: None) -> None:
    ct2_model = stage.settings_from_env().model
    assert ct2_model == "dropbox-dash/faster-whisper-large-v3-turbo"
    assert engine.model_for(engine.MLX, None) == "mlx-community/whisper-large-v3-turbo"
    # the Mac recorded the MLX name: a PC computes the same fingerprint, no re-transcribing
    assert engine.fingerprint_model(ct2_model) == "mlx-community/whisper-large-v3-turbo"
    assert engine.fingerprint_model("someone/else") == "someone/else"


@pytest.mark.unit
def test_an_mlx_model_becomes_its_ctranslate2_conversion_with_a_log_line(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    logged: list[dict[str, Any]] = []
    monkeypatch.setattr(engine, "_MAPPED", set())
    monkeypatch.setattr(engine._log, "warning", lambda event, **kw: logged.append(kw))
    monkeypatch.setenv("MAKTABA_STT_MODEL", "mlx-community/whisper-large-v3-turbo")
    assert stage.settings_from_env().model == engine.DEFAULT_MODEL[engine.CT2]
    assert stage.settings_from_env().model == engine.DEFAULT_MODEL[engine.CT2]
    assert len(logged) == 1 and logged[0]["model"] == "mlx-community/whisper-large-v3-turbo"
    assert engine.model_for(engine.CT2, "mlx-community/whisper-large-v3-mlx") == (
        "Systran/faster-whisper-large-v3"
    )
    # an MLX model with no CTranslate2 twin: the default, said
    assert (
        engine.model_for(engine.CT2, "mlx-community/whisper-small-mlx")
        == (engine.DEFAULT_MODEL[engine.CT2])
    )
    assert logged[-1]["to"] == engine.DEFAULT_MODEL[engine.CT2]
    # the old name of the turbo conversion, a CTranslate2 repo, a folder: as given
    assert (
        engine.model_for(engine.CT2, "mobiuslabsgmbh/faster-whisper-large-v3-turbo")
        == (engine.DEFAULT_MODEL[engine.CT2])
    )
    assert engine.model_for(engine.CT2, "Systran/faster-whisper-small") == (
        "Systran/faster-whisper-small"
    )
    folder = tmp_path / "whisper-mlx"
    folder.mkdir()
    assert engine.model_for(engine.CT2, str(folder)) == str(folder)
    # the Mac keeps what it is given
    assert engine.model_for(engine.MLX, "mlx-community/whisper-small-mlx") == (
        "mlx-community/whisper-small-mlx"
    )


@pytest.mark.unit
def test_a_size_name_is_the_repo_faster_whisper_downloads(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake = types.ModuleType("faster_whisper.utils")
    fake._MODELS = {  # type: ignore[attr-defined]
        "large-v3": "Systran/faster-whisper-large-v3",
        "turbo": "mobiuslabsgmbh/faster-whisper-large-v3-turbo",
    }
    monkeypatch.setitem(sys.modules, "faster_whisper.utils", fake)
    assert engine.ct2_repo("large-v3") == "Systran/faster-whisper-large-v3"
    assert engine.ct2_repo("turbo") == engine.DEFAULT_MODEL[engine.CT2]
    assert engine.ct2_repo("org/model") == "org/model"


@pytest.mark.unit
def test_settings_read_the_engine_s_own_knobs(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = stage.settings_from_env()
    assert (s.max_batch, s.device, s.compute_type, s.cpu_threads) == (8, "auto", "auto", 0)
    monkeypatch.setenv("MAKTABA_STT_BATCH", "12")
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "cuda")
    monkeypatch.setenv("MAKTABA_STT_COMPUTE_TYPE", "int8_float16")
    monkeypatch.setenv("MAKTABA_STT_CPU_THREADS", "6")
    monkeypatch.setenv("MAKTABA_STT_GPU_GB", "3")  # MLX's
    s = stage.settings_from_env()
    assert (s.max_batch, s.device, s.compute_type, s.cpu_threads) == (12, "cuda", "int8_float16", 6)
    assert s.gpu_limit_gb == WhisperSettings().gpu_limit_gb
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    s = stage.settings_from_env()
    assert (s.device, s.compute_type, s.gpu_limit_gb) == ("auto", "auto", 3.0)


# --- can this worker transcribe? ---------------------------------------------------------


def _installed(monkeypatch: pytest.MonkeyPatch, *missing: str) -> None:
    real = importlib.util.find_spec

    def find_spec(name: str, *a: Any) -> Any:
        if name in missing:
            return None
        if name in ("faster_whisper", "ctranslate2"):
            return object()
        return real(name, *a)

    monkeypatch.setattr(importlib.util, "find_spec", find_spec)


def test_unavailable_says_why_per_engine(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # Whisper's own needs (with the second listen on, the stage also waits for a machine
    # that runs it: tests/stages/test_transcribe_placement.py)
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    _installed(monkeypatch, "faster_whisper")
    assert stage.unavailable() == "faster-whisper is not installed (the stt extra)"
    _installed(monkeypatch)
    assert stage.unavailable() is None  # a repo is fetched on first use, as on the Mac

    folder = tmp_path / "model"
    folder.mkdir()
    monkeypatch.setenv("MAKTABA_STT_MODEL", str(folder))
    assert "no CTranslate2 model (model.bin)" in str(stage.unavailable())
    (folder / "model.bin").write_bytes(b"\0")
    assert stage.unavailable() is None
    monkeypatch.setenv("MAKTABA_STT_MODEL", str(tmp_path / "gone" / "model"))
    assert "no such folder" in str(stage.unavailable())

    monkeypatch.delenv("MAKTABA_STT_MODEL")
    monkeypatch.setenv("HF_HUB_OFFLINE", "1")
    monkeypatch.setattr("huggingface_hub.try_to_load_from_cache", lambda *a, **k: None)
    assert "Hugging Face is offline" in str(stage.unavailable())
    monkeypatch.setattr("huggingface_hub.try_to_load_from_cache", lambda *a, **k: "/c/model.bin")
    assert stage.unavailable() is None
    monkeypatch.delenv("HF_HUB_OFFLINE")

    # a GPU asked for that is not usable: nothing transcribes here, and it says why
    monkeypatch.setattr(
        ct2, "cuda_problem", lambda: "no CUDA device (an NVIDIA GPU and its driver)"
    )
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "cuda")
    assert stage.unavailable() == (
        "MAKTABA_STT_DEVICE=cuda, but no CUDA device (an NVIDIA GPU and its driver)"
    )
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "auto")  # the CPU then
    assert stage.unavailable() is None
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "gpu")
    assert "auto, cuda, cuda:<n> or cpu" in str(stage.unavailable())

    # the Mac's engine on a PC: as before
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    monkeypatch.setattr("platform.system", lambda: "Windows")
    assert stage.unavailable() == "MLX-Whisper needs Apple Silicon"


@pytest.mark.unit
def test_the_stage_takes_the_machine_s_transcriber(on_ct2: None) -> None:
    s = WhisperSettings(model="x/y", device="cpu")
    t = stage.get_transcriber(s)
    assert isinstance(t, ct2.CT2Transcriber) and t is stage.get_transcriber(s)
    assert t.backend == "ct2-batched"


# --- device and precision ----------------------------------------------------------------


def _fake_ctranslate2(monkeypatch: pytest.MonkeyPatch, devices: int) -> None:
    fake = types.ModuleType("ctranslate2")
    fake.get_cuda_device_count = lambda: devices  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "ctranslate2", fake)


@pytest.mark.unit
def test_the_device_is_cuda_when_usable_else_the_cpu(monkeypatch: pytest.MonkeyPatch) -> None:
    _fake_ctranslate2(monkeypatch, 1)
    monkeypatch.setattr(ct2, "cuda_problem", lambda: None)
    assert ct2.choose_device("auto") == ("cuda", 0)
    assert ct2.choose_device("") == ("cuda", 0)
    assert ct2.choose_device("CPU") == ("cpu", 0)
    assert ct2.choose_device("cuda") == ("cuda", 0)
    with pytest.raises(RuntimeError, match="no CUDA device 1"):
        ct2.choose_device("cuda:1")
    with pytest.raises(ValueError, match="auto, cuda"):
        ct2.choose_device("metal")

    monkeypatch.setattr(
        ct2, "cuda_problem", lambda: "no CUDA device (an NVIDIA GPU and its driver)"
    )
    assert ct2.choose_device("auto") == ("cpu", 0)
    with pytest.raises(RuntimeError, match="MAKTABA_STT_DEVICE=cuda, but no CUDA device"):
        ct2.choose_device("cuda")


@pytest.mark.unit
def test_cuda_needs_a_device_and_cublas(monkeypatch: pytest.MonkeyPatch) -> None:
    for devices, cublas, want in (
        (0, "x", "no CUDA device (an NVIDIA GPU and its driver)"),
        (1, None, "cuBLAS 12 is not installed (nvidia-cublas-cu12, the stt extra)"),
        (1, "x", None),
    ):
        _fake_ctranslate2(monkeypatch, devices)
        monkeypatch.setattr(ct2, "cuda_libraries", lambda c=cublas: c)
        ct2.cuda_problem.cache_clear()
        assert ct2.cuda_problem() == want
    ct2.cuda_problem.cache_clear()


@pytest.mark.unit
def test_cublas_is_loaded_from_the_wheel_first(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    win = sys.platform == "win32"
    sub, name = ("bin", "cublas64_12.dll") if win else ("lib", "libcublas.so.12")
    wheel = tmp_path / "nvidia" / "cublas" / sub
    wheel.mkdir(parents=True)
    (wheel / name).write_bytes(b"")
    if not win:
        (wheel / "libcublasLt.so.12").write_bytes(b"")
    loaded: list[str] = []
    monkeypatch.setattr(ct2, "_nvidia_dirs", lambda s: [wheel] if s == sub else [])

    def load(path: str, **_k: Any) -> bool:
        loaded.append(path)
        return True

    monkeypatch.setattr(ct2, "_load", load)
    monkeypatch.setattr(ct2, "_CUDA", [])
    monkeypatch.setattr(ct2, "_DLL_DIRS", [None])
    assert ct2.cuda_libraries() == str(wheel / name)
    assert loaded[-1] == str(wheel / name)
    assert ct2.cuda_libraries() == str(wheel / name) and len(loaded) == (1 if win else 2)  # once

    monkeypatch.setattr(ct2, "_CUDA", [])
    monkeypatch.setattr(ct2, "_nvidia_dirs", lambda s: [])
    monkeypatch.setattr(ct2, "_load", lambda path, **k: False)
    monkeypatch.delenv("CUDA_PATH", raising=False)
    assert ct2.cuda_libraries() is None


@pytest.mark.unit
def test_the_precision_follows_the_device(monkeypatch: pytest.MonkeyPatch) -> None:
    gpu = ["float32", "float16", "int8_float16", "int8_float32", "int8", "bfloat16"]
    cpu = ["float32", "int8_float32", "int8", "int16"]
    assert ct2.choose_compute_type("cuda", "auto", gpu) == "float16"
    assert ct2.choose_compute_type("cpu", "", cpu) == "int8"
    assert ct2.choose_compute_type("cuda", "INT8_float16", gpu) == "int8_float16"
    logged: list[str] = []
    monkeypatch.setattr(ct2, "_LOGGED", set())
    monkeypatch.setattr(ct2._log, "warning", lambda event, **kw: logged.append(kw["using"]))
    assert ct2.choose_compute_type("cpu", "int8_float16", cpu) == "int8"  # not on a CPU
    assert ct2.choose_compute_type("cuda", "bfloat16", gpu) == "float16"  # not as NumPy
    assert logged == ["int8", "float16"]
    assert ct2.choose_compute_type("cuda", "auto", ["float32", "int8_float32"]) == "float32"


# --- the spectrogram and the windows ------------------------------------------------------


def _filters(n_mels: int = 8) -> np.ndarray:
    rng = np.random.default_rng(0)
    return rng.random((n_mels, ct2.N_FFT // 2 + 1)).astype(np.float32)


def test_the_spectrogram_ends_in_a_window_of_silence_whatever_the_block() -> None:
    rng = np.random.default_rng(1)
    audio = (rng.standard_normal(SAMPLE_RATE * 2) * 0.1).astype(np.float32)
    mel = ct2.log_mel(audio, _filters())
    # frames x mels, one frame per 10 ms of the audio and of the 30 s after it
    assert mel.shape == ((len(audio) + ct2.N_SAMPLES) // ct2.HOP_LENGTH, 8)
    assert mel.dtype == np.float32
    assert mel.max() - mel.min() <= 2.0 + 1e-6  # Whisper's 80 dB floor, scaled by 1/4
    np.testing.assert_allclose(ct2.log_mel(audio, _filters(), block=333), mel, rtol=0, atol=1e-6)


def test_the_spectrogram_is_faster_whispers() -> None:
    fe_mod = pytest.importorskip("faster_whisper.feature_extractor")
    fe = fe_mod.FeatureExtractor(feature_size=128)
    rng = np.random.default_rng(2)
    audio = (rng.standard_normal(SAMPLE_RATE * 3) * 0.2).astype(np.float32)
    theirs = fe(audio, padding=ct2.N_SAMPLES).T  # mels x frames there
    ours = ct2.log_mel(audio, fe.mel_filters, block=1000)
    assert ours.shape == theirs.shape
    np.testing.assert_allclose(ours, theirs, rtol=0, atol=2e-4)


# --- one pass, with a fake model -------------------------------------------------------


class FakeTokenizer:
    timestamp_begin = TS
    eot = EOT
    sot_sequence = tuple(SOT)
    sot_prev = SOT_PREV

    def decode(self, tokens: list[int]) -> str:
        return " ".join(f"w{t}" for t in tokens if t < EOT)

    def encode(self, text: str) -> list[int]:
        return [500 + len(w) for w in text.split()]


@dataclass
class Result:
    sequences_ids: list[list[int]]
    scores: list[float]
    no_speech_prob: float


@dataclass
class FakeWhisper:
    """``ctranslate2.models.Whisper``: an encoder output that names the window (its
    file's first sample, the window's start frame), and a decoder that writes one
    sentence per window, 0-5 s into it."""

    encoded: list[int] = field(default_factory=list)
    generated: list[dict[str, Any]] = field(default_factory=list)
    #: windows (file, window) decoded improbably at temperature 0, once
    bad_once: set[tuple[int, int]] = field(default_factory=set)
    language: tuple[str, float] = ("<|ar|>", 0.97)

    def encode(self, features: np.ndarray, to_cpu: bool = False) -> np.ndarray:
        assert to_cpu and features.ndim == 3 and features.shape[2] == batched.N_FRAMES
        self.encoded.append(len(features))
        return features[:, :2, :3].copy()  # (batch, 2, 3): "the window's encoding"

    def generate(self, features: np.ndarray, prompts: list[list[int]], **kw: Any) -> list[Result]:
        self.generated.append({"rows": len(features), "prompt": prompts[0], **kw})
        out = []
        for row in features:
            key = (int(row[0, 0]), int(row[1, 0]))
            hot = kw.get("sampling_temperature", 0.0)
            if key in self.bad_once and not hot:
                out.append(Result([[ts(0), 7, ts(5)]], [-3.0], 0.0))
            else:
                out.append(Result([[ts(0), 100 + key[0], 101, ts(5)]], [-0.25], 0.01))
        return out

    def detect_language(self, features: np.ndarray) -> list[list[tuple[str, float]]]:
        return [[self.language, ("<|en|>", 0.01)] for _ in features]


def _find_alignment(
    tok: Any, texts: list[list[int]], features: np.ndarray, frames: list[int]
) -> list[list[dict[str, Any]]]:
    """Each text token a word, 0.4 s apart from 0.5 s into the window."""
    assert len(texts) == len(features) == len(frames)
    return [
        [
            {
                "word": f" w{t}",
                "tokens": [t],
                "start": 0.5 + 0.4 * i,
                "end": 0.8 + 0.4 * i,
                "probability": 0.9,
            }  # fmt: skip
            for i, t in enumerate(text)
        ]
        for text in texts
    ]


def _loaded(model: FakeWhisper) -> ct2.Loaded:
    return ct2.Loaded(
        model=model,
        tokenizer=FakeTokenizer(),
        mel_filters=np.ones((4, ct2.N_FFT // 2 + 1), np.float32),
        find_alignment=_find_alignment,
        suppress_tokens=[1, 2],
        storage=lambda a: a,
        device="cuda",
        compute_type="float16",
        version="faster-whisper 9.9, ctranslate2 9.9, cuda float16",
    )


class Mel:
    """Stands in for the spectrogram: the encoder's input names its file and window."""

    def __init__(self, audio: np.ndarray) -> None:
        self.file = int(audio[0])
        self.frames = len(audio) // ct2.HOP_LENGTH + batched.N_FRAMES

    @property
    def shape(self) -> tuple[int, int]:
        return (self.frames, 4)


def _transcriber(model: FakeWhisper, *, words: bool = True, batch: int = 8) -> ct2.CT2Transcriber:
    t = ct2.CT2Transcriber(
        WhisperSettings(model="fake/whisper", word_timestamps=words, max_batch=batch),
        loader=lambda s: _loaded(model),
    )
    t._load()

    def window_of(mel: Mel, seek: int, size: int) -> np.ndarray:
        win = np.zeros((4, batched.N_FRAMES), np.float32)
        win[0, 0], win[1, 0] = mel.file, seek
        return win

    t._mel_of, t._window_of = Mel, window_of
    return t


def _audio(file: int, seconds: int) -> np.ndarray:
    a = np.zeros(SAMPLE_RATE * seconds, np.float32)
    a[0] = file
    return a


@pytest.mark.asyncio
async def test_files_share_each_pass_and_come_out_as_the_mac_s() -> None:
    model = FakeWhisper()
    t = _transcriber(model)
    r1, r2 = await asyncio.gather(t.transcribe(_audio(1, 90)), t.transcribe(_audio(2, 60)))
    assert max(g["rows"] for g in model.generated) == 2  # both files in one pass
    assert [len(r.sentences) for r in (r1, r2)] == [3, 2]
    # bounded by their words, which are on the file's timeline (window start + offset)
    s = r1.sentences[1]
    assert (s.start, s.end, s.text) == (30.5, 31.2, "w101 w101")
    assert s.words == [(30.5, 30.8, " w101"), (30.9, 31.2, " w101")]
    assert all(x.words for r in (r1, r2) for x in r.sentences)
    assert r1.backend == "ct2-batched" and r1.backend_version == _loaded(model).version
    assert r1.detected_language == "ar" and r1.language == "ar"
    assert t.device == "cuda" and t.audio_done == pytest.approx(150)
    g = model.generated[0]
    assert g["prompt"] == SOT and g["beam_size"] == 1 and "sampling_temperature" not in g
    assert g["max_length"] == len(SOT) + ct2.SAMPLE_LEN and g["suppress_tokens"] == [1, 2]
    assert g["return_scores"] and g["return_no_speech_prob"]
    assert g["max_initial_timestamp_index"] == 50


@pytest.mark.asyncio
async def test_without_word_timings_a_sentence_keeps_whisper_s_timestamps() -> None:
    model = FakeWhisper()
    r = await _transcriber(model, words=False).transcribe(_audio(3, 40))
    assert [(s.start, s.end, s.words) for s in r.sentences] == [
        (0.0, 5.0, None),
        (30.0, 35.0, None),
    ]


@pytest.mark.asyncio
async def test_an_improbable_window_is_sampled_warmer_on_its_kept_encoding() -> None:
    model = FakeWhisper(bad_once={(1, 3000)})  # file 1's second window
    t = _transcriber(model)
    r = await t.transcribe(_audio(1, 90))
    assert len(r.sentences) == 3 and r.sentences[1].text == "w101 w101"
    warm = [g for g in model.generated if g.get("sampling_temperature")]
    assert len(warm) == 1 and warm[0]["sampling_temperature"] == 0.2
    assert warm[0]["sampling_topk"] == 0 and warm[0]["rows"] == 1
    assert sum(model.encoded) == 3  # the window was not encoded again


@pytest.mark.asyncio
async def test_a_prompt_goes_before_the_start_tokens_of_its_own_file_only() -> None:
    model = FakeWhisper()
    t = _transcriber(model)
    await asyncio.gather(
        t.transcribe(_audio(1, 30), prompt="الشيخ نبيل"), t.transcribe(_audio(2, 30))
    )
    prompts = sorted((g["prompt"], g["rows"]) for g in model.generated)
    assert prompts == [(SOT, 1), ([SOT_PREV, 505, 504, *SOT], 1)]
    assert [g["max_length"] for g in model.generated if g["prompt"][0] == SOT_PREV] == [
        len(SOT) + 3 + ct2.SAMPLE_LEN
    ]


@pytest.mark.asyncio
async def test_an_unsure_language_is_not_recorded() -> None:
    model = FakeWhisper(language=("<|ur|>", 0.3))
    r = await _transcriber(model).transcribe(_audio(1, 30))
    assert r.detected_language is None


@pytest.mark.asyncio
async def test_a_resumed_file_gives_the_uninterrupted_transcript() -> None:
    whole = await _transcriber(FakeWhisper()).transcribe(_audio(4, 120))
    progress = FileProgress()
    t = _transcriber(FakeWhisper(), batch=1)
    first = asyncio.ensure_future(t.transcribe(_audio(4, 120), progress=progress))
    while progress.seek < 2 * batched.N_FRAMES:
        await asyncio.sleep(0)
    cp, _ = progress.snapshot()
    first.cancel()
    resumed = await _transcriber(FakeWhisper()).transcribe(_audio(4, 120), resume=cp)
    assert [(s.start, s.end, s.text, s.words) for s in resumed.sentences] == [
        (s.start, s.end, s.text, s.words) for s in whole.sentences
    ]


@pytest.mark.asyncio
async def test_a_load_failure_reaches_every_waiting_file_and_is_tried_again() -> None:
    calls: list[int] = []

    def loader(s: WhisperSettings) -> ct2.Loaded:
        calls.append(1)
        raise RuntimeError("MAKTABA_STT_DEVICE=cuda, but no CUDA device")

    t = ct2.CT2Transcriber(WhisperSettings(model="fake/whisper"), loader=loader)
    got = await asyncio.gather(t.transcribe(_audio(1, 30)), return_exceptions=True)
    assert isinstance(got[0], RuntimeError) and "no CUDA device" in str(got[0])
    with pytest.raises(RuntimeError):
        await t.transcribe(_audio(1, 30))
    assert len(calls) == 2


@pytest.mark.unit
def test_the_hooks_shape_windows_and_scores_as_whisper_does() -> None:
    model = FakeWhisper()
    hooks = ct2.Hooks(_loaded(model))
    mel = np.arange(4 * 5, dtype=np.float32).reshape(5, 4)  # 5 frames x 4 mels
    win = hooks.window_of(mel, 1, 3)
    assert win.shape == (4, batched.N_FRAMES) and (win[:, 3:] == 0).all()
    np.testing.assert_array_equal(win[:, :3], mel[1:4].T)
    windows = [np.full((4, batched.N_FRAMES), k, np.float32) for k in range(3)]
    cached_row = np.full((2, 3), 42, np.float32)
    enc = hooks.encode(windows, [None, cached_row, None])
    assert model.encoded == [2] and enc.shape == (3, 2, 3)
    assert (enc[1] == 42).all() and (enc[2] == 2).all()
    assert (hooks.encode(windows, [cached_row] * 3) == 42).all() and model.encoded == [2]
    out = hooks.decode(np.zeros((2, 2, 3), np.float32), [0.0, 0.4])
    assert [d.temperature for d in out] == [0.0, 0.4]
    # Whisper's average log probability counts the end token: 4 tokens, mean -0.25
    assert out[0].avg_logprob == pytest.approx(-0.25 * 4 / 5)
    assert out[0].compression_ratio == pytest.approx(ct2.compression_ratio("w100 w101"))
    assert ct2.compression_ratio("") == 0.0
    assert hooks.detect(np.zeros((3, 2, 3), np.float32), [0, 2]) == ["ar", "ar"]


@pytest.mark.unit
def test_punctuation_joins_its_word_as_whisper_merges_it() -> None:
    model = FakeWhisper()
    hooks = ct2.Hooks(_loaded(model))

    def found(*_a: Any) -> list[list[dict[str, Any]]]:
        return [
            [
                {"word": " قال", "tokens": [10], "start": 0.0, "end": 0.4, "probability": 0.9},
                {"word": ",", "tokens": [11], "start": 0.4, "end": 0.5, "probability": 0.9},
                {"word": " الشيخ", "tokens": [12], "start": 0.6, "end": 1.0, "probability": 0.9},
            ]
        ]

    hooks.loaded.find_alignment = found
    from maktaba_pipeline.stt.align import AlignItem

    seg: dict[str, Any] = {
        "seek": 3000,
        "start": 30.0,
        "end": 31.0,
        "tokens": [ts(0), 10, 11, 12, ts(1)],
    }
    empty: dict[str, Any] = {"seek": 3000, "start": 31.0, "end": 32.0, "tokens": [ts(1), ts(2)]}
    hooks.align_batch([AlignItem([seg], 3000, 0.0, 0), AlignItem([empty], 3000, 0.0, 1)],
                      np.zeros((2, 2, 3), np.float32))  # fmt: skip
    assert [(w["word"], w["start"], w["end"]) for w in seg["words"]] == [
        (" قال,", 30.0, 30.4),  # the mark joins the word, its time does not move
        (" الشيخ", 30.6, 31.0),
    ]
    assert empty["words"] == []


# --- the second listen and the transcript ----------------------------------------------


@pytest.mark.unit
def test_without_a_gpu_for_the_second_listen_whisper_s_words_stay(on_ct2: None) -> None:
    """(story 38.9 runs it on CUDA where the GPU holds it: tests/stt/test_cohere_cuda.py)"""
    from maktaba_pipeline.stt import cohere
    from maktaba_pipeline.stt.settings import Sentence, Transcript
    from tests.conftest import NO_TEST_GPU

    assert cohere.unavailable() == NO_TEST_GPU
    t = Transcript([Sentence(0.0, 1.0, "قال الشيخ", -0.1, 0.0)], 1.0, "ar", "m", 1.0)
    out = stage._refine(t, np.zeros(SAMPLE_RATE, np.float32), "CohereLabs/x")
    assert out == {
        "model": "CohereLabs/x",
        "source": "skipped",
        "skipped": f"RuntimeError: {NO_TEST_GPU}",
    }
    assert t.sentences[0].text == "قال الشيخ"


@pytest.mark.unit
def test_the_transcript_records_the_engine_s_version() -> None:
    from maktaba_pipeline.stt.settings import Transcript

    t = Transcript([], 1.0, "ar", "m", 1.0, backend="ct2-batched", backend_version="fw 1, ct2 4")
    assert stage._backend_version(t) == "fw 1, ct2 4"
    mac = Transcript([], 1.0, "ar", "m", 1.0)
    assert mac.backend == "mlx-batched"
    assert stage._backend_version(mac) == stage._backend_version()


@pytest.mark.asyncio
async def test_the_legacy_faster_whisper_backend_runs_on_the_engine(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.stt.faster_whisper import FasterWhisperBackend
    from maktaba_pipeline.stt.protocol import TranscriptionHints
    from maktaba_pipeline.stt.settings import Sentence, Transcript

    asked: list[tuple[WhisperSettings, str | None]] = []

    class Engine:
        def __init__(self, s: WhisperSettings) -> None:
            self.s = s

        async def transcribe(self, audio: Any, *, prompt: str | None = None) -> Transcript:
            asked.append((self.s, prompt))
            words = [(0.5, 0.9, " قال"), (1.0, 1.6, " الشيخ")]
            return Transcript(
                [Sentence(0.5, 1.6, "قال الشيخ", -0.2, 0.0, words)], 2.0, "ar", self.s.model,
                1.0, backend="ct2-batched", backend_version="faster-whisper 1.2.1",
            )  # fmt: skip

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        return np.zeros(SAMPLE_RATE * 2, np.float32)

    monkeypatch.setattr(stage, "decode_audio", decode)
    monkeypatch.setattr(ct2, "get_transcriber", Engine)
    backend = FasterWhisperBackend(device="cuda", force_ready=True)
    hints = TranscriptionHints(word_timestamps=True, initial_prompt="نبيل")
    segs = [s async for s in backend.transcribe("/x.wav", "ar", hints)]
    assert [(s.start_sec, s.end_sec, s.text) for s in segs] == [(0.5, 1.6, "قال الشيخ")]
    assert [(w.text, w.start_sec) for w in segs[0].words] == [(" قال", 0.5), (" الشيخ", 1.0)]
    assert segs[0].confidence == pytest.approx(round(float(np.exp(-0.2)), 4))
    s, prompt = asked[0]
    assert (s.device, s.compute_type, s.model, prompt) == ("cuda", "float16", "large-v3", "نبيل")


@pytest.mark.unit
def test_the_progress_line_says_where_the_files_are_decoded(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    s = WhisperSettings(model="x/where", device="auto")
    assert stage._where(s) == "on the GPU"  # not loaded yet: the GPU unless told the CPU
    ct2.get_transcriber(s).device = "cpu"  # the stage's one, loaded: auto found no GPU
    assert stage._where(s) == "on the CPU"
    assert stage._where(WhisperSettings(model="x/where-cpu", device="cpu")) == "on the CPU"
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    assert stage._where(WhisperSettings()) == "on the GPU"
