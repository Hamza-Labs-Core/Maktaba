"""Production-style classifier: ffmpeg argv, pure parsers, metrics, classifier."""

from __future__ import annotations

import functools

import pytest

from maktaba_pipeline.classify.production_style import (
    ANALYSIS_FPS,
    THUMB_FPS,
    THUMB_H,
    THUMB_W,
    FrameStat,
    StyleMetrics,
    WindowSample,
    classify_style,
    compute_metrics,
    ffmpeg_sample_args,
    metrics_from_dict,
    parse_frame_stats,
    parse_stream_info,
    sample_windows,
    split_thumbs,
)

pytestmark = pytest.mark.unit

# Captured from ffmpeg 7.1 (gyan full) running ffmpeg_sample_args() on a 2024 episode.
HEADER = """\
Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'Y:/x/ep.mp4':
  Metadata:
    major_brand     : isom
  Duration: 00:20:44.85, start: 0.000000, bitrate: 3018 kb/s
  Stream #0:0[0x1](und): Video: h264 (Main) (avc1 / 0x31637661), yuv420p(tv, bt709, progressive), 1920x1080 [SAR 1:1 DAR 16:9], 2885 kb/s, 25 fps, 25 tbr, 12800 tbn (default)
  Stream #0:1[0x2](eng): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 128 kb/s (default)
Stream mapping:
  Stream #0:0 (h264) -> fps:default
Output #0, null, to 'pipe:':
  Stream #0:0: Video: wrapped_avframe, yuv420p(tv, bt709, progressive), 160x90 [SAR 1:1 DAR 16:9], q=2-31, 200 kb/s, 10 fps, 10 tbn
"""  # noqa: E501


def _block(i: int, t: float, score: float, ydif: float, sat: float = 8.5, y: float = 63.7) -> str:
    p = "[Parsed_metadata_6 @ 00000226debcf7c0]"
    return "\n".join(
        [
            f"{p} frame:{i}    pts:{int(t * 10)}       pts_time:{t}",
            f"{p} lavfi.scd.mafd=0.000",
            f"{p} lavfi.scd.score={score:.3f}",
            f"{p} lavfi.signalstats.YMIN=3",
            f"{p} lavfi.signalstats.YLOW=5",
            f"{p} lavfi.signalstats.YAVG={y}",
            f"{p} lavfi.signalstats.YHIGH=180",
            f"{p} lavfi.signalstats.UAVG=125.014",
            f"{p} lavfi.signalstats.VAVG=134.597",
            f"{p} lavfi.signalstats.SATAVG={sat}",
            f"{p} lavfi.signalstats.HUEAVG=176.964",
            f"{p} lavfi.signalstats.YDIF={ydif}",
            f"{p} lavfi.signalstats.YBITDEPTH=8",
        ]
    )


CAPTURED = (
    HEADER
    + "[scdet @ 00000145a450ea80] lavfi.scd.score: 28.955, lavfi.scd.time: 0.1\n"
    + _block(0, 0, 0.0, 0)
    + "\n"
    + _block(1, 0.1, 28.955, 31.2)
    + "\n"
)


class TestArgs:
    def test_seek_before_input_and_two_outputs(self) -> None:
        a = ffmpeg_sample_args("Y:/ch/ep.mp4", 100.0, 30.0)
        assert a.index("-ss") < a.index("-i")
        assert a[a.index("-ss") + 1] == "100.00" and a[a.index("-t") + 1] == "30.00"
        graph = a[a.index("-filter_complex") + 1]
        assert "scdet" in graph and "signalstats" in graph and "metadata=mode=print" in graph
        assert f"scale={THUMB_W}:{THUMB_H}" in graph and "format=gray" in graph
        # bars / logos are cropped away before anything is measured
        assert graph.index("crop=") < graph.index("split")
        assert a[-3:] == ["-f", "rawvideo", "pipe:1"]
        assert "-an" in a

    def test_hwaccel_optional(self) -> None:
        a = ffmpeg_sample_args("x.mp4", 0, 10, hwaccel="cuda", binary="ff")
        assert a[0] == "ff" and a[a.index("-hwaccel") + 1] == "cuda"
        assert "-hwaccel" not in ffmpeg_sample_args("x.mp4", 0, 10)


class TestWindows:
    def test_spread_over_body(self) -> None:
        ws = sample_windows(1500.0, count=4, length_s=30)
        assert len(ws) == 4
        starts = [s for s, _ in ws]
        assert starts == sorted(starts)
        assert starts[0] > 1500 * 0.08 - 1 and starts[-1] + 30 < 1500 * 0.94 + 1

    def test_skips_end_credits(self) -> None:
        ws = sample_windows(360.0, count=4, length_s=30)
        assert len(ws) == 4 and ws[-1][0] + 30 <= 360 - 60

    def test_short_file_single_window(self) -> None:
        ws = sample_windows(50.0, count=4, length_s=30)
        assert ws == [(10.0, 30.0)]

    def test_short_file_fewer_windows(self) -> None:
        assert len(sample_windows(200.0, count=4, length_s=30)) == 3

    def test_unknown_duration(self) -> None:
        assert sample_windows(None, length_s=30) == [(0.0, 30.0)]


class TestParsers:
    def test_frame_stats(self) -> None:
        fr = parse_frame_stats(CAPTURED)
        assert len(fr) == 2
        assert fr[0] == FrameStat(0.0, 0.0, 0.0, 63.7, 5.0, 180.0, 8.5, 125.014, 134.597)
        assert fr[1].scene == pytest.approx(28.955) and fr[1].ydif == pytest.approx(31.2)
        assert fr[1].t == pytest.approx(0.1)

    def test_frame_stats_ignores_noise_and_bad_numbers(self) -> None:
        log = _block(0, 0, 0.0, 0).replace("YDIF=0", "YDIF=nan") + "\nframe=  10 fps=0.0\n"
        (f,) = parse_frame_stats(log)
        assert f.ydif == 0.0
        assert parse_frame_stats("no metadata here") == []

    def test_stream_info(self) -> None:
        info = parse_stream_info(CAPTURED)
        assert (info.width, info.height, info.fps) == (1920, 1080, 25.0)
        assert info.bitrate_kbps == 3018.0
        assert info.duration_s == pytest.approx(1244.85)

    def test_stream_info_ignores_output_section(self) -> None:
        info = parse_stream_info(HEADER.split("Stream #0:0[0x1]")[0])
        assert info.width is None and info.bitrate_kbps == 3018.0

    def test_split_thumbs_drops_partial(self) -> None:
        n = THUMB_W * THUMB_H
        assert [len(t) for t in split_thumbs(b"\0" * (2 * n + 7))] == [n, n]


# ---------------------------------------------------------------------------
# synthetic windows

N = THUMB_W * THUMB_H


def _thumb(level: int, pattern: int = 0) -> bytes:
    """Distinct looks: look 0 is flat; look k > 0 is a diagonal gradient of its own slope."""
    if not pattern:
        return bytes([level] * N)
    return bytes(
        (level + pattern * 37 * (x // 8) + pattern * 53 * (y // 4)) % 256
        for y in range(THUMB_H)
        for x in range(THUMB_W)
    )


def _window(
    looks: list[int],
    *,
    secs: float = 30.0,
    ydif: float = 1.5,
    sat: float = 10.0,
    yavg: float = 90.0,
    cut_at_change: bool = True,
) -> WindowSample:
    """Thumbs follow ``looks`` (one entry per THUMB_FPS tick); frames follow them."""
    step = ANALYSIS_FPS // THUMB_FPS
    thumbs = [_thumb(40 + 30 * (k % 5), k) for k in looks]
    frames: list[FrameStat] = []
    for i in range(int(secs * ANALYSIS_FPS)):
        j = min(i // step, len(looks) - 1)
        cut = cut_at_change and i % step == 0 and j > 0 and looks[j] != looks[j - 1]
        k = looks[j]
        frames.append(
            FrameStat(
                i / ANALYSIS_FPS,
                40.0 if cut else 1.0,
                30.0 if cut else ydif,
                yavg + 8 * (k % 7) if cut or k else yavg,
                16,
                200,
                sat + 3 * (k % 5),
                128 + (k % 4),
                128 - (k % 3),
            )
        )
    return WindowSample(secs, frames, thumbs)


@functools.cache
def _talk_episode() -> list[WindowSample]:
    # one presenter look, a second camera angle every ~10 s that keeps coming back
    pat = ([0] * 20 + [1] * 10) * 2
    return [_window(pat) for _ in range(2)]


@functools.cache
def _cinematic_episode() -> list[WindowSample]:
    # presenter half the time, the rest one-off inserts with strong colour/lighting changes
    out = []
    k = 10
    for _ in range(2):
        looks: list[int] = []
        while len(looks) < 60:
            looks += [0] * 6
            looks += [k] * 3 + [k + 1] * 3
            k += 2
        out.append(_window(looks[:60], ydif=4.0, sat=14.0, yavg=50.0))
    return out


@functools.cache
def _static_episode() -> list[WindowSample]:
    return [_window([0] * 60, ydif=0.0) for _ in range(2)]


@functools.cache
def _m_talk() -> StyleMetrics:
    return compute_metrics(_talk_episode())


@functools.cache
def _m_cinematic() -> StyleMetrics:
    return compute_metrics(_cinematic_episode())


@functools.cache
def _m_static() -> StyleMetrics:
    return compute_metrics(_static_episode())


@pytest.fixture(scope="module", autouse=True)
def _warm_metric_caches() -> None:
    """Build the cached episodes once, outside any test's timed call phase."""
    _m_talk()
    _m_cinematic()
    _m_static()


class TestMetrics:
    def test_talk_metrics(self) -> None:
        m = _m_talk()
        assert m.sampled_s == pytest.approx(60.0)
        assert m.insert_share == 0.0
        assert m.top3_share == 1.0
        assert m.dark_share == 0.0
        assert m.cuts_per_min == pytest.approx(3 * 2 / 1.0)
        assert m.still_share == 0.0

    def test_cinematic_metrics(self) -> None:
        m = _m_cinematic()
        assert m.insert_share == pytest.approx(0.5, abs=0.05)
        assert m.looks_per_min > 10
        assert 0 < m.insert_luma < 100 and m.dark_share > 0.3

    def test_static_metrics(self) -> None:
        m = _m_static()
        assert m.still_share > 0.99 and m.cuts_per_min == 0.0 and m.looks_per_min == 1.0

    def test_dict_roundtrip(self) -> None:
        m = _m_talk()
        d = m.to_dict()
        assert metrics_from_dict(d | {"unknown": 1}) == metrics_from_dict(d)
        assert metrics_from_dict(d).cuts_per_min == pytest.approx(m.cuts_per_min, abs=1e-3)

    def test_empty(self) -> None:
        m = compute_metrics([])
        assert m.sampled_s == 0.0
        assert classify_style([m]).confidence == 0.0


def _metrics(**kw: float) -> StyleMetrics:
    base: dict[str, float] = dict(
        sampled_s=120.0,
        cuts_per_min=2.0,
        motion=1.5,
        motion_p90=4.0,
        still_share=0.02,
        luma_mean=120.0,
        luma_std=6.0,
        dark_share=0.0,
        sat_mean=12.0,
        sat_std=1.5,
        chroma_spread=2.0,
        looks_per_min=3.0,
        top3_share=0.85,
        revisit_share=0.5,
        insert_share=0.03,
        insert_luma=0.0,
        insert_sat=0.0,
        edge_density=0.25,
    )
    base.update(kw)
    return StyleMetrics(**base)  # type: ignore[arg-type]


class TestClassify:
    def test_talk(self) -> None:
        r = classify_style([_m_talk()] * 3)
        assert (r.style, r.tier) == ("talk", 1)
        assert r.confidence > 0.5 and r.episodes == 3

    def test_static(self) -> None:
        r = classify_style([_m_static()] * 3)
        assert (r.style, r.tier) == ("static", 0)

    def test_cinematic(self) -> None:
        r = classify_style([_m_cinematic()] * 3)
        assert (r.style, r.tier) == ("cinematic", 3)

    def test_produced(self) -> None:
        # daylight b-roll: many one-off looks, but evenly lit
        m = _metrics(cuts_per_min=8.0, looks_per_min=30.0, top3_share=0.4, insert_share=0.55,
                     insert_luma=125.0, luma_mean=130.0, dark_share=0.05, motion=4.5)  # fmt: skip
        r = classify_style([m] * 3)
        assert (r.style, r.tier) == ("produced", 2)
        assert r.scores["production"] > 0.9 and r.scores["lowkey"] < 0.1

    def test_ai_imagery_is_cinematic(self) -> None:
        # dark virtual set + dark AI inserts (qisas al-anbiya 2024 profile)
        m = _metrics(cuts_per_min=7.0, looks_per_min=13.5, insert_share=0.39, insert_luma=92.0,
                     luma_mean=71.0, dark_share=0.36, motion=2.8)  # fmt: skip
        r = classify_style([m] * 3)
        assert (r.style, r.tier) == ("cinematic", 3)
        assert any("dark frames" in x for x in r.reasons)

    def test_dark_studio_without_inserts_stays_talk(self) -> None:
        # low-key lit podcast set: dark, but it never cuts away
        m = _metrics(luma_mean=61.0, dark_share=0.69, looks_per_min=2.0, insert_share=0.0)
        assert classify_style([m] * 3).style == "talk"

    def test_multicamera_studio_stays_talk(self) -> None:
        # many cuts between a handful of recurring angles: still a studio talk
        m = _metrics(cuts_per_min=8.0, looks_per_min=9.8, top3_share=0.45, insert_share=0.15,
                     insert_luma=116.0, motion=4.4)  # fmt: skip
        assert classify_style([m] * 3).style == "talk"

    def test_dissolve_edited_dark_period_drama_is_produced(self) -> None:
        # staged period sets, dimly lit, dissolves instead of cuts (karbala 2013)
        m = _metrics(cuts_per_min=0.5, looks_per_min=24.0, insert_share=0.41, insert_luma=100.0,
                     luma_mean=80.0, dark_share=0.3, motion=3.2)  # fmt: skip
        r = classify_style([m] * 3)
        assert r.style == "produced" and r.scores["lowkey"] >= 0.45

    def test_moody_multicamera_studio_stays_talk(self) -> None:
        # purple mood lighting + bokeh foreground shots (qissa wa aya 2018)
        m = _metrics(cuts_per_min=2.5, looks_per_min=10.0, insert_share=0.21, insert_luma=91.0,
                     luma_mean=91.0, dark_share=0.0, motion=3.75)  # fmt: skip
        assert classify_style([m] * 3).style == "talk"

    def test_still_picture_with_pulsing_badge_is_static(self) -> None:
        m = _metrics(cuts_per_min=0.0, looks_per_min=0.5, still_share=0.78, motion=0.2,
                     motion_p90=0.52)  # fmt: skip
        assert classify_style([m] * 3).style == "static"

    def test_locked_off_lecture_is_not_static(self) -> None:
        m = _metrics(cuts_per_min=0.0, looks_per_min=0.5, still_share=0.17, motion=0.66,
                     motion_p90=1.53)  # fmt: skip
        assert classify_style([m] * 3).style == "talk"

    def test_one_odd_episode_does_not_flip_season(self) -> None:
        talk = _metrics()
        odd = _metrics(cuts_per_min=20.0, looks_per_min=70.0, top3_share=0.1, insert_share=0.99,
                       insert_luma=89.0, luma_mean=89.0, dark_share=0.2, motion=5.0)  # fmt: skip
        r = classify_style([talk, odd, talk])
        assert r.style == "talk"
        assert "2/3 episodes agree" in r.reasons

    def test_no_samples(self) -> None:
        r = classify_style([])
        assert r.style == "talk" and r.confidence == 0.0
