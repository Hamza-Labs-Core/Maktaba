"""Extension state: enabled flag and settings, stored in the ``extensions`` table.

Environment variables are only a bootstrap: they seed the row the first time an
extension is seen. After that the database is the source of truth, and secret
values are never returned by :func:`public_settings`.

Secret settings are not in ``extensions.settings`` (which a machine's role can read):
since slot 0120 they are sealed in ``home_secrets`` (Epic 32 story 32.6, README Q7;
:mod:`maktaba_pipeline.home_secrets`). A value still in the row from before the home's
API moved it is used until then. On a node ``home_secrets`` is not readable, so a
secret is simply absent there; the stages that need one run on the home.
"""

from __future__ import annotations

import os
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from typing import Any

from ..engine.sql import dumps, is_pg, loads, now, to_ts
from ..home_secrets import get_secret, load_key, put_secret
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


async def _seal_secrets(
    db: Any, manifest: Manifest, values: Mapping[str, str], *, create_key: bool
) -> dict[str, str]:
    """Write the non-empty secret ``values`` to home_secrets; return the ones that could
    not be sealed (no key file), which the caller keeps out of the row anyway."""
    secrets = manifest.secret_keys()
    pending = {k: v for k, v in values.items() if k in secrets and v}
    if not pending:
        return {}
    key = load_key(create=create_key)
    if key is None:
        return pending
    for k, v in pending.items():
        await put_secret(db, manifest.secret_name(k), v, key)
    return {}


async def ensure_rows(db: Any, extensions: Iterable[Extension]) -> None:
    """Insert a row per extension on first sight, seeded from the environment.

    An extension whose required settings are all present in the environment
    starts enabled; otherwise it starts disabled. Secret values from the environment
    go to home_secrets (only the home's workers call this), never into the row.
    """
    for ext in extensions:
        existing = await db.fetchrow(
            "SELECT name FROM extensions WHERE name = $1", ext.manifest.name
        )
        if existing is not None:
            continue
        defaults = env_defaults(ext.manifest)
        secrets = ext.manifest.secret_keys()
        await _seal_secrets(db, ext.manifest, defaults, create_key=False)
        row = {k: v for k, v in defaults.items() if k not in secrets}
        await db.execute(
            "INSERT INTO extensions (name, enabled, settings, updated_at) VALUES ($1,$2,$3,$4)"
            " ON CONFLICT (name) DO NOTHING",
            ext.manifest.name,
            bool(ext.is_configured(defaults)),
            dumps(row),
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
    for f in ext.manifest.settings:
        if not f.secret or f.home_only:
            continue
        value = await get_secret(db, ext.manifest.secret_name(f.key))
        if value:
            stored[f.key] = value
    home_only = {f.key for f in ext.manifest.settings if f.home_only}
    layered = {**env_defaults(ext.manifest), **{k: v for k, v in stored.items() if v != ""}}
    merged = {k: v for k, v in layered.items() if k not in home_only}
    return ExtensionState(ext.manifest.name, enabled, merged, bool(ext.is_configured(merged)))


def public_settings(manifest: Manifest, settings: Mapping[str, str]) -> dict[str, str]:
    """Settings safe to return from an API: secret values are masked."""
    secrets = manifest.secret_keys()
    return {k: (REDACTED if k in secrets and v else v) for k, v in settings.items()}


async def save_settings(db: Any, ext: Extension, updates: Mapping[str, str]) -> None:
    """Merge ``updates`` into the stored settings; a masked secret leaves the value
    unchanged, a new one goes to home_secrets (never the row)."""
    row = await db.fetchrow("SELECT settings FROM extensions WHERE name = $1", ext.manifest.name)
    stored = {str(k): str(v) for k, v in (loads(row["settings"]) or {}).items()} if row else {}
    secrets = ext.manifest.secret_keys()
    new_secrets = {k: v for k, v in updates.items() if k in secrets and v and v != REDACTED}
    # a value still in the row from before the move goes along with this save
    for k in secrets:
        legacy = stored.pop(k, "")
        stale = legacy and k not in new_secrets
        if stale and await get_secret(db, ext.manifest.secret_name(k)) is None:
            new_secrets[k] = legacy
    if await _seal_secrets(db, ext.manifest, new_secrets, create_key=False):
        raise RuntimeError(
            "no secrets key file on this machine: secret settings can only be saved on the home"
        )
    merged = {**stored, **{k: v for k, v in updates.items() if k not in secrets}}
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
