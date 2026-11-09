"""Finding a video's file as the file system names it, and the stand-in hash marker.

Shared by the stages that open a video's file by its stored path (``content_hash`` in
``tv.maktaba.core-media``, ``transcribe``, ``subtitle_gen``) and by the code that reads the
marker (``thumb_small``, the mediaintel import). It lived in the ``content_hash`` stage's
module until that stage moved into its first-party plugin (Epic 54 story 54.36): a plugin
imports the pipeline's library modules, never another plugin.

The cache stores names composed (NFC, as Windows wrote them); the share may hold a name
decomposed (NFD: ``أ`` as ``ا`` + U+0654), and SMB matches names byte for byte.
"""

from __future__ import annotations

import os
import unicodedata
from typing import Any

__all__ = ["PENDING", "READ_TIMEOUT_S", "is_pending", "on_disk"]

#: ``videos.metadata`` key: the row's ``content_hash`` is a stand-in until ``content_hash``
#: runs (a video the mediaintel import made before its file was read)
PENDING = "hash_pending"

#: longest a name lookup or the head + tail read may take. Some names (``⧸``, ``｜``) make
#: macOS's SMB client hang instead of failing; without a limit the job holds a nas_read
#: slot for good while its heartbeat keeps the reaper away.
READ_TIMEOUT_S = 120.0


def is_pending(metadata: dict[str, Any] | None) -> bool:
    return bool((metadata or {}).get(PENDING))


def on_disk(path: str) -> str | None:
    """``path`` as the file system names it: as given, else its NFD / NFC form, else the
    entry of its folder whose normalised name is the same; None when the folder lists no
    such entry. A folder that cannot be listed (the share stalled) raises ``OSError``:
    that is not a missing file."""
    if os.path.exists(path):
        return path
    parent, name = os.path.split(path)
    for form in ("NFD", "NFC"):
        alt = os.path.join(parent, unicodedata.normalize(form, name))
        if os.path.exists(alt):
            return alt
    entries = os.listdir(parent)
    want = unicodedata.normalize("NFC", name)
    return next(
        (os.path.join(parent, e) for e in entries if unicodedata.normalize("NFC", e) == want),
        None,
    )
