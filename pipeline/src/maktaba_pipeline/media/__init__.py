"""Media side-car artefacts: subtitles, thumbnails, and embedded extraction.

This package owns the on-disk shape of sidecars (the canonical files
under ``<library>/.maktaba/subs/`` and the host-facing aliases beside
each video). DB tables that index these artefacts (``subtitle_files``)
are written by stage handlers, not by helpers in this tree.
"""

from __future__ import annotations
