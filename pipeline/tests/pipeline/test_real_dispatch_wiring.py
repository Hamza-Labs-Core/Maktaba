"""Track R1 wiring: real dispatch override map + default stages.

These guard the two integration seams the per-stage adapters need:

- :func:`maktaba_pipeline.handlers.build_real_dispatch` returns a map
  the runtime's ``dispatch_overrides`` accepts, with PROBE bound to the
  real adapter and the not-yet-wired stages (notably THUMBNAIL, which
  has no implementing module) left off so they keep the placeholder.
- ``__main__._DEFAULT_STAGES`` must be a subset of
  ``build_real_dispatch()``'s keys: every default stage needs a real
  handler. A stage that only has the runtime's no-op placeholder (SCAN,
  INDEX, THUMBNAIL) would be silently marked ``done`` by a default
  worker without doing the work — the same silent-drain foot-gun that
  was caught for SCAN. TRANSCRIBE enqueues SUBTITLE_GEN + INDEX, so
  this invariant must hold for the whole default set, not just SCAN.
  SUBTITLE_GEN now has a real adapter (Track R4) and joins both the
  override map and the defaults; INDEX still does not, so it stays out
  of both.
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.__main__ import _DEFAULT_STAGES
from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import (
    build_real_dispatch,
    extract_handler,
    probe_handler,
    subtitle_handler,
    transcribe_handler,
)

pytestmark = pytest.mark.unit


def test_build_real_dispatch_binds_probe_extract_transcribe_subtitle() -> None:
    overrides = build_real_dispatch()
    assert overrides[Stage.PROBE] is probe_handler
    # Track R2: EXTRACT now has a real thin-wrapper adapter
    # (commit_extract persists the audio_cache artifact, advances the
    # FSM, and enqueues TRANSCRIBE), so it joins the override map.
    assert overrides[Stage.EXTRACT] is extract_handler
    # Track R3: TRANSCRIBE now has a real thin-wrapper adapter
    # (commit_transcribe creates + activates the transcript, persists
    # every backend segment via commit_segment, advances the FSM
    # AUDIO_EXTRACTED -> TRANSCRIBED, and enqueues SUBTITLE_GEN +
    # INDEX), so it joins the override map too.
    assert overrides[Stage.TRANSCRIBE] is transcribe_handler
    # Track R4: SUBTITLE_GEN now has a real thin-wrapper adapter
    # (commit_subtitles loads the transcript's ordered segments,
    # renders SRT + VTT via the existing generator, persists both via
    # the pre-existing subtitle_files registry, and advances the FSM
    # TRANSCRIBED -> INDEXED). It is a leaf — TRANSCRIBE already fanned
    # it out in parallel with INDEX — so it joins the override map.
    assert overrides[Stage.SUBTITLE_GEN] is subtitle_handler
    # Stages without a thin-wrapper adapter must stay on the runtime
    # placeholder — registering a half-built handler would be worse
    # than the no-op drain. THUMBNAIL has no module at all; INDEX
    # consumes the transcript TRANSCRIBE produces but its real
    # orchestration is still pending; SCAN has no real handler.
    for stage in (
        Stage.INDEX,
        Stage.THUMBNAIL,
        Stage.SCAN,
    ):
        assert stage not in overrides


def test_default_stages_subset_of_real_handlers() -> None:
    # Generic, future-proof invariant: every stage a default worker
    # claims must have a REAL handler in build_real_dispatch(). A stage
    # that only has the runtime's no-op placeholder would be silently
    # marked ``done`` without doing the work (the silent-drain
    # foot-gun). This auto-guards any future stage added to either set.
    overrides = build_real_dispatch()
    assert set(_DEFAULT_STAGES) <= set(overrides), (
        "placeholder-only stage(s) in _DEFAULT_STAGES would silently "
        f"no-op-drain: {set(_DEFAULT_STAGES) - set(overrides)}"
    )

    # The current real-handler stages, in pipeline order. PROBE leads;
    # SUBTITLE_GEN joins the tail (Track R4 — it now has a real adapter
    # so a default worker runs the real render + persist).
    assert _DEFAULT_STAGES == (
        Stage.PROBE,
        Stage.EXTRACT,
        Stage.TRANSCRIBE,
        Stage.SUBTITLE_GEN,
    )
    assert _DEFAULT_STAGES[0] is Stage.PROBE
    assert Stage.PROBE in overrides
    assert Stage.EXTRACT in overrides
    assert Stage.TRANSCRIBE in overrides
    assert Stage.SUBTITLE_GEN in overrides

    # INDEX / THUMBNAIL / SCAN have no real handler yet (only the
    # runtime placeholder). TRANSCRIBE enqueues SUBTITLE_GEN + INDEX,
    # so leaving INDEX in the defaults would silently no-op-drain real
    # work — they must be excluded from both the override map and the
    # defaults until their adapters land.
    for stage in (
        Stage.INDEX,
        Stage.THUMBNAIL,
        Stage.SCAN,
    ):
        assert stage not in overrides
        assert stage not in _DEFAULT_STAGES


def test_real_dispatch_is_runtime_compatible() -> None:
    """The override map must satisfy the runtime's StageHandler typing.

    ``build_default_dispatch`` does ``table.update(overrides)`` then
    looks each handler up by ``job.stage`` — so values must be awaitable
    ``(db, job)`` callables. ``probe_handler``'s extra keyword arg has a
    default, so it is call-compatible.
    """
    from maktaba_pipeline.runtime import build_default_dispatch

    dispatch = build_default_dispatch(build_real_dispatch())
    assert callable(dispatch)
