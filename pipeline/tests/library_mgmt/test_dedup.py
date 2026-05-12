"""Story 9.4 — content-hash dedup decisions."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.library_mgmt.dedup import (
    DedupOutcome,
    ExistingVideo,
    PathOutOfRootError,
    decide,
    is_path_in_roots,
)


@pytest.mark.unit
def test_is_path_in_roots_handles_prefix_boundary() -> None:
    assert is_path_in_roots("/mnt/media/x.mp4", ["/mnt/media"])
    # The trap: ``/mnt/media2`` starts with ``/mnt/media`` as a string but
    # is a different directory.
    assert not is_path_in_roots("/mnt/media2/x.mp4", ["/mnt/media"])
    assert not is_path_in_roots("/mnt/other/x.mp4", ["/mnt/media"])


@pytest.mark.unit
def test_decide_new_when_no_existing(tmp_path: Path) -> None:
    f = tmp_path / "v.mp4"
    f.write_bytes(b"x")
    decision = decide(
        candidate_path=str(f),
        candidate_hash="hash-1",
        library_id="lib-1",
        roots_canonical=[str(tmp_path)],
        existing=None,
    )
    assert decision.outcome is DedupOutcome.NEW
    assert decision.existing is None


@pytest.mark.unit
def test_decide_moved_when_existing_path_gone(tmp_path: Path) -> None:
    new = tmp_path / "v.mp4"
    new.write_bytes(b"x")
    existing = ExistingVideo(video_id="vid-1", path=str(tmp_path / "old.mp4"), library_id="lib-1")
    decision = decide(
        candidate_path=str(new),
        candidate_hash="hash-1",
        library_id="lib-1",
        roots_canonical=[str(tmp_path)],
        existing=existing,
        other_path_exists=False,
    )
    assert decision.outcome is DedupOutcome.MOVED
    assert decision.existing is existing


@pytest.mark.unit
def test_decide_duplicate_when_both_paths_exist(tmp_path: Path) -> None:
    new = tmp_path / "v.mp4"
    new.write_bytes(b"x")
    existing = ExistingVideo(video_id="vid-1", path=str(tmp_path / "old.mp4"), library_id="lib-1")
    decision = decide(
        candidate_path=str(new),
        candidate_hash="hash-1",
        library_id="lib-1",
        roots_canonical=[str(tmp_path)],
        existing=existing,
        other_path_exists=True,
    )
    assert decision.outcome is DedupOutcome.DUPLICATE


@pytest.mark.unit
def test_decide_rejects_path_outside_roots(tmp_path: Path) -> None:
    other = tmp_path / "outside.mp4"
    other.write_bytes(b"x")
    safe_root = tmp_path / "safe"
    safe_root.mkdir()
    with pytest.raises(PathOutOfRootError):
        decide(
            candidate_path=str(other),
            candidate_hash="h",
            library_id="lib",
            roots_canonical=[str(safe_root)],
            existing=None,
        )


@pytest.mark.unit
def test_decide_requires_non_empty_hash(tmp_path: Path) -> None:
    f = tmp_path / "v.mp4"
    f.write_bytes(b"x")
    with pytest.raises(ValueError):
        decide(
            candidate_path=str(f),
            candidate_hash="",
            library_id="lib",
            roots_canonical=[str(tmp_path)],
            existing=None,
        )
