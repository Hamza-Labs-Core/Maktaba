"""Story 9.16 — root canonicalisation and overlap detection."""

from __future__ import annotations

import os

import pytest

from maktaba_pipeline.library_mgmt.roots import (
    canonicalise,
    detect_runtime_overlap,
    find_overlap,
    find_self_overlap,
    paths_overlap,
)


@pytest.mark.unit
def test_canonicalise_strips_trailing_slash(tmp_path) -> None:
    p = tmp_path / "media"
    p.mkdir()
    canon = canonicalise(str(p) + "/")
    assert not canon.endswith(os.sep)
    assert canon == str(p)


@pytest.mark.unit
def test_canonicalise_normalises_dotdot(tmp_path) -> None:
    a = tmp_path / "a"
    a.mkdir()
    spelled = str(a / ".." / "a")
    assert canonicalise(spelled) == str(a)


@pytest.mark.unit
def test_canonicalise_resolves_symlink(tmp_path) -> None:
    real = tmp_path / "real"
    real.mkdir()
    link = tmp_path / "link"
    link.symlink_to(real)
    assert canonicalise(str(link)) == canonicalise(str(real))


@pytest.mark.unit
def test_paths_overlap_handles_prefix_both_ways() -> None:
    assert paths_overlap("/mnt/media", "/mnt/media")
    assert paths_overlap("/mnt/media", "/mnt/media/sub")
    assert paths_overlap("/mnt/media/sub", "/mnt/media")
    assert not paths_overlap("/mnt/media", "/mnt/media2")
    assert not paths_overlap("/mnt/a", "/mnt/b")


@pytest.mark.unit
def test_find_self_overlap_rejects_nested_in_one_library() -> None:
    overlap = find_self_overlap(["/a", "/a/b"])
    assert overlap is not None
    assert "/a" in (overlap.existing, overlap.proposed)


@pytest.mark.unit
def test_find_self_overlap_passes_for_disjoint_roots() -> None:
    assert find_self_overlap(["/a", "/b"]) is None


@pytest.mark.unit
def test_find_overlap_skips_same_library_id() -> None:
    existing = [("lib-1", "/mnt/media")]
    # Same library updating its own roots — no overlap reported.
    assert find_overlap("lib-1", ["/mnt/media/lectures"], existing) is None


@pytest.mark.unit
def test_find_overlap_detects_cross_library_collision() -> None:
    existing = [("lib-1", "/mnt/media")]
    overlap = find_overlap("lib-2", ["/mnt/media/lectures"], existing)
    assert overlap is not None
    assert overlap.existing_library_id == "lib-1"
    assert overlap.proposed_library_id == "lib-2"


@pytest.mark.unit
def test_detect_runtime_overlap_picks_up_remount(tmp_path) -> None:
    real = tmp_path / "shared"
    real.mkdir()
    a = tmp_path / "linkA"
    b = tmp_path / "linkB"
    a.symlink_to(real)
    b.symlink_to(real)
    declared = [
        ("lib-1", str(a), str(a)),
        ("lib-2", str(b), str(b)),
    ]
    overlaps = detect_runtime_overlap(declared)
    assert overlaps, "expected runtime overlap for symlink-into-shared-dir"
