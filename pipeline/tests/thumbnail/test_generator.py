"""THUMBNAIL generator — FFmpeg argv + artifact extraction (Story 7.7).

The ffmpeg subprocess is mocked via the ``runner`` DI seam: the fake
records the argv it was handed and writes a stub file at the output path
(argv's last element), so we can assert both the exact command line and
that each artifact landed on disk without spawning ffmpeg.
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.thumbnail.generator import (
    ThumbnailConfig,
    ThumbnailError,
    build_poster_args,
    build_sprite_args,
    generate_thumbnails,
    thumbnail_dir_for,
)

# NOT ``unit``-marked: the ``generate_*`` tests drive ``asyncio.run``,
# which opens the event-loop self-pipe the unit-tier netguard forbids
# (the same caveat documented on every async DB handler test).


class _FakeFFmpeg:
    """Records argv and writes a stub output file (argv[-1])."""

    def __init__(self, *, fail: bool = False, fail_first: int = 0) -> None:
        self.calls: list[list[str]] = []
        self.fail = fail
        self.fail_first = fail_first

    async def __call__(self, args: list[str]) -> None:
        self.calls.append(list(args))
        if self.fail or len(self.calls) <= self.fail_first:
            raise ThumbnailError("ffmpeg_thumbnail", returncode=1, stderr_tail="boom")
        out = Path(args[-1])
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(b"\xff\xd8\xff")  # JPEG SOI marker — enough to look real


def _run(coro: Any) -> Any:
    return asyncio.run(coro)


def test_build_poster_args_seeks_before_input() -> None:
    args = build_poster_args("/m/movie.mkv", 90.0, "/out/poster.jpg", width=640)
    # -ss must precede -i for fast input seek.
    assert args.index("-ss") < args.index("-i")
    assert args[args.index("-ss") + 1] == "90.000"
    assert "-frames:v" in args and args[args.index("-frames:v") + 1] == "1"
    assert "scale=640:-2" in args
    assert args[-1] == "/out/poster.jpg"


def test_build_sprite_args_tiles_grid_across_duration() -> None:
    args = build_sprite_args(
        "/m/movie.mkv", "/out/sprite.jpg", duration_sec=100.0, columns=5, rows=5, tile_width=160
    )
    vf = args[args.index("-vf") + 1]
    # 25 tiles over 100 s → one frame every 4 s → fps 0.25.
    assert "fps=0.250000" in vf
    assert "scale=160:-2" in vf
    assert "tile=5x5" in vf
    # Exactly one packed sheet is emitted.
    assert args[args.index("-frames:v") + 1] == "1"


def test_build_sprite_args_decodes_keyframes_only_single_threaded() -> None:
    """The sheet is built from keyframes (``-skip_frame nokey`` must precede ``-i``):
    a full decode of the library would take days on the share."""
    args = build_sprite_args("/m/movie.mkv", "/out/sprite.jpg", duration_sec=3600.0)
    i = args.index("-i")
    assert args[args.index("-skip_frame") + 1] == "nokey"
    assert args.index("-skip_frame") < i
    assert args[args.index("-threads") + 1] == "1" and args.index("-threads") < i
    assert args[i + 1] == "/m/movie.mkv"


def test_build_sprite_args_zero_duration_falls_back_to_1fps() -> None:
    args = build_sprite_args("/m/x.mkv", "/o/s.jpg", duration_sec=0.0)
    vf = args[args.index("-vf") + 1]
    assert "fps=1.000000" in vf


def test_generate_thumbnails_writes_poster_sprite_and_chapters(tmp_path: Path) -> None:
    fake = _FakeFFmpeg()
    result = _run(
        generate_thumbnails(
            "/m/movie.mkv",
            duration_sec=400.0,
            content_hash="deadbeef",
            chapters=[(0, 0.0), (1, 120.0), (2, 300.0)],
            root=tmp_path,
            runner=fake,
        )
    )
    # poster + sprite + 3 chapter thumbs = 5 ffmpeg invocations.
    assert len(fake.calls) == 5
    assert result.poster.exists() and result.poster.name == "poster.jpg"
    assert result.sprite.exists() and result.sprite.name == "sprite.jpg"
    assert set(result.chapter_thumbs) == {0, 1, 2}
    for seq, p in result.chapter_thumbs.items():
        assert p.exists() and p.name == f"chapter-{seq}.jpg"
    # Everything landed under the content-addressed dir.
    expected_dir = thumbnail_dir_for("deadbeef", root=tmp_path)
    assert result.poster.parent == expected_dir


def test_generate_thumbnails_poster_seeks_at_25pct(tmp_path: Path) -> None:
    fake = _FakeFFmpeg()
    _run(
        generate_thumbnails(
            "/m/movie.mkv",
            duration_sec=400.0,
            content_hash="h",
            root=tmp_path,
            runner=fake,
        )
    )
    poster_call = fake.calls[0]
    # 25 % of 400 s = 100 s.
    assert poster_call[poster_call.index("-ss") + 1] == "100.000"


def test_generate_thumbnails_unknown_duration_uses_fallback_seek(tmp_path: Path) -> None:
    fake = _FakeFFmpeg()
    cfg = ThumbnailConfig(fallback_seek_sec=2.0)
    _run(
        generate_thumbnails(
            "/m/x.mkv", duration_sec=0.0, content_hash="h", cfg=cfg, root=tmp_path, runner=fake
        )
    )
    poster_call = fake.calls[0]
    assert poster_call[poster_call.index("-ss") + 1] == "2.000"


def test_generate_thumbnails_no_chapters_skips_chapter_pass(tmp_path: Path) -> None:
    fake = _FakeFFmpeg()
    result = _run(
        generate_thumbnails(
            "/m/x.mkv", duration_sec=100.0, content_hash="h", root=tmp_path, runner=fake
        )
    )
    assert result.chapter_thumbs == {}
    assert len(fake.calls) == 2  # poster + sprite only


def test_generate_thumbnails_propagates_ffmpeg_failure(tmp_path: Path) -> None:
    fake = _FakeFFmpeg(fail=True)
    with pytest.raises(ThumbnailError) as ei:
        _run(
            generate_thumbnails(
                "/m/x.mkv", duration_sec=100.0, content_hash="h", root=tmp_path, runner=fake
            )
        )
    assert ei.value.kind == "ffmpeg_thumbnail"


def test_poster_tries_earlier_points_when_the_seek_finds_no_frame(tmp_path: Path) -> None:
    """A video track shorter than the container (seen: 15 s of VP9 in a 3:51 file) or a
    tiny clip: no frame at 25 %, so 1 s, then the start."""
    from maktaba_pipeline.thumbnail.generator import generate_poster

    fake = _FakeFFmpeg(fail_first=2)
    out = _run(
        generate_poster(
            "/m/x.mp4", duration_sec=231.5, content_hash="h", root=tmp_path, runner=fake
        )
    )
    assert out.exists()
    seeks = [c[c.index("-ss") + 1] for c in fake.calls]
    assert seeks == ["57.875", "1.000", "0.000"]


def test_poster_failing_everywhere_still_fails(tmp_path: Path) -> None:
    from maktaba_pipeline.thumbnail.generator import generate_poster

    fake = _FakeFFmpeg(fail=True)
    with pytest.raises(ThumbnailError):
        _run(
            generate_poster(
                "/m/x.mp4", duration_sec=100.0, content_hash="h", root=tmp_path, runner=fake
            )
        )
    assert len(fake.calls) == 3


def test_sprite_decodes_every_frame_when_keyframes_give_none(tmp_path: Path) -> None:
    """A 10-second clip has fewer keyframes than the 5x5 grid: the keyframes-only run
    encodes nothing, the second decodes every frame."""
    from maktaba_pipeline.thumbnail.generator import generate_sprite

    fake = _FakeFFmpeg(fail_first=1)
    out = _run(
        generate_sprite("/m/x.mp4", duration_sec=9.8, content_hash="h", root=tmp_path, runner=fake)
    )
    assert out.exists()
    assert "-skip_frame" in fake.calls[0] and "-skip_frame" not in fake.calls[1]


def test_sprite_writes_its_index_beside_it(tmp_path: Path) -> None:
    """Story 45.19: the player's scrub preview reads the grid and the seconds between
    two tiles from ``sprite.json`` (served by the API)."""
    import json

    from maktaba_pipeline.thumbnail.generator import SPRITE_INDEX_VERSION, generate_sprite

    fake = _FakeFFmpeg()
    out = _run(
        generate_sprite(
            "/m/x.mp4", duration_sec=3600.0, content_hash="h", root=tmp_path, runner=fake
        )
    )
    vf = fake.calls[0][fake.calls[0].index("-vf") + 1]
    assert "tile=10x10" in vf and "fps=0.027778" in vf
    index = json.loads(out.with_suffix(".json").read_text(encoding="utf-8"))
    assert index == {
        "version": SPRITE_INDEX_VERSION,
        "columns": 10,
        "rows": 10,
        "count": 100,
        "interval_sec": 36.0,
        "tile_width": 160,
        "duration_sec": 3600.0,
    }
    assert not list(tmp_path.rglob("*.tmp"))


def test_sprite_index_of_an_unknown_duration_is_one_a_second() -> None:
    from maktaba_pipeline.thumbnail.generator import sprite_index

    cfg = ThumbnailConfig(sprite_columns=5, sprite_rows=5)
    assert sprite_index(0.0, cfg)["interval_sec"] == 1.0
    assert sprite_index(100.0, cfg)["count"] == 25


def test_a_failed_sprite_leaves_no_index(tmp_path: Path) -> None:
    from maktaba_pipeline.thumbnail.generator import generate_sprite

    fake = _FakeFFmpeg(fail=True)
    with pytest.raises(ThumbnailError):
        _run(
            generate_sprite(
                "/m/x.mp4", duration_sec=60.0, content_hash="h", root=tmp_path, runner=fake
            )
        )
    assert not list(tmp_path.rglob("sprite.json"))
