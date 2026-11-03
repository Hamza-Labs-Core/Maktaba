"""A transcribe job on a machine whose path map does not cover the video's folder fails
saying so (the home's path, looked up as it is on a Windows worker), not as a plain
missing file. On a database-free event loop: the integration tier."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.engine.context import StageFailure
from maktaba_pipeline.engine.stages import transcribe as tr

pytestmark = pytest.mark.integration


@pytest.mark.asyncio
async def test_a_path_the_map_does_not_cover_fails_saying_so(tmp_path: Any) -> None:
    home_path = "/Volumes/Elsewhere/Lectures/a.mp4"  # no entry maps it on this machine
    with pytest.raises(StageFailure) as err:
        await tr._on_share(home_path, home_path)
    assert err.value.kind in {"nas_unavailable", "file_missing"}
    assert "path map has no entry" in err.value.message
    # a mapped path that is not there: missing, and nothing about the map
    mapped = str(tmp_path / "a.mp4")
    with pytest.raises(StageFailure) as err:
        await tr._on_share(mapped, home_path)
    assert err.value.kind == "file_missing" and "path map" not in err.value.message
