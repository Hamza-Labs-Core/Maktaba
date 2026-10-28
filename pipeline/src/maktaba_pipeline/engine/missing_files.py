"""Missing files at the scan (Epic 40, story 40.3).

A ``file_missing`` failure on a share that answers is ``item_permanent``: it needs an
admin, but the share may have been half mounted or the file renamed back since. After
every completed scan of a library whose roots all answer, each video with an open
``file_missing`` failure (its latest job of a stage failed with that cause) is looked at
again:

* the file is there → ``videos.missing_confirmed_at`` is cleared and every stage whose
  latest job for the video failed is queued again (forced);
* still gone → ``missing_confirmed_at`` is set (the failures page says "confirmed by the
  scan", and offers a re-download for a YouTube video).

A library with a root that does not answer is skipped: a missing share says nothing about
its files.
"""

from __future__ import annotations

import asyncio
import os
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from .failure_policy import _roots, read_config, root_answers
from .paths import find_existing, to_local
from .sql import now, to_ts, uid
from .stages.access_check import requeue_failed

__all__ = ["MissingReport", "confirm_missing"]

_OPEN_MISSING = """
SELECT DISTINCT v.id AS id, v.path AS path
  FROM processing_jobs j
  JOIN videos v ON v.id = j.subject_id
 WHERE j.subject_kind = 'video' AND v.library_id = $1 AND j.state = 'failed'
   AND j.error_cause LIKE 'file_missing%'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
"""


@dataclass(frozen=True, slots=True)
class MissingReport:
    checked: bool
    healed: int = 0
    confirmed: int = 0


def _exists(path: str) -> bool:
    return os.path.exists(find_existing(to_local(path)))


async def confirm_missing(db: Any, library_id: UUID) -> MissingReport:
    cfg = await read_config(db)
    row = await db.fetchrow("SELECT roots FROM libraries WHERE id = $1", uid(library_id))
    roots = _roots(row["roots"]) if row is not None else ()
    if not roots:
        return MissingReport(checked=False)
    for root in roots:
        if not await root_answers(root, cfg.probe_timeout_seconds):
            return MissingReport(checked=False)
    healed = confirmed = 0
    t = to_ts(db, now())
    for r in await db.fetch(_OPEN_MISSING, uid(library_id)):
        vid = UUID(str(r["id"]))
        found = await asyncio.to_thread(_exists, str(r["path"]))
        if not found:
            # story 47.5: a byte-identical copy still on disk takes the row's place
            from ..scanner.copies import promote_copy

            found = await promote_copy(db, vid) is not None
        if found:
            await db.execute(
                "UPDATE videos SET missing_confirmed_at = NULL WHERE id = $1", uid(vid)
            )
            await requeue_failed(db, vid, "the scan found the file again")
            healed += 1
        else:
            await db.execute(
                "UPDATE videos SET missing_confirmed_at = $2"
                " WHERE id = $1 AND missing_confirmed_at IS NULL",
                uid(vid),
                t,
            )
            confirmed += 1
    return MissingReport(True, healed, confirmed)
