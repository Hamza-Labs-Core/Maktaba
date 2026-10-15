"""Change sets: the compute / apply seam (plan 06-11 notes N1 and N1a).

Every stage that changes user-visible structure (``detect``, ``tag``,
``rate_style``, ``plan_channels``, ``write_channels``, and the Plex / dizqueTV
writers) is split into two pure-ish steps:

* ``compute_changes(...)`` returns a typed, JSON-serialisable :class:`ChangeSet`
  against the current state; it writes nothing;
* ``apply_changes(...)`` performs it.

:func:`apply_or_review` runs both in ``auto`` mode and stores the change set in
the job's ``job_events`` row (kind ``applied``, ``data.changes``) and in
``output_summary``. In ``review`` mode (reserved for the approval workflow that
will be built later) the change set is stored and nothing is applied.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable, Iterable, Mapping
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from .events import log_event
from .hashing import canonical_hash

if TYPE_CHECKING:
    from .context import StageContext

__all__ = ["ChangeSet", "apply_or_review", "diff_keyed", "diff_order"]

_MAX_ITEMS = 200  # keep one job_events row small; the counts stay exact


@dataclass(frozen=True, slots=True)
class ChangeSet:
    """A typed diff against current state."""

    kind: str
    added: tuple[Any, ...] = ()
    removed: tuple[Any, ...] = ()
    changed: tuple[Any, ...] = ()
    reordered: tuple[Any, ...] = ()
    #: anything else worth showing (counts, labels); must be JSON-serialisable
    meta: Mapping[str, Any] = field(default_factory=dict)

    @property
    def is_empty(self) -> bool:
        return not (self.added or self.removed or self.changed or self.reordered)

    @property
    def counts(self) -> dict[str, int]:
        return {
            "added": len(self.added),
            "removed": len(self.removed),
            "changed": len(self.changed),
            "reordered": len(self.reordered),
        }

    def digest(self) -> str:
        return canonical_hash(self.to_json(limit=None))

    def to_json(self, *, limit: int | None = _MAX_ITEMS) -> dict[str, Any]:
        def cut(xs: tuple[Any, ...]) -> list[Any]:
            return list(xs if limit is None else xs[:limit])

        out: dict[str, Any] = {
            "kind": self.kind,
            "counts": self.counts,
            "added": cut(self.added),
            "removed": cut(self.removed),
            "changed": cut(self.changed),
            "reordered": cut(self.reordered),
        }
        if self.meta:
            out["meta"] = dict(self.meta)
        return out


def diff_keyed(
    old: Mapping[str, Any],
    new: Mapping[str, Any],
    *,
    kind: str,
    meta: Mapping[str, Any] | None = None,
) -> ChangeSet:
    """Diff two ``key -> value`` mappings into added / removed / changed."""
    added = tuple(sorted(k for k in new if k not in old))
    removed = tuple(sorted(k for k in old if k not in new))
    changed = tuple(
        {"key": k, "from": old[k], "to": new[k]}
        for k in sorted(new)
        if k in old and old[k] != new[k]
    )
    return ChangeSet(kind, added, removed, changed, (), meta or {})


def diff_order(
    old: Iterable[str], new: Iterable[str]
) -> tuple[tuple[str, ...], tuple[str, ...], bool]:
    """``(added, removed, reordered)`` between two ordered id lists."""
    o, n = list(old), list(new)
    so, sn = set(o), set(n)
    added = tuple(x for x in n if x not in so)
    removed = tuple(x for x in o if x not in sn)
    common_o = [x for x in o if x in sn]
    common_n = [x for x in n if x in so]
    return added, removed, common_o != common_n


async def apply_or_review(
    ctx: StageContext,
    changes: ChangeSet,
    apply: Callable[[ChangeSet], Awaitable[None]],
) -> str:
    """Apply ``changes`` (``auto``) or park them (``review``).

    Returns ``"applied"``, ``"review"`` or ``"noop"``. The change set is logged
    before returning so history shows exactly what changed.
    """
    job = ctx.job
    if changes.is_empty:
        return "noop"
    data = {"changes": changes.to_json(), "digest": changes.digest()}
    if ctx.write_mode == "review":
        await log_event(
            ctx.db,
            job_id=job.id,
            subject_kind=job.subject_kind,
            subject_id=job.subject.id,
            library_id=job.library_id,
            stage=job.stage,
            level="info",
            kind="review",
            message=f"{changes.kind}: {_summary(changes)} awaiting review",
            data=data,
        )
        return "review"
    await apply(changes)
    await log_event(
        ctx.db,
        job_id=job.id,
        subject_kind=job.subject_kind,
        subject_id=job.subject.id,
        library_id=job.library_id,
        stage=job.stage,
        level="info",
        kind="applied",
        message=f"{changes.kind}: {_summary(changes)}",
        data=data,
    )
    return "applied"


def _summary(c: ChangeSet) -> str:
    parts = [f"{n} {k}" for k, n in c.counts.items() if n]
    return ", ".join(parts) or "no changes"
