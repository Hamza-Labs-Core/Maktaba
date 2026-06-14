"""Core domain types for the channel scheduler (Story 27.2).

The scheduler turns a channel's programming rule + the library into a
continuous, wall-clock-anchored timeline of program blocks. These types
are the contract between the three moving parts:

- :class:`ContentItem` — a candidate video the channel's source resolves
  to (with the metadata smart-mix/marathon need).
- :class:`PlanItem` — a per-mode planner's output: "play this slice of
  this source" (or "pad this much") in sequence, with no absolute times
  yet.
- :class:`Block` — what the packer produces and the repo writes to
  ``channel_programs``: an absolute ``[start_at, end_at)`` window.

Everything here is pure Python (no driver imports) so the planners and
the packer are unit-tested without a database.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from enum import StrEnum
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "Block",
    "BlockKind",
    "ChannelDef",
    "ChannelMode",
    "ContentItem",
    "FillerItem",
    "PlanItem",
    "Planner",
]


class ChannelMode(StrEnum):
    """The four programming modes (mirrors the slot-0081 CHECK + the Go
    ``handlers/channels`` mode constants)."""

    SHUFFLE = "shuffle"
    MARATHON = "marathon"
    SCHEDULE = "schedule"
    SMART_MIX = "smart_mix"


class BlockKind(StrEnum):
    """A block's nature (mirrors the slot-0082 ``kind`` CHECK)."""

    PROGRAM = "program"
    FILLER = "filler"
    BUMPER = "bumper"
    SLATE = "slate"


@dataclass(slots=True, frozen=True)
class ContentItem:
    """One candidate video resolved from a channel's source.

    ``duration_ms`` is the playable length. The optional fields feed the
    smart-mix (``genre``/``length_bucket``) and marathon
    (``series_id``/``season``/``episode``) planners; they are ``None``
    when Epic 26 enrichment never ran, which is exactly the case
    smart-mix's fallback (D7) handles.
    """

    video_id: UUID
    duration_ms: int
    title: str = ""
    snapshot: dict[str, Any] = field(default_factory=dict)
    genre: str | None = None
    length_bucket: str | None = None  # short | medium | long
    series_id: UUID | None = None
    season: int | None = None
    episode: int | None = None

    def to_snapshot(self) -> dict[str, Any]:
        """Build the cached guide metadata blob for this item (D8). The
        explicit ``snapshot`` wins over derived fields so the caller can
        override any key."""
        snap: dict[str, Any] = {}
        if self.title:
            snap["title"] = self.title
        if self.genre:
            snap["genre"] = self.genre
        if self.season is not None:
            snap["season"] = self.season
        if self.episode is not None:
            snap["episode"] = self.episode
        snap["duration_ms"] = self.duration_ms
        snap.update(self.snapshot)
        return snap


@dataclass(slots=True, frozen=True)
class FillerItem:
    """A short clip (station ID / bumper / promo) drawn to pad a slot to
    its boundary (D6). ``video_id`` is the underlying media; the packer
    coalesces a run of these into one looping filler block."""

    filler_item_id: UUID
    video_id: UUID | None
    duration_ms: int
    kind: BlockKind = BlockKind.FILLER
    snapshot: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class PlanItem:
    """A planner's output element. A PROGRAM item carries a real source
    slice; a FILLER item (``video_id is None``) is a pad request the
    packer fills from the filler pool (or slate)."""

    video_id: UUID | None
    duration_ms: int
    offset_ms: int = 0
    kind: BlockKind = BlockKind.PROGRAM
    snapshot: dict[str, Any] = field(default_factory=dict)

    @property
    def is_pad(self) -> bool:
        return self.video_id is None and self.kind != BlockKind.PROGRAM


@dataclass(slots=True)
class Block:
    """A scheduled, absolutely-timed program block (one
    ``channel_programs`` row). The timeline is contiguous:
    ``prev.end_at == next.start_at`` (D2)."""

    channel_id: UUID
    seq: int
    kind: BlockKind
    start_at: datetime
    end_at: datetime
    source_offset: int  # ms into the source
    source_duration: int  # ms played from the source
    video_id: UUID | None = None
    filler_item_id: UUID | None = None
    title_snapshot: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class ChannelDef:
    """The channel record the scheduler reads (slot 0081)."""

    id: UUID
    mode: ChannelMode
    mode_config: dict[str, Any]
    source_filter: dict[str, Any] | None
    library_id: UUID | None
    enabled: bool = True
    transition: str = "cut"


class Planner(Protocol):
    """A per-mode planner. ``plan`` yields the ordered :class:`PlanItem`
    sequence whose cumulative duration covers ``[start_at, until)``;
    ``export_state`` returns the cursor (shuffle bag / marathon index) to
    persist so a horizon top-up continues rather than restarts (D5)."""

    def plan(
        self, content: list[ContentItem], *, start_at: datetime, until: datetime
    ) -> list[PlanItem]: ...

    def export_state(self) -> dict[str, Any]: ...
