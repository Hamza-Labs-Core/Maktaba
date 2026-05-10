"""Re-export of :class:`Stage` for the path the story names.

Story 1.6's acceptance criteria reference
``pipeline.src.maktaba_pipeline.domain.stages``; the actual enum lives
alongside :class:`State` in :mod:`.states` so both stay in lockstep
with the canonical manifest. This module is the import-path the rest
of the codebase should use when it only needs the stage names.
"""

from __future__ import annotations

from .states import Stage

__all__ = ["Stage"]
