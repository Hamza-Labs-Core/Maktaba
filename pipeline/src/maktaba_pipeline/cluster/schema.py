"""The schema guard of a joined machine (Epic 31 design section 5.1).

A node's code is written against the migrations it ships: :data:`SCHEMA_REV` is the
highest slot in ``shared/db/migrations`` (a test keeps it equal). The home migrates its
own database at start; a node never does. So before a node claims anything, and again
at every storage check (every refresh while paused), it compares:

- **home behind** (the home's applied version < :data:`SCHEMA_REV`): the node's code may
  touch tables and columns the home does not have. The node pauses: it claims nothing
  and runs no writer (channel top-up, reconciler, pruner, reaper, storage probe) until
  the home is updated; then it resumes by itself.
- **home ahead**: newer migrations are additive by rule, so the node keeps running and
  the per-stage ``version_ahead`` check keeps it off the stages whose definition moved
  on. A migration that breaks older code declares it: it (re)defines
  ``maktaba_min_node_schema()`` to return the oldest node schema still compatible (its
  own slot, normally). A node whose :data:`SCHEMA_REV` is below that pauses too.

The applied version is ``max(goose_db_version.version_id) where is_applied``; the
machines' role (``maktaba_worker``) can read it (slot 0103 granted every table of the
schema, goose's own included). A home whose version cannot be read is not judged: the
node runs as before and says so once in its log.

The pause shows on the Machines page as the reason of every stage
(``paused: home database at 0119, this machine needs 0124 — update the home``): an older
home's page shows the text as is, a current one translates it.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

from ..engine.sql import is_pg
from ..log import get_logger

__all__ = [
    "MIN_NODE_FUNCTION",
    "REASON_PAUSED",
    "SCHEMA_REV",
    "HomeSchema",
    "highest_slot",
    "pause_reason",
    "read_home_schema",
]

#: the highest migration slot this code ships (``shared/db/migrations``); a test fails
#: when a migration is added without raising it
SCHEMA_REV = 137

#: the reason code every stage gets while the machine is paused
REASON_PAUSED = "paused"

#: what a migration that breaks older code (re)defines: ``RETURNS integer``, the oldest
#: node schema it still works with
MIN_NODE_FUNCTION = "maktaba_min_node_schema"

_SLOT_RE = re.compile(r"^(\d{4})_[a-z0-9_]+\.sql$")

_log = get_logger()


@dataclass(frozen=True, slots=True)
class HomeSchema:
    """What the home's database says about its schema."""

    #: the applied goose version; None when it cannot be read (not judged)
    applied: int | None
    #: ``maktaba_min_node_schema()``; None when the home declares no breaking change
    min_node: int | None = None


async def read_home_schema(db: Any) -> HomeSchema:
    """The home's applied version and its oldest compatible node schema.

    SQLite (tests, a single machine) has no goose table to read: never judged."""
    if not is_pg(db):
        return HomeSchema(applied=None)
    applied: int | None
    try:
        v = await db.fetchval(
            "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied"
        )
        applied = int(v) if v is not None else None
    except Exception as exc:  # noqa: BLE001 - no table / no right: not judged
        _log.debug("home_schema_unreadable", error=str(exc))
        applied = None
    min_node: int | None = None
    try:
        if await db.fetchval(f"SELECT to_regproc('{MIN_NODE_FUNCTION}') IS NOT NULL"):
            v = await db.fetchval(f"SELECT {MIN_NODE_FUNCTION}()")
            min_node = int(v) if v is not None else None
    except Exception as exc:  # noqa: BLE001 - an unreadable declaration declares nothing
        _log.debug("home_min_node_schema_unreadable", error=str(exc))
    return HomeSchema(applied=applied, min_node=min_node)


def pause_reason(home: HomeSchema, required: int = SCHEMA_REV) -> str | None:
    """``paused: <why>`` when this machine must not run against ``home``, else None."""
    if home.applied is not None and home.applied < required:
        return (
            f"{REASON_PAUSED}: home database at {home.applied:04d}, "
            f"this machine needs {required:04d} — update the home"
        )
    if home.min_node is not None and home.min_node > required:
        at = f" at {home.applied:04d}" if home.applied is not None else ""
        return (
            f"{REASON_PAUSED}: home database{at} needs this machine at {home.min_node:04d} "
            f"or newer (it has {required:04d}) — update this machine"
        )
    return None


def highest_slot(names: Any) -> int:
    """The highest ``NNNN_<topic>.sql`` slot among file names (tests, tools)."""
    best = 0
    for name in names:
        m = _SLOT_RE.match(str(name))
        if m and not str(name).endswith(".sqlite.sql"):
            best = max(best, int(m.group(1)))
    return best
