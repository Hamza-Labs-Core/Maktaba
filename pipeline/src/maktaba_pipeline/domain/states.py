"""Canonical video FSM — the Python binding of Story 1.6.

Source of truth: ``shared/states/states.json``. The Go binding under
``shared/states/go`` mirrors this module one-for-one; tests in both
trees pin the parity.

The 12 :class:`State` values are exactly the strings the slot 0004
``videos_state_valid`` CHECK constraint accepts. The 7 :class:`Stage`
values are exactly the strings the slot 0002/0004 stage CHECKs accept.
The :class:`Trigger` enum is a 10-element superset that adds three
side-channel triggers (``filesystem``, ``library``, ``integrity``) for
the broadcast transitions.

Use :func:`lookup` to resolve a ``(from, trigger, outcome)`` triple to
its target state. ``None`` means the triple is not in the transition
graph; callers should raise :class:`IllegalStateTransition` on that
result. The orchestrator's :func:`maktaba_pipeline.orchestrator.advance.
advance_after_stage` is the only function that should ever issue an
``UPDATE videos SET state = …`` statement.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass

__all__ = [
    "BROADCAST_TRANSITIONS",
    "EXPLICIT_TRANSITIONS",
    "IllegalStateTransition",
    "Outcome",
    "STATE_CLASS",
    "Stage",
    "State",
    "TERMINAL_DROP_STATES",
    "Trigger",
    "allowed_targets",
    "allowed_transitions",
    "is_stage_trigger",
    "lookup",
]


class State(enum.StrEnum):
    """The 12 canonical video states.

    Mirrors the ``videos_state_valid`` CHECK constraint added by slot
    0004. The class hierarchy (open / terminal-good / terminal-soft /
    terminal-bad / sink) drives the broadcast transition rules in
    :data:`BROADCAST_TRANSITIONS`.
    """

    DISCOVERED = "discovered"
    PROBED = "probed"
    AUDIO_EXTRACTED = "audio_extracted"
    TRANSCRIBED = "transcribed"
    INDEXED = "indexed"
    THUMBNAILED = "thumbnailed"
    READY = "ready"
    READY_NO_AUDIO = "ready_no_audio"
    MISSING = "missing"
    SUPERSEDED = "superseded"
    CORRUPTED = "corrupted"
    FAILED = "failed"


class Stage(enum.StrEnum):
    """The 7 canonical pipeline stage names.

    Mirrors ``processing_jobs_stage_chk`` (slot 0002) and
    ``processing_jobs_stage_valid`` (slot 0004); both enforce the same
    set. Adding a new stage requires (a) a new value here, (b) a
    follow-up migration that ALTERs both CHECKs, and (c) extending the
    transition table below.
    """

    SCAN = "scan"
    PROBE = "probe"
    EXTRACT = "extract"
    TRANSCRIBE = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX = "index"
    THUMBNAIL = "thumbnail"


class Trigger(enum.StrEnum):
    """Superset of :class:`Stage` plus three side-channel triggers.

    Side-channel triggers (``filesystem``, ``library``, ``integrity``)
    carry the broadcast transitions to ``MISSING``, ``SUPERSEDED``, and
    ``CORRUPTED`` respectively. They never appear in
    ``processing_jobs.stage`` — that column rejects anything outside
    the 7 :class:`Stage` values.
    """

    SCAN = "scan"
    PROBE = "probe"
    EXTRACT = "extract"
    TRANSCRIBE = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX = "index"
    THUMBNAIL = "thumbnail"
    FILESYSTEM = "filesystem"
    LIBRARY = "library"
    INTEGRITY = "integrity"


# Recognized outcomes from plan-01-06 §3.4. Open vocabulary at the type
# level; closed at the runtime-table level. Anything not listed in
# EXPLICIT_TRANSITIONS or matched by a broadcast row raises
# IllegalStateTransition.
class Outcome(enum.StrEnum):
    OK = "ok"
    NO_AUDIO = "no_audio"
    PARTIAL = "partial"
    EXHAUSTED = "exhausted"
    REDISCOVERED = "rediscovered"
    DELETED = "deleted"
    REPLACED = "replaced"
    FAIL = "fail"
    ALL_GATES_OK = "all_gates_ok"


# Class assignment matches shared/states/states.json. The class
# determines which broadcast rows can fire from a given state.
STATE_CLASS: dict[State, str] = {
    State.DISCOVERED: "open",
    State.PROBED: "open",
    State.AUDIO_EXTRACTED: "open",
    State.TRANSCRIBED: "open",
    State.INDEXED: "open",
    State.THUMBNAILED: "open",
    State.READY: "terminal-good",
    State.READY_NO_AUDIO: "terminal-good",
    State.MISSING: "sink",
    State.SUPERSEDED: "terminal-soft",
    State.CORRUPTED: "terminal-bad",
    State.FAILED: "terminal-bad",
}


# Terminal-drop states: a stage that finishes after the row hit one of
# these is racing with a side-channel transition. advance_after_stage
# logs `late_stage_finish` and no-ops instead of erroring (story edge
# case 1).
TERMINAL_DROP_STATES: frozenset[State] = frozenset(
    {State.SUPERSEDED, State.CORRUPTED, State.FAILED}
)


def is_stage_trigger(t: Trigger) -> bool:
    """True iff ``t`` is one of the 7 canonical pipeline stages."""
    return t.value in {s.value for s in Stage}


@dataclass(frozen=True, slots=True)
class _Key:
    """Triple key into the explicit transition table."""

    frm: State
    trigger: Trigger
    outcome: str


# The 11 hand-named edges from plan-01-06 §4. Broadcast rows are
# evaluated separately by :func:`lookup` so we don't enumerate ~70
# expanded rows here.
EXPLICIT_TRANSITIONS: dict[_Key, State] = {
    _Key(State.DISCOVERED, Trigger.PROBE, Outcome.OK.value): State.PROBED,
    _Key(State.PROBED, Trigger.EXTRACT, Outcome.OK.value): State.AUDIO_EXTRACTED,
    _Key(State.PROBED, Trigger.PROBE, Outcome.NO_AUDIO.value): State.READY_NO_AUDIO,
    _Key(State.AUDIO_EXTRACTED, Trigger.TRANSCRIBE, Outcome.OK.value): State.TRANSCRIBED,
    _Key(State.TRANSCRIBED, Trigger.SUBTITLE_GEN, Outcome.PARTIAL.value): State.TRANSCRIBED,
    _Key(State.TRANSCRIBED, Trigger.INDEX, Outcome.PARTIAL.value): State.TRANSCRIBED,
    _Key(State.TRANSCRIBED, Trigger.SUBTITLE_GEN, Outcome.OK.value): State.INDEXED,
    _Key(State.TRANSCRIBED, Trigger.INDEX, Outcome.OK.value): State.INDEXED,
    _Key(State.INDEXED, Trigger.THUMBNAIL, Outcome.OK.value): State.THUMBNAILED,
    _Key(State.THUMBNAILED, Trigger.SCAN, Outcome.ALL_GATES_OK.value): State.READY,
    _Key(State.MISSING, Trigger.SCAN, Outcome.REDISCOVERED.value): State.DISCOVERED,
}


# Broadcast rules — each row matches a (trigger, outcome) pair from any
# source state whose class is in ``source_classes``. The Stage wildcard
# only matches the 7 canonical stage triggers (never side-channel ones).
@dataclass(frozen=True, slots=True)
class _BroadcastRow:
    source_classes: frozenset[str]
    trigger: Trigger | None  # None means "any stage trigger"
    outcome: str
    to: State


BROADCAST_TRANSITIONS: tuple[_BroadcastRow, ...] = (
    _BroadcastRow(
        source_classes=frozenset({"open", "terminal-good", "sink"}),
        trigger=Trigger.FILESYSTEM,
        outcome=Outcome.DELETED.value,
        to=State.MISSING,
    ),
    _BroadcastRow(
        source_classes=frozenset({"open", "terminal-good"}),
        trigger=None,  # any stage
        outcome=Outcome.EXHAUSTED.value,
        to=State.FAILED,
    ),
    _BroadcastRow(
        source_classes=frozenset({"open", "terminal-good"}),
        trigger=Trigger.INTEGRITY,
        outcome=Outcome.FAIL.value,
        to=State.CORRUPTED,
    ),
    _BroadcastRow(
        source_classes=frozenset({"open", "terminal-good", "terminal-soft"}),
        trigger=Trigger.LIBRARY,
        outcome=Outcome.REPLACED.value,
        to=State.SUPERSEDED,
    ),
)


def lookup(frm: State, trigger: Trigger, outcome: str) -> State | None:
    """Resolve ``(frm, trigger, outcome)`` to a target state.

    Returns ``None`` if the triple is not in the transition graph. The
    explicit table is checked first; on a miss the broadcast rules are
    evaluated. Outcome is taken as a string so callers can pass either
    an :class:`Outcome` value or a literal string (the JSON manifest
    uses strings).
    """
    explicit = EXPLICIT_TRANSITIONS.get(_Key(frm, trigger, outcome))
    if explicit is not None:
        return explicit

    cls = STATE_CLASS[frm]
    for row in BROADCAST_TRANSITIONS:
        if cls not in row.source_classes:
            continue
        if row.outcome != outcome:
            continue
        if row.trigger is None:
            # Stage-wildcard row — only stage triggers may match.
            if is_stage_trigger(trigger):
                return row.to
            continue
        if row.trigger.value == trigger.value:
            return row.to
    return None


# View dict[State, set[State]] — the simpler shape the story names.
# Built lazily and cached so the module's import-time cost stays low.
allowed_targets: dict[State, frozenset[State]]


def _build_allowed_targets() -> dict[State, frozenset[State]]:
    out: dict[State, set[State]] = {s: set() for s in State}
    for key, target in EXPLICIT_TRANSITIONS.items():
        out[key.frm].add(target)
    for src in State:
        cls = STATE_CLASS[src]
        for row in BROADCAST_TRANSITIONS:
            if cls in row.source_classes:
                out[src].add(row.to)
    return {s: frozenset(t) for s, t in out.items()}


allowed_targets = _build_allowed_targets()


# `allowed_transitions` is the richer triple-keyed table the
# orchestrator uses; the simpler `allowed_targets` view satisfies the
# story's `dict[State, set[State]]` shape.
allowed_transitions: dict[_Key, State] = EXPLICIT_TRANSITIONS


class IllegalStateTransition(Exception):
    """Raised when a (from, trigger, outcome) triple is not in the FSM.

    Carries the triple so observability can log it; callers that want
    to dispatch on the source state can read ``self.from_``.
    """

    def __init__(
        self,
        video_id: object,
        from_: State,
        trigger: Trigger,
        outcome: str,
    ) -> None:
        self.video_id = video_id
        self.from_ = from_
        self.trigger = trigger
        self.outcome = outcome
        super().__init__(
            f"illegal state transition for video {video_id}: "
            f"from={from_.value} trigger={trigger.value} outcome={outcome}"
        )
