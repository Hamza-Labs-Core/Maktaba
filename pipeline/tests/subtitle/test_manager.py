"""Epic 4 — :mod:`maktaba_pipeline.subtitle.manager` tests."""

from __future__ import annotations

import asyncio
import json
import re
from contextlib import asynccontextmanager
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.subtitle.formats import SubtitleFormat
from maktaba_pipeline.subtitle.manager import (
    SubtitleRecord,
    SubtitleSource,
    cache_path_for,
    register_subtitle,
    soft_delete_subtitle,
    write_atomic,
)

# --- write_atomic -----------------------------------------------------


def test_write_atomic_writes_bytes_and_returns_stats(tmp_path: Path) -> None:
    dest = tmp_path / "a" / "b" / "file.srt"
    size, digest = write_atomic(dest, "hello\n")
    assert dest.read_bytes() == b"hello\n"
    assert size == 6
    # sha256("hello\n") is a 64-hex string.
    assert re.fullmatch(r"[0-9a-f]{64}", digest) is not None


def test_write_atomic_replaces_existing(tmp_path: Path) -> None:
    dest = tmp_path / "f.vtt"
    write_atomic(dest, "old")
    write_atomic(dest, "new contents")
    assert dest.read_text() == "new contents"


def test_write_atomic_no_temp_left_behind(tmp_path: Path) -> None:
    dest = tmp_path / "f.srt"
    write_atomic(dest, "x")
    # Only the final file should remain — no .tmp-* sibling.
    siblings = list(tmp_path.iterdir())
    assert siblings == [dest]


@pytest.mark.unit
def test_write_atomic_delegates_to_canonical_helper(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The subtitle write path goes through the ONE canonical
    :func:`maktaba_pipeline.integrity.atomic_write_bytes` recipe.

    This pins the DRY fix for HLB-405: ``subtitle/manager.write_atomic``
    must not re-implement the atomic dance — it delegates to the shared
    helper, so that helper is no longer dead code on a real write path.
    """
    from maktaba_pipeline.integrity import atomic_write_bytes as canonical

    called: dict[str, object] = {}

    def spy(target: object, data: bytes, *a: object, **k: object) -> None:
        called["target"] = target
        called["data"] = data
        canonical(target, data, *a, **k)  # type: ignore[arg-type]

    # Patch the name as bound inside subtitle.manager — proves the
    # production wrapper actually routes through the canonical helper.
    monkeypatch.setattr("maktaba_pipeline.subtitle.manager.atomic_write_bytes", spy)
    dest = tmp_path / "v" / "generated.en.srt"
    size, digest = write_atomic(dest, "hello\n")

    assert called, "write_atomic must invoke the canonical atomic_write_bytes helper"
    assert called["data"] == b"hello\n"
    assert dest.read_bytes() == b"hello\n"
    assert size == 6


@pytest.mark.unit
def test_write_atomic_interrupted_write_leaves_no_torn_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A crash mid-write must NOT leave a partial subtitle on the target.

    Simulates the process dying while streaming bytes to the temp file:
    the canonical helper writes to an ``O_EXCL`` temp and only
    ``os.replace``s on success, so a pre-existing target is untouched
    and no half-written file is published. Proves the durability
    guarantee on the wired path (HLB-405).
    """
    from maktaba_pipeline.integrity import AtomicWriteError

    dest = tmp_path / "v" / "generated.en.srt"
    write_atomic(dest, "GOOD ORIGINAL CONTENT")  # an existing good sidecar
    original = dest.read_bytes()

    def boom(src: object, dst: object, *a: object, **k: object) -> None:
        raise OSError("simulated crash before rename")

    # Interrupt the final rename inside the canonical helper.
    monkeypatch.setattr("maktaba_pipeline.integrity.atomic.os.replace", boom)
    with pytest.raises(AtomicWriteError):  # canonical helper wraps the OSError
        write_atomic(dest, "TORN HALF WRITTEN GARBAGE")
    monkeypatch.undo()

    # Target still holds the original, fully-formed content...
    assert dest.read_bytes() == original
    # ...and no temp/partial sibling was left published as the artifact.
    survivors = sorted(p.name for p in dest.parent.iterdir())
    assert survivors == [dest.name], f"torn/temp file leaked: {survivors}"


# --- cache_path_for ---------------------------------------------------


def test_cache_path_for_groups_by_video_id(tmp_path: Path) -> None:
    vid = uuid4()
    p = cache_path_for(vid, "ara", SubtitleFormat.VTT, SubtitleSource.GENERATED, root=tmp_path)
    assert p.parent == tmp_path / str(vid)
    assert p.name == "generated.ara.vtt"


# --- register / soft_delete -------------------------------------------


class _FakeSubtitleDB:
    """Minimal in-memory DB that routes the two SQL fragments used."""

    dialect = "postgres"

    def __init__(self) -> None:
        self.rows: dict[int, dict[str, Any]] = {}
        self._next_id = 1

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    async def execute(self, sql: str, *args: Any) -> None:
        raise AssertionError(f"unexpected execute: {sql}")

    async def fetchrow(self, sql: str, *args: Any) -> dict[str, Any] | None:
        sql_norm = " ".join(sql.split())
        if sql_norm.startswith("INSERT INTO subtitle_files"):
            return self._do_upsert(args)
        if sql_norm.startswith("UPDATE subtitle_files SET deleted_at"):
            return self._do_soft_delete(args)
        raise AssertionError(f"unexpected fetchrow: {sql}")

    def _do_upsert(self, args: tuple[Any, ...]) -> dict[str, Any]:
        (
            video_id,
            transcript_id,
            language,
            fmt,
            source,
            path,
            byte_size,
            sha256,
            is_embedded,
            is_external,
            metadata,
        ) = args
        for rid, row in self.rows.items():
            if (
                row["deleted_at"] is None
                and row["video_id"] == video_id
                and row["language"] == language
                and row["format"] == fmt
                and row["source"] == source
            ):
                row.update(
                    path=path,
                    byte_size=byte_size,
                    sha256=sha256,
                    metadata=metadata,
                    transcript_id=transcript_id,
                )
                return {"id": rid}
        rid = self._next_id
        self._next_id += 1
        self.rows[rid] = {
            "id": rid,
            "video_id": video_id,
            "transcript_id": transcript_id,
            "language": language,
            "format": fmt,
            "source": source,
            "path": path,
            "byte_size": byte_size,
            "sha256": sha256,
            "is_embedded": is_embedded,
            "is_external": is_external,
            "metadata": metadata,
            "deleted_at": None,
        }
        return {"id": rid}

    def _do_soft_delete(self, args: tuple[Any, ...]) -> dict[str, Any] | None:
        video_id, language, fmt, source = args
        for row in self.rows.values():
            if (
                row["deleted_at"] is None
                and row["video_id"] == video_id
                and row["language"] == language
                and row["format"] == fmt
                and row["source"] == source
            ):
                row["deleted_at"] = "now"
                return {"path": row["path"]}
        return None


def _record(video_id: UUID, *, path: Path, source: SubtitleSource) -> SubtitleRecord:
    return SubtitleRecord(
        video_id=video_id,
        language="ara",
        format=SubtitleFormat.VTT,
        source=source,
        path=path,
        byte_size=10,
        sha256="abc",
    )


def test_register_subtitle_inserts_then_upserts() -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        vid = uuid4()
        path = Path("/tmp/s.vtt")
        rid1 = await register_subtitle(db, _record(vid, path=path, source=SubtitleSource.GENERATED))
        rid2 = await register_subtitle(
            db,
            _record(vid, path=Path("/tmp/s2.vtt"), source=SubtitleSource.GENERATED),
        )
        # Upsert path: same (video, language, format, source) returns the same id.
        assert rid1 == rid2
        row = db.rows[rid1]
        assert row["path"] == "/tmp/s2.vtt"
        # source=generated → is_embedded/is_external both false.
        assert row["is_embedded"] is False
        assert row["is_external"] is False

    asyncio.run(run())


def test_register_subtitle_serializes_metadata_as_json() -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        vid = uuid4()
        record = SubtitleRecord(
            video_id=vid,
            language="ara",
            format=SubtitleFormat.SRT,
            source=SubtitleSource.EMBEDDED,
            path=Path("/tmp/x.srt"),
            byte_size=1,
            sha256="d",
            metadata={"track": 2, "forced": True},
        )
        rid = await register_subtitle(db, record)
        stored = json.loads(db.rows[rid]["metadata"])
        assert stored == {"track": 2, "forced": True}

    asyncio.run(run())


def test_soft_delete_subtitle_tombstones_and_unlinks(tmp_path: Path) -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        vid = uuid4()
        file = tmp_path / "out.vtt"
        file.write_text("x")
        await register_subtitle(db, _record(vid, path=file, source=SubtitleSource.GENERATED))
        deleted = await soft_delete_subtitle(
            db,
            video_id=vid,
            language="ara",
            fmt=SubtitleFormat.VTT,
            source=SubtitleSource.GENERATED,
        )
        assert deleted is True
        assert not file.exists()
        # The row is tombstoned, not removed.
        only_row = next(iter(db.rows.values()))
        assert only_row["deleted_at"] is not None

    asyncio.run(run())


def test_soft_delete_returns_false_when_missing() -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        deleted = await soft_delete_subtitle(
            db,
            video_id=uuid4(),
            language="ara",
            fmt=SubtitleFormat.VTT,
            source=SubtitleSource.GENERATED,
        )
        assert deleted is False

    asyncio.run(run())


def test_soft_delete_tolerates_missing_file(tmp_path: Path) -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        vid = uuid4()
        ghost = tmp_path / "never-existed.srt"
        await register_subtitle(db, _record(vid, path=ghost, source=SubtitleSource.GENERATED))
        deleted = await soft_delete_subtitle(
            db,
            video_id=vid,
            language="ara",
            fmt=SubtitleFormat.VTT,
            source=SubtitleSource.GENERATED,
        )
        assert deleted is True

    asyncio.run(run())


@pytest.mark.parametrize(
    "source,expected_embedded,expected_external",
    [
        (SubtitleSource.EMBEDDED, True, False),
        (SubtitleSource.EXTERNAL, False, True),
        (SubtitleSource.GENERATED, False, False),
    ],
)
def test_register_sets_source_flags(
    source: SubtitleSource,
    expected_embedded: bool,
    expected_external: bool,
) -> None:
    async def run() -> None:
        db = _FakeSubtitleDB()
        rid = await register_subtitle(
            db,
            _record(uuid4(), path=Path("/tmp/x.vtt"), source=source),
        )
        row = db.rows[rid]
        assert row["is_embedded"] is expected_embedded
        assert row["is_external"] is expected_external

    asyncio.run(run())
