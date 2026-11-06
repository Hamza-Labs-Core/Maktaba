"""The offline switch (#640): ``MAKTABA_OFFLINE=1`` stops every outbound call the
pipeline makes by itself.

``maktaba-server serve --offline`` sets the variable for itself and every role, and
``shared/go/ports/outbound.go`` lists each outbound call with the switch that stops it.
While it is on:

* the stages that reach YouTube (:data:`OUTBOUND_STAGES`) are not claimed (their jobs wait,
  ``unavailable: offline``), and the reconciler queues no scheduled YouTube refresh;
* ``books`` answers from the books already looked up; for the others its fingerprint says
  offline, so its next run once the switch is off looks them up;
* enrichment does no web search;
* the dictionary is never downloaded;
* Hugging Face reads its cache only (``HF_HUB_OFFLINE``, set beside it).
"""

from __future__ import annotations

import os

__all__ = ["ENV", "OUTBOUND_STAGES", "REASON", "Offline", "check", "offline"]

ENV = "MAKTABA_OFFLINE"

#: why a stage is not claimed while offline (``workers.stages_off``)
REASON = "offline (MAKTABA_OFFLINE): no outbound call"

#: the stages whose every job reaches the internet (YouTube's listings, downloads and
#: pages): not claimed while offline
OUTBOUND_STAGES = frozenset(
    {"youtube_sync", "youtube_fetch", "youtube_backfill", "exact_dates", "repair_broken"}
)


class Offline(OSError):
    """An outbound call refused because the switch is on."""


def offline() -> bool:
    """Whether ``MAKTABA_OFFLINE`` is on (``1``, ``true``, ``on``, ``yes``), as
    ``ports.Offline`` reads it."""
    return os.environ.get(ENV, "").strip().lower() in ("1", "true", "on", "yes")


def check(what: str) -> None:
    """Raise :class:`Offline` for ``what`` (a host, a call) while offline."""
    if offline():
        raise Offline(f"{what}: {REASON}")
