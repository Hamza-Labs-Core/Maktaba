"""Track R1 wiring: real dispatch override map + default stages.

These guard the two integration seams the per-stage adapters need:

- :func:`maktaba_pipeline.handlers.build_real_dispatch` returns a map
  the runtime's ``dispatch_overrides`` accepts, with SCAN/PROBE/EXTRACT
  bound to their real adapters and the not-yet-wired stages (notably
  THUMBNAIL, which has no implementing module) left off so they keep
  the placeholder.
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
)

pytestmark = pytest.mark.unit


def test_build_real_dispatch_binds_scan_probe_and_extract() -> None:
    overrides = build_real_dispatch()
    # Gap-closure: SCAN now has a real library-scoped thin-wrapper
    # adapter (slot 0058 + SqlScanStore + Story 1.1 orchestrator).
    assert overrides[Stage.SCAN] is scan_handler
    assert overrides[Stage.PROBE] is probe_handler
    # Track R2: EXTRACT now has a real thin-wrapper adapter
    # (commit_extract persists the audio_cache artifact, advances the
    # FSM, and enqueues TRANSCRIBE), so it joins the override map.
    assert overrides[Stage.EXTRACT] is extract_handler
    # Stages without a thin-wrapper adapter must stay on the runtime
    # placeholder — registering a half-built handler would be worse
    # than the no-op drain. THUMBNAIL has no module at all.
    for stage in (
        Stage.TRANSCRIBE,
        Stage.SUBTITLE_GEN,
        Stage.INDEX,
        Stage.THUMBNAIL,
    ):
        assert stage not in overrides


def test_default_stages_includes_scan_with_real_handler() -> None:
    # Gap-closure (HLB-257/255): SCAN now HAS a real handler — it is
    # bound in build_real_dispatch() to scan_handler, which walks the
    # library and enqueues per-video PROBE jobs. So SCAN is safe in the
    # defaults: a default worker claiming a SCAN job does real work
    # rather than the silent no-op drain. It leads the tuple as the
    # pipeline's entry stage.
    assert Stage.SCAN in _DEFAULT_STAGES
    assert _DEFAULT_STAGES[0] is Stage.SCAN

    overrides = build_real_dispatch()
    assert Stage.SCAN in overrides
    assert Stage.PROBE in overrides
    # EXTRACT has a real handler now and is safe in the defaults: a
    # default worker claiming an EXTRACT job runs the real adapter
    # rather than the silent no-op drain.
    assert Stage.EXTRACT in overrides

    # The full canonical pipeline is now present in the defaults.
    assert set(_DEFAULT_STAGES) >= {
        Stage.SCAN,
        Stage.PROBE,
        Stage.EXTRACT,
        Stage.TRANSCRIBE,
        Stage.SUBTITLE_GEN,
        Stage.INDEX,
        Stage.THUMBNAIL,
    }


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
