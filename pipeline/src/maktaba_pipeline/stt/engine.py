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

import hashlib
import platform
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path

from ..log import get_logger

__all__ = [
    "BACKEND",
    "CT2",
    "CT2_FILES",
    "CT2_PINS",
    "DEFAULT_BATCH",
    "DEFAULT_MODEL",
    "MLX",
    "SECOND_LISTEN_PINS",
    "Pin",
    "PinMismatch",
    "ct2_repo",
    "current",
    "fingerprint_model",
    "for_machine",
    "model_for",
    "pin_for",
    "verify",
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


@dataclass(frozen=True)
class Pin:
    """A model repo at one commit, and the hash of each file Maktaba loads from it: its
    SHA-256, or (a small file of a gated repo, which Hugging Face describes without a
    sign-in only by its git blob id) the SHA-1 of its git blob."""

    revision: str
    sha256: Mapping[str, str]
    git_sha1: Mapping[str, str] = field(default_factory=dict)


#: the CTranslate2 conversions Maktaba downloads (the default, and the one an MLX large-v3
#: maps to), each at the commit it was checked at, with its files' SHA-256 (the weights'
#: from Hugging Face's own LFS record, the small files hashed at that commit; their git
#: blob ids match the commit's): a repo that moves or is rewritten cannot change what a
#: worker loads. A model of the operator's own (MAKTABA_STT_MODEL) is fetched as its repo
#: has it.
CT2_PINS: dict[str, Pin] = {
    "dropbox-dash/faster-whisper-large-v3-turbo": Pin(
        "0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf",
        {
            "config.json": "b0253ea6c0d3bea6b1e19e91a02acfd3b53f4467362efcb5a3e6b16c9b3a9b7e",
            "preprocessor_config.json": (
                "7ccc62c6f2765af1f3b46c00c9b5894426835a05021c8b9c01eecb6dfb542711"
            ),
            "model.bin": "e76620f83d5f5b69efd3d87e3dc180c1bd21df9fbebacfd4335e5e1efcc018da",
            "tokenizer.json": "297b13372ac43916285644fb9687add3cc62ee2a1adb60da3dc25cc94c1871fd",
            "vocabulary.json": "c69260f2ab26d659b7c398f9a2b2b48ed0df16c3b47d7326782fd9cba71690c1",
        },
    ),
    "Systran/faster-whisper-large-v3": Pin(
        "edaa852ec7e145841d8ffdb056a99866b5f0a478",
        {
            "config.json": "a9306624f5ec14270a014b647e5c316b6e03a662c369758d1b90697a7b0655b9",
            "preprocessor_config.json": (
                "7ccc62c6f2765af1f3b46c00c9b5894426835a05021c8b9c01eecb6dfb542711"
            ),
            "model.bin": "69f74147e3334731bc3a76048724833325d2ec74642fb52620eda87352e3d4f1",
            "tokenizer.json": "6d8cbd7cd0d8d5815e478dac67b85a26bbe77c1f5e0c6d76d1ce2abc0e5f21ca",
            "vocabulary.json": "c69260f2ab26d659b7c398f9a2b2b48ed0df16c3b47d7326782fd9cba71690c1",
        },
    ),
}

#: the second listen's model as Windows and Linux fetch it (transformers' files,
#: stt/cohere_cuda.py ALLOW_PATTERNS; story 38.9): the commit of 2026-07-13, the weights and
#: the tokenizer's model by Hugging Face's LFS record, the small files by their git blob ids
#: (the repo is gated: their content is not to be had without a sign-in). On Apple Silicon
#: mlx-audio fetches the repo itself, as it always has.
SECOND_LISTEN_PINS: dict[str, Pin] = {
    "CohereLabs/cohere-transcribe-arabic-07-2026": Pin(
        "c3e911b42149bf7a1e53d5cef9878aee87515a23",
        {
            "model.safetensors": "404ff5dccd66b1985a06059b9d5ed970a676761c0821f9c10756db6e4a1910a5",
            "tokenizer.model": "6d21e6a83b2d0d3e1241a7817e4bef8eb63bcb7cfe4a2675af9a35ff3bbf0e14",
        },
        {
            "config.json": "accb69f98a8f47eae1619ccee7414aac8fe4328e",
            "generation_config.json": "85204c3c9045ec9585d6c1d2a7e1357557f983f1",
            "normalizer.json": "dd6ae819ad738ac1a546e9f9282ef325c33b9ea0",
            "preprocessor_config.json": "fc14ee1e20351f89d07da5e15897444ee4c12818",
            "processor_config.json": "84095bb3753423c38a58fcefad2612c09be5461c",
            "special_tokens_map.json": "c4a109a3143a087ca7ca8c122cb48ab73b5547a2",
            "tokenizer.json": "87cef32848a773931b2ae42003d4dea25c7e4e38",
            "tokenizer_config.json": "6072d9b482dfe704f52a8f61d7b24172097013ea",
        },
    ),
}


def pin_for(repo: str) -> Pin | None:
    """The pin of a repo Maktaba fetches at a fixed commit, or None."""
    return CT2_PINS.get(repo) or SECOND_LISTEN_PINS.get(repo)


#: written into a snapshot once its files matched their pin: checked once, not each load
VERIFIED = ".maktaba-sha256"


class PinMismatch(RuntimeError):
    """A downloaded file is not the one pinned."""


def verify(folder: Path, pin: Pin) -> None:
    """Check the files of a snapshot of ``pin``'s commit against their SHA-256, once (a
    marker remembers it). A missing or different file is removed (the next download
    fetches it again) and raises :class:`PinMismatch`."""
    marker = folder / VERIFIED
    try:
        if marker.read_text(encoding="utf-8").strip() == pin.revision:
            return
    except OSError:
        pass
    bad: list[str] = []
    wanted = [(n, h, "SHA-256") for n, h in pin.sha256.items()]
    wanted += [(n, h, "git blob") for n, h in pin.git_sha1.items()]
    for name, want, kind in sorted(wanted):
        path = folder / name
        try:
            got = _git_blob_sha1(path) if kind == "git blob" else _sha256(path)
        except OSError:
            bad.append(f"{name} is missing")
            continue
        if got != want:
            bad.append(f"{name} has {kind} {got}, not {want}")
            for p in {path.resolve(), path}:  # the cache's blob and the snapshot's link
                p.unlink(missing_ok=True)
    if bad:
        raise PinMismatch(f"{folder}: " + "; ".join(bad) + " (removed: it is fetched again)")
    marker.write_text(pin.revision + "\n", encoding="utf-8")


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def _git_blob_sha1(path: Path) -> str:
    """The file's git blob id: the SHA-1 of ``blob <size>``, a NUL byte and its bytes."""
    h = hashlib.sha1(f"blob {path.stat().st_size}\0".encode(), usedforsecurity=False)
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


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
