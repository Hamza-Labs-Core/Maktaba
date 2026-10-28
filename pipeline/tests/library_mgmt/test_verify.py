"""Story 47.3: the verification proves a new file plays before it replaces anything.

Real ffmpeg on media made per test (``ffmpeg -f lavfi``): a good remux and a re-encode
pass; a truncated file, a corrupt middle and an MP4 without faststart fail, each with its
reason in ``reasons`` and what was measured in ``measured``.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.engine import media
from maktaba_pipeline.engine.media import ProcResult
from maktaba_pipeline.library_mgmt.swap import SwapVerifyFailed, swap_in
from maktaba_pipeline.library_mgmt.verify import (
    VerifyResult,
    count_frames,
    duration_tolerance,
    moov_before_mdat,
    probe,
    verify_media,
)
from maktaba_pipeline.runtime import Database
from tests.library_mgmt import media_fixtures as mf
from tests.library_mgmt.epic47_world import make_world, same

pytestmark = [pytest.mark.integration, mf.needs_ffmpeg]


@pytest.fixture(scope="module")
def originals(tmp_path_factory: pytest.TempPathFactory) -> dict[str, Path]:
    d = tmp_path_factory.mktemp("verify-media")
    mkv = mf.mkv_h264_aac(d / "lecture.mkv")
    return {
        "mkv": mkv,
        "remux": mf.remux_to_mp4(mkv, d / "remux.mp4"),
        "reencode": mf.reencode_to_mp4(mkv, d / "reencode.mp4"),
        "moov_end": mf.mp4_moov_at_end(d / "moov_end.mp4"),
        "good_mp4": mf.mp4_faststart(d / "good.mp4"),
    }


@pytest.mark.asyncio
async def test_a_good_remux_passes(originals: dict[str, Path]) -> None:
    r = await verify_media(str(originals["remux"]), str(originals["mkv"]), mode="remux")
    assert r.ok, r.reasons
    m = r.measured
    assert m["faststart"] is True
    assert m["decode_errors"] is None
    assert m["frames"] == m["original_frames"] == 100
    assert m["new"]["video_codec"] == "h264" and m["new"]["audio_streams"] == 1
    assert abs(m["duration_delta"]) <= m["duration_tolerance"]
    assert r.as_json()["ok"] is True


@pytest.mark.asyncio
async def test_a_good_reencode_passes(originals: dict[str, Path]) -> None:
    r = await verify_media(str(originals["reencode"]), str(originals["mkv"]), mode="reencode")
    assert r.ok, r.reasons


@pytest.mark.asyncio
async def test_a_good_mp4_passes_on_its_own(originals: dict[str, Path]) -> None:
    # a download: nothing to compare with
    r = await verify_media(str(originals["good_mp4"]), None)
    assert r.ok, r.reasons
    assert "original" not in r.measured


@pytest.mark.asyncio
async def test_a_truncated_file_fails(originals: dict[str, Path], tmp_path: Path) -> None:
    bad = mf.truncated(originals["remux"], tmp_path / "truncated.mp4")
    r = await verify_media(str(bad), str(originals["mkv"]), mode="remux")
    assert not r.ok
    assert r.reasons


@pytest.mark.asyncio
async def test_a_truncated_mkv_fails(originals: dict[str, Path], tmp_path: Path) -> None:
    bad = mf.truncated(originals["mkv"], tmp_path / "truncated.mkv")
    r = await verify_media(str(bad), str(originals["mkv"]), mode="remux")
    assert not r.ok
    # it still opens: the duration, the frame count or the decode gives it away
    assert any(k in " ".join(r.reasons) for k in ("duration", "frames", "decode errors")), r.reasons


@pytest.mark.asyncio
async def test_a_corrupt_middle_fails_the_full_decode(
    originals: dict[str, Path], tmp_path: Path
) -> None:
    bad = mf.corrupt_middle(originals["remux"], tmp_path / "corrupt.mp4")
    # ffprobe still reads it: the header and duration are intact
    info = await probe(str(bad))
    assert info is not None and info.duration is not None
    r = await verify_media(str(bad), str(originals["mkv"]), mode="remux")
    assert not r.ok
    assert r.measured["decode_errors"], r.measured
    assert any(x.startswith("decode errors") for x in r.reasons)


@pytest.mark.asyncio
async def test_an_mp4_without_faststart_fails(originals: dict[str, Path]) -> None:
    assert moov_before_mdat(str(originals["moov_end"])) is False
    assert moov_before_mdat(str(originals["good_mp4"])) is True
    assert moov_before_mdat(str(originals["mkv"])) is None
    r = await verify_media(str(originals["moov_end"]), None)
    assert not r.ok
    assert r.measured["faststart"] is False


@pytest.mark.asyncio
async def test_a_missing_audio_stream_and_a_size_change_fail(
    originals: dict[str, Path], tmp_path: Path
) -> None:
    out = tmp_path / "noaudio.mp4"
    mf._ff(
        "-i",
        str(originals["mkv"]),
        "-an",
        "-vf",
        "scale=160:120",
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-movflags",
        "+faststart",
        str(out),
    )
    r = await verify_media(str(out), str(originals["mkv"]), mode="reencode")
    assert not r.ok
    joined = " ".join(r.reasons)
    assert "audio streams" in joined and "160x120" in joined


@pytest.mark.asyncio
async def test_a_missing_or_empty_file_fails(tmp_path: Path) -> None:
    empty = tmp_path / "empty.mp4"
    empty.write_bytes(b"")
    assert not (await verify_media(str(empty), None)).ok
    assert not (await verify_media(str(tmp_path / "nope.mp4"), None)).ok


@pytest.mark.asyncio
async def test_frames_are_counted(originals: dict[str, Path]) -> None:
    assert await count_frames(str(originals["mkv"])) == 100


def test_the_duration_tolerance_is_half_a_second_or_half_a_percent() -> None:
    assert duration_tolerance(10.0) == 0.5
    assert duration_tolerance(3600.0) == 18.0


@pytest.mark.asyncio
async def test_every_subprocess_goes_through_the_runner_seam(
    originals: dict[str, Path],
) -> None:
    seen: list[list[str]] = []
    real = media._runner

    async def spy(args: list[str], timeout: float) -> ProcResult:
        seen.append(args)
        return await real(args, timeout)

    media.set_runner(spy)
    try:
        r = await verify_media(str(originals["remux"]), str(originals["mkv"]), mode="remux")
    finally:
        media.reset_runner()
    assert r.ok
    decode = [a for a in seen if "-xerror" in a]
    assert len(decode) == 1
    assert decode[0][-7:] == ["-map", "0:v:0", "-map", "0:a?", "-f", "null", "-"]
    assert any("-count_packets" in a for a in seen)


# ---------------------------------------------------------------- with the swap


@pytest.mark.asyncio
async def test_a_real_remux_is_verified_on_the_share_and_swapped_in(
    db: Database, tmp_path: Path
) -> None:
    w = await make_world(db, tmp_path)
    mkv = mf.mkv_h264_aac(tmp_path / "talk.mkv")
    vid, orig = await w.video("talk.mkv", mkv.read_bytes())
    work = tmp_path / "work"
    work.mkdir()
    good = mf.remux_to_mp4(orig, work / "talk.mp4")

    async def check(staged: str) -> VerifyResult:
        return await verify_media(staged, str(orig), mode="remux")

    res = await swap_in(
        db,
        library_id=w.library_id,
        kind="convert",
        source=str(good),
        final_path=str(w.root / "talk.mp4"),
        verify=check,
        video_id=vid,
        to_local=same,
    )
    assert res.action.state == "done"
    assert res.action.verify is not None and res.action.verify["ok"] is True
    assert res.action.verify["measured"]["frames"] == 100
    p = await w.assert_tracked(vid)
    assert p.name == "talk.mp4" and (await probe(str(p))) is not None


@pytest.mark.asyncio
async def test_a_corrupt_conversion_never_replaces_the_original(
    db: Database, tmp_path: Path
) -> None:
    w = await make_world(db, tmp_path)
    mkv = mf.mkv_h264_aac(tmp_path / "talk.mkv", seconds=3.0)
    vid, orig = await w.video("talk.mkv", mkv.read_bytes())
    work = tmp_path / "work"
    work.mkdir()
    bad = mf.corrupt_middle(mf.remux_to_mp4(orig, work / "ok.mp4"), work / "talk.mp4")

    async def check(staged: str) -> VerifyResult:
        return await verify_media(staged, str(orig), mode="remux")

    with pytest.raises(SwapVerifyFailed) as err:
        await swap_in(
            db,
            library_id=w.library_id,
            kind="convert",
            source=str(bad),
            final_path=str(w.root / "talk.mp4"),
            verify=check,
            video_id=vid,
            to_local=same,
        )
    a = err.value.action
    assert a is not None and a.state == "failed"
    assert a.verify is not None and a.verify["measured"]["decode_errors"]
    p = await w.assert_tracked(vid)
    assert p == orig and not (w.root / "talk.mp4").exists() and not w.staged_files()
