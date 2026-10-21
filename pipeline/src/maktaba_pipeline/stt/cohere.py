"""The models :mod:`maktaba_pipeline.stt.refine` uses: Cohere Transcribe Arabic on MLX
(through ``mlx-audio``) and the CAMeL Tools MSA dictionary.

Both load once per process, on first use, and both are optional: a worker without the
packages, the model (a gated Hugging Face repo: the home accepts its licence and logs in
once with ``hf auth login``, then publishes a copy to the share that the other machines
load from, :mod:`~.model_share`) or the dictionary's data (``camel_data -i morphology-db-msa-r13``,
fetched here on first use) stores Whisper's transcript as it is and says why in the
transcript's metadata. The second listen runs on MLX: a machine on the CTranslate2 engine
(Windows, Linux: :mod:`.engine`) keeps Whisper's words, and says so (:func:`unavailable`).
"""

from __future__ import annotations

import importlib.util
import threading
from concurrent.futures import ThreadPoolExecutor
from functools import lru_cache
from pathlib import Path
from typing import Any

from ..log import get_logger
from . import engine, model_share

__all__ = [
    "DEFAULT_MODEL",
    "CamelDictionary",
    "CohereRecogniser",
    "dictionary",
    "loaded",
    "recogniser",
    "unavailable",
]

_log = get_logger()

DEFAULT_MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"

#: an Arabic stretch of 28 s is about 80 words, ~200 tokens
_MAX_TOKENS = 448


class CohereRecogniser:
    """One loaded model on its own thread; :meth:`__call__` hears stretches of 16 kHz
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


_REC: dict[str, CohereRecogniser] = {}
_REC_LOCK = threading.Lock()


_PUBLISHED: set[str] = set()


def unavailable() -> str | None:
    """Why this machine has no second listen at all, or None: Cohere Transcribe runs on
    MLX, and a machine on the CTranslate2 engine has none (its transcripts keep Whisper's
    words; ``models download`` does not fetch the model or the dictionary)."""
    if engine.current() != engine.MLX:
        return "the second listen runs on Apple Silicon (MLX) only: Whisper's words are kept"
    return None


def recogniser(model_id: str, share: Path | None = None) -> CohereRecogniser:
    """The process's recogniser for ``model_id``, loaded on first use: from Hugging Face,
    else from the copy on ``share`` (the share's models folder, :mod:`~.model_share`).
    Raises when the package is missing or neither has the model. A Hugging Face load
    publishes the snapshot to ``share`` (once per process, on a thread of its own)."""
    why = unavailable()
    if why is not None:
        raise RuntimeError(why)
    if importlib.util.find_spec("mlx_audio") is None:
        raise RuntimeError("mlx-audio is not installed (the stt extra)")
    with _REC_LOCK:
        rec = _REC.get(model_id)
        if rec is None:
            rec = _REC[model_id] = _open(model_id, share)
        return rec


def loaded(model_id: str) -> bool:
    """The process has ``model_id`` loaded already (no need to look for the share)."""
    return model_id in _REC


def _open(model_id: str, share: Path | None) -> CohereRecogniser:
    try:
        rec = CohereRecogniser(model_id)
    except Exception as hf_exc:  # noqa: BLE001 - gated, not logged in, offline: the share
        try:
            path = model_share.from_share(model_id, share)
        except (OSError, ValueError, model_share.ShareModelError) as exc:
            raise RuntimeError(
                f"Hugging Face: {type(hf_exc).__name__}: {hf_exc} | share: {exc}"
            ) from hf_exc
        _log.info("stt.model_from_share", model=model_id, folder=str(path), hf_error=str(hf_exc))
        return CohereRecogniser(model_id, path, source="share")
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
