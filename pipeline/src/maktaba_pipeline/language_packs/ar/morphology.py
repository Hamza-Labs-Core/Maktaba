"""CAMeL Tools' MSA morphological analyser, one per process (Epic 52, story 52.4).

The Arabic pack decides "known" with it, and the second listen's speller
(:class:`maktaba_pipeline.stt.cohere.CamelDictionary`) spells with the same instance, so a
transcribing machine holds one copy. Its data is the ``model.refine`` asset; nothing here
downloads it: without CAMeL Tools or its data the analyser is unavailable and the pack
says ``UNSURE`` instead of ``UNKNOWN``.
"""

from __future__ import annotations

import importlib.util
import threading
from typing import Any

__all__ = ["Analysis", "analyse", "analyser", "available"]

_LOCK = threading.Lock()
_ONE: list[Any] = []

#: (lemma, part of speech, the analysis's spelling with its diacritics, its log probability)
Analysis = tuple[str, str, str, float]


def analyser() -> Any | None:
    """The process's ``camel_tools`` Analyzer (MSA DB r13, no backoff), or None."""
    if _ONE:
        return _ONE[0]
    with _LOCK:
        if _ONE:
            return _ONE[0]
        an: Any = None
        if importlib.util.find_spec("camel_tools") is not None:
            try:
                from camel_tools.morphology.analyzer import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
                    Analyzer,
                )
                from camel_tools.morphology.database import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
                    MorphologyDB,
                )

                an = Analyzer(MorphologyDB.builtin_db("calima-msa-r13"), backoff="NONE")
            except Exception:  # noqa: BLE001 - no data here: the pack goes without it
                an = None
        _ONE.append(an)
        return an


def available() -> bool:
    return analyser() is not None


def analyse(word: str) -> list[Analysis]:
    an = analyser()
    if an is None:
        return []
    with _LOCK:
        got = an.analyze(word)
    return [
        (
            str(a.get("lex", "")),
            str(a.get("pos", "")),
            str(a.get("diac", "")),
            float(a.get("pos_lex_logprob", -99.0)),
        )
        for a in got
    ]
