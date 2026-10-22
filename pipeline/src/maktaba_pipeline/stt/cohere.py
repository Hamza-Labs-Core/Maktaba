"""The models :mod:`maktaba_pipeline.stt.refine` uses: Cohere Transcribe Arabic and the
CAMeL Tools MSA dictionary. Cohere runs on the GPU of this machine's Whisper engine
(:mod:`.engine`): on MLX (through ``mlx-audio``) on Apple Silicon, and through transformers
on torch's CUDA on Windows and Linux (:mod:`.cohere_cuda`, story 38.9).

Both load once per process, on first use, and both are optional: a worker without the
packages, the model (a gated Hugging Face repo: the home accepts its licence and logs in
once with ``hf auth login``, then publishes a copy to the share that the other machines
load from, :mod:`~.model_share`) or the dictionary's data (``camel_data -i morphology-db-msa-r13``,
fetched here on first use) stores Whisper's transcript as it is and says why in the
transcript's metadata. So does a Windows or Linux machine without an NVIDIA GPU that holds
the model beside Whisper (:func:`unavailable`), and, until the model is loaded, one whose
GPU has too little free memory for it at that moment.
"""

from __future__ import annotations

import importlib.util
import os
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path
from typing import Any

from ..log import get_logger
from . import engine, model_share
from .settings import WhisperSettings

__all__ = [
    "DEFAULT_MODEL",
    "CamelDictionary",
    "CohereRecogniser",
    "Recogniser",
    "dictionary",
    "load_failure",
    "loaded",
    "recogniser",
    "unavailable",
]

_log = get_logger()

DEFAULT_MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"

#: an Arabic stretch of 28 s is about 80 words, ~200 tokens
_MAX_TOKENS = 448


class CohereRecogniser:
    """On MLX: one loaded model on its own thread; :meth:`__call__` hears stretches of 16 kHz
    mono float32 audio (NumPy). MLX ties a GPU stream to the thread that made it, so
    the model is loaded and run on one dedicated thread, like Whisper's
    (:class:`~maktaba_pipeline.stt.batched.BatchedTranscriber`); calls queue for it."""

    def __init__(self, model_id: str, path: Path | None = None, *, source: str = "hf") -> None:
        self.model_id = model_id
        #: where the weights came from: ``hf`` (Hugging Face) or ``share`` (the home's copy)
        self.source = source
        self._path = path
        self._gpu = ThreadPoolExecutor(max_workers=1, thread_name_prefix="cohere")
        try:
            self._model: Any = self._gpu.submit(self._load).result()
        except BaseException:
            self._gpu.shutdown(wait=False)
            raise

    def _load(self) -> Any:
        from mlx_audio.stt.utils import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            load,
        )

        return load(str(self._path) if self._path is not None else self.model_id)

    def __call__(self, stretches: list[Any]) -> list[str]:
        """The text of each stretch (each at most 30 s), decoded together in batches."""
        return self._gpu.submit(self._hear, stretches).result()

    def _hear(self, stretches: list[Any]) -> list[str]:
        """On the model's thread. Uses the model's own batch decoder
        (``_transcribe_waveforms_batched``, pinned with mlx-audio 0.5.9) so a file's
        stretches share GPU passes; without it, one ``generate`` per stretch."""
        import mlx.core as mx  # type: ignore[import-untyped,import-not-found,unused-ignore]

        batched = getattr(self._model, "_transcribe_waveforms_batched", None)
        if batched is None:
            return [
                str(self._model.generate(a, language="ar", max_tokens=_MAX_TOKENS).text).strip()
                for a in stretches
            ]
        texts, _counts, _prompt = batched(
            [mx.array(a, dtype=mx.float32) for a in stretches],
            language="ar",
            punctuation=True,
            batch_size=self._model._resolve_batch_size(None),
            max_tokens=_MAX_TOKENS,
        )
        return [str(t).strip() for t in texts]


#: a loaded recogniser: :class:`CohereRecogniser` on MLX, :class:`.cohere_cuda.CudaRecogniser`
#: on CUDA. Both hear a list of stretches and say where the weights came from (``source``)
Recogniser = Any

_REC: dict[str, Recogniser] = {}
_REC_LOCK = threading.Lock()

#: a failed load (no licence and no copy on the share, files that would not load) is not
#: tried again for this long, doubling each time up to RETRY_MAX_S: a load takes 20-26 s
#: (measured on CUDA), and every file would pay it. A token added, a copy published or a
#: GPU freed is seen at the next try (a restart tries at once).
RETRY_AFTER_S = 600.0
RETRY_MAX_S = 6 * 3600.0


@dataclass
class _Failure:
    message: str
    at: float
    tries: int

    def wait_s(self) -> float:
        return min(RETRY_MAX_S, RETRY_AFTER_S * 2.0 ** (self.tries - 1))


_FAILED: dict[str, _Failure] = {}
#: the clock of the backoff (tests replace it)
_clock = time.monotonic


_PUBLISHED: set[str] = set()


def unavailable(device: str | None = None) -> str | None:
    """Why this machine has no second listen at all, or None: its transcripts then keep
    Whisper's words, and ``models download`` fetches neither the model nor the dictionary.
    Apple Silicon always has one; Windows and Linux need torch's CUDA and an NVIDIA GPU that
    holds the model beside Whisper (:func:`.cohere_cuda.problem`). ``device`` is Whisper's
    (``MAKTABA_STT_DEVICE`` when not given)."""
    if engine.current() == engine.MLX:
        return None
    from . import cohere_cuda

    if device is None:
        device = os.environ.get("MAKTABA_STT_DEVICE") or "auto"
    return cohere_cuda.problem(device)


def recogniser(
    model_id: str, share: Path | None = None, settings: WhisperSettings | None = None
) -> Recogniser:
    """The process's recogniser for ``model_id``, loaded on first use: from Hugging Face,
    else from the copy on ``share`` (the share's models folder, :mod:`~.model_share`).
    Raises when the package is missing or neither has the model, and on CUDA when the GPU
    has no room for it now. A failed load and a GPU without room alike are not tried again
    until their backoff is over (:func:`load_failure`). A Hugging Face load publishes the
    snapshot to ``share`` (once per process, on a thread of its own). ``settings`` are
    Whisper's: on CUDA the second listen uses its GPU and leaves room for its batch."""
    cuda = engine.current() != engine.MLX
    why = unavailable(settings.device if settings is not None and cuda else None)
    if why is not None:
        raise RuntimeError(why)
    if not cuda and importlib.util.find_spec("mlx_audio") is None:
        raise RuntimeError("mlx-audio is not installed (the stt extra)")
    with _REC_LOCK:
        rec = _REC.get(model_id)
        if rec is None:
            failed = load_failure(model_id)
            if failed is not None:
                raise RuntimeError(failed)
            if cuda:
                from . import cohere_cuda

                # a GPU without room (one that also drives a desktop) stays so for a while:
                # it waits out its backoff like a failed load, so that a worker does not
                # make every transcript without its second listen meanwhile
                try:
                    full = cohere_cuda.room(model_id, settings or WhisperSettings())
                except Exception as exc:  # noqa: BLE001 - the driver's answer: no room known
                    full = f"the GPU's free memory could not be read: {type(exc).__name__}: {exc}"
                if full is not None:
                    _failed(model_id, full, "cohere.no_room")
                    raise RuntimeError(full)
            try:
                rec = _REC[model_id] = _open(model_id, share, settings)
            except Exception as exc:
                _failed(model_id, f"{type(exc).__name__}: {exc}", "cohere.load_failed")
                raise
            _FAILED.pop(model_id, None)
        return rec


def _failed(model_id: str, message: str, event: str) -> None:
    """Remember that ``model_id`` did not load here (``message``), one more try than the
    last time: :func:`load_failure` says so until its backoff is over."""
    before = _FAILED.get(model_id)
    f = _Failure(message[:300], _clock(), 1 if before is None else before.tries + 1)
    _FAILED[model_id] = f
    _log.warning(event, model=model_id, error=f.message, retry_s=f.wait_s())


def load_failure(model_id: str) -> str | None:
    """Why ``model_id`` is not loaded here for now: its last load failed, and it is not
    tried again until its backoff is over (:data:`RETRY_AFTER_S`); None otherwise."""
    f = _FAILED.get(model_id)
    if f is None:
        return None
    left = f.at + f.wait_s() - _clock()
    if left <= 0:
        return None
    return f"{f.message} (tried {f.tries}x; next try in {round(left / 60)} min)"


def loaded(model_id: str) -> bool:
    """The process has ``model_id`` loaded already (no need to look for the share)."""
    return model_id in _REC


def _new(
    model_id: str, path: Path | None, source: str, settings: WhisperSettings | None
) -> Recogniser:
    """A recogniser on this machine's engine."""
    if engine.current() == engine.MLX:
        return CohereRecogniser(model_id, path, source=source)
    from .cohere_cuda import CudaRecogniser

    return CudaRecogniser(model_id, path, source=source, settings=settings)


def _open(model_id: str, share: Path | None, settings: WhisperSettings | None = None) -> Recogniser:
    try:
        rec = _new(model_id, None, "hf", settings)
    except Exception as hf_exc:  # noqa: BLE001 - gated, not logged in, offline: the share
        if engine.current() != engine.MLX:
            from .cohere_cuda import LoadError

            if isinstance(hf_exc, LoadError):  # here, but it would not load: no copy either
                raise
        try:
            path = model_share.from_share(model_id, share)
        except (OSError, ValueError, model_share.ShareModelError) as exc:
            raise RuntimeError(
                f"Hugging Face: {type(hf_exc).__name__}: {hf_exc} | share: {exc}"
            ) from hf_exc
        _log.info("stt.model_from_share", model=model_id, folder=str(path), hf_error=str(hf_exc))
        return _new(model_id, path, "share", settings)
    if share is not None and model_id not in _PUBLISHED:
        _PUBLISHED.add(model_id)
        threading.Thread(
            target=model_share.publish_from_cache,
            args=(model_id, share),
            name="cohere-publish",
            daemon=True,
        ).start()
    return rec


class CamelDictionary:
    """Spelling from CAMeL Tools' MSA analyser: the most likely analysis's word,
    undiacritised; on a tie, the candidate closest to the word as heard."""

    def __init__(self) -> None:
        from camel_tools.morphology.analyzer import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            Analyzer,
        )
        from camel_tools.morphology.database import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            MorphologyDB,
        )
        from camel_tools.utils.dediac import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            dediac_ar,
        )

        self._an = Analyzer(MorphologyDB.builtin_db("calima-msa-r13"), backoff="NONE")
        self._dediac = dediac_ar
        self._lock = threading.Lock()

    @lru_cache(maxsize=65_536)  # noqa: B019 - one instance per process, never freed
    def spell(self, word: str) -> str | None:
        with self._lock:
            analyses = self._an.analyze(word)
        best: dict[str, float] = {}
        for a in analyses:
            s = self._dediac(str(a.get("diac", "")))
            if s:
                best[s] = max(best.get(s, -99.0), float(a.get("pos_lex_logprob", -99.0)))
        if not best:
            return None
        top = max(best.values())
        tied = [s for s, p in best.items() if p == top]
        return max(tied, key=lambda s: _closeness(s, word))


def _closeness(a: str, b: str) -> int:
    """Letters in place: «إلى» is closer to «الى» than «إلي» is."""
    return sum(x == y for x, y in zip(a, b, strict=False))


_DICT: list[CamelDictionary | None] = []


def dictionary() -> CamelDictionary | None:
    """The process's dictionary, or None when CAMeL Tools or its data is missing (the
    data is fetched once, on first use)."""
    if _DICT:
        return _DICT[0]
    with _REC_LOCK:
        if _DICT:
            return _DICT[0]
        d: CamelDictionary | None = None
        if importlib.util.find_spec("camel_tools") is not None:
            from ..model_dirs import ensure_camel_data

            ensure_camel_data()
            try:
                d = CamelDictionary()
            except Exception:  # noqa: BLE001 - the data may be missing: fetch it once
                try:
                    from camel_tools.data import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
                        CATALOGUE,
                    )

                    CATALOGUE.download_package("morphology-db-msa-r13")
                    d = CamelDictionary()
                except Exception:  # noqa: BLE001 - spell as heard
                    _log.warning("stt.dictionary_unavailable", exc_info=True)
        _DICT.append(d)
        return d
