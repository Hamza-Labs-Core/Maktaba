"""Which Whisper engine this machine transcribes with (plan 03-10, story 38.8).

Two engines run the same batched Whisper (:class:`~.batched.BatchedTranscriber`): MLX
on Apple Silicon, and CTranslate2 (faster-whisper's model format) on Windows and Linux,
on an NVIDIA GPU when CUDA is usable there, else on the CPU (:mod:`.ct2`). Each loads
whisper-large-v3-turbo by default in its own format, and a transcript records which
engine made it (``transcripts.backend``).

Light on purpose: the transcribe stage, ``models status|download`` and workers without
the ``stt`` extra import it.
"""

from __future__ import annotations

import platform
from pathlib import Path

from ..log import get_logger

__all__ = [
    "BACKEND",
    "CT2",
    "CT2_FILES",
    "DEFAULT_BATCH",
    "DEFAULT_MODEL",
    "MLX",
    "ct2_repo",
    "current",
    "fingerprint_model",
    "for_machine",
    "model_for",
]

_log = get_logger()

MLX = "mlx"
CT2 = "ct2"


def for_machine(system: str, machine: str) -> str:
    """The engine of a machine (``platform.system()``, ``platform.machine()``): MLX on
    Apple Silicon, CTranslate2 elsewhere."""
    return MLX if system == "Darwin" and machine == "arm64" else CT2


def current() -> str:
    """The engine this machine runs."""
    return for_machine(platform.system(), platform.machine())


#: whisper-large-v3-turbo for each engine. CTranslate2's is the conversion faster-whisper
#: itself downloads for "large-v3-turbo" (1.2.1 names it mobiuslabsgmbh/faster-whisper-
#: large-v3-turbo, a repo since moved here: the old name only redirects): float16, 1.6 GB
DEFAULT_MODEL = {
    MLX: "mlx-community/whisper-large-v3-turbo",
    CT2: "dropbox-dash/faster-whisper-large-v3-turbo",
}

#: what ``transcripts.backend`` records
BACKEND = {MLX: "mlx-batched", CT2: "ct2-batched"}

#: files decoded together when ``MAKTABA_STT_BATCH`` is unset: the 96 GB Mac's 32; on
#: CTranslate2 a batch a 4 GB GPU (or a CPU) holds. The supervisor sizes it per machine.
DEFAULT_BATCH = {MLX: 32, CT2: 8}

#: what the CTranslate2 engine loads from a model repo (faster-whisper's own list), and so
#: what ``models download`` fetches
CT2_FILES = [
    "config.json",
    "preprocessor_config.json",
    "model.bin",
    "tokenizer.json",
    "vocabulary.*",
]

#: a repo that moved: its old name only redirects
_MOVED = {"mobiuslabsgmbh/faster-whisper-large-v3-turbo": DEFAULT_MODEL[CT2]}

#: MLX conversions whose weights CTranslate2 has under another name
_CT2_OF_MLX = {
    DEFAULT_MODEL[MLX]: DEFAULT_MODEL[CT2],
    "mlx-community/whisper-large-v3-mlx": "Systran/faster-whisper-large-v3",
}

_MAPPED: set[str] = set()


def _is_mlx(model: str) -> bool:
    name = model.replace("\\", "/").rstrip("/")
    return name.startswith("mlx-community/") or name.rsplit("/", 1)[-1].endswith("-mlx")


def model_for(engine: str, model: str | None) -> str:
    """The model ``engine`` loads for ``MAKTABA_STT_MODEL`` = ``model``: its default when
    unset; on CTranslate2 an MLX conversion (``mlx-community/...``, which it cannot load)
    becomes the CTranslate2 conversion of the same weights, or the default when there is
    none, with a line in the log; anything else as given."""
    model = (model or "").strip()
    if not model:
        return DEFAULT_MODEL[engine]
    if engine == CT2 and _is_mlx(model) and not Path(model).expanduser().is_dir():
        to = _CT2_OF_MLX.get(model, DEFAULT_MODEL[CT2])
        if model not in _MAPPED:
            _MAPPED.add(model)
            _log.warning(
                "stt.model_mapped",
                model=model,
                to=to,
                reason="an MLX model: the CTranslate2 engine loads a CTranslate2 conversion",
            )
        return to
    return _MOVED.get(model, model) if engine == CT2 else model


#: one Whisper under two engines' names, as the fingerprint names it (the MLX name: what
#: the Macs recorded first)
_SAME_WHISPER = {ct2: mlx for mlx, ct2 in _CT2_OF_MLX.items()}


def fingerprint_model(model: str) -> str:
    """The model as the transcribe fingerprint names it: the same weights count as the same
    model on either engine, so a machine on the other engine does not transcribe the
    library again (the transcript still records the model and engine that made it)."""
    return _SAME_WHISPER.get(model, model)


def ct2_repo(model: str) -> str:
    """A CTranslate2 model's Hugging Face repo: a size faster-whisper knows by name
    ("large-v3", "turbo") as the repo it downloads for it, else ``model`` itself."""
    if "/" in model or "\\" in model or Path(model).expanduser().is_dir():
        return model
    try:
        from faster_whisper.utils import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            _MODELS,
        )
    except ImportError:
        return model
    repo = str(_MODELS.get(model, model))
    return _MOVED.get(repo, repo)
