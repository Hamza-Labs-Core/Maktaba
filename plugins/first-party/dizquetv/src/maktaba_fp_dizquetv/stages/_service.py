"""What every dizqueTV stage of the plugin shares (issue #827): one service per stage whose
clients go through the home's service door when the job runs in the plugin's runner (dizqueTV
through the door's operations, Plex through its allow-list), and the job's binding to that
door."""

from __future__ import annotations

import contextlib
from collections.abc import Iterator
from typing import Any

from maktaba_pipeline.extensions.dizquetv.audit import AuditService
from maktaba_pipeline.extensions.dizquetv.channels import ChannelsService
from maktaba_pipeline.extensions.dizquetv.shows import ShowsService
from maktaba_pipeline.integrations.dizquetv import tz_offset_minutes
from maktaba_pipeline.plugins.service import bind, dizquetv_client, plex_client

SHOWS = ShowsService(dizquetv_client, plex_client)
CHANNELS = ChannelsService(dizquetv_client, plex_client, tz_offset_minutes)
AUDIT = AuditService(dizquetv_client)


async def write_mode(db: Any, stage: str) -> str:
    row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", stage)
    return "auto" if row is None else str(row["write_mode"])


@contextlib.contextmanager
def door(db: Any, ctx: Any) -> Iterator[None]:
    """In the runner (the worker set ``service_via``), the job's dizqueTV and Plex calls go
    through the door; in-process the clients are the worker's own."""
    via = getattr(ctx, "service_via", None)
    if via is None:
        yield
        return
    with bind(db, str(via)):
        yield
