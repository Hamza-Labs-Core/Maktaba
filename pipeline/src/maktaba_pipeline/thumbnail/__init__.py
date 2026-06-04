"""THUMBNAIL stage (Story 7.7 / Epic 8 static assets).

Two surfaces:

- :mod:`maktaba_pipeline.thumbnail.generator` — the pure FFmpeg layer
  that extracts a poster image, a sprite sheet for the scrubber, and one
  thumbnail per chapter start.
- :mod:`maktaba_pipeline.thumbnail.handler` — the runtime stage adapter
  that resolves the job's prerequisites, drives the generator, persists
  ``videos.poster_path`` / ``sprite_path``, and advances the FSM
  ``INDEXED -> THUMBNAILED``.
"""

from __future__ import annotations

from .generator import (
    DEFAULT_CONFIG,
    ThumbnailConfig,
    ThumbnailError,
    ThumbnailSet,
    build_poster_args,
    build_sprite_args,
    generate_thumbnails,
    thumbnail_dir_for,
)
from .handler import commit_thumbnails, thumbnail_handler

__all__ = [
    "DEFAULT_CONFIG",
    "ThumbnailConfig",
    "ThumbnailError",
    "ThumbnailSet",
    "build_poster_args",
    "build_sprite_args",
    "commit_thumbnails",
    "generate_thumbnails",
    "thumbnail_dir_for",
    "thumbnail_handler",
]
