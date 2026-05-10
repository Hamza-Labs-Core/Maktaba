"""Search suggestion service — Epic 5 Story 5.6.

The suggest path is a prefix lookup over ``search_suggestion_terms``
with a tiny LRU cache in front. A separate offline build job
(:func:`build_ngrams_for_library`) populates the table from
``transcript_units`` text on a cadence the orchestrator chooses.
"""

from __future__ import annotations

from .build import NgramExtractor, build_ngrams_for_library
from .cache import SuggestCache
from .service import Suggestion, SuggestRequest, SuggestService

__all__ = [
    "NgramExtractor",
    "SuggestCache",
    "SuggestRequest",
    "SuggestService",
    "Suggestion",
    "build_ngrams_for_library",
]
