"""The second listen on Windows and Linux (story 38.9): Cohere Transcribe Arabic through
transformers (its ``cohere_asr`` model) on torch's CUDA build, on the NVIDIA GPU Whisper
transcribes on (:mod:`.ct2`).

On that GPU only: on the CPU the 2 B model would hear an hour of lectures in hours, so a
machine without one keeps Whisper's words and says why (:func:`problem`), as a Mac
without the model does: no NVIDIA GPU, a torch without CUDA, transcription set to the CPU,
a GPU too small to hold the model beside Whisper (:data:`MIN_GPU_GB`), older than torch's
CUDA build runs on (:data:`MIN_COMPUTE`) or with a driver too old for it
(:data:`MIN_DRIVER`). The answer comes from the driver (NVML) and torch's own version
file, without importing torch: a worker asks it on its event loop. The model is
4.1 GB in 16 bits (bfloat16 on a GPU that computes in it, Ampere and newer; float16 before
that). It loads only when the GPU has room for it at that moment (:func:`room`, from
``torch.cuda.mem_get_info``), and it hears the stretches in batches sized from what is left
once it is loaded, halved when one does not fit.

The sizes below were measured on an RTX 2080 SUPER (8 GB, driver 616.64, Windows) with
torch 2.14.1+cu130, transformers 5.17.0 and the model's architecture with random weights
(the gated model itself was not downloaded there).
"""

from __future__ import annotations

import functools
import importlib.util
import re
import sys
import threading
import time
from pathlib import Path
from typing import Any

from ..log import get_logger
from . import engine
from .settings import SAMPLE_RATE, WhisperSettings

__all__ = [
    "ALLOW_PATTERNS",
    "MIN_COMPUTE",
    "MIN_DRIVER",
    "MIN_GPU_GB",
    "CudaRecogniser",
    "LoadError",
    "device_index",
    "need_gb",
    "problem",
    "room",
    "weights_gb",
]

_log = get_logger()

GIB = float(1 << 30)

#: what transformers loads from the model's repo (its config, weights, processor and
#: tokenizer files), and so what ``models download`` fetches: not the repo's own copy of
#: the model's code (transformers has it) nor its example recordings
ALLOW_PATTERNS = ["*.json", "*.safetensors", "*.model"]

#: the language the stretches are heard in (the processor's prompt)
LANGUAGE = "ar"
#: an Arabic stretch of 28 s is about 80 words, ~200 tokens
MAX_TOKENS = 448

#: the default model's weights in 16 bits: 2.07 B parameters, 4.13 GB on disk (for a model
#: not downloaded yet; else the size of its files)
WEIGHTS_GB = 3.85
#: what loading the model takes of the GPU's free memory beyond its weights, with Whisper
#: in the process: on Windows the driver's budget (WDDM, what ``mem_get_info`` reports)
#: drops 1.4 to 1.8 GiB more than the weights (measured four times beside CTranslate2's
#: Whisper); elsewhere a margin for CUDA's own kernels and buffers
LOAD_EXTRA_GB = 1.6 if sys.platform == "win32" else 0.5
#: one 28 s stretch's working memory: the encoder's activations and the decoder's cache
#: for 200 tokens (0.086 GiB measured, at every batch from 1 to 16)
STRETCH_GB = 0.09
#: the fewest stretches the room must hold: a batch of 4 hears 43x real time on the RTX
#: 2080 SUPER, one alone 11x (the decoder's 200 steps take the same time either way)
MIN_BATCH = 4
#: the most stretches heard at once (:data:`~.refine.BATCH` hands it no more)
MAX_BATCH = 32
#: Whisper's working memory per file of its batch (story 38.8), left free for it: a GPU
#: that runs short spills to system memory on Windows, and both models slow down, Whisper
#: up to fourfold and Cohere up to eightfold (measured)
WHISPER_FILE_GB = 0.14
#: kept free after the model is loaded, for the batches' own variation
MARGIN_GB = 0.25
#: the smallest GPU (total memory, GiB) the second listen fits on beside Whisper. Windows'
#: WDDM budget leaves a process ~1 GiB short of the card (6.95 of 8 measured), and an 8 GB
#: card ~5.8 GiB once Whisper is loaded, short of the 6.4 the model, a batch and Whisper's
#: room need, so it takes a 10 GB card there; elsewhere an 8 GB one, beside Whisper in
#: int8_float16. The supervisor sizes Whisper to leave the room on such a GPU
#: (``PlanSTT``, same threshold)
MIN_GPU_GB = 9.5 if sys.platform == "win32" else 7.5
#: the oldest GPU torch's CUDA 13.0 build runs on: Turing (compute capability 7.5); its
#: kernels are built for sm_75 and newer, so an older card loads nothing
MIN_COMPUTE = (7, 5)
#: the oldest NVIDIA driver CUDA 13.0 runs on: R580
MIN_DRIVER = 580


def device_index(device: str) -> int | None:
    """The CUDA device of ``MAKTABA_STT_DEVICE`` (Whisper's): ``auto`` and ``cuda`` are
    device 0, ``cuda:<n>`` device n; ``cpu`` (or anything else) is None."""
    want = (device or "auto").strip().lower()
    if want in ("auto", "cuda"):
        return 0
    name, _, index = want.partition(":")
    if name == "cuda" and index.isdigit():
        return int(index)
    return None


def _runs_on(capability: tuple[int, int], arch_list: list[str]) -> bool:
    """A GPU of ``capability`` runs a torch built for ``arch_list`` (``sm_75``, ...): a
    cubin runs on the same major version at the same or a later minor one, PTX
    (``compute_XY``) on any later GPU."""
    for arch in arch_list:
        kind, _, num = arch.partition("_")
        if not num.isdigit():
            continue
        major, minor = divmod(int(num), 10)
        if kind == "sm" and major == capability[0] and minor <= capability[1]:
            return True
        if kind == "compute" and (major, minor) <= capability:
            return True
    return False


def problem(device: str = "auto") -> str | None:
    """Why this machine has no second listen at all, or None. ``device`` is Whisper's
    (``MAKTABA_STT_DEVICE``): the second listen runs on the same GPU."""
    index = device_index(device)
    if index is None:
        if device.strip().lower() == "cpu":
            return (
                "transcription is set to run on the CPU (MAKTABA_STT_DEVICE=cpu): the second"
                " listen runs on an NVIDIA GPU only"
            )
        return f"MAKTABA_STT_DEVICE={device} is not a GPU (auto, cuda, cuda:<n> or cpu)"
    return _gpu_problem(index)


@functools.cache
def _gpu_problem(index: int) -> str | None:
    """The machine's answer, once per GPU: the packages, torch's build, then the GPU as
    its driver reports it (its memory first). No torch import, no CUDA started."""
    for module, package in (
        ("torch", "torch"),
        ("transformers", "transformers"),
        ("accelerate", "accelerate"),
        ("librosa", "librosa"),
    ):
        if importlib.util.find_spec(module) is None:
            return f"{package} is not installed (the stt extra)"
    built = torch_build()
    if built is not None and built[1] is None:
        return (
            f"torch {built[0]} is a CPU build: the second listen runs on an NVIDIA GPU only"
            " (on the CPU the 2 B model is far too slow), so Whisper's words are kept"
        )
    from .ct2 import gpu_info

    gpu = gpu_info(index)
    if gpu is None:
        return (
            "no NVIDIA GPU here" + (f" ({index})" if index else "") + " (the driver reports"
            " none): the second listen runs on an NVIDIA GPU only, so Whisper's words are kept"
        )
    return gpu_problem(gpu)


def gpu_problem(gpu: Any) -> str | None:
    """Why this NVIDIA GPU (a :class:`.ct2.GpuInfo`) cannot run the second listen, or None:
    its memory (:data:`MIN_GPU_GB`), its compute capability (:data:`MIN_COMPUTE`) and its
    driver (:data:`MIN_DRIVER`); one its driver does not report is not held against it."""
    if gpu.total_gb < MIN_GPU_GB:
        return (
            f"{gpu.name} has {gpu.total_gb:.1f} GB: the second listen needs a GPU of"
            f" {MIN_GPU_GB:g} GB or more here (Cohere's {WEIGHTS_GB * GIB / 1e9:.1f} GB of"
            " weights beside Whisper), so Whisper's words are kept"
        )
    if gpu.compute is not None and tuple(gpu.compute) < MIN_COMPUTE:
        return (
            f"{gpu.name} (compute capability {gpu.compute[0]}.{gpu.compute[1]}) is older than"
            f" the GPUs torch's CUDA build runs on ({MIN_COMPUTE[0]}.{MIN_COMPUTE[1]}, Turing,"
            " and newer), so Whisper's words are kept"
        )
    if gpu.driver_major is not None and gpu.driver_major < MIN_DRIVER:
        return (
            f"the NVIDIA driver {gpu.driver} is older than torch's CUDA build runs on"
            f" (R{MIN_DRIVER} or later): update it, and the second listen runs here"
        )
    return None


_TORCH_CUDA = re.compile(r"""^cuda\s*(?::[^=]*)?=\s*['"]?([^'"\s]+?)['"]?\s*$""", re.M)
_TORCH_VERSION = re.compile(r"""^__version__\s*(?::[^=]*)?=\s*['"]([^'"]+)['"]""", re.M)


@functools.cache
def torch_build() -> tuple[str, str | None] | None:
    """``(torch's version, its CUDA version or None for a CPU build)`` from torch's own
    ``version.py``, read as text (importing torch takes seconds); None when it cannot be
    read, which is not held against the machine (the load says what went wrong)."""
    spec = importlib.util.find_spec("torch")
    origin = getattr(spec, "origin", None)
    if not origin:
        return None
    try:
        text = (Path(origin).parent / "version.py").read_text(encoding="utf-8")
    except OSError:
        return None
    version, cuda = _TORCH_VERSION.search(text), _TORCH_CUDA.search(text)
    if version is None or cuda is None:
        return None
    return version.group(1), None if cuda.group(1) == "None" else cuda.group(1)


def _local_folder(model: str) -> Path | None:
    """``model``'s files on this machine: a folder, else its Hugging Face snapshot (at its
    pinned commit, :data:`.engine.SECOND_LISTEN_PINS`)."""
    folder = Path(model).expanduser()
    if folder.is_dir():
        return folder
    try:
        from huggingface_hub import snapshot_download

        pin = engine.pin_for(model)
        return Path(
            snapshot_download(
                model,
                revision=pin.revision if pin else None,
                allow_patterns=ALLOW_PATTERNS,
                local_files_only=True,
            )
        )
    except Exception:  # noqa: BLE001 - not downloaded (yet)
        return None


def weights_gb(model: str, folder: Path | None = None) -> float:
    """The size of ``model``'s weights (GiB): its safetensors on this machine, else the
    default model's."""
    folder = folder or _local_folder(model)
    if folder is not None:
        size = sum(p.stat().st_size for p in folder.glob("*.safetensors"))
        if size:
            return size / GIB
    return WEIGHTS_GB


def need_gb(weights: float, whisper_batch: int) -> float:
    """The free GPU memory (GiB) the model needs before it loads: its weights, what loading
    takes besides, a batch of :data:`MIN_BATCH` stretches and Whisper's room."""
    return (
        weights + LOAD_EXTRA_GB + MIN_BATCH * STRETCH_GB + max(0, whisper_batch) * WHISPER_FILE_GB
    )


def room(model: str, settings: WhisperSettings) -> str | None:
    """Why the model cannot load beside Whisper right now, or None: the GPU's free memory
    (``torch.cuda.mem_get_info``) must hold :func:`need_gb`. Asked before the model loads;
    no room is remembered like a failed load (``cohere.recogniser``: not asked again until
    its backoff is over, 10 min doubling to 6 h), and a relisten job that meets it gives
    itself back, its video's try unspent."""
    index = device_index(settings.device) or 0
    import torch

    free, total = torch.cuda.mem_get_info(index)
    need = need_gb(weights_gb(model), settings.max_batch)
    if free / GIB >= need:
        return None
    return (
        f"too little free GPU memory for the second listen: {free / GIB:.1f} of"
        f" {total / GIB:.1f} GB free, it needs {need:.1f} GB (Cohere's weights, a batch, and"
        f" room for Whisper's {settings.max_batch} files)"
    )


class LoadError(RuntimeError):
    """The model's files are here but did not load onto the GPU: the home's copy on the
    share would not load either, so it is not fetched."""


def model_dir(model: str) -> Path:
    """The model's folder: a local one as it is, else its Hugging Face snapshot of exactly
    :data:`ALLOW_PATTERNS` (what ``models download`` fetches), downloaded when missing
    (gated: the account must have accepted its licence): the default model at its pinned
    commit, its files checked once (:func:`.engine.verify`)."""
    folder = Path(model).expanduser()
    if folder.is_dir():
        return folder
    from huggingface_hub import snapshot_download

    pin = engine.pin_for(model)
    snap = Path(
        snapshot_download(
            model, revision=pin.revision if pin else None, allow_patterns=ALLOW_PATTERNS
        )
    )
    if pin is not None:
        engine.verify(snap, pin)
    return snap


class CudaRecogniser:
    """Cohere Transcribe on one CUDA device; :meth:`__call__` hears stretches of 16 kHz
    mono float32 audio (NumPy). One call at a time uses the GPU (calls queue on a lock),
    and each gives back the memory its batches took (Whisper shares the GPU)."""

    def __init__(
        self,
        model_id: str,
        path: Path | None = None,
        *,
        source: str = "hf",
        settings: WhisperSettings | None = None,
    ) -> None:
        settings = settings or WhisperSettings()
        self.model_id = model_id
        #: where the weights came from: ``hf`` (Hugging Face) or ``share`` (the home's copy)
        self.source = source
        self._lock = threading.Lock()
        self._whisper_batch = settings.max_batch
        t0 = time.monotonic()
        folder = path or model_dir(model_id)
        import torch

        self._torch = torch
        try:
            self._usable(settings)
            self._load(folder, model_id, settings)
        except Exception as exc:
            torch.cuda.empty_cache()  # whatever reached the GPU goes back to Whisper
            raise LoadError(f"{type(exc).__name__}: {exc}") from exc
        from importlib.metadata import version

        dtype = str(self.dtype).removeprefix("torch.")
        #: what heard it, for the transcript's metadata
        self.runtime = (
            f"transformers {version('transformers')}, torch {torch.__version__},"
            f" {self.device} {dtype}"
        )
        _log.info(
            "cohere.loaded",
            model=model_id,
            source=source,
            device=str(self.device),
            dtype=dtype,
            batch=self.batch,
            seconds=round(time.monotonic() - t0, 1),
        )

    def _usable(self, settings: WhisperSettings) -> None:
        """What only torch can tell: CUDA works, and this GPU is one its build runs on."""
        torch = self._torch
        index = device_index(settings.device) or 0
        if not torch.cuda.is_available() or index >= torch.cuda.device_count():
            raise RuntimeError(
                f"torch {torch.__version__} finds no CUDA device {index} (CUDA"
                f" {torch.version.cuda}, an NVIDIA GPU and its driver)"
            )
        capability = tuple(int(x) for x in torch.cuda.get_device_capability(index))
        archs = list(torch.cuda.get_arch_list())
        if not _runs_on((capability[0], capability[1]), archs):
            raise RuntimeError(
                f"compute capability {capability[0]}.{capability[1]} is older than the GPUs"
                f" torch's CUDA {torch.version.cuda} build runs on ({', '.join(archs)})"
            )

    def _load(self, folder: Path, model_id: str, settings: WhisperSettings) -> None:
        torch = self._torch
        from transformers import AutoConfig, AutoModelForSpeechSeq2Seq, AutoProcessor
        from transformers.utils import logging as transformers_logging

        # no "Loading weights" bar in the log
        transformers_logging.disable_progress_bar()  # type: ignore[no-untyped-call,unused-ignore]

        config = AutoConfig.from_pretrained(folder)
        if config.model_type != "cohere_asr":
            raise RuntimeError(
                f"{model_id} is a {config.model_type} model: the second listen loads a"
                " cohere_asr one (Cohere Transcribe)"
            )
        index = device_index(settings.device) or 0
        self.device = torch.device("cuda", index)
        major, _minor = torch.cuda.get_device_capability(index)
        self.dtype = torch.bfloat16 if major >= 8 else torch.float16
        self._processor: Any = AutoProcessor.from_pretrained(folder)  # type: ignore[no-untyped-call,unused-ignore]
        self._model: Any = AutoModelForSpeechSeq2Seq.from_pretrained(
            folder, dtype=self.dtype, device_map=str(self.device)
        ).eval()
        #: stretches heard at once: what the GPU holds once the model is loaded
        self.batch = self._fit_batch()

    def _fit_batch(self) -> int:
        free, _total = self._torch.cuda.mem_get_info(self.device)
        spare = free / GIB - MARGIN_GB - self._whisper_batch * WHISPER_FILE_GB
        return max(1, min(MAX_BATCH, int(spare / STRETCH_GB)))

    def __call__(self, stretches: list[Any]) -> list[str]:
        """The text of each stretch (each at most 30 s; a longer one is heard in parts and
        joined), in order."""
        with self._lock:
            try:
                return self._hear_all(stretches)
            finally:
                self._torch.cuda.empty_cache()

    def _hear_all(self, stretches: list[Any]) -> list[str]:
        out: list[str] = []
        i = 0
        while i < len(stretches):
            part = stretches[i : i + self.batch]
            try:
                out.extend(self._hear(part))
            except self._torch.cuda.OutOfMemoryError:
                self._torch.cuda.empty_cache()
                if self.batch == 1:
                    raise
                self.batch = max(1, self.batch // 2)
                _log.warning("cohere.batch_halved", batch=self.batch, reason="out of GPU memory")
                continue
            i += len(part)
        return out

    def _hear(self, part: list[Any]) -> list[str]:
        import numpy as np

        inputs = self._processor(
            audio=[np.asarray(a, dtype=np.float32) for a in part],
            sampling_rate=SAMPLE_RATE,
            language=LANGUAGE,
            punctuation=True,
            return_tensors="pt",
        )
        chunks = inputs.pop("audio_chunk_index", None)
        inputs = inputs.to(self.device, dtype=self.dtype)
        with self._torch.inference_mode():
            ids = self._model.generate(**inputs, max_new_tokens=MAX_TOKENS)
        texts = self._processor.decode(
            ids, skip_special_tokens=True, audio_chunk_index=chunks, language=LANGUAGE
        )
        if isinstance(texts, str):
            texts = [texts]
        return [str(t).strip() for t in texts]
