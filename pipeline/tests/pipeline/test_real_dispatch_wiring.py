"""Track R1 wiring: real dispatch override map + SCAN in defaults.

These guard the two integration seams the per-stage adapters need:

- :func:`maktaba_pipeline.handlers.build_real_dispatch` returns a map
  the runtime's ``dispatch_overrides`` accepts, with PROBE bound to the
  real adapter and the not-yet-wired stages (notably THUMBNAIL, which
  has no implementing module) left off so they keep the placeholder.
- ``__main__._DEFAULT_STAGES`` lists ``Stage.SCAN`` first again so a
  default worker claims scan jobs.
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.__main__ import _DEFAULT_STAGES
from maktaba_pipeline.db.jobs import Stage
from maktaba_pipeline.handlers import build_real_dispatch, probe_handler

pytestmark = pytest.mark.unit


def test_build_real_dispatch_binds_probe_only() -> None:
    overrides = build_real_dispatch()
    assert overrides[Stage.PROBE] is probe_handler
    # Stages without a thin-wrapper adapter must stay on the runtime
    # placeholder — registering a half-built handler would be worse
    # than the no-op drain. THUMBNAIL has no module at all.
    for stage in (
        Stage.EXTRACT,
        Stage.TRANSCRIBE,
        Stage.SUBTITLE_GEN,
        Stage.INDEX,
        Stage.THUMBNAIL,
        Stage.SCAN,
    ):
        assert stage not in overrides


def test_default_stages_restores_scan_first() -> None:
    assert _DEFAULT_STAGES[0] is Stage.SCAN
    # The remaining canonical pipeline stages are still present.
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
