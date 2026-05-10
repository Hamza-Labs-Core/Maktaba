"""Tests for :mod:`maktaba_pipeline.scanner.dryrun` (Story 1.4 AC #3)."""

from __future__ import annotations

import io
import json
from datetime import UTC, datetime
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.scanner import (
    DryRunStore,
    LibraryRecord,
    SaveCandidateParams,
)


def _params(
    library_id: UUID,
    *,
    path: str,
    content_hash: str = "h",
    size: int = 42,
) -> SaveCandidateParams:
    return SaveCandidateParams(
        library_id=library_id,
        content_hash=content_hash,
        path=path,
        filename=path.rsplit("/", 1)[-1],
        size_bytes=size,
        mtime=datetime(2026, 5, 10, 12, 0, 0, tzinfo=UTC),
        last_seen_at=datetime(2026, 5, 10, 12, 0, 0, tzinfo=UTC),
        enqueue_probe=True,
    )


@pytest.mark.asyncio
async def test_save_candidate_emits_jsonl_line() -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=("/a",))
    buf = io.StringIO()
    store = DryRunStore(library=library, writer=buf)

    res = await store.save_candidate(_params(library.id, path="/a/v.mp4", content_hash="abc"))

    assert res.inserted is True
    assert res.job_id is None
    lines = buf.getvalue().splitlines()
    assert len(lines) == 1
    payload = json.loads(lines[0])
    assert payload["action"] == "would_insert"
    assert payload["content_hash"] == "abc"
    assert payload["path"] == "/a/v.mp4"
    assert payload["filename"] == "v.mp4"
    assert payload["size_bytes"] == 42


@pytest.mark.asyncio
async def test_get_library_returns_synthetic_record() -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=("/a",))
    store = DryRunStore(library=library, writer=io.StringIO())

    got = await store.get_library(library.id)
    assert got is library

    miss = await store.get_library(uuid4())
    assert miss is None


@pytest.mark.asyncio
async def test_find_video_by_path_always_misses() -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=("/a",))
    store = DryRunStore(library=library, writer=io.StringIO())
    # Dry-run never reads existing rows — every candidate hits the
    # would_insert path.
    assert await store.find_video_by_path(library.id, "/anywhere") is None


@pytest.mark.asyncio
async def test_each_call_appends_one_jsonl_line() -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=("/a",))
    buf = io.StringIO()
    store = DryRunStore(library=library, writer=buf)

    for i in range(5):
        await store.save_candidate(_params(library.id, path=f"/a/v{i}.mp4", content_hash=f"h{i}"))

    lines = buf.getvalue().splitlines()
    assert len(lines) == 5
    hashes = [json.loads(line)["content_hash"] for line in lines]
    assert hashes == [f"h{i}" for i in range(5)]
