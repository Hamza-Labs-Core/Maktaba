"""tv.maktaba.thumbnails does exactly what its stages did in-process (Epic 54, stories 54.26,
54.29): a seeded library of real files run on two copies of one database, the wave's stages
in-process on one and through the plugin on the other (the real plugin API, its statements
and its role), each side from the same files; then every row, job, log line and file the
stages wrote compared."""

from __future__ import annotations

import os
from collections.abc import Iterator
from pathlib import Path

import pytest

from tests.plugins.firstparty.files_world import ffmpeg, prove  # noqa: F401

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
#: the stages wave 2 moved into tv.maktaba.thumbnails (story 54.29)
STAGES = ["thumb_small", "chapter_thumbs"]


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES if not want or s in want]


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.thumbnails selected")
    return pg_template


@pytest.fixture
def root(tmp_path: Path) -> Iterator[Path]:
    yield tmp_path


@pytest.mark.asyncio
@pytest.mark.usefixtures("ffmpeg")
async def test_thumbnails_wave_2_is_identical(
    template: str, root: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    await prove(
        pg_url=PG_URL,
        template=template,
        plugin_id="tv.maktaba.thumbnails",
        stages=selected(),
        keys={},
        config=HERE.parent / "parity.toml",
        root=root,
        monkeypatch=monkeypatch,
    )
