"""What every Plex stage of the plugin shares (story 54.35): one PlexService (its index
cache serves every job, as in-process) whose client goes through the home's service door
when the job runs in the plugin's runner, and the job's binding to that door."""

from __future__ import annotations

import contextlib
from collections.abc import Iterator
from typing import Any

from maktaba_pipeline.extensions.plex.collection import PlexService
from maktaba_pipeline.plugins.service import bind, plex_client

SERVICE = PlexService(plex_client)


@contextlib.contextmanager
def door(db: Any, ctx: Any) -> Iterator[None]:
    """In the runner (the worker set ``service_via``), the job's Plex calls go through the
    door; in-process the client is the worker's own."""
    via = getattr(ctx, "service_via", None)
    if via is None:
        yield
        return
    with bind(db, str(via)):
        yield
