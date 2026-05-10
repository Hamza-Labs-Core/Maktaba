"""Story 9.17 — library audit log helper.

Library lifecycle events land in the canonical `audit_log` table with
``category='library'``. This module is the small Pythonic wrapper the
Pipeline workers use; the API-side equivalent lives in Go (see
``api/internal/handlers/libraries`` audit-write paths).

Event names form a closed vocabulary so the API surface (Story 9.17
AC-2) can render type-safe filter pills. Every event ships a
``payload`` dict that's serialised to JSON; ``payload`` is parameterised
(no string interpolation) so it cannot inject SQL even when the source
is user-supplied (e.g., a collection name). Length is capped at 8 KiB
per the AC-EC.

Audit writes are *best-effort* (AC EC): the caller catches and logs but
does not propagate exceptions, so a partial DB outage cannot block the
underlying action (e.g., a library deletion).
"""

from __future__ import annotations

import json
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass
from typing import Any

__all__ = [
    "AUDIT_PAYLOAD_MAX_BYTES",
    "AuditEvent",
    "AuditWriter",
    "LibraryAuditEvent",
    "encode_payload",
    "write_event",
]

#: AC EC — payload length cap.
AUDIT_PAYLOAD_MAX_BYTES: int = 8 * 1024


class LibraryAuditEvent:
    """The closed event vocabulary for ``category='library'``."""

    SCAN_TRIGGERED = "scan-triggered"
    SETTINGS_CHANGED = "settings-changed"
    VIDEO_PURGED = "video-purged"
    LIBRARY_DELETED = "library-deleted"
    SPEAKER_MERGED = "speaker-merged"
    FILE_PURGE_RESULTS = "file-purge-results"
    DUPLICATE_DETECTED = "duplicate-detected"
    RUNTIME_ROOT_OVERLAP = "runtime-root-overlap"
    PATH_OUT_OF_ROOT = "path-out-of-root"
    TOPIC_RECLUSTER = "topic-recluster"


_ALLOWED_EVENTS: frozenset[str] = frozenset(
    {
        LibraryAuditEvent.SCAN_TRIGGERED,
        LibraryAuditEvent.SETTINGS_CHANGED,
        LibraryAuditEvent.VIDEO_PURGED,
        LibraryAuditEvent.LIBRARY_DELETED,
        LibraryAuditEvent.SPEAKER_MERGED,
        LibraryAuditEvent.FILE_PURGE_RESULTS,
        LibraryAuditEvent.DUPLICATE_DETECTED,
        LibraryAuditEvent.RUNTIME_ROOT_OVERLAP,
        LibraryAuditEvent.PATH_OUT_OF_ROOT,
        LibraryAuditEvent.TOPIC_RECLUSTER,
    }
)


@dataclass(slots=True, frozen=True)
class AuditEvent:
    """One row destined for `audit_log`.

    ``actor_user_id`` is None for system-triggered events (e.g., a
    nightly recluster); the API-side writer fills it in from the
    request principal otherwise.
    """

    category: str
    event: str
    payload: Mapping[str, Any]
    actor_user_id: str | None = None
    library_id: str | None = None
    video_id: str | None = None


class AuditWriter:
    """Best-effort audit-log inserter.

    The actual SQL is injected via the ``insert_fn`` callable so this
    module stays DB-agnostic; production wires it to
    :func:`db.audit_log.insert` (psycopg).
    """

    def __init__(
        self,
        insert_fn: Callable[[AuditEvent, str], Awaitable[None]],
        *,
        on_error: Callable[[Exception], None] | None = None,
    ) -> None:
        self._insert = insert_fn
        self._on_error = on_error

    async def write(self, event: AuditEvent) -> None:
        if event.category != "library":
            raise ValueError(
                f"AuditWriter only writes category='library' events; got {event.category!r}"
            )
        if event.event not in _ALLOWED_EVENTS:
            raise ValueError(f"unknown event {event.event!r}")
        encoded = encode_payload(event.payload)
        try:
            await self._insert(event, encoded)
        except Exception as exc:  # noqa: BLE001 — best-effort by design
            if self._on_error is not None:
                self._on_error(exc)


def encode_payload(payload: Mapping[str, Any]) -> str:
    """Serialise ``payload`` to a JSON string and enforce the size cap.

    Truncating mid-key would yield invalid JSON, so when the cap is hit
    we fall back to a sentinel object describing the overflow.
    """
    text = json.dumps(payload, sort_keys=True, ensure_ascii=False)
    if len(text.encode("utf-8")) <= AUDIT_PAYLOAD_MAX_BYTES:
        return text
    return json.dumps(
        {
            "_truncated": True,
            "reason": f"payload exceeded {AUDIT_PAYLOAD_MAX_BYTES} bytes",
            "keys": sorted(payload.keys()),
        },
        sort_keys=True,
    )


async def write_event(
    writer: AuditWriter,
    event: str,
    payload: Mapping[str, Any],
    *,
    library_id: str | None = None,
    video_id: str | None = None,
    actor_user_id: str | None = None,
) -> None:
    """Convenience wrapper that builds the :class:`AuditEvent`."""
    await writer.write(
        AuditEvent(
            category="library",
            event=event,
            payload=payload,
            actor_user_id=actor_user_id,
            library_id=library_id,
            video_id=video_id,
        )
    )
