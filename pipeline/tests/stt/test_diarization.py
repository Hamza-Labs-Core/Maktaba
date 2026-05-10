"""Story 3.9 — diarization speaker assignment + lazy import."""

from __future__ import annotations

import asyncio
import importlib
import sys

from maktaba_pipeline.stt import diarization as dz
from maktaba_pipeline.stt.diarization import DiarizationGate, Interval, assign_speakers
from maktaba_pipeline.stt.protocol import Segment


def test_assign_speakers_no_intervals_leaves_segments_unchanged() -> None:
    segs = [Segment(seq=0, start_sec=0.0, end_sec=1.0, text="x")]
    out = assign_speakers(segs, [])
    assert out is segs  # short-circuit returns the same list


def test_assign_speakers_uses_midpoint_match() -> None:
    segs = [
        Segment(seq=0, start_sec=0.0, end_sec=2.0, text="a"),
        Segment(seq=1, start_sec=2.0, end_sec=4.0, text="b"),
    ]
    intervals = [
        Interval(start_sec=0.0, end_sec=2.0, speaker="Speaker 1"),
        Interval(start_sec=2.0, end_sec=4.0, speaker="Speaker 2"),
    ]
    out = assign_speakers(segs, intervals)
    assert [s.speaker for s in out] == ["Speaker 1", "Speaker 2"]


def test_assign_speakers_splits_overlap() -> None:
    segs = [Segment(seq=0, start_sec=0.0, end_sec=4.0, text="hello world")]
    intervals = [
        Interval(start_sec=0.0, end_sec=2.0, speaker="S1"),
        Interval(start_sec=2.0, end_sec=4.0, speaker="S2"),
    ]
    out = assign_speakers(segs, intervals)
    # Story 3.9 EC: the segment is split at the diarization boundary.
    assert len(out) == 2
    assert out[0].speaker == "S1"
    assert out[1].speaker == "S2"
    assert out[0].metadata["split_from"] == "0.a"
    assert out[1].metadata["split_from"] == "0.b"


def test_diarization_gate_serialises_runs() -> None:
    gate = DiarizationGate(capacity=1)
    holding: list[int] = []

    async def _hold(idx: int) -> None:
        async with gate.slot():
            holding.append(idx)
            # Yield so a sibling can attempt entry — but the gate keeps it out.
            await asyncio.sleep(0.01)
            holding.append(-idx)

    async def _drive() -> None:
        await asyncio.gather(_hold(1), _hold(2), _hold(3))

    asyncio.run(_drive())
    # Each task records its enter/exit pair adjacently — the gate
    # serialised them.
    for i in range(0, len(holding), 2):
        assert holding[i] + holding[i + 1] == 0


def test_pyannote_not_imported_when_diarization_module_loaded() -> None:
    # Reload the module fresh and confirm pyannote stayed out of sys.modules.
    importlib.reload(dz)
    assert "pyannote" not in sys.modules
    assert "pyannote.audio" not in sys.modules
