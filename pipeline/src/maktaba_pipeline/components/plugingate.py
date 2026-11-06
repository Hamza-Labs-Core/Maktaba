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
PLUGIN_API_MINOR = 0


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
    return level != "store"


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
