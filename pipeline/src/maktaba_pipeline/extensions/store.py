"""Extension state: enabled flag and settings, stored in the ``extensions`` table.

Environment variables are only a bootstrap: they seed the row the first time an
extension is seen. After that the database is the source of truth, and secret
values are never returned by :func:`public_settings`.
"""

from __future__ import annotations

import os
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from typing import Any

from ..engine.sql import dumps, is_pg, loads, now, to_ts
from .base import Extension, Manifest

__all__ = [
    "REDACTED",
    "ExtensionState",
    "ensure_rows",
    "env_defaults",
    "load_state",
    "public_settings",
    "save_settings",
    "set_enabled",
]

REDACTED = "********"


@dataclass(frozen=True, slots=True)
class ExtensionState:
    name: str
    enabled: bool
    settings: Mapping[str, str]
    configured: bool


def env_defaults(manifest: Manifest) -> dict[str, str]:
    out: dict[str, str] = {}
    for f in manifest.settings:
        value = os.environ.get(f.env, "") if f.env else ""
        out[f.key] = value or f.default
    return out


async def ensure_rows(db: Any, extensions: Iterable[Extension]) -> None:
    """Insert a row per extension on first sight, seeded from the environment.

    An extension whose required settings are all present in the environment
    starts enabled; otherwise it starts disabled.
    """
    for ext in extensions:
        existing = await db.fetchrow(
            "SELECT name FROM extensions WHERE name = $1", ext.manifest.name
        )
        if existing is not None:
            continue
        defaults = env_defaults(ext.manifest)
        await db.execute(
            "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1,$2,$3,$4)"
            " ON CONFLICT (name) DO NOTHING",
            ext.manifest.name,
            bool(ext.is_configured(defaults)),
            dumps(defaults),
            to_ts(db, now()),
        )


async def load_state(db: Any, ext: Extension) -> ExtensionState:
    row = await db.fetchrow(
        "SELECT enabled, settings FROM extensions WHERE name = $1", ext.manifest.name
    )
    stored: dict[str, str] = {}
    enabled = False
    if row is not None:
        enabled = bool(row["enabled"])
        stored = {str(k): str(v) for k, v in (loads(row["settings"]) or {}).items()}
    merged = {**env_defaults(ext.manifest), **{k: v for k, v in stored.items() if v != ""}}
    return ExtensionState(ext.manifest.name, enabled, merged, bool(ext.is_configured(merged)))


def public_settings(manifest: Manifest, settings: Mapping[str, str]) -> dict[str, str]:
    """Settings safe to return from an API: secret values are masked."""
    secrets = manifest.secret_keys()
    return {k: (REDACTED if k in secrets and v else v) for k, v in settings.items()}


async def save_settings(db: Any, ext: Extension, updates: Mapping[str, str]) -> None:
    """Merge ``updates`` into the stored settings; a masked secret leaves the value unchanged."""
    state = await load_state(db, ext)
    merged = dict(state.settings)
    for k, v in updates.items():
        if v == REDACTED and k in ext.manifest.secret_keys():
            continue
        merged[k] = v
    await db.execute(
        "UPDATE extensions SET settings = $2, updated_at = $3 WHERE name = $1",
        ext.manifest.name,
        dumps(merged),
        to_ts(db, now()),
    )
    if is_pg(db):
        await db.execute("SELECT pg_notify('jobs.control', $1)", dumps({"table": "extensions"}))


async def set_enabled(db: Any, name: str, enabled: bool) -> None:
    await db.execute(
        "UPDATE extensions SET enabled = $2, updated_at = $3 WHERE name = $1",
        name,
        enabled,
        to_ts(db, now()),
    )
    if is_pg(db):
        await db.execute("SELECT pg_notify('jobs.control', $1)", dumps({"table": "extensions"}))
