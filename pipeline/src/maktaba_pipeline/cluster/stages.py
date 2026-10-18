"""Which stages this worker claims, and why not the others (Epic 31 design section 5).

For every graph stage the first reason that applies, in this order, keeps it off; the
reason is stored as a code in ``workers.stages_off`` (the Machines page translates it):

1. ``not_selected``: not in ``--stages`` / in ``--exclude-stages``
2. ``version_ahead``: newer version in the database (``stages_ahead``)
3. ``unavailable``: cannot run here (no handler; ``StageImpl.unavailable()``; the
   detail is logged and kept in :attr:`StageSelector.detail`)
4. ``home_only``: ``placement == "home"`` on a node
5. ``storage``: storage not reachable (``nas_read`` stages need the read check,
   ``nas_write`` the write check)
6. ``switched_off``: ``cluster_node_stages.enabled = false`` for this machine
7. ``draining``: ``cluster_nodes.drain``

:func:`select_stages` is the pure rule; :class:`StageSelector` holds a worker's inputs,
re-reads the moving ones (versions, switches, drain, storage) at :meth:`refresh`, and
exposes the current set to the claim loop, which reads it on every poll.
"""

from __future__ import annotations

from collections.abc import Collection, Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.graph import StageDef
from ..log import get_logger

__all__ = [
    "REASON_AHEAD",
    "REASON_DRAIN",
    "REASON_EXCLUDED",
    "REASON_HOME_ONLY",
    "REASON_NOT_REQUESTED",
    "REASON_STORAGE",
    "REASON_SWITCHED_OFF",
    "REASON_UNAVAILABLE",
    "StageSelector",
    "select_stages",
]

REASON_NOT_REQUESTED = "not_selected"
REASON_EXCLUDED = "not_selected"
REASON_AHEAD = "version_ahead"
REASON_UNAVAILABLE = "unavailable"
REASON_HOME_ONLY = "home_only"
REASON_STORAGE = "storage"
REASON_SWITCHED_OFF = "switched_off"
REASON_DRAIN = "draining"

#: pool -> the storage check it needs
STORAGE_POOLS: dict[str, str] = {"nas_read": "read", "nas_write": "write"}

_log = get_logger()


def _cannot_run(st: StageDef, handlers: Collection[str]) -> str | None:
    """Why this worker cannot run the stage (the detail, logged), or None."""
    if st.legacy:
        return None if st.name in handlers else "no handler in this worker"
    from ..engine.stages.registry import get_impl

    impl = get_impl(st.name)
    if impl is None or impl.run is None:
        return "not implemented in this version"
    return impl.unavailable() if impl.unavailable is not None else None


def select_stages(
    graph: Iterable[StageDef],
    *,
    requested: Collection[str] | None = None,
    excluded: Collection[str] = (),
    handlers: Collection[str] = (),
    ahead: Collection[str] = (),
    role: str = "home",
    storage: Mapping[str, Any] | None = None,
    switches: Mapping[str, bool] | None = None,
    drain: bool = False,
    detail: dict[str, str] | None = None,
) -> tuple[list[str], dict[str, str]]:
    """``(served stages in graph order, {stage: reason code} for the others)``.

    ``detail``, when given, receives the text behind each ``unavailable``.
    """
    served: list[str] = []
    off: dict[str, str] = {}
    switches = switches or {}
    for st in graph:
        reason: str | None = None
        if requested is not None and st.name not in requested:
            reason = REASON_NOT_REQUESTED
        elif st.name in excluded:
            reason = REASON_EXCLUDED
        elif st.name in ahead:
            reason = REASON_AHEAD
        elif (why := _cannot_run(st, handlers)) is not None:
            reason = REASON_UNAVAILABLE
            if detail is not None:
                detail[st.name] = why
            if requested is not None:
                _log.warning("stage_unavailable_here", stage=st.name, reason=why)
        elif role == "node" and st.placement == "home":
            reason = REASON_HOME_ONLY
        elif (
            storage is not None
            and st.pool in STORAGE_POOLS
            and storage.get(STORAGE_POOLS[st.pool]) == "error"
        ):
            reason = REASON_STORAGE
        elif switches.get(st.name) is False:
            reason = REASON_SWITCHED_OFF
        elif drain:
            reason = REASON_DRAIN
        if reason is None:
            served.append(st.name)
        else:
            off[st.name] = reason
    return served, off


@dataclass
class StageSelector:
    """A worker's stage selection; :attr:`served` is what the claim loop reads."""

    graph: Sequence[StageDef]
    requested: frozenset[str] | None = None
    excluded: frozenset[str] = frozenset()
    handlers: frozenset[str] = frozenset()
    role: str = "home"
    ahead: dict[str, int] = field(default_factory=dict)
    storage: dict[str, Any] | None = None
    switches: dict[str, bool] = field(default_factory=dict)
    drain: bool = False
    served: tuple[str, ...] = ()
    stages_off: dict[str, str] = field(default_factory=dict)
    #: the text behind each ``unavailable`` code
    detail: dict[str, str] = field(default_factory=dict)

    def compute(self) -> bool:
        """Recompute from the current inputs; True when the served set changed."""
        detail: dict[str, str] = {}
        served, off = select_stages(
            self.graph,
            requested=self.requested,
            excluded=self.excluded,
            handlers=self.handlers,
            ahead=self.ahead,
            role=self.role,
            storage=self.storage,
            switches=self.switches,
            drain=self.drain,
            detail=detail,
        )
        self.detail = detail
        changed = tuple(served) != self.served or off != self.stages_off
        self.served = tuple(served)
        self.stages_off = off
        return changed

    def current(self) -> tuple[str, ...]:
        return self.served

    @property
    def pools(self) -> list[str]:
        by_name = {s.name: s.pool for s in self.graph}
        return sorted({by_name[s] for s in self.served})
