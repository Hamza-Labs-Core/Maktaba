"""Story 1.6 acceptance tests for the FSM enums and transition graph.

The tests in this module are pure-Python: they exercise the domain
module without a database connection. The SQL-side tests (CHECK
constraints, NOTIFY trigger payload) live in
``pipeline/tests/db/test_states_migration.py``.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.domain.stages import Stage
from maktaba_pipeline.domain.states import (
    BROADCAST_TRANSITIONS,
    EXPLICIT_TRANSITIONS,
    STATE_CLASS,
    TERMINAL_DROP_STATES,
    IllegalStateTransition,
    Outcome,
    State,
    Trigger,
    allowed_targets,
    is_stage_trigger,
    lookup,
)

# -----------------------------------------------------------------
# Manifest source-of-truth: pin the bindings against states.json
# -----------------------------------------------------------------

_REPO_ROOT = Path(__file__).resolve().parents[3]
_MANIFEST = _REPO_ROOT / "shared" / "states" / "states.json"


@pytest.fixture(scope="module")
def manifest() -> dict[str, Any]:
    data: dict[str, Any] = json.loads(_MANIFEST.read_text(encoding="utf-8"))
    return data


@pytest.mark.unit
def test_manifest_file_exists() -> None:
    assert _MANIFEST.is_file(), f"missing {_MANIFEST}"


@pytest.mark.unit
def test_state_enum_matches_manifest(manifest: dict[str, Any]) -> None:
    expected = [s["db"] for s in manifest["states"]]
    got = [s.value for s in State]
    assert got == expected


@pytest.mark.unit
def test_stage_enum_matches_manifest(manifest: dict[str, Any]) -> None:
    assert [s.value for s in Stage] == manifest["stages"]


@pytest.mark.unit
def test_trigger_enum_matches_manifest(manifest: dict[str, Any]) -> None:
    assert [t.value for t in Trigger] == manifest["triggers"]


@pytest.mark.unit
def test_state_classes_match_manifest(manifest: dict[str, Any]) -> None:
    expected = {s["db"]: s["class"] for s in manifest["states"]}
    got = {s.value: STATE_CLASS[s] for s in State}
    assert got == expected


@pytest.mark.unit
def test_terminal_drop_states_match_manifest(manifest: dict[str, Any]) -> None:
    expected_db = set(manifest["terminal_drop_states"])
    got_db = {s.name for s in TERMINAL_DROP_STATES}
    assert got_db == expected_db


# -----------------------------------------------------------------
# Story acceptance: enum sets are exactly what spec lists
# -----------------------------------------------------------------


@pytest.mark.unit
def test_state_enum_matches_spec() -> None:
    assert {s.name for s in State} == {
        "DISCOVERED",
        "PROBED",
        "AUDIO_EXTRACTED",
        "TRANSCRIBED",
        "INDEXED",
        "THUMBNAILED",
        "READY",
        "READY_NO_AUDIO",
        "MISSING",
        "SUPERSEDED",
        "CORRUPTED",
        "FAILED",
    }


@pytest.mark.unit
def test_stage_enum_matches_spec() -> None:
    assert {s.value for s in Stage} == {
        "scan",
        "probe",
        "extract",
        "transcribe",
        "subtitle_gen",
        "index",
        "thumbnail",
    }


@pytest.mark.unit
def test_trigger_is_superset_of_stage() -> None:
    stage_values = {s.value for s in Stage}
    trigger_values = {t.value for t in Trigger}
    assert stage_values.issubset(trigger_values)
    side_channel = trigger_values - stage_values
    assert side_channel == {"filesystem", "library", "integrity"}


@pytest.mark.unit
def test_is_stage_trigger() -> None:
    for s in Stage:
        assert is_stage_trigger(Trigger(s.value)) is True
    for side_channel in (Trigger.FILESYSTEM, Trigger.LIBRARY, Trigger.INTEGRITY):
        assert is_stage_trigger(side_channel) is False


# -----------------------------------------------------------------
# Explicit transition table: every named edge is reachable
# -----------------------------------------------------------------


@pytest.mark.unit
@pytest.mark.parametrize(
    "frm,trig,out,to",
    [
        (State.DISCOVERED, Trigger.PROBE, "ok", State.PROBED),
        (State.PROBED, Trigger.EXTRACT, "ok", State.AUDIO_EXTRACTED),
        (State.PROBED, Trigger.PROBE, "no_audio", State.READY_NO_AUDIO),
        (State.AUDIO_EXTRACTED, Trigger.TRANSCRIBE, "ok", State.TRANSCRIBED),
        (State.TRANSCRIBED, Trigger.SUBTITLE_GEN, "partial", State.TRANSCRIBED),
        (State.TRANSCRIBED, Trigger.INDEX, "partial", State.TRANSCRIBED),
        (State.TRANSCRIBED, Trigger.SUBTITLE_GEN, "ok", State.INDEXED),
        (State.TRANSCRIBED, Trigger.INDEX, "ok", State.INDEXED),
        (State.INDEXED, Trigger.THUMBNAIL, "ok", State.THUMBNAILED),
        (State.THUMBNAILED, Trigger.SCAN, "all_gates_ok", State.READY),
        (State.MISSING, Trigger.SCAN, "rediscovered", State.DISCOVERED),
    ],
)
def test_lookup_explicit_edges(
    frm: State, trig: Trigger, out: str, to: State
) -> None:
    assert lookup(frm, trig, out) == to


# -----------------------------------------------------------------
# Broadcast rows: every legal source reaches the right sink
# -----------------------------------------------------------------


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [
        State.DISCOVERED,
        State.PROBED,
        State.AUDIO_EXTRACTED,
        State.TRANSCRIBED,
        State.INDEXED,
        State.THUMBNAILED,
        State.READY,
        State.READY_NO_AUDIO,
        State.MISSING,
    ],
)
def test_filesystem_deleted_reaches_missing(src: State) -> None:
    assert lookup(src, Trigger.FILESYSTEM, "deleted") == State.MISSING


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [State.SUPERSEDED, State.CORRUPTED, State.FAILED],
)
def test_filesystem_deleted_rejected_from_terminal_bad_soft(src: State) -> None:
    assert lookup(src, Trigger.FILESYSTEM, "deleted") is None


@pytest.mark.unit
@pytest.mark.parametrize("stage", [s for s in Stage])
@pytest.mark.parametrize(
    "src",
    [
        State.DISCOVERED,
        State.PROBED,
        State.AUDIO_EXTRACTED,
        State.TRANSCRIBED,
        State.INDEXED,
        State.THUMBNAILED,
        State.READY,
        State.READY_NO_AUDIO,
    ],
)
def test_exhausted_from_any_stage_reaches_failed(src: State, stage: Stage) -> None:
    assert lookup(src, Trigger(stage.value), "exhausted") == State.FAILED


@pytest.mark.unit
def test_exhausted_via_side_channel_trigger_is_rejected() -> None:
    # Side-channel triggers don't carry an exhausted edge.
    for trig in (Trigger.FILESYSTEM, Trigger.LIBRARY, Trigger.INTEGRITY):
        assert lookup(State.DISCOVERED, trig, "exhausted") is None


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [
        State.DISCOVERED,
        State.PROBED,
        State.AUDIO_EXTRACTED,
        State.TRANSCRIBED,
        State.INDEXED,
        State.THUMBNAILED,
        State.READY,
        State.READY_NO_AUDIO,
    ],
)
def test_integrity_fail_reaches_corrupted(src: State) -> None:
    assert lookup(src, Trigger.INTEGRITY, "fail") == State.CORRUPTED


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [State.MISSING, State.SUPERSEDED, State.CORRUPTED, State.FAILED],
)
def test_integrity_fail_rejected_from_non_eligible(src: State) -> None:
    assert lookup(src, Trigger.INTEGRITY, "fail") is None


@pytest.mark.unit
@pytest.mark.parametrize(
    "src",
    [
        State.DISCOVERED,
        State.PROBED,
        State.AUDIO_EXTRACTED,
        State.TRANSCRIBED,
        State.INDEXED,
        State.THUMBNAILED,
        State.READY,
        State.READY_NO_AUDIO,
        State.SUPERSEDED,  # re-supersede is allowed; pointer updates, state stays
    ],
)
def test_library_replaced_reaches_superseded(src: State) -> None:
    assert lookup(src, Trigger.LIBRARY, "replaced") == State.SUPERSEDED


@pytest.mark.unit
@pytest.mark.parametrize("src", [State.MISSING, State.CORRUPTED, State.FAILED])
def test_library_replaced_rejected_from_non_eligible(src: State) -> None:
    assert lookup(src, Trigger.LIBRARY, "replaced") is None


# -----------------------------------------------------------------
# Negative cases — the FSM must reject unmapped triples
# -----------------------------------------------------------------


@pytest.mark.unit
@pytest.mark.parametrize(
    "frm,trig,out",
    [
        # Unknown outcome.
        (State.DISCOVERED, Trigger.PROBE, "weird"),
        # Wrong trigger for the source state.
        (State.DISCOVERED, Trigger.TRANSCRIBE, "ok"),
        # Skipping a stage.
        (State.AUDIO_EXTRACTED, Trigger.THUMBNAIL, "ok"),
        # READY_NO_AUDIO is terminal-good — extract is not allowed.
        (State.READY_NO_AUDIO, Trigger.EXTRACT, "ok"),
        # subtitle_gen before transcribe (plan §11.6 explicit example).
        (State.AUDIO_EXTRACTED, Trigger.SUBTITLE_GEN, "ok"),
        # Self-loop probe/ok on PROBED.
        (State.PROBED, Trigger.PROBE, "ok"),
    ],
)
def test_lookup_rejects_invalid_triples(
    frm: State, trig: Trigger, out: str
) -> None:
    assert lookup(frm, trig, out) is None


# -----------------------------------------------------------------
# allowed_targets view satisfies the story's `dict[State, set[State]]`
# -----------------------------------------------------------------


@pytest.mark.unit
def test_allowed_targets_covers_every_state() -> None:
    assert set(allowed_targets.keys()) == set(State)


@pytest.mark.unit
def test_allowed_targets_for_open_state_includes_broadcasts() -> None:
    # DISCOVERED is class=open: it can reach MISSING (filesystem),
    # FAILED (any-stage exhausted), CORRUPTED (integrity), SUPERSEDED
    # (library), plus its explicit edge to PROBED.
    targets = allowed_targets[State.DISCOVERED]
    for required in (
        State.PROBED,
        State.MISSING,
        State.FAILED,
        State.CORRUPTED,
        State.SUPERSEDED,
    ):
        assert required in targets


@pytest.mark.unit
def test_allowed_targets_for_terminal_bad_is_empty() -> None:
    # CORRUPTED and FAILED have no outbound edges.
    assert allowed_targets[State.CORRUPTED] == frozenset()
    assert allowed_targets[State.FAILED] == frozenset()


@pytest.mark.unit
def test_allowed_targets_missing_only_re_enters_discovered() -> None:
    # MISSING has one explicit outbound edge (→ DISCOVERED) plus one
    # broadcast self-loop on filesystem/deleted.
    assert allowed_targets[State.MISSING] == frozenset(
        {State.DISCOVERED, State.MISSING}
    )


@pytest.mark.unit
def test_terminal_drop_states_match_constant() -> None:
    assert frozenset(
        {State.SUPERSEDED, State.CORRUPTED, State.FAILED}
    ) == TERMINAL_DROP_STATES


# -----------------------------------------------------------------
# IllegalStateTransition payload
# -----------------------------------------------------------------


@pytest.mark.unit
def test_illegal_state_transition_carries_triple() -> None:
    err = IllegalStateTransition(
        "abc", State.DISCOVERED, Trigger.TRANSCRIBE, "ok"
    )
    assert err.from_ is State.DISCOVERED
    assert err.trigger is Trigger.TRANSCRIBE
    assert err.outcome == "ok"
    msg = str(err)
    for token in ("abc", "discovered", "transcribe", "ok"):
        assert token in msg


# -----------------------------------------------------------------
# Outcome enum is closed (tests that EXPLICIT_TRANSITIONS only uses
# values from Outcome, sanity check that no typos slipped in).
# -----------------------------------------------------------------


@pytest.mark.unit
def test_explicit_transitions_use_known_outcomes() -> None:
    known = {o.value for o in Outcome}
    for key in EXPLICIT_TRANSITIONS:
        assert key.outcome in known, key.outcome


@pytest.mark.unit
def test_broadcast_rows_use_known_outcomes() -> None:
    known = {o.value for o in Outcome}
    for row in BROADCAST_TRANSITIONS:
        assert row.outcome in known, row.outcome
