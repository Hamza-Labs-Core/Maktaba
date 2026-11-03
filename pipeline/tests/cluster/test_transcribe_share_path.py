"""A transcribe job on a machine whose path map does not cover the video's folder fails
saying so (the home's path, looked up as it is on a Windows worker), not as a plain
missing file. On a database-free event loop: the integration tier."""

from __future__ import annotations

import sys
from typing import Any

import pytest

from maktaba_pipeline.engine.context import StageFailure
from maktaba_pipeline.engine.stages import transcribe as tr

pytestmark = pytest.mark.integration


def test_only_another_system_s_path_left_as_it_was_is_unmapped() -> None:
    home = "/Volumes/Elsewhere/Lectures/a.mp4"
    assert tr.unmapped_here(home, home, windows=True)  # the PC, no entry for it
    assert not tr.unmapped_here(
        "O:/Lectures/a.mp4", "/Volumes/Originals/Lectures/a.mp4", windows=True
    )
    assert not tr.unmapped_here(home, home, windows=False)  # the Mac home's own path
    assert tr.unmapped_here("Y:/Lectures/a.mp4", "Y:/Lectures/a.mp4", windows=False)
    assert tr.unmapped_here("//nas/share/a.mp4", "//nas/share/a.mp4", windows=False)
    assert not tr.unmapped_here("//nas/share/a.mp4", "//nas/share/a.mp4", windows=True)


@pytest.mark.asyncio
async def test_a_path_the_map_does_not_cover_fails_saying_so(tmp_path: Any) -> None:
    # another system's path that no entry maps on this machine: the Mac home's on Windows
    # (the PC), a Windows home's drive letter elsewhere
    home_path = (
        "/Volumes/Elsewhere/Lectures/a.mp4" if sys.platform == "win32" else "Y:/Lectures/a.mp4"
    )
    with pytest.raises(StageFailure) as err:
        await tr._on_share(home_path, home_path)
    assert err.value.kind in {"nas_unavailable", "file_missing"}
    assert "path map has no entry" in err.value.message
    # a mapped path that is not there: missing, and nothing about the map
    mapped = str(tmp_path / "a.mp4")
    with pytest.raises(StageFailure) as err:
        await tr._on_share(mapped, home_path)
    assert err.value.kind == "file_missing" and "path map" not in err.value.message
