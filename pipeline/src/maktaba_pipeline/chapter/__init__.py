"""Chapter inference — Epic 5 Story 5.7.

Splits a transcript into chapters using cosine-distance shifts
between adjacent unit-embedding centroids, with a minimum-length
post-pass to suppress spurious boundaries.
"""

from __future__ import annotations

from .boundary import detect_boundaries
from .inferer import ChapterInferer, ChapterRow
from .merge import enforce_min_chapter_sec
from .repo import save_chapters

__all__ = [
    "ChapterInferer",
    "ChapterRow",
    "detect_boundaries",
    "enforce_min_chapter_sec",
    "save_chapters",
]
