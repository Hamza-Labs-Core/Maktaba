"""Track R1 wiring: real dispatch override map + default stages.

These guard the two integration seams the per-stage adapters need:

- :func:`maktaba_pipeline.handlers.build_real_dispatch` returns a map
  the runtime's ``dispatch_overrides`` accepts, with PROBE bound to the
  real adapter and the not-yet-wired stages (notably THUMBNAIL, which
  has no implementing module) left off so they keep the placeholder.
- ``__main__._DEFAULT_STAGES`` must NOT list ``Stage.SCAN``: SCAN has
  no real handler (it is absent from ``build_real_dispatch()`` and only
  gets the runtime's no-op placeholder), so a default worker claiming
  SCAN jobs would silently mark them ``done`` without scanning.
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.__main__ import _DEFAULT_STAGES
from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import build_real_dispatch, extract_handler, probe_handler

pytestmark = pytest.mark.unit


def test_build_real_dispatch_binds_probe_and_extract() -> None:
    overrides = build_real_dispatch()
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
        Stage.SCAN,
    ):
        assert stage not in overrides


def test_default_stages_excludes_scan_without_real_handler() -> None:
    # SCAN has no real handler — it is absent from build_real_dispatch()
    # and only gets the runtime's no-op placeholder. A default worker
    # claiming SCAN jobs would silently mark them ``done`` without ever
    # scanning, so SCAN must stay out of the defaults until it has a
    # real adapter.
    assert Stage.SCAN not in _DEFAULT_STAGES
    assert _DEFAULT_STAGES[0] is Stage.PROBE

    overrides = build_real_dispatch()
    assert Stage.PROBE in overrides
    # EXTRACT has a real handler now and is safe in the defaults: a
    # default worker claiming an EXTRACT job runs the real adapter
    # rather than the silent no-op drain.
    assert Stage.EXTRACT in overrides
    assert Stage.SCAN not in overrides

    # The remaining canonical pipeline stages are still present.
    assert set(_DEFAULT_STAGES) >= {
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
