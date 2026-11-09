"""A video's own thumbnail on the share, and what the thumbnail stages fingerprint.

``thumb_hash`` (``tv.maktaba.core-media``) and ``thumb_small`` (``tv.maktaba.thumbnails``)
pick the same source: the yt-dlp sidecar (``<stem>.webp / .jpg / .png`` next to the video,
else in the folder's ``.thumbs/``:
:func:`~maktaba_pipeline.discovery.ytdlp_sidecars.find_sidecar_thumbnail`), else the poster
Maktaba already generated. This lived in the ``thumb_hash`` stage's module until that stage
moved into its first-party plugin (Epic 54 story 54.36): a plugin imports the pipeline's
library modules, never another plugin.
"""

from __future__ import annotations

import asyncio
import os
from typing import TYPE_CHECKING, Any

from ..discovery.ytdlp_sidecars import THUMBS_DIR, find_sidecar_thumbnail
from ..engine.paths import to_local

if TYPE_CHECKING:
    from ..engine.stages._common import VideoFacts

__all__ = ["find_thumbnail", "fingerprint_doc", "thumbs_dir_source"]


def find_thumbnail(video_local_path: str, poster_path: str | None) -> str | None:
    """The video's sidecar thumbnail (next to it, else in ``.thumbs/``), else the poster,
    else None."""
    sidecar = find_sidecar_thumbnail(video_local_path)
    if sidecar is not None:
        return sidecar
    if poster_path and os.path.isfile(poster_path):
        return poster_path
    return None


def _thumbs_dir_name(video_local_path: str) -> str | None:
    found = find_sidecar_thumbnail(video_local_path)
    if found is None or os.path.basename(os.path.dirname(found)) != THUMBS_DIR:
        return None
    return os.path.basename(found)


async def thumbs_dir_source(v: VideoFacts) -> str | None:
    """The name of the video's thumbnail when it is found in the folder's ``.thumbs/``
    (none next to the video), else None. A few stats, off the event loop."""
    return await asyncio.to_thread(lambda: _thumbs_dir_name(to_local(v.path)))


async def fingerprint_doc(v: VideoFacts, version: int) -> dict[str, Any]:
    """What ``thumb_hash`` and ``thumb_small`` fingerprint: the file's identity, the
    poster they could fall back to, and a thumbnail found in ``.thumbs/``.

    Only a ``.thumbs/`` thumbnail is named. Until 2026-11 the stages looked next to the
    video only, so such a video was answered from the poster; its fingerprint now
    changes and its next job runs. Every other video's fingerprint stays exactly what it
    was, so none of the ~230k done jobs runs again for this change.
    """
    doc: dict[str, Any] = {
        "size": v.size_bytes, "mtime": v.mtime, "poster": v.poster_path, "v": version
    }  # fmt: skip
    thumbs = await thumbs_dir_source(v)
    if thumbs is not None:
        doc["thumbs"] = thumbs
    return doc
