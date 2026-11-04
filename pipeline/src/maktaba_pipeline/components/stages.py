"""Which components are on, and what that means for the pipeline's stages (Epic 51,
story 51.06).

A component's **gate** is open when it is enabled, not held, and every requirement is on
(``any:a|b`` needs one of them). The pipeline reads only desired state here: a
requirement is on when its own gate is open (an asset: when it is installed, ``want`` not
``absent``); whether it is running is the supervisor's to report (51.04), and a stage that
cannot run on a machine is already kept off by its ``unavailable()`` check. A component
with no desired row follows today's behaviour (on), so a home that was never seeded
(51.03) changes nothing. Core components (``switchable = false``) are always on.

A closed gate is ``off`` (not enabled, or a requirement off: ``dependency_off``, or its
settings could not be moved to this release's shape: ``settings_invalid``, story 51.13) or
``held`` (an admin stopped it, or a requirement is held). For the stages of a stage
group:

- this machine stops claiming them: :func:`machine_reasons` gives
  ``component_off:<id>`` / ``component_held:<id>`` for ``select_stages``;
- the home's leader turns them off for every machine: :func:`home_reasons` gives the
  ``job_stages.disabled_reason`` (``component_off`` / ``component_held``).
"""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.sync import COMPONENT_HELD, COMPONENT_OFF
from .catalog import Catalog, Component, default
from .store import Desired

__all__ = [
    "HELD",
    "OFF",
    "ON",
    "SETTINGS_INVALID",
    "Gate",
    "extension_name",
    "gates",
    "home_reasons",
    "machine_reasons",
    "stage_components",
]

ON = "on"
OFF = "off"
HELD = "held"
#: the reason of a component whose settings section could not be moved (story 51.13)
SETTINGS_INVALID = "settings_invalid"


@dataclass(frozen=True, slots=True)
class Gate:
    id: str
    state: str  # on, off or held
    #: the desired ``want`` ("" when the component has no row: today's behaviour)
    want: str = ""
    generation: int = 0
    #: why it is closed beyond its own want: "" | "held" | "dependency_off" |
    #: "settings_invalid"
    reason: str = ""
    params: dict[str, Any] = field(default_factory=dict)

    @property
    def open(self) -> bool:
        return self.state == ON


def stage_components(cat: Catalog | None = None) -> list[Component]:
    """The switchable components that own pipeline stages: the stage groups, and the
    built-in extensions (``ext.*``, story 51.07; an extension loaded through an entry
    point is in no catalog, and keeps its ``extensions`` row as its only switch)."""
    cat = cat or default()
    return [
        c
        for c in cat.components()
        if c.switchable and c.stages and (c.has_part("stages") or c.kind == "extension")
    ]


def extension_name(c: Component) -> str | None:
    """The ``extensions`` row of a built-in extension component (``ext.plex`` -> plex)."""
    if c.kind != "extension" or not c.id.startswith("ext."):
        return None
    return c.id.removeprefix("ext.")


def _own(c: Component, d: Desired | None) -> Gate:
    if not c.switchable or d is None:
        return Gate(
            c.id, ON, want="" if d is None else d.want, generation=0 if d is None else d.generation
        )
    if c.kind == "asset":
        state = OFF if d.want == "absent" else ON
        return Gate(c.id, state, want=d.want, generation=d.generation)
    if d.want != "enabled":
        return Gate(c.id, OFF, want=d.want, generation=d.generation)
    if d.hold is not None:
        return Gate(c.id, HELD, want=d.want, generation=d.generation, reason="held")
    return Gate(c.id, ON, want=d.want, generation=d.generation)


def gates(
    desired: Mapping[str, Desired],
    cat: Catalog | None = None,
    blocked: Mapping[str, Sequence[tuple[str, str]]] | None = None,
) -> dict[str, Gate]:
    """Every component's gate from ``desired`` (one machine's merged desired state),
    requirements first. ``blocked`` (``settings_migrate.blocked``: component -> its failed
    sections) keeps a component that would be on off with ``settings_invalid``: it never
    runs on the defaults (story 51.13), and what requires it waits (``dependency_off``)."""
    cat = cat or default()
    out: dict[str, Gate] = {}
    for cid in cat.order():
        c = cat.get(cid)
        if c is None:
            continue
        g = _own(c, desired.get(cid))
        failed = (blocked or {}).get(cid)
        if g.open and c.switchable and failed:
            section, error = failed[0]
            g = Gate(
                cid,
                OFF,
                want=g.want,
                generation=g.generation,
                reason=SETTINGS_INVALID,
                params={"section": section, "error": error},
            )
        if g.open and c.switchable:
            for group in cat.requires(cid):
                have = [out[r] for r in group if r in out]
                if any(r.open for r in have) or len(have) < len(group):
                    # one is on, or one is not in this catalog (a newer release's): met
                    continue
                held = next((r for r in have if r.state == HELD), None)
                first = held or have[0]
                g = Gate(
                    cid,
                    HELD if held is not None else OFF,
                    want=g.want,
                    generation=g.generation,
                    reason="dependency_off",
                    params={"component": first.id},
                )
                break
        out[cid] = g
    return out


def machine_reasons(gs: Mapping[str, Gate], cat: Catalog | None = None) -> dict[str, str]:
    """stage -> ``component_off:<id>`` / ``component_held:<id>`` for this machine's
    ``select_stages`` (only the stages of closed stage groups)."""
    out: dict[str, str] = {}
    for c in stage_components(cat):
        g = gs.get(c.id)
        if g is None or g.open:
            continue
        code = COMPONENT_HELD if g.state == HELD else COMPONENT_OFF
        for st in c.stages:
            out[st] = f"{code}:{c.id}"
    return out


def home_reasons(gs: Mapping[str, Gate], cat: Catalog | None = None) -> dict[str, str]:
    """stage -> ``job_stages.disabled_reason`` (``component_off`` / ``component_held``)
    for the home's leader (only the stages of closed stage groups)."""
    return {st: why.partition(":")[0] for st, why in machine_reasons(gs, cat).items()}
