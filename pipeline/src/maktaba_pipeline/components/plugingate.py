"""A plugin entry's gates (Epic 54, story 54.37 §3), the twin of ``plugingate.go``.

Both are tested on one table, ``shared/components/testdata/plugin_gates.json``: the same
input gives the same reason in every runner.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any

__all__ = [
    "PLUGIN_API_MAJOR",
    "PLUGIN_API_MINOR",
    "GateInput",
    "plugin_api_served",
    "plugin_gate",
    "unreviewed",
    "version_in_range",
]

PLUGIN_API_MAJOR = 1
PLUGIN_API_MINOR = 2


@dataclass(frozen=True)
class GateInput:
    server_version: str = ""
    range: str = ""
    plugin_api: str = "1.0"
    required: Sequence[str] = field(default_factory=tuple)
    granted: Sequence[str] = field(default_factory=tuple)
    review_level: str = "none"
    allow_unreviewed: bool = False
    revoked: bool = False
    entitlement_missing: bool = False


def unreviewed(level: str) -> bool:
    """A review level that needs the home's opt-in (54.10): anything but a store review."""
    return level not in ("store", "automated", "reviewed")


def _semver(s: str) -> tuple[int, int, int] | None:
    for sep in "-+":
        s = s.split(sep, 1)[0]
    parts = s.split(".")
    if len(parts) != 3 or not all(p.isdigit() for p in parts):
        return None
    return int(parts[0]), int(parts[1]), int(parts[2])


def version_in_range(version: str, rng: str) -> bool:
    """Whether ``version`` meets every clause of a manifest range; a development build
    (not x.y.z) meets every range."""
    v = _semver(version.strip().removeprefix("v"))
    if v is None:
        return True
    for clause in rng.split(","):
        clause = clause.replace(" ", "")
        if not clause:
            continue
        op = clause[: len(clause) - len(clause.lstrip("<>="))]
        want = _semver(clause[len(op) :])
        if want is None:
            return False
        ok = {
            ">=": v >= want,
            ">": v > want,
            "<=": v <= want,
            "<": v < want,
            "=": v == want,
        }.get(op, False)
        if not ok:
            return False
    return True


def plugin_api_served(v: str) -> bool:
    major, _, minor = v.strip().partition(".")
    if not major.isdigit() or not minor.isdigit():
        return False
    return int(major) == PLUGIN_API_MAJOR and int(minor) <= PLUGIN_API_MINOR


def plugin_gate(g: GateInput) -> tuple[str, dict[str, Any]]:
    """The first gate that keeps the plugin waiting ("" when none does) and its params."""
    if g.revoked:
        return "revoked", {}
    if not version_in_range(g.server_version, g.range):
        return "incompatible", {"range": g.range, "server": g.server_version}
    if not plugin_api_served(g.plugin_api):
        return "incompatible", {
            "plugin_api": g.plugin_api,
            "served": f"{PLUGIN_API_MAJOR}.{PLUGIN_API_MINOR}",
        }
    if g.entitlement_missing:
        return "entitlement_missing", {}
    if unreviewed(g.review_level) and not g.allow_unreviewed:
        return "unreviewed_disallowed", {}
    missing = [c for c in g.required if c not in g.granted]
    if missing:
        return "grants_needed", {"capabilities": ",".join(missing)}
    return "", {}


#: the home's plugin policy's app_settings key (the API's ``plugins.PolicyKey``)
POLICY_KEY = "plugins.policy"


def _json(v: Any) -> Any:
    import json

    if isinstance(v, str | bytes):
        try:
            return json.loads(v)
        except ValueError:
            return None
    return v


async def read(db: Any, version: str) -> dict[str, tuple[str, dict[str, Any]]]:
    """Each installed plugin entry's gate reason ("" none), from the tables the API keeps
    (plugin_installs, plugin_grants, the policy, plugin_keep_off, plugin_health): the same
    inputs the supervisor's runner reads. A schema without them answers nothing."""
    out: dict[str, tuple[str, dict[str, Any]]] = {}
    try:
        rows = await db.fetch(
            "SELECT plugin_id, manifest, review_level, revoked FROM plugin_installs"
        )
        grants = {
            str(r["plugin_id"]): list(_json(r["capabilities"]) or [])
            if isinstance(r["capabilities"], str)
            else list(r["capabilities"] or [])
            for r in await db.fetch("SELECT plugin_id, capabilities FROM plugin_grants")
        }
        kept = {
            str(r["plugin_id"]) for r in await db.fetch("SELECT plugin_id FROM plugin_keep_off")
        }
        tripped = {
            str(r["plugin_id"])
            for r in await db.fetch("SELECT plugin_id FROM plugin_health WHERE tripped")
        }
        pol = await db.fetchrow("SELECT value FROM app_settings WHERE key = $1", POLICY_KEY)
    except Exception:  # noqa: BLE001 - an older schema: no plugins
        return out
    allow = bool((_json(pol["value"]) or {}).get("allow_unreviewed")) if pol is not None else False
    for r in rows:
        pid = str(r["plugin_id"])
        m = _json(r["manifest"]) or {}
        caps = m.get("capabilities") or {}
        reason, params = plugin_gate(
            GateInput(
                server_version=version,
                range=str(m.get("maktaba", "")),
                plugin_api=str(m.get("plugin_api", "")),
                required=tuple(caps.get("required") or ()),
                granted=tuple(grants.get(pid, ())),
                review_level=str(r["review_level"]),
                allow_unreviewed=allow,
                revoked=bool(r["revoked"]),
            )
        )
        if not reason and pid in tripped:
            reason = "plugin_crash_loop"
        if pid in kept:
            params = {**params, "kept_off": True}
        out["plugin." + pid] = (reason, params)
    return out
