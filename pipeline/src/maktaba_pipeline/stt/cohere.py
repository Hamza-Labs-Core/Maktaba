"""The models :mod:`maktaba_pipeline.stt.refine` uses: Cohere Transcribe Arabic on MLX
(through ``mlx-audio``) and the CAMeL Tools MSA dictionary.

Both load once per process, on first use, and both are optional: a worker without the
packages, the model's licence (a gated Hugging Face repo: accept it and log in once with
``hf auth login``) or the dictionary's data (``camel_data -i morphology-db-msa-r13``,
fetched here on first use) stores Whisper's transcript as it is and says why in the
transcript's metadata.
"""

from __future__ import annotations

import importlib.util
import threading
from concurrent.futures import ThreadPoolExecutor
from functools import lru_cache
from typing import Any

from ..log import get_logger

__all__ = ["DEFAULT_MODEL", "CamelDictionary", "CohereRecogniser", "dictionary", "recogniser"]

_log = get_logger()

DEFAULT_MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"

#: an Arabic stretch of 28 s is about 80 words, ~200 tokens
_MAX_TOKENS = 448


class CohereRecogniser:
    """One loaded model on its own thread; :meth:`__call__` hears stretches of 16 kHz
    mono float32 audio (NumPy). MLX ties a GPU stream to the thread that made it, so
    the model is loaded and run on one dedicated thread, like Whisper's
    (:class:`~maktaba_pipeline.stt.batched.BatchedTranscriber`); calls queue for it."""

    def __init__(self, model_id: str) -> None:
        self.model_id = model_id
        self._gpu = ThreadPoolExecutor(max_workers=1, thread_name_prefix="cohere")
        self._model: Any = self._gpu.submit(self._load).result()

    def _load(self) -> Any:
        from mlx_audio.stt.utils import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            load,
        )

        return load(self.model_id)

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


def recogniser(model_id: str) -> CohereRecogniser:
    """The process's recogniser for ``model_id`` (loaded on first use; raises when the
    package, the licence or the network is missing)."""
    if importlib.util.find_spec("mlx_audio") is None:
        raise RuntimeError("mlx-audio is not installed (the stt extra)")
    with _REC_LOCK:
        rec = _REC.get(model_id)
        if rec is None:
            rec = _REC[model_id] = CohereRecogniser(model_id)
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
