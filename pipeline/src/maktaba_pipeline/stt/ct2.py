"""Whisper on CTranslate2: the batched transcriber of Windows and Linux (story 38.8).

The same algorithm as Apple Silicon's (:class:`~.batched.BatchedTranscriber`: every
file in flight keeps its own seek, each pass decodes one 30 s window of each as one
batch, with stock Whisper's seek, temperature fallback and word timings), with
CTranslate2 doing the model's work on faster-whisper's model files, tokenizer and word
alignment. A file resumes from its checkpoint as on the Mac, and its sentences, word
timings and bounds come out in the same shape.

Not faster-whisper's ``BatchedInferencePipeline``: it batches one file's windows, cut at
Silero VAD's pauses, decodes them at a single temperature (no fallback) and has no seek
to resume a file from. CTranslate2's own batching is used instead: ``encode``,
``generate``, ``detect_language`` and ``align`` each take a batch of windows.

Where it runs (``MAKTABA_STT_DEVICE``, default ``auto``): on an NVIDIA GPU when CUDA is
usable and the GPU has the memory free for the model and its batch (:func:`gpu_short`,
the driver's own count through NVML), else on the CPU; a model that fails to load on the
GPU all the same is loaded on the CPU, and the process does not try the GPU again
(:func:`load`). CTranslate2's wheels need only the NVIDIA driver and cuBLAS 12, which the
``stt`` extra installs from PyPI (``nvidia-cublas-cu12``) and :func:`cuda_libraries`
loads, once, before the model goes on the GPU. GPUs are numbered as nvidia-smi and NVML
number them (``CUDA_DEVICE_ORDER=PCI_BUS_ID`` unless set). The precision
(``MAKTABA_STT_COMPUTE_TYPE``, default ``auto``) is float16 on CUDA and int8 on the CPU;
the supervisor asks for int8_float16 on a small GPU.

The encoder's output comes back to the CPU (as NumPy) once a pass: the rows a decode at
one temperature, a prompt, the language guess or the word alignment need are taken from
it there, as the MLX engine takes them from its GPU array (4 MB a window in float16,
well under a millisecond to copy each way against a pass of half a second or more).
"""

from __future__ import annotations

import ctypes
import functools
import importlib.util
import os
import sys
import threading
import time
import zlib
from collections.abc import Callable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import numpy as np

from ..log import get_logger
from . import engine
from .align import AlignItem, WordTiming, merge_punctuations, place
from .batched import LANGUAGE_MIN_PROB, N_FRAMES, BatchedTranscriber, metric_tag
from .settings import SAMPLE_RATE, WhisperSettings

__all__ = [
    "CT2Transcriber",
    "Loaded",
    "choose_compute_type",
    "choose_device",
    "cuda_libraries",
    "cuda_problem",
    "get_transcriber",
    "gpu_free_gb",
    "gpu_need_gb",
    "gpu_short",
    "load",
    "log_mel",
    "unavailable",
]

_log = get_logger()

N_FFT = 400
HOP_LENGTH = 160
N_SAMPLES = 30 * SAMPLE_RATE  # one window of audio
#: Whisper's context, prompt included, and the most tokens one window decodes (as
#: MLX-Whisper's ``sample_len``)
MAX_LENGTH = 448
SAMPLE_LEN = MAX_LENGTH // 2
#: the first timestamp a window may start with: 1 s (``max_initial_timestamp``)
MAX_INITIAL_TIMESTAMP_INDEX = 50
#: frames of the spectrogram computed at once: a long lecture never holds its spectrum
#: whole (5 min of audio, ~150 MB of Fourier transforms)
MEL_BLOCK = 30_000

#: the precisions this engine runs: its encoder output comes back as NumPy, which has no
#: bfloat16
COMPUTE_TYPES = ("float16", "int8_float16", "int8", "int8_float32", "float32")
DEFAULT_COMPUTE = {"cuda": "float16", "cpu": "int8"}


# --- CUDA ----------------------------------------------------------------------------

# CUDA numbers GPUs fastest first by default, nvidia-smi and NVML (and the supervisor's
# plan, which reads nvidia-smi) by their PCI bus: one numbering for all of them. Before
# CUDA starts in this process; an operator's own setting stands.
os.environ.setdefault("CUDA_DEVICE_ORDER", "PCI_BUS_ID")


def _nvidia_dirs(sub: str) -> list[Path]:
    """``site-packages/nvidia/<lib>/<sub>`` of the NVIDIA wheels installed here."""
    spec = importlib.util.find_spec("nvidia")
    if spec is None or not spec.submodule_search_locations:
        return []
    return sorted(
        d
        for root in spec.submodule_search_locations
        for d in Path(root).glob(f"*/{sub}")
        if d.is_dir()
    )


_CUDA_LOCK = threading.Lock()
_CUDA: list[str | None] = []  # once: where cuBLAS came from
_DLL_DIRS: list[Any] = []  # the os.add_dll_directory handles, kept for the process


def _add_dll_dirs() -> None:
    """Windows: the NVIDIA wheels' DLL folders, where a DLL loaded from them finds the
    others it needs (cublas64_12.dll needs cublasLt64_12.dll). Once, before CTranslate2
    is imported."""
    if sys.platform != "win32" or _DLL_DIRS:
        return
    for d in _nvidia_dirs("bin"):
        try:
            _DLL_DIRS.append(os.add_dll_directory(str(d)))
        except OSError:
            continue
    _DLL_DIRS.append(None)  # done, even with none


def cuda_libraries() -> str | None:
    """Load cuBLAS 12, once: from the ``nvidia-cublas-cu12`` wheel, else wherever the
    system has it (a CUDA toolkit on PATH, CUDA_PATH or the linker's path). Returns the
    file it loaded, or None when there is none.

    CTranslate2 opens cuBLAS by name when it first needs it (``LoadLibrary`` /
    ``dlopen``), which searches neither site-packages nor the folders added with
    ``os.add_dll_directory``; a library already loaded is the one it gets."""
    with _CUDA_LOCK:
        if _CUDA:
            return _CUDA[0]
        _add_dll_dirs()
        found: str | None = None
        if sys.platform == "win32":
            name = "cublas64_12.dll"
            wheel = [d / name for d in _nvidia_dirs("bin")]
            toolkit = os.environ.get("CUDA_PATH")
            system = [Path(toolkit) / "bin" / name] if toolkit else []
            for path in [*wheel, *system]:
                if path.is_file() and _load(str(path)):
                    found = str(path)
                    break
            # a plain name: Windows' own search, PATH included
            if found is None and _load(name, winmode=0):
                found = name
        else:
            libs = [d for d in _nvidia_dirs("lib") if (d / "libcublas.so.12").is_file()]
            for d in libs:
                if _load(str(d / "libcublasLt.so.12")) and _load(str(d / "libcublas.so.12")):
                    found = str(d / "libcublas.so.12")
                    break
            if found is None and _load("libcublas.so.12"):
                found = "libcublas.so.12"
        _CUDA.append(found)
        return found


def _load(path: str, **kwargs: Any) -> bool:
    try:
        if sys.platform != "win32":
            kwargs.setdefault("mode", ctypes.RTLD_GLOBAL)
        ctypes.CDLL(path, **kwargs)
    except OSError:
        return False
    return True


@functools.cache
def cuda_problem() -> str | None:
    """Why CTranslate2 cannot run on an NVIDIA GPU here, or None when it can."""
    _add_dll_dirs()
    try:
        import ctranslate2  # type: ignore[import-untyped,import-not-found,unused-ignore]
    except ImportError:
        return "ctranslate2 is not installed (the stt extra)"
    try:
        count = int(ctranslate2.get_cuda_device_count())
    except Exception as exc:  # noqa: BLE001 - a broken driver: say what it said
        return f"CUDA: {type(exc).__name__}: {exc}"
    if count < 1:
        return "no CUDA device (an NVIDIA GPU and its driver)"
    if cuda_libraries() is None:
        return "cuBLAS 12 is not installed (nvidia-cublas-cu12, the stt extra)"
    return None


#: why Whisper would not load on the GPU in this process (:func:`load`): "auto" then
#: transcribes on the CPU without trying again
_GPU_FAILED: list[str] = []


def choose_device(want: str) -> tuple[str, int]:
    """``(device, index)`` for ``MAKTABA_STT_DEVICE``: "auto" is CUDA when it is usable
    and the model did not fail to load there (else the CPU, logged once); "cuda" /
    "cuda:<n>" must be usable (raises, saying why); "cpu" is the CPU."""
    want = (want or "auto").strip().lower()
    if want == "cpu":
        return "cpu", 0
    if want == "auto":
        problem = _GPU_FAILED[0] if _GPU_FAILED else cuda_problem()
        if problem is None:
            return "cuda", 0
        _log_once("stt.cpu", "transcribing on the CPU", reason=problem)
        return "cpu", 0
    name, _, index = want.partition(":")
    if name != "cuda" or (index and not index.isdigit()):
        raise ValueError(f"MAKTABA_STT_DEVICE={want!r}: auto, cuda, cuda:<n> or cpu")
    problem = cuda_problem()
    if problem is not None:
        raise RuntimeError(f"MAKTABA_STT_DEVICE={want}, but {problem}")
    n = int(index or 0)
    import ctranslate2  # type: ignore[import-untyped,import-not-found,unused-ignore]

    if n >= int(ctranslate2.get_cuda_device_count()):
        raise RuntimeError(f"MAKTABA_STT_DEVICE={want}: this machine has no CUDA device {n}")
    return "cuda", n


def choose_compute_type(device: str, want: str, supported: Sequence[str]) -> str:
    """The precision for ``MAKTABA_STT_COMPUTE_TYPE`` on ``device``: "auto" is the
    device's default; one this engine does not run, or the device cannot, falls back to
    it with a line in the log."""
    want = (want or "auto").strip().lower()
    default = DEFAULT_COMPUTE[device]
    if default not in supported:
        default = "float32"  # every device runs it
    if want == "auto":
        return default
    if want not in COMPUTE_TYPES or want not in supported:
        _log_once(
            f"stt.compute_type:{device}:{want}",
            "compute type not available, using the default",
            compute_type=want,
            device=device,
            using=default,
            supported=sorted(set(supported) & set(COMPUTE_TYPES)),
        )
        return default
    return want


# --- the GPU's memory -----------------------------------------------------------------

#: Whisper turbo on CUDA, measured (story 38.8, RTX 2080 SUPER): what loading it takes by
#: precision, CUDA's own buffers included (float16 and int8_float16 measured; int8 and
#: int8_float32 keep int8 weights as int8_float16 does; float32 twice float16's weights),
#: and what each file of a batch adds while its window is encoded. Another model takes
#: more or less in proportion to its weights (model.bin; turbo's is 1.62 GB). The
#: supervisor's GPU tiers (cmd/maktaba-server/internal/supervisor/stt.go) use the same
#: figures.
LOAD_GB = {"float16": 2.3, "int8_float16": 1.2, "int8": 1.2, "int8_float32": 1.2, "float32": 3.9}
FILE_GB = 0.14
TURBO_BIN_GB = 1.62
#: left free beside it: what another program allocates between the check and the load
MARGIN_GB = 0.25


class _NvmlMemory(ctypes.Structure):
    _fields_ = [  # noqa: RUF012 - ctypes' own declaration
        ("total", ctypes.c_ulonglong),
        ("free", ctypes.c_ulonglong),
        ("used", ctypes.c_ulonglong),
    ]


def _nvml() -> Any | None:
    """NVML, the NVIDIA driver's own library (``nvml.dll`` in System32, older drivers'
    under NVSMI; ``libnvidia-ml.so.1``), or None without one."""
    names = ["libnvidia-ml.so.1"]
    if sys.platform == "win32":
        pf = os.environ.get("PROGRAMFILES", r"C:\Program Files")
        names = ["nvml.dll", str(Path(pf) / "NVIDIA Corporation" / "NVSMI" / "nvml.dll")]
    for name in names:
        try:
            return ctypes.CDLL(name)
        except OSError:
            continue
    return None


def gpu_free_gb(index: int) -> float | None:
    """The free memory (GiB) of NVIDIA GPU ``index`` as its driver counts it (every
    process's allocations, the display's included), or None when it cannot say."""
    lib = _nvml()
    if lib is None:
        return None
    try:
        if lib.nvmlInit_v2() != 0:
            return None
        try:
            handle = ctypes.c_void_p()
            mem = _NvmlMemory()
            if lib.nvmlDeviceGetHandleByIndex_v2(ctypes.c_uint(index), ctypes.byref(handle)):
                return None
            if lib.nvmlDeviceGetMemoryInfo(handle, ctypes.byref(mem)):
                return None
            return float(mem.free) / (1 << 30)
        finally:
            lib.nvmlShutdown()
    except (OSError, AttributeError):  # an NVML without these entry points
        return None


def gpu_need_gb(compute_type: str, batch: int, model_bin_gb: float | None = None) -> float:
    """What Whisper needs free on the GPU to load in ``compute_type`` and decode a batch
    of ``batch`` files (``model_bin_gb``: its weights' size, when not turbo's)."""
    scale = (model_bin_gb / TURBO_BIN_GB) if model_bin_gb else 1.0
    return (
        LOAD_GB.get(compute_type, LOAD_GB["float32"]) * scale + FILE_GB * max(1, batch) + MARGIN_GB
    )


def gpu_short(index: int, compute_type: str, batch: int, folder: str | None = None) -> str | None:
    """Why GPU ``index`` has too little memory free for Whisper (``folder``: its model's,
    to size it), or None when it has enough or cannot say."""
    free = gpu_free_gb(index)
    if free is None:
        return None
    weights = Path(folder) / "model.bin" if folder else None
    size = weights.stat().st_size / 1e9 if weights is not None and weights.is_file() else None
    need = gpu_need_gb(compute_type, batch, size)
    if free >= need:
        return None
    return (
        f"the GPU has {free:.1f} GiB free, Whisper in {compute_type} with a batch of"
        f" {batch} needs {need:.1f} GiB"
    )


def cpu_threads_default() -> int:
    """The CPU's share when a GPU worker transcribes there instead: half the logical CPUs
    (2 to 16), as the supervisor's CPU plan gives."""
    return min(16, max(2, (os.cpu_count() or 4) // 2))


_LOGGED: set[str] = set()


def _log_once(key: str, message: str, **fields: Any) -> None:
    if key not in _LOGGED:
        _LOGGED.add(key)
        _log.warning(key.split(":", 1)[0], message=message, **fields)


# --- the model -------------------------------------------------------------------------


def _looks_like_path(model: str) -> bool:
    return (
        model.startswith(("/", ".", "~", "\\"))
        or (len(model) > 1 and model[1] == ":")
        or "\\" in model
        or model.count("/") > 1
    )


def model_problem(model: str) -> str | None:
    """Why ``model`` cannot be loaded here, or None. A Hugging Face repo is fetched on
    first use (as on the Mac) unless Hugging Face is offline and it is not here yet."""
    folder = Path(model).expanduser()
    if folder.is_dir():
        if (folder / "model.bin").is_file():
            return None
        return f"MAKTABA_STT_MODEL={model} is a folder with no CTranslate2 model (model.bin) in it"
    if _looks_like_path(model):
        return f"MAKTABA_STT_MODEL={model}: no such folder"
    if os.environ.get("HF_HUB_OFFLINE", "").strip().lower() in ("1", "true", "yes", "on"):
        try:
            from huggingface_hub import try_to_load_from_cache
        except ImportError:  # pragma: no cover - a base dependency
            return None
        if not isinstance(try_to_load_from_cache(engine.ct2_repo(model), "model.bin"), str):
            return (
                f"{model} is not downloaded and Hugging Face is offline (HF_HUB_OFFLINE):"
                " run maktaba-pipeline models download"
            )
    return None


def model_dir(model: str) -> str:
    """The folder of ``model``: a local one as it is, else the Hugging Face snapshot of
    exactly the files :data:`.engine.CT2_FILES` names (what ``models download`` fetches),
    downloaded on first use: a model Maktaba pins (:data:`.engine.CT2_PINS`) at its
    commit, its files checked against their SHA-256 once (:func:`.engine.verify`)."""
    folder = Path(model).expanduser()
    if folder.is_dir():
        return str(folder)
    from huggingface_hub import snapshot_download

    repo = engine.ct2_repo(model)
    pin = engine.CT2_PINS.get(repo)
    snap = Path(
        snapshot_download(
            repo,
            revision=pin.revision if pin else None,
            allow_patterns=engine.CT2_FILES,
        )
    )
    if pin is not None:
        engine.verify(snap, pin)
    return str(snap)


@dataclass
class Loaded:
    """A Whisper model on its device, and what a pass needs from faster-whisper."""

    #: ``ctranslate2.models.Whisper``: encode, generate, detect_language
    model: Any
    #: faster-whisper's ``Tokenizer`` for the settings' language, transcribing
    tokenizer: Any
    #: the model's mel filters, (mels, 201)
    mel_filters: np.ndarray
    #: ``WhisperModel.find_alignment``: (tokenizer, texts, encoder rows, frames) -> words
    find_alignment: Callable[[Any, list[list[int]], Any, list[int]], list[list[dict[str, Any]]]]
    #: the tokens Whisper never writes (its "-1": symbols, the special tokens)
    suppress_tokens: list[int]
    #: a NumPy array as what the model takes (``ctranslate2.StorageView.from_array``)
    storage: Callable[[np.ndarray], Any]
    device: str
    compute_type: str
    version: str


def load(settings: WhisperSettings) -> Loaded:
    """Load ``settings.model`` on the device and in the precision the settings ask for.
    With ``auto``, a GPU with too little memory free (:func:`gpu_short`) leaves it to the
    CPU, and so does a load that fails on the GPU, for the rest of the process."""
    _add_dll_dirs()
    folder = model_dir(settings.model)
    auto = (settings.device or "auto").strip().lower() == "auto"
    device, index = choose_device(settings.device)
    import ctranslate2  # type: ignore[import-untyped,import-not-found,unused-ignore]
    from faster_whisper import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        WhisperModel,
    )
    from faster_whisper.tokenizer import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        Tokenizer,
    )
    from faster_whisper.transcribe import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        get_suppressed_tokens,
    )

    def on_cpu(reason: str) -> tuple[str, int, str]:
        _log_once(f"stt.cpu:{reason}", "transcribing on the CPU", reason=reason)
        cpu = list(ctranslate2.get_supported_compute_types("cpu", 0))
        return "cpu", 0, choose_compute_type("cpu", settings.compute_type, cpu)

    supported = list(ctranslate2.get_supported_compute_types(device, index))
    compute = choose_compute_type(device, settings.compute_type, supported)
    if device == "cuda" and auto:
        short = gpu_short(index, compute, settings.max_batch, folder)
        if short is not None:
            device, index, compute = on_cpu(short)

    def model_on(device: str, index: int, compute: str) -> Any:
        threads = settings.cpu_threads if settings.cpu_threads > 0 else 0
        if device == "cpu" and threads == 0 and auto:
            threads = cpu_threads_default()  # a GPU worker's plan sets none
        return WhisperModel(
            folder,
            device=device,
            device_index=index,
            compute_type=compute,
            cpu_threads=threads,
            num_workers=1,
        )

    try:
        fw = model_on(device, index, compute)
    except Exception as exc:
        if device != "cuda" or not auto:
            raise
        _GPU_FAILED.append(f"Whisper did not load on the GPU: {type(exc).__name__}: {exc}"[:300])
        device, index, compute = on_cpu(_GPU_FAILED[0])
        fw = model_on(device, index, compute)
    tok = Tokenizer(
        fw.hf_tokenizer, fw.model.is_multilingual, task="transcribe", language=settings.language
    )
    from importlib.metadata import version

    where = f"{device}:{index}" if device == "cuda" and index else device
    return Loaded(
        model=fw.model,
        tokenizer=tok,
        mel_filters=np.asarray(fw.feature_extractor.mel_filters, dtype=np.float32),
        find_alignment=fw.find_alignment,
        suppress_tokens=list(get_suppressed_tokens(tok, [-1])),
        storage=ctranslate2.StorageView.from_array,
        device=where,
        compute_type=compute,
        version=(
            f"faster-whisper {version('faster-whisper')}, ctranslate2 {ctranslate2.__version__},"
            f" {where} {compute}"
        ),
    )


# --- one pass ---------------------------------------------------------------------------


def log_mel(audio: np.ndarray, filters: np.ndarray, block: int = MEL_BLOCK) -> np.ndarray:
    """Whisper's log-mel spectrogram of the whole file followed by 30 s of silence, frames
    by mels (MLX-Whisper's ``log_mel_spectrogram(audio, padding=N_SAMPLES)``, the same
    sums as faster-whisper's ``FeatureExtractor``), its Fourier transforms taken
    ``block`` frames at a time."""
    x = np.pad(np.asarray(audio, dtype=np.float32), (0, N_SAMPLES))
    x = np.pad(x, (N_FFT // 2, N_FFT // 2), mode="reflect")
    frames = (len(x) - N_FFT) // HOP_LENGTH  # the last frame is dropped, as Whisper does
    window = np.hanning(N_FFT + 1)[:-1].astype(np.float32)
    filters_t = np.ascontiguousarray(filters.T, dtype=np.float32)
    out = np.empty((frames, filters.shape[0]), np.float32)
    for a in range(0, frames, block):
        b = min(frames, a + block)
        chunk = x[a * HOP_LENGTH : (b - 1) * HOP_LENGTH + N_FFT]
        windows = np.lib.stride_tricks.sliding_window_view(chunk, N_FFT)[::HOP_LENGTH]
        power = np.abs(np.fft.rfft(windows * window, axis=-1)) ** 2
        out[a:b] = np.log10(np.maximum(power.astype(np.float32) @ filters_t, 1e-10))
    if frames:
        np.maximum(out, out.max() - 8.0, out=out)
    out += 4.0
    out /= 4.0
    return out


@dataclass
class Decoded:
    """One window's decode, as :class:`~.batched.BatchedTranscriber` reads it."""

    tokens: list[int]
    avg_logprob: float
    compression_ratio: float
    no_speech_prob: float
    temperature: float


def compression_ratio(text: str) -> float:
    """How repetitive a window's text is (Whisper's: raw bytes over zlib's)."""
    raw = text.encode("utf-8")
    return len(raw) / len(zlib.compress(raw)) if raw else 0.0


@dataclass
class Hooks:
    """The :class:`~.batched.BatchedTranscriber` hooks, on CTranslate2."""

    loaded: Loaded

    @property
    def n_mels(self) -> int:
        return int(self.loaded.mel_filters.shape[0])

    def mel_of(self, audio: np.ndarray) -> np.ndarray:
        return log_mel(audio, self.loaded.mel_filters)

    def window_of(self, mel: np.ndarray, seek: int, size: int) -> np.ndarray:
        """One window, mels by frames as CTranslate2 takes it, padded to 30 s."""
        win = np.zeros((self.n_mels, N_FRAMES), np.float32)
        part = mel[seek : seek + size]
        win[:, : len(part)] = part.T
        return win

    def encode(self, windows: list[np.ndarray], cached: list[Any]) -> np.ndarray:
        """The pass's encoder output, back on the CPU: one row per window, a window
        decoded again after a fallback keeping its earlier row."""
        todo = [k for k, c in enumerate(cached) if c is None]
        if not todo:
            return np.stack(cached)
        stacked = np.stack([windows[k] for k in todo])
        fresh = np.array(self.loaded.model.encode(self.loaded.storage(stacked), to_cpu=True))
        if len(todo) == len(windows):
            return fresh
        rows = iter(fresh)
        return np.stack([next(rows) if c is None else c for c in cached])

    def decode(
        self, features: Any, temps: Sequence[float], prompt: str | None = None
    ) -> list[Decoded]:
        """Each window greedy at temperature 0, sampled at a fallback's (one call per
        temperature), Whisper's timestamp rules applied by CTranslate2; ``prompt`` (the
        library's ``stt.initial_prompt``) before the start tokens, as Whisper's
        ``initial_prompt``."""
        ld, tok = self.loaded, self.loaded.tokenizer
        start: list[int] = []
        if prompt and prompt.strip():
            start = [tok.sot_prev, *tok.encode(" " + prompt.strip())[-(MAX_LENGTH // 2 - 1) :]]
        start += list(tok.sot_sequence)
        max_length = min(MAX_LENGTH, len(start) + SAMPLE_LEN)
        out: dict[int, Decoded] = {}
        for t in sorted(set(temps)):
            rows = [k for k, x in enumerate(temps) if x == t]
            sampling: dict[str, Any] = (
                {"sampling_topk": 0, "sampling_temperature": t} if t > 0 else {}
            )
            results = ld.model.generate(
                ld.storage(np.ascontiguousarray(features[rows])),
                [start] * len(rows),
                beam_size=1,
                max_length=max_length,
                return_scores=True,
                return_no_speech_prob=True,
                suppress_blank=True,
                suppress_tokens=ld.suppress_tokens,
                max_initial_timestamp_index=MAX_INITIAL_TIMESTAMP_INDEX,
                **sampling,
            )
            for k, r in zip(rows, results, strict=True):
                tokens = [int(x) for x in r.sequences_ids[0]]
                # CTranslate2's score is the mean log probability (length penalty 1);
                # Whisper's average counts the end token too
                n = len(tokens)
                avg = float(r.scores[0]) * n / (n + 1)
                text = str(tok.decode(tokens)).strip()
                out[k] = Decoded(tokens, avg, compression_ratio(text), float(r.no_speech_prob), t)
        return [out[k] for k in range(len(temps))]

    def detect(self, features: np.ndarray, rows: list[int]) -> list[str]:
        """The language heard in each of these windows ("" when unsure)."""
        got = self.loaded.model.detect_language(
            self.loaded.storage(np.ascontiguousarray(features[rows]))
        )
        out = []
        for langs in got:
            token, prob = langs[0]
            code = str(token).removeprefix("<|").removesuffix("|>")
            out.append(code if float(prob) >= LANGUAGE_MIN_PROB else "")
        return out

    def align_batch(self, items: list[AlignItem], features: np.ndarray) -> None:
        """Word timings for every window of the pass, in one CTranslate2 call: its
        cross-attention alignment (faster-whisper's ``find_alignment``), placed on the
        sentences as MLX-Whisper places them (:func:`~.align.place`)."""
        tok = self.loaded.tokenizer
        work = []
        for it in items:
            per_seg = [[t for t in s["tokens"] if t < tok.eot] for s in it.segments]
            text = [t for seg in per_seg for t in seg]
            if it.segments and text:
                work.append((it, per_seg, text))
        if work:
            rows = [it.row for it, _, _ in work]
            found = self.loaded.find_alignment(
                tok,
                [text for _, _, text in work],
                self.loaded.storage(np.ascontiguousarray(features[rows])),
                [it.num_frames for it, _, _ in work],
            )
            for (it, per_seg, _), words in zip(work, found, strict=True):
                timings = [
                    WordTiming(
                        str(w["word"]),
                        [int(t) for t in w["tokens"]],
                        float(w["start"]),
                        float(w["end"]),
                        float(w["probability"]),
                    )
                    for w in words
                ]
                place(it, per_seg, timings, merge_punctuations)
        for it in items:  # windows with no text
            for s in it.segments:
                s.setdefault("words", [])


class CT2Transcriber(BatchedTranscriber):
    """:class:`~.batched.BatchedTranscriber` on CTranslate2: one model, one thread, many
    files in flight. ``loader`` loads the model (:func:`load`; tests pass a fake)."""

    backend = engine.BACKEND[engine.CT2]

    def __init__(
        self,
        settings: WhisperSettings | None = None,
        *,
        loader: Callable[[WhisperSettings], Loaded] | None = None,
    ) -> None:
        super().__init__(settings)
        self._loader = loader or load
        #: where the model runs, once loaded ("cuda", "cpu")
        self.device: str | None = None

    def _load(self) -> None:
        if self._decode is not None and self._mel_of is not None:
            return
        t0 = time.monotonic()
        ld = self._loader(self.settings)
        hooks = Hooks(ld)
        self.device, self.backend_version = ld.device, ld.version
        self._tokenizer = ld.tokenizer
        self._window_of = hooks.window_of
        self._encode = hooks.encode
        self._detect = hooks.detect
        self._align_batch = hooks.align_batch if self.settings.word_timestamps else None
        self._decode = hooks.decode
        self._mel_of = hooks.mel_of  # last: set, the model counts as loaded
        self._register_metrics()
        _log.info(
            "whisper.loaded",
            engine=engine.CT2,
            model=self.settings.model,
            device=ld.device,
            compute_type=ld.compute_type,
            batch=self.settings.max_batch,
            slot=metric_tag(),
            seconds=round(time.monotonic() - t0, 1),
        )


_SHARED: dict[WhisperSettings, CT2Transcriber] = {}


def get_transcriber(settings: WhisperSettings) -> CT2Transcriber:
    """The process's transcriber for these settings (one model per settings)."""
    t = _SHARED.get(settings)
    if t is None:
        t = _SHARED[settings] = CT2Transcriber(settings)
    return t


def unavailable(settings: WhisperSettings) -> str | None:
    """Why this worker cannot transcribe on CTranslate2, or None: the ``stt`` extra is
    missing, the model is not one it can load, or a GPU it was told to use is not usable
    (with ``auto`` it transcribes on the CPU instead)."""
    for module, package in (("faster_whisper", "faster-whisper"), ("ctranslate2", "ctranslate2")):
        if importlib.util.find_spec(module) is None:
            return f"{package} is not installed (the stt extra)"
    problem = model_problem(settings.model)
    if problem is not None:
        return problem
    if settings.device.strip().lower() not in ("", "auto", "cpu"):
        try:
            choose_device(settings.device)
        except (RuntimeError, ValueError) as exc:
            return str(exc)
    return None
