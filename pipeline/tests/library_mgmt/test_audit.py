"""Story 9.17 — append-only audit log helper."""

from __future__ import annotations

import json

import pytest

from maktaba_pipeline.library_mgmt.audit import (
    AUDIT_PAYLOAD_MAX_BYTES,
    AuditEvent,
    AuditWriter,
    LibraryAuditEvent,
    encode_payload,
    write_event,
)


@pytest.mark.unit
def test_encode_payload_round_trips_small_dict() -> None:
    p = {"by_user": "u-1", "library": "lib-1"}
    encoded = encode_payload(p)
    assert json.loads(encoded) == p


@pytest.mark.unit
def test_encode_payload_truncates_large_blob() -> None:
    huge = {"k": "x" * (AUDIT_PAYLOAD_MAX_BYTES * 2)}
    encoded = encode_payload(huge)
    parsed = json.loads(encoded)
    assert parsed.get("_truncated") is True
    assert "k" in parsed["keys"]


@pytest.mark.asyncio
async def test_audit_writer_rejects_non_library_category() -> None:
    async def insert(event: AuditEvent, payload: str) -> None:
        pass

    writer = AuditWriter(insert)
    bad = AuditEvent(category="security", event=LibraryAuditEvent.SCAN_TRIGGERED, payload={})
    with pytest.raises(ValueError):
        await writer.write(bad)


@pytest.mark.asyncio
async def test_audit_writer_rejects_unknown_event() -> None:
    async def insert(event: AuditEvent, payload: str) -> None:
        pass

    writer = AuditWriter(insert)
    bad = AuditEvent(category="library", event="not-a-real-event", payload={})
    with pytest.raises(ValueError):
        await writer.write(bad)


@pytest.mark.asyncio
async def test_write_event_dispatches_to_insert_fn() -> None:
    captured: list[tuple[AuditEvent, str]] = []

    async def insert(event: AuditEvent, payload: str) -> None:
        captured.append((event, payload))

    writer = AuditWriter(insert)
    await write_event(
        writer,
        LibraryAuditEvent.LIBRARY_DELETED,
        {"by_user": "u-1"},
        library_id="lib-1",
    )
    assert len(captured) == 1
    event, payload = captured[0]
    assert event.event == LibraryAuditEvent.LIBRARY_DELETED
    assert event.library_id == "lib-1"
    assert json.loads(payload) == {"by_user": "u-1"}


@pytest.mark.asyncio
async def test_audit_writer_swallows_db_errors() -> None:
    # AC EC: audit writes are best-effort; a DB outage must not block
    # the calling action.
    seen: list[Exception] = []

    async def angry_insert(event: AuditEvent, payload: str) -> None:
        raise RuntimeError("db down")

    writer = AuditWriter(angry_insert, on_error=seen.append)
    await write_event(writer, LibraryAuditEvent.SCAN_TRIGGERED, {}, library_id="lib-1")
    assert len(seen) == 1
    assert isinstance(seen[0], RuntimeError)
