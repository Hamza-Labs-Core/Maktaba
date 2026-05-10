"""Story 2.3 — stream-extraction argv shape + error envelope."""

from __future__ import annotations

import asyncio

from maktaba_pipeline.audio.extract import (
    DEFAULT_CHUNK_BYTES,
    ExtractError,
    StreamHandle,
    build_ffmpeg_args,
    cache_path_for,
)


def test_argv_default_flags_match_story_2_3_ac1() -> None:
    args = build_ffmpeg_args("/tmp/in.mkv", track_index=1)
    assert args[0] == "ffmpeg"
    # Required flags.
    for flag in ("-hide_banner", "-nostdin", "-threads", "1"):
        assert flag in args
    # PCM output configuration.
    assert "-map" in args and "0:a:1" in args
    assert "-ac" in args and "1" in args[args.index("-ac") + 1 : args.index("-ac") + 2]
    assert "-ar" in args and "16000" in args[args.index("-ar") + 1 : args.index("-ar") + 2]
    sf = args.index("-sample_fmt")
    assert "s16" in args[sf + 1 : sf + 2]
    assert args[-1] == "pipe:1"
    assert "-f" in args and "s16le" in args[args.index("-f") + 1 : args.index("-f") + 2]


def test_argv_includes_input_seek_with_safety_margin() -> None:
    args = build_ffmpeg_args("/tmp/in.mkv", track_index=0, start_sec=320.5)
    assert "-ss" in args
    ss_index = args.index("-ss")
    seek_value = float(args[ss_index + 1])
    # AC: ``-ss`` placed *before* ``-i``; lead-in subtraction.
    assert seek_value < 320.5
    assert ss_index < args.index("-i")
    # Default safety margin is 0.5 s.
    assert abs(seek_value - 320.0) < 1e-3


def test_extract_error_envelope_shape_matches_ac3() -> None:
    err = ExtractError("ffmpeg_decode", returncode=183, stderr_tail="bad codec")
    env = err.to_envelope()
    assert env == {"kind": "ffmpeg_decode", "returncode": 183, "stderr_tail": "bad codec"}


def test_cache_path_uses_content_hash_under_root(tmp_path) -> None:
    p = cache_path_for("abc123", root=tmp_path)
    assert p == tmp_path / "abc123.wav"


def test_default_chunk_size_is_64KiB() -> None:
    assert DEFAULT_CHUNK_BYTES == 64 * 1024


def test_stream_handle_terminates_idempotently() -> None:
    """A terminated handle does not crash on a second call."""

    class _FakeProc:
        def __init__(self) -> None:
            self.returncode = 0

        def terminate(self) -> None:
            return

        def kill(self) -> None:
            return

        async def wait(self) -> int:
            return 0

    handle = StreamHandle(process=_FakeProc())  # type: ignore[arg-type]
    rc1 = asyncio.run(handle.terminate())
    rc2 = asyncio.run(handle.terminate())
    assert rc1 == 0 and rc2 == 0
