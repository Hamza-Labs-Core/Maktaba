"""Production style of a series: studio talk vs produced vs cinematic.

A TV channel should not mix production tiers: a presenter-in-a-studio
lecture and a show full of b-roll, graphics and AI imagery are not the same
kind of programme even when the presenter is. Titles cannot tell them
apart, so this module looks at the pictures — cheaply.

Sampling: a few short windows per episode (seeked with ``-ss`` before ``-i``
so only those bytes are read from the share), decoded at a fixed low frame
rate, centre-cropped (bars, logos, tickers) and downscaled before any
analysis. One ffmpeg call per window yields

* per-frame metadata from ``scdet`` (scene-change score) and ``signalstats``
  (luma, chroma, saturation, frame difference) printed by ``metadata=print``
  on stderr, and
* a tiny raw grayscale thumbnail stream (``THUMB_W``×``THUMB_H`` at
  ``THUMB_FPS``) on stdout, used to count distinct *looks* (camera set-ups /
  shots) and how often the picture returns to the same few looks, plus an
  edge-density estimate (on-screen text and graphics are edge-heavy).

Everything here is pure: :func:`ffmpeg_sample_args` builds the command, the
``parse_*`` functions read its output, :func:`compute_metrics` reduces one
episode's windows to :class:`StyleMetrics` and :func:`classify_style` turns a
season's episodes into a :class:`StyleResult`. The caller runs ffmpeg (see
``scripts/mediaintel/scan_visual.py``). No imaging dependency.

Styles and tiers:

====== ========== ============================================================
tier   style      what it looks like (and what decides it)
====== ========== ============================================================
0      static     one still picture (recitation over an image, audio-only):
                  no cuts, ≤ 2.5 looks/min, frozen frames
1      talk       studio / lecture / podcast, single or multi-camera, or a
                  presenter holding the shot on location: few *inserts*
2      produced   b-roll, location footage, composited or staged sets,
                  crane/jib beauty shots, graphics: many inserts and looks
3      cinematic  produced *and* low-key, hard-cut montage: dark virtual sets
                  and AI imagery (the 2024–25 shows)
====== ========== ============================================================
"""

from __future__ import annotations

import math
import re
import statistics
from collections.abc import Sequence
from dataclasses import asdict, dataclass, field
from typing import Any, Literal

__all__ = [
    "ANALYSIS_FPS",
    "STYLE_VERSION",
    "THUMB_H",
    "THUMB_W",
    "TIERS",
    "FrameStat",
    "StreamInfo",
    "StyleMetrics",
    "StyleResult",
    "WindowSample",
    "classify_style",
    "compute_metrics",
    "episode_style",
    "ffmpeg_sample_args",
    "is_static",
    "lowkey_score",
    "metrics_from_dict",
    "parse_frame_stats",
    "parse_stream_info",
    "production_score",
    "sample_windows",
    "split_thumbs",
]

STYLE_VERSION: int = 1

Style = Literal["static", "talk", "produced", "cinematic"]
TIERS: dict[str, int] = {"static": 0, "talk": 1, "produced": 2, "cinematic": 3}

ANALYSIS_FPS = 10  # scene / motion analysis rate (normalises 25/30/60 fps sources)
ANALYSIS_W = 160
THUMB_FPS = 2
THUMB_W, THUMB_H = 64, 36
_LOOK_W, _LOOK_H = 16, 9  # look clustering grid (block means of the thumbnail)

# Centre crop before analysis: drops letterbox / pillarbox bars (2.35:1 or 4:3
# inside 16:9), channel logos and tickers, which otherwise read as "dark" and
# dilute scene-change scores.
CROP = "trunc(iw*0.76/2)*2:trunc(ih*0.70/2)*2"
# Seconds before the end that are never sampled (credits, end cards, next-episode promo).
END_CARD_S = 60.0
# A frame is a hard cut when its scdet score is at least this (0–100 scale).
CUT_SCORE = 12.0
# Mean absolute difference (0–255, on the 16×9 look grid) that separates two looks.
LOOK_DISTANCE = 14.0
# a look held this long in one run is a presenter/set-up shot, not an insert
HELD_S = 8.0
# frame mean luma below which a frame counts as dark (low-key lighting, night, AI grade)
DARK_Y = 60.0
# signalstats YDIF (mean abs luma difference to previous frame) below which a
# frame counts as frozen.
STILL_YDIF = 0.35
# gradient magnitude (|gx|+|gy| on the 64×36 thumbnail) that counts as an edge
EDGE_GRADIENT = 40


# ---------------------------------------------------------------------------
# sampling


def sample_windows(
    duration_s: float | None, *, count: int = 4, length_s: float = 30.0
) -> list[tuple[float, float]]:
    """(start, length) windows spread over the body of the file.

    Windows sit at the centres of ``count`` equal slices of the span from 8 %
    to 94 % (and at least ``END_CARD_S`` before the end), skipping the opening
    titles and the closing credits. Files too short for that get one window
    over (most of) the whole file.
    """
    if not duration_s or duration_s <= 0:
        return [(0.0, length_s)]
    lo = duration_s * 0.08
    hi = min(duration_s * 0.94, duration_s - END_CARD_S)
    if hi - lo < length_s * 1.25:
        hi = duration_s * 0.94
    span = hi - lo
    if count < 1 or span < count * length_s * 1.25:
        n = max(1, min(count, int(span // (length_s * 1.25))))
        if n == 1:
            start = max(0.0, (duration_s - length_s) / 2)
            return [(round(start, 2), round(min(length_s, duration_s), 2))]
        count = n
    step = span / count
    return [(round(lo + step * (i + 0.5) - length_s / 2, 2), length_s) for i in range(count)]


def ffmpeg_sample_args(
    path: str,
    start_s: float,
    length_s: float,
    *,
    binary: str = "ffmpeg",
    hwaccel: str | None = None,
) -> list[str]:
    """ffmpeg argv for one window.

    stderr (log level info) carries the stream header and one
    ``metadata=print`` block per analysed frame; stdout carries raw
    ``THUMB_W``×``THUMB_H`` gray thumbnails at ``THUMB_FPS``.
    """
    graph = (
        f"[0:v]fps={ANALYSIS_FPS},crop={CROP},scale={ANALYSIS_W}:-2:flags=fast_bilinear,"
        "format=yuv420p,split=2[a][b];"
        "[a]scdet=threshold=100,signalstats,metadata=mode=print[m];"
        f"[b]fps={THUMB_FPS},scale={THUMB_W}:{THUMB_H}:flags=area,format=gray[t]"
    )
    args = [binary, "-hide_banner", "-nostats", "-nostdin", "-v", "info"]
    if hwaccel:
        args += ["-hwaccel", hwaccel]
    return args + [
        "-ss", f"{start_s:.2f}", "-t", f"{length_s:.2f}", "-i", path,
        "-an", "-sn", "-dn", "-filter_complex", graph,
        "-map", "[m]", "-f", "null", "-",
        "-map", "[t]", "-f", "rawvideo", "pipe:1",
    ]  # fmt: skip


# ---------------------------------------------------------------------------
# parsing


@dataclass(frozen=True, slots=True)
class FrameStat:
    t: float
    scene: float  # scdet score, 0–100
    ydif: float  # mean |ΔY| to previous frame, 0–255
    yavg: float
    ylow: float
    yhigh: float
    satavg: float
    uavg: float
    vavg: float


@dataclass(frozen=True, slots=True)
class StreamInfo:
    width: int | None = None
    height: int | None = None
    fps: float | None = None
    bitrate_kbps: float | None = None
    duration_s: float | None = None


_FRAME_RE = re.compile(r"\]\s*frame:\s*(\d+)\s+pts:\s*\S+\s+pts_time:\s*([-\d.eE+]+)")
_KV_RE = re.compile(r"\]\s*lavfi\.([\w.]+)=([-\d.eE+naif]+)\s*$")
_KEYS = {
    "scd.score": "scene",
    "signalstats.YDIF": "ydif",
    "signalstats.YAVG": "yavg",
    "signalstats.YLOW": "ylow",
    "signalstats.YHIGH": "yhigh",
    "signalstats.SATAVG": "satavg",
    "signalstats.UAVG": "uavg",
    "signalstats.VAVG": "vavg",
}


def _num(s: str) -> float:
    try:
        v = float(s)
    except ValueError:
        return 0.0
    return v if math.isfinite(v) else 0.0


def parse_frame_stats(log: str) -> list[FrameStat]:
    """Frames from ffmpeg's ``metadata=print`` log (other lines are ignored)."""
    frames: list[FrameStat] = []
    cur: dict[str, float] | None = None

    def flush() -> None:
        if cur is not None:
            frames.append(FrameStat(**{k: cur.get(k, 0.0) for k in FrameStat.__slots__}))

    for line in log.splitlines():
        if "Parsed_metadata" not in line:
            continue
        m = _FRAME_RE.search(line)
        if m:
            flush()
            cur = {"t": _num(m.group(2))}
            continue
        kv = _KV_RE.search(line)
        if kv and cur is not None and kv.group(1) in _KEYS:
            cur[_KEYS[kv.group(1)]] = _num(kv.group(2))
    flush()
    return frames


_VIDEO_RE = re.compile(r"Stream #\d+:\d+.*?: Video: .*?(\d{2,5})x(\d{2,5})")
_FPS_RE = re.compile(r"([\d.]+) fps")
_DUR_RE = re.compile(r"Duration: (\d+):(\d+):([\d.]+).*?bitrate: (\d+) kb/s")


def parse_stream_info(log: str) -> StreamInfo:
    """Input video size, frame rate and overall bitrate from ffmpeg's header."""
    width = height = None
    fps = bitrate = duration = None
    in_input = False
    for line in log.splitlines():
        if line.startswith("Input #"):
            in_input = True
        elif line.startswith(("Output #", "Stream mapping")):
            in_input = False
        if not in_input:
            continue
        if duration is None and (d := _DUR_RE.search(line)):
            h, mi, s, br = d.groups()
            duration = int(h) * 3600 + int(mi) * 60 + float(s)
            bitrate = float(br)
        if width is None and (v := _VIDEO_RE.search(line)):
            width, height = int(v.group(1)), int(v.group(2))
            if f := _FPS_RE.search(line):
                fps = float(f.group(1))
    return StreamInfo(width, height, fps, bitrate, duration)


def split_thumbs(raw: bytes) -> list[bytes]:
    """Raw gray thumbnail stream → one ``THUMB_W*THUMB_H`` buffer per frame."""
    n = THUMB_W * THUMB_H
    return [raw[i : i + n] for i in range(0, len(raw) - n + 1, n)]


# ---------------------------------------------------------------------------
# metrics


@dataclass(frozen=True, slots=True)
class WindowSample:
    length_s: float
    frames: Sequence[FrameStat]
    thumbs: Sequence[bytes]


@dataclass(frozen=True, slots=True)
class StyleMetrics:
    """One episode, reduced over its sampled windows."""

    sampled_s: float
    cuts_per_min: float  # hard cuts (scdet ≥ CUT_SCORE)
    motion: float  # median YDIF of non-cut frames (0–255)
    motion_p90: float
    still_share: float  # frames with YDIF < STILL_YDIF
    luma_mean: float  # mean YAVG (0–255)
    luma_std: float  # spread of YAVG across frames (lighting changes)
    dark_share: float  # frames with YAVG < DARK_Y — low-key / night / graded
    sat_mean: float  # mean SATAVG
    sat_std: float  # spread of SATAVG across frames (palette changes)
    chroma_spread: float  # spread of the (U,V) frame means — colour variety
    looks_per_min: float  # distinct looks (16×9 clusters) per sampled minute
    top3_share: float  # share of thumbnails in the 3 most used looks
    revisit_share: float  # look changes that go back to a look already seen
    insert_share: float  # thumbnails in inserts: short looks that never come back
    insert_luma: float  # mean YAVG of the insert frames (0 when there are none)
    insert_sat: float  # mean SATAVG of the insert frames
    edge_density: float  # share of thumbnail pixels on an edge (text, graphics, detail)
    width: int | None = None
    height: int | None = None
    fps: float | None = None
    bitrate_kbps: float | None = None
    version: int = STYLE_VERSION

    def to_dict(self) -> dict[str, Any]:
        return {k: (round(v, 4) if isinstance(v, float) else v) for k, v in asdict(self).items()}


def metrics_from_dict(d: dict[str, Any]) -> StyleMetrics:
    names = StyleMetrics.__slots__
    return StyleMetrics(**{k: d[k] for k in names if k in d})


def _pstd(xs: Sequence[float]) -> float:
    return statistics.pstdev(xs) if len(xs) > 1 else 0.0


def _mean(xs: Sequence[float]) -> float:
    return statistics.fmean(xs) if xs else 0.0


def _quantile(xs: Sequence[float], q: float) -> float:
    if not xs:
        return 0.0
    s = sorted(xs)
    return s[min(len(s) - 1, int(q * (len(s) - 1) + 0.5))]


# Pixel arithmetic in lanes. These run for every sampled thumbnail of every video (240 a
# video), and the look search compares each one with every look seen so far, so they work
# on a whole picture at once: the pixels go into one Python integer, 16 bits a pixel
# ("lanes", lane i = pixel i), and each step is one big-integer operation (a loop in C)
# instead of a loop over pixels or blocks in Python. Every lane stays in 0..65535 at every
# step (the bounds are noted where they matter), so no lane carries into or borrows from
# its neighbour, and the results are exactly the integers per-pixel loops give.

_LANE = 16
_BLOCK_W, _BLOCK_H = THUMB_W // _LOOK_W, THUMB_H // _LOOK_H
_BLOCK_SHIFT = (_BLOCK_W * _BLOCK_H).bit_length() - 1  # block mean = block sum >> shift
if 1 << _BLOCK_SHIFT != _BLOCK_W * _BLOCK_H or 255 << _BLOCK_SHIFT >= 1 << _LANE:
    raise RuntimeError("look blocks must hold a power of two of pixels, summing within a lane")
_GRID = _LOOK_W * _LOOK_H
_EDGE_SPAN = THUMB_W * (THUMB_H - 1)  # pixels that have a pixel below them


def _lanes(n: int, value: int) -> int:
    """``n`` lanes, each holding ``value``."""
    return int.from_bytes(value.to_bytes(2, "little") * n, "little")


def _widen(data: bytes) -> int:
    """One lane per byte of ``data``."""
    buf = bytearray(2 * len(data))
    buf[0::2] = data
    return int.from_bytes(buf, "little")


_IMAGE_LOW8 = _lanes(THUMB_W * THUMB_H, 0xFF)
_GRID_ONES, _GRID_256 = _lanes(_GRID, 1), _lanes(_GRID, 0x100)
_SPAN_ONES, _SPAN_256 = _lanes(_EDGE_SPAN, 1), _lanes(_EDGE_SPAN, 0x100)
_SPAN_ALL = (1 << (_LANE * _EDGE_SPAN)) - 1
# lane i gets bit 15 set exactly when it held at least EDGE_GRADIENT (at most 510 + 32728)
_EDGE_BIAS = _lanes(_EDGE_SPAN, 0x8000 - EDGE_GRADIENT)
# the last column has no right neighbour: its lanes are left out of the count
_EDGE_COUNTED = int.from_bytes(
    b"".join(b"\x00\x00" if i % THUMB_W == THUMB_W - 1 else b"\x01\x00" for i in range(_EDGE_SPAN)),
    "little",
)


def _absdiff(a: int, a_256: int, b: int, b_256: int, ones: int) -> int:
    """Lane-wise ``|a - b|`` of two lane sets holding 0..255.

    ``a_256`` and ``b_256`` are ``a`` and ``b`` with 256 added to every lane (``| x256``,
    kept by callers that reuse one side), ``ones`` holds 1 in every lane.
    """
    p = a_256 - b  # a - b + 256 in every lane, 1..511: no borrow
    q = b_256 - a
    # bit 8 of p is set where a >= b, of q where b >= a; the low byte is then the difference
    # (both are 0 where a == b)
    return (p & (((p >> 8) & ones) * 0xFF)) + (q & (((q >> 8) & ones) * 0xFF))


def _look_vec(thumb: bytes) -> bytes:
    """Mean of each ``_BLOCK_W``×``_BLOCK_H`` block, one byte each, row-major over the grid."""
    x = _widen(thumb)
    row = _LANE * THUMB_W
    # lane i: the sum of pixel i and the _BLOCK_H - 1 pixels below it (at most 1020)
    v = x
    for k in range(1, _BLOCK_H):
        v += x >> (k * row)
    # ... and of the _BLOCK_W - 1 lanes to its right: a block's sum at its top-left pixel
    # (at most 4080, 12 bits)
    h = v
    for k in range(1, _BLOCK_W):
        h += v >> (k * _LANE)
    raw = ((h >> _BLOCK_SHIFT) & _IMAGE_LOW8).to_bytes(2 * THUMB_W * THUMB_H, "little")
    # a block's top-left pixel is lane by * _BLOCK_H * THUMB_W + bx * _BLOCK_W: its mean is
    # the low byte of that lane, at twice that offset
    step, row_bytes = 2 * _BLOCK_W, 2 * _BLOCK_H * THUMB_W
    return b"".join(
        raw[by * row_bytes : by * row_bytes + step * _LOOK_W : step] for by in range(_LOOK_H)
    )


def _edge_share(thumb: bytes) -> float:
    """Share of pixels whose gradient (|gx| + |gy|, to the right and below) is an edge."""
    x = _widen(thumb)
    px = x & _SPAN_ALL
    right = (x >> _LANE) & _SPAN_ALL
    down = (x >> (_LANE * THUMB_W)) & _SPAN_ALL
    px_256 = px | _SPAN_256
    grad = _absdiff(px, px_256, right, right | _SPAN_256, _SPAN_ONES) + _absdiff(
        px, px_256, down, down | _SPAN_256, _SPAN_ONES
    )
    edges = (((grad + _EDGE_BIAS) >> 15) & _EDGE_COUNTED).bit_count()
    return edges / ((THUMB_W - 1) * (THUMB_H - 1))


def _edge_shares(thumbs: Sequence[bytes]) -> list[float]:
    """:func:`_edge_share` of each thumbnail, worked out once per distinct picture."""
    seen: dict[bytes, float] = {}
    out: list[float] = []
    for t in thumbs:
        e = seen.get(t)
        if e is None:
            e = seen[t] = _edge_share(t)
        out.append(e)
    return out


@dataclass(frozen=True, slots=True)
class _LookStats:
    labels: list[int]  # look id per thumbnail (all windows, in order)
    looks: int
    top3_share: float
    revisit_share: float
    recurring: list[bool]  # per thumbnail: its look recurs or is held (not an insert)


def _looks(windows: Sequence[Sequence[bytes]]) -> _LookStats:
    """Cluster thumbnails into looks (greedy, first-come representatives).

    A *run* is a maximal stretch of consecutive thumbnails with the same look;
    every window starts a new run. A look is an *insert* (b-roll, graphic, AI
    shot) when it never comes back and is never held for ``HELD_S`` — studio
    angles keep coming back, and an on-location presenter holds the shot.
    """
    reps: list[int] = []  # each look's grid, in lanes
    reps_256: list[int] = []  # ... with 256 added to every lane
    rep_sums: list[int] = []
    counts: list[int] = []
    runs: list[int] = []
    longest: list[int] = []
    labels: list[int] = []
    grids: dict[bytes, bytes] = {}  # a frozen picture repeats byte for byte
    changes = revisits = 0
    for thumbs in windows:
        prev = -1
        run = 0
        for th in thumbs:
            g = grids.get(th)
            if g is None:
                g = grids[th] = _look_vec(th)
            v, sv = _widen(g), sum(g)
            v_256 = v | _GRID_256
            # the distance is the mean |a - b| over the grid, and |sum(a) - sum(b)| is at most
            # sum(|a - b|): a look whose sum is that far off cannot be nearer than the best
            # so far, so the per-cell comparison is skipped (same answer, same order)
            best, best_d = -1, LOOK_DISTANCE
            for i, sr in enumerate(rep_sums):
                if abs(sv - sr) / _GRID >= best_d:
                    continue
                diff = _absdiff(v, v_256, reps[i], reps_256[i], _GRID_ONES)
                d = sum(diff.to_bytes(2 * _GRID, "little")) / _GRID  # lanes are 0..255
                if d < best_d:
                    best, best_d = i, d
            if best < 0:
                reps.append(v)
                reps_256.append(v_256)
                rep_sums.append(sv)
                counts.append(0)
                runs.append(0)
                longest.append(0)
                best = len(reps) - 1
            elif best != prev and prev >= 0:
                revisits += 1
            if best != prev:
                runs[best] += 1
                run = 0
                if prev >= 0:
                    changes += 1
            run += 1
            longest[best] = max(longest[best], run)
            counts[best] += 1
            labels.append(best)
            prev = best
    total = sum(counts)
    if not total:
        return _LookStats([], 0, 1.0, 0.0, [])
    top3 = sum(sorted(counts, reverse=True)[:3]) / total
    return _LookStats(
        labels,
        len(reps),
        top3,
        revisits / changes if changes else 0.0,
        [runs[i] >= 2 or longest[i] >= HELD_S * THUMB_FPS for i in labels],
    )


def compute_metrics(
    windows: Sequence[WindowSample], info: StreamInfo | None = None
) -> StyleMetrics:
    """Reduce one episode's sampled windows to :class:`StyleMetrics`."""
    frames = [f for w in windows for f in w.frames]
    # the first frame of each window has no predecessor: no score, no YDIF
    body = [f for w in windows for f in list(w.frames)[1:]]
    good = [[t for t in w.thumbs if len(t) == THUMB_W * THUMB_H] for w in windows]
    thumbs = [t for ths in good for t in ths]
    sampled = sum(min(w.length_s, len(w.frames) / ANALYSIS_FPS) for w in windows)
    minutes = max(sampled / 60.0, 1e-6)
    calm = [f.ydif for f in body if f.scene < CUT_SCORE]
    looks = _looks(good)
    # frames under an insert look (thumbnail j of a window ≈ analysis frame j·step)
    step = ANALYSIS_FPS // THUMB_FPS
    inserts: list[FrameStat] = []
    k = 0
    for w, ths in zip(windows, good, strict=True):
        fr = list(w.frames)
        inserts += [
            fr[j * step]
            for j in range(len(ths))
            if not looks.recurring[k + j] and j * step < len(fr)
        ]
        k += len(ths)
    info = info or StreamInfo()
    return StyleMetrics(
        sampled_s=sampled,
        cuts_per_min=sum(1 for f in body if f.scene >= CUT_SCORE) / minutes,
        motion=statistics.median(calm) if calm else 0.0,
        motion_p90=_quantile(calm, 0.9),
        still_share=(sum(1 for f in body if f.ydif < STILL_YDIF) / len(body)) if body else 1.0,
        luma_mean=_mean([f.yavg for f in frames]),
        luma_std=_pstd([f.yavg for f in frames]),
        dark_share=(sum(1 for f in frames if f.yavg < DARK_Y) / len(frames)) if frames else 0.0,
        sat_mean=_mean([f.satavg for f in frames]),
        sat_std=_pstd([f.satavg for f in frames]),
        chroma_spread=math.hypot(_pstd([f.uavg for f in frames]), _pstd([f.vavg for f in frames])),
        looks_per_min=looks.looks / minutes,
        top3_share=looks.top3_share,
        revisit_share=looks.revisit_share,
        insert_share=(looks.recurring.count(False) / len(thumbs)) if thumbs else 0.0,
        insert_luma=_mean([f.yavg for f in inserts]),
        insert_sat=_mean([f.satavg for f in inserts]),
        # every 2nd thumbnail is plenty for a density estimate
        edge_density=_mean(_edge_shares(thumbs[::2])),
        width=info.width,
        height=info.height,
        fps=info.fps,
        bitrate_kbps=info.bitrate_kbps,
    )


# ---------------------------------------------------------------------------
# classification
#
# Calibrated on 45 sampled episodes of 15 Sheikh Nabil Al-Awadi seasons
# (2013–2025) plus 16 files from 8 other channels, each checked by eye on a
# contact sheet:
#
# * talk: studio angles keep coming back and on-location presenters hold the
#   shot, so few frames are *inserts* (season median ≤ 0.23) even with 8
#   cuts/min of multi-camera switching; production score ≤ 0.32.
# * produced: location b-roll, composited drama backgrounds, crane/jib beauty
#   shots, staged period sets, motion graphics — inserts ≥ 0.35, 15–50
#   looks/min; production score ≥ 0.69.
# * cinematic: the 2024–25 AI-imagery shows are produced *and low-key*: dark
#   virtual sets and dark, warm AI inserts (mean luma 65–105) where real
#   b-roll and lit studios sit at 100–165 — and they are hard-cut montage.
# * static: one picture (± a pulsing badge or changing verse text): no cuts,
#   hardly any motion.


@dataclass(frozen=True, slots=True)
class StyleResult:
    style: Style
    tier: int
    confidence: float
    reasons: list[str] = field(default_factory=list)
    scores: dict[str, float] = field(default_factory=dict)
    episodes: int = 0


def _med(ms: Sequence[StyleMetrics], name: str) -> float:
    return statistics.median(float(getattr(m, name)) for m in ms)


def _ramp(x: float, lo: float, hi: float) -> float:
    """0 at ``lo``, 1 at ``hi``, linear between (``lo > hi`` ramps downwards)."""
    return min(1.0, max(0.0, (x - lo) / (hi - lo)))


def is_static(m: StyleMetrics) -> bool:
    """A still picture: no cuts, at most a couple of looks, essentially no motion."""
    return (
        m.cuts_per_min <= 1.0
        and m.looks_per_min <= 2.5
        and (m.still_share >= 0.6 or m.motion_p90 <= 1.0)
    )


def production_score(m: StyleMetrics) -> float:
    """0 ≈ studio talk (recurring angles, held shots) … 1 ≈ wall-to-wall inserts.

    Motion is deliberately left out: in the centre crop a gesturing presenter
    moves as much as a b-roll pan does.
    """
    return 0.65 * _ramp(m.insert_share, 0.20, 0.40) + 0.35 * _ramp(m.looks_per_min, 8.0, 25.0)


def lowkey_score(m: StyleMetrics) -> float:
    """0 ≈ evenly lit studio / daylight footage … 1 ≈ dark, graded, synthetic look."""
    insert = _ramp(m.insert_luma, 115.0, 85.0) if m.insert_share > 0.02 else 0.0
    return (
        0.40 * insert
        + 0.30 * _ramp(m.dark_share, 0.05, 0.30)
        + 0.30 * _ramp(m.luma_mean, 120.0, 85.0)
    )


PRODUCED_AT = 0.50  # production score from which a season is at least "produced"
CINEMATIC_LOWKEY_AT = 0.45  # low-key score from which a produced season is "cinematic"…
# …when it is hard-cut montage. The 2013–15 dimly lit period-drama sets are as dark as
# the AI shows but dissolve between shots (≤ 0.7 cuts/min against 5–8.5).
CINEMATIC_MIN_CUTS = 2.0


def _style_of(prod: float, lowkey: float, cuts: float) -> Style:
    if prod < PRODUCED_AT:
        return "talk"
    if lowkey >= CINEMATIC_LOWKEY_AT and cuts >= CINEMATIC_MIN_CUTS:
        return "cinematic"
    return "produced"


def _margin(style: Style, prod: float, lowkey: float, cuts: float) -> float:
    """Distance to the nearest decision boundary (0 = on it), in score units."""
    cut_margin = (cuts - CINEMATIC_MIN_CUTS) / 10.0
    if style == "talk":
        return PRODUCED_AT - prod
    if style == "cinematic":
        return min(prod - PRODUCED_AT, lowkey - CINEMATIC_LOWKEY_AT, cut_margin)
    return min(prod - PRODUCED_AT, max(CINEMATIC_LOWKEY_AT - lowkey, -cut_margin))


def episode_style(m: StyleMetrics) -> Style:
    """Style of a single episode (used for the agreement count)."""
    if is_static(m):
        return "static"
    return _style_of(production_score(m), lowkey_score(m), m.cuts_per_min)


def classify_style(episodes: Sequence[StyleMetrics]) -> StyleResult:
    """Style of a season from its sampled episodes.

    Scores are medians over the episodes, so one odd episode (a guest
    episode, a compilation, a mis-filed upload) does not flip the season.
    """
    ms = [m for m in episodes if m.sampled_s > 0]
    if not ms:
        return StyleResult("talk", TIERS["talk"], 0.0, ["no sampled frames"])
    n = len(ms)
    per_ep = [episode_style(m) for m in ms]
    static_n = per_ep.count("static")
    if static_n * 2 > n:
        return StyleResult(
            "static",
            TIERS["static"],
            round(0.5 + 0.5 * static_n / n, 3),
            [
                f"{static_n}/{n} episodes are a still picture",
                f"{_med(ms, 'cuts_per_min'):.1f} cuts/min, motion p90 "
                f"{_med(ms, 'motion_p90'):.2f}, still frames {_med(ms, 'still_share'):.0%}",
            ],
            episodes=n,
        )
    moving = [m for m in ms if not is_static(m)]
    prod = statistics.median(production_score(m) for m in moving)
    lowkey = statistics.median(lowkey_score(m) for m in moving)
    cuts = _med(moving, "cuts_per_min")
    style = _style_of(prod, lowkey, cuts)
    agree = per_ep.count(style)
    margin = _margin(style, prod, lowkey, cuts)
    confidence = round(min(1.0, 0.4 + 2.0 * margin) * (0.5 + 0.5 * agree / n), 3)
    reasons = [
        f"inserts {_med(moving, 'insert_share'):.0%} of frames, "
        f"{_med(moving, 'looks_per_min'):.1f} looks/min, "
        f"{_med(moving, 'cuts_per_min'):.1f} cuts/min",
        f"motion {_med(moving, 'motion'):.2f}",
        f"luma {_med(moving, 'luma_mean'):.0f} (dark frames {_med(moving, 'dark_share'):.0%}), "
        f"insert luma {_med(moving, 'insert_luma'):.0f}",
        f"{agree}/{n} episodes agree",
    ]
    scores = {"production": round(prod, 3), "lowkey": round(lowkey, 3)}
    return StyleResult(style, TIERS[style], confidence, reasons, scores, n)
