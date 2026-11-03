"""Component settings moved to this release's shape, once, on the home (Epic 51, story
51.13).

Every rules section has a schema version (``settings.schema_version``); the version its
stored value was written at is ``app_settings`` key ``rules_version.<section>`` (missing:
1.0). At start the home's pipeline checks every section, whatever its component's state:

- same major: nothing to do (an older minor is written up to the code's);
- an older major: the section's migrations run on the stored value, the result is checked
  in full, and the new value, the new version (with ``previous`` = the old major and value)
  and an ``audit_log`` row (``category = 'components'``, ``action = 'settings.migrate'``)
  are written in one transaction;
- a migration that raises, or a result that does not validate: nothing is written to the
  section, the version key gets ``"failed": "<error>"``, and the owning component is
  reported Failed with ``settings_invalid`` (status part :data:`PART`);
- a newer major (a rollback): left as it is; the section is read field by field.

Each section is done under the same advisory lock the API's ``PATCH /api/rules/{section}``
takes, and the write compares the stored version first, so a second worker (or a second
home during a switchover) finds nothing to do. Nodes never migrate; they read what the home
wrote (``rules.read`` moves an older value in memory if it gets there first).

:func:`watch` re-reports the Failed components every :data:`WATCH_S` seconds, so a section
an admin saves in full (``replace``) leaves Failed within that time.

The component runner (story 51.06) can call :func:`blocked` to keep a component whose
settings failed from starting.
"""

from __future__ import annotations

import asyncio
import contextlib
import dataclasses
from collections.abc import Iterable, Mapping
from typing import Any

from .. import rules, settings
from ..engine.sql import dumps, is_pg, loads
from ..log import get_logger
from . import store
from .catalog import Catalog, default

__all__ = [
    "AUDIT_ACTION",
    "PART",
    "REASON",
    "WATCH_S",
    "Outcome",
    "blocked",
    "failed_sections",
    "migrate_all",
    "migrate_section",
    "owner",
    "owners",
    "report",
    "run_pass",
    "watch",
]

_log = get_logger()

#: the status part the settings check reports under (its own: the stage runner's ``pipeline``
#: report never prunes it)
PART = "settings"
REASON = "settings_invalid"
AUDIT_ACTION = "settings.migrate"
#: how often :func:`watch` re-reads the version keys
WATCH_S = 15.0
#: who a migration's audit row names
_BY = "pipeline"
#: the component a section no component names belongs to (core, always on)
_CORE = "core.pipeline"


@dataclasses.dataclass(frozen=True, slots=True)
class Outcome:
    """What the pass did with a section: ``none``, ``minor`` (the minor written up),
    ``migrated``, ``failed`` (with the error) or ``newer`` (stored by a later release)."""

    section: str
    action: str
    error: str = ""
    from_version: tuple[int, int] = (1, 0)
    to_version: tuple[int, int] = (1, 0)


def owners(catalog: Catalog | None = None) -> dict[str, str]:
    """section -> the id of the component whose catalog ``settings`` name it."""
    out: dict[str, str] = {}
    for c in (catalog or default()).components():
        for s in c.settings:
            if s.startswith(rules.KEY_PREFIX):
                out[s.removeprefix(rules.KEY_PREFIX)] = c.id
    return out


def owner(section: str, catalog: Catalog | None = None) -> str:
    """The component that owns ``section`` (``core.pipeline`` when none names it)."""
    return owners(catalog).get(section, _CORE)


async def _row(db: Any, key: str) -> Any:
    row = await db.fetchrow("SELECT value FROM app_settings WHERE key = $1", key)
    return None if row is None else row["value"]


async def _put(db: Any, key: str, value: Any) -> None:
    if is_pg(db):
        await db.execute(
            "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2::jsonb, now())"
            " ON CONFLICT (key) DO UPDATE SET value = excluded.value,"
            " updated_at = excluded.updated_at",
            key,
            dumps(value),
        )
    else:
        await db.execute(
            "INSERT INTO app_settings (key, value, updated_at)"
            " VALUES ($1, $2, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))"
            " ON CONFLICT (key) DO UPDATE SET value = excluded.value,"
            " updated_at = excluded.updated_at",
            key,
            dumps(value),
        )


async def _audit(db: Any, component: str, payload: Mapping[str, Any]) -> None:
    cast = "::jsonb" if is_pg(db) else ""
    await db.execute(
        "INSERT INTO audit_log (category, action, target_kind, target_id, actor_source, payload)"
        f" VALUES ($1, $2, 'component', $3, $4, $5{cast})",
        "components",
        AUDIT_ACTION,
        component,
        _BY,
        dumps(dict(payload)),
    )


async def migrate_section(db: Any, section: str, *, catalog: Catalog | None = None) -> Outcome:
    """Check one section and move it to the code's version (see the module text)."""
    cls = rules.SECTIONS[section][0]
    code = settings.schema_version(cls)
    key, vkey = rules.KEY_PREFIX + section, rules.VERSION_PREFIX + section
    async with db.transaction():
        if is_pg(db):
            # the API's PATCH of this section takes the same lock
            await db.execute("SELECT pg_advisory_xact_lock(hashtext($1))", key)
        raw, vraw = await _row(db, key), await _row(db, vkey)
        stored = loads(raw) if raw is not None else None
        v = rules.parse_version(vraw)
        have = (v.major, v.minor)
        if v.major > code[0]:
            return Outcome(section, "newer", from_version=have, to_version=code)
        if v.major == code[0]:
            if v.failed:
                # a full save cleared the shape problem but not the mark: keep it, the
                # API's replace is what clears it
                return Outcome(section, "failed", v.failed, have, code)
            if v.minor < code[1] and (raw is not None or vraw is not None):
                await _put(db, vkey, dataclasses.replace(v, minor=code[1]).to_json())
                return Outcome(section, "minor", from_version=have, to_version=code)
            return Outcome(section, "none", from_version=have, to_version=code)
        # an older major
        if not isinstance(stored, dict) or not stored:
            # nothing stored to move: the version key is written on the first save
            if vraw is not None:
                await _put(db, vkey, {"major": code[0], "minor": code[1]})
            return Outcome(section, "none", from_version=have, to_version=code)
        try:
            moved = settings.migrate(cls, stored, v.major)
        except settings.MigrationError as e:
            error = _one_line(str(e))
            if v.failed != error:
                await _put(db, vkey, dataclasses.replace(v, failed=error).to_json())
            return Outcome(section, "failed", error, have, code)
        version = {
            "major": code[0],
            "minor": code[1],
            "previous": {"major": v.major, "value": stored},
        }
        await _put(db, key, moved)
        await _put(db, vkey, version)
        await _audit(
            db,
            owner(section, catalog),
            {
                "section": section,
                "by": _BY,
                "before": {"version": [v.major, v.minor], "value": stored},
                "after": {"version": list(code), "value": moved},
            },
        )
    _log.info(
        "settings_migrated",
        section=section,
        from_version=f"{v.major}.{v.minor}",
        to_version=f"{code[0]}.{code[1]}",
    )
    return Outcome(section, "migrated", from_version=have, to_version=code)


def _one_line(s: str) -> str:
    line = " ".join(s.split())
    return line[:500]


async def migrate_all(
    db: Any, sections: Iterable[str] | None = None, *, catalog: Catalog | None = None
) -> list[Outcome]:
    """:func:`migrate_section` for every section (or ``sections``). One section's database
    error is logged and the others still run."""
    out: list[Outcome] = []
    for section in sections if sections is not None else rules.SECTIONS:
        try:
            out.append(await migrate_section(db, section, catalog=catalog))
        except Exception as exc:
            if rules._missing_table(exc):  # noqa: SLF001 - a bare database: nothing stored
                return out
            _log.exception("settings_migrate_failed", section=section)
    for o in out:
        if o.action == "failed":
            _log.warning(
                REASON, section=o.section, component=owner(o.section, catalog), error=o.error
            )
    return out


async def failed_sections(db: Any) -> dict[str, str]:
    """section -> error, for every ``rules_version.<section>`` marked failed."""
    try:
        rows = await db.fetch(
            "SELECT key, value FROM app_settings WHERE key LIKE $1", rules.VERSION_PREFIX + "%"
        )
    except Exception as exc:
        if rules._missing_table(exc):  # noqa: SLF001
            return {}
        raise
    out: dict[str, str] = {}
    for r in rows:
        section = str(r["key"]).removeprefix(rules.VERSION_PREFIX)
        v = rules.parse_version(r["value"])
        if v.failed and section in rules.SECTIONS:
            out[section] = v.failed
    return out


def blocked(
    failed: Mapping[str, str], catalog: Catalog | None = None
) -> dict[str, list[tuple[str, str]]]:
    """component -> [(section, error)] for the components that own a failed section."""
    out: dict[str, list[tuple[str, str]]] = {}
    for section, error in sorted(failed.items()):
        out.setdefault(owner(section, catalog), []).append((section, error))
    return out


async def report(db: Any, failed: Mapping[str, str], *, catalog: Catalog | None = None) -> None:
    """The home's status part :data:`PART`: Failed with ``settings_invalid`` (params
    ``{section, error}``) for each component that owns a failed section; none otherwise
    (the empty report clears what an earlier one said)."""
    cat = catalog or default()
    try:
        desired, _ = await store.desired(db)
    except Exception:  # noqa: BLE001 - the generation is a nicety; the report is not
        desired = {}
    rows: list[store.Status] = []
    for cid, items in sorted(blocked(failed, cat).items()):
        section, error = items[0]
        d = desired.get(cid)
        rows.append(
            store.Status(
                id=cid,
                phase="failed",
                observed_generation=d.generation if d is not None else 0,
                conditions=(
                    store.Condition(
                        type="Ready",
                        status="false",
                        reason=REASON,
                        params={"section": section, "error": error},
                    ),
                ),
                detail={"sections": [s for s, _ in items]},
            )
        )
    await store.report_status(db, store.HOME, PART, rows, catalog=cat)


async def run_pass(db: Any, *, catalog: Catalog | None = None) -> dict[str, str]:
    """The start-up pass: every section checked and moved, then the Failed components
    reported. Answers the failed sections (section -> error)."""
    await migrate_all(db, catalog=catalog)
    failed = await failed_sections(db)
    try:
        await report(db, failed, catalog=catalog)
    except Exception as exc:
        if not rules._missing_table(exc):  # noqa: SLF001 - no component tables yet
            raise
    return failed


async def watch(
    db: Any,
    shutdown: asyncio.Event,
    *,
    interval: float = WATCH_S,
    catalog: Catalog | None = None,
) -> None:
    """Re-report the Failed components whenever the failed sections change, until
    ``shutdown``. A database error is logged and tried again on the next round."""
    last: dict[str, str] | None = None
    while not shutdown.is_set():
        try:
            failed = await failed_sections(db)
            if failed != last:
                await report(db, failed, catalog=catalog)
                last = failed
        except Exception:  # noqa: BLE001 - a loop that must outlive one bad round
            _log.exception("settings_watch_failed")
        with contextlib.suppress(TimeoutError):
            await asyncio.wait_for(shutdown.wait(), timeout=interval)
