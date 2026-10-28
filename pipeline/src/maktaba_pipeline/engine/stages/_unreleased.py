"""A stage that is in the graph before its story ships (Epic 47, story 47.1).

The graph sync keeps such a stage off (``StageDef.unreleased``), so nothing claims it; if
a job reaches a worker anyway (an operator turned it on), it fails at once with a reason
that says why, instead of a missing handler.
"""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ..context import StageContext, StageFailure
from ..graph import StageResult
from .registry import StageImpl

__all__ = ["unreleased_impl"]


def unreleased_impl(stage: str, story: str) -> StageImpl:
    """An implementation that fails every job: ``stage`` ships with ``story``."""

    async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
        raise StageFailure(
            "not_implemented",
            f"{stage} is not implemented yet (Epic 47, story {story})",
            retryable=False,
        )

    return StageImpl(run=run)
