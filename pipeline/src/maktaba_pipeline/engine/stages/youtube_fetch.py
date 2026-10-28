"""``youtube_fetch`` (Epic 47, story 47.9): Download a library's new YouTube uploads.

In the graph, off, until the story ships (story 47.1).
"""

from __future__ import annotations

from ._unreleased import unreleased_impl

__all__ = ["IMPL"]

IMPL = unreleased_impl("youtube_fetch", "47.9")
