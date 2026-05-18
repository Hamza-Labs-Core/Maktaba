"""Track R1 wiring: real dispatch override map + default stages.

These guard the two integration seams the per-stage adapters need:

- :func:`maktaba_pipeline.handlers.build_real_dispatch` returns a map
  the runtime's ``dispatch_overrides`` accepts, with SCAN/PROBE/EXTRACT/
  TRANSCRIBE bound to their real adapters and the not-yet-wired stages
  (notably THUMBNAIL, which has no implementing module) left off so they
  keep the placeholder.
- ``__main__._DEFAULT_STAGES`` must be a subset of
  ``build_real_dispatch()``'s keys: every default stage needs a real
  handler. A stage that only has the runtime's no-op placeholder
  (SUBTITLE_GEN, INDEX, THUMBNAIL) would be silently marked ``done`` by
  a default worker without doing the work — the same silent-drain
  foot-gun that was caught for SCAN. TRANSCRIBE now enqueues
  SUBTITLE_GEN + INDEX, so this invariant must hold for the whole
  default set.
- ``__main__._DEFAULT_STAGES`` now lists ``Stage.SCAN``: gap-closure
  (HLB-257/255) landed slot 0058 + ``SqlScanStore`` + the real
  ``scan_handler``, so a default worker claiming a SCAN job runs the
  real library walk instead of the silent no-op drain. SCAN leads the
  tuple because it is the pipeline's entry stage.

  History: before gap-closure SCAN was deliberately *excluded* from
  the defaults and the override map precisely because it had no real
  handler — marking a SCAN job ``done`` without scanning would have
  silently "completed" a library that was never walked. That hazard is
  now resolved by the real adapter, so the exclusion is lifted.
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.__main__ import _DEFAULT_STAGES
from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import (
    build_real_dispatch,
    extract_handler,
    probe_handler,
    scan_handler,
    transcribe_handler,
)

pytestmark = pytest.mark.unit


def test_build_real_dispatch_binds_scan_probe_extract_and_transcribe() -> None:
    overrides = build_real_dispatch()
    # Gap-closure: SCAN now has a real library-scoped thin-wrapper
    # adapter (slot 0058 + SqlScanStore + Story 1.1 orchestrator).
    assert overrides[Stage.SCAN] is scan_handler
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
    # Stages without a thin-wrapper adapter must stay on the runtime
    # placeholder — registering a half-built handler would be worse
    # than the no-op drain. THUMBNAIL has no module at all; SUBTITLE_GEN
    # / INDEX consume the transcript TRANSCRIBE now produces but their
    # real orchestration is still pending.
    for stage in (
        Stage.SUBTITLE_GEN,
        Stage.INDEX,
        Stage.THUMBNAIL,
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

    # Gap-closure (HLB-257/255): SCAN now HAS a real handler — it is
    # bound in build_real_dispatch() to scan_handler, which walks the
    # library and enqueues per-video PROBE jobs. So SCAN is safe in the
    # defaults and leads the tuple as the pipeline's entry stage; the
    # per-video PROBE -> EXTRACT -> TRANSCRIBE chain follows.
    assert _DEFAULT_STAGES == (
        Stage.SCAN,
        Stage.PROBE,
        Stage.EXTRACT,
        Stage.TRANSCRIBE,
    )
    assert _DEFAULT_STAGES[0] is Stage.SCAN
    assert Stage.SCAN in overrides
    assert Stage.PROBE in overrides
    assert Stage.EXTRACT in overrides
    assert Stage.TRANSCRIBE in overrides

    # The full canonical pipeline is now present in the defaults.
    assert set(_DEFAULT_STAGES) >= {
        Stage.SCAN,
        Stage.PROBE,
        Stage.EXTRACT,
        Stage.TRANSCRIBE,
    }

    # SUBTITLE_GEN / INDEX / THUMBNAIL have no real handler yet (only
    # the runtime placeholder). TRANSCRIBE now enqueues SUBTITLE_GEN +
    # INDEX, so leaving any of these in the defaults would silently
    # no-op-drain real work — they must be excluded from both the
    # override map and the defaults until their adapters land.
    for stage in (
        Stage.SUBTITLE_GEN,
        Stage.INDEX,
        Stage.THUMBNAIL,
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
