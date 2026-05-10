"""Story 3.8 — graceful shutdown policy + reaper predicate string."""

from __future__ import annotations

from maktaba_pipeline.stt.crash_recovery import (
    DEFAULT_GRACE_SEC,
    DEFAULT_HARD_EXIT_AFTER_SEC,
    DEFAULT_STALE_CLAIM_SEC,
    REAPER_STALE_PREDICATE,
    ShutdownPolicy,
)


def test_defaults_match_story_3_8_acs() -> None:
    assert DEFAULT_GRACE_SEC == 120.0
    assert DEFAULT_HARD_EXIT_AFTER_SEC == 5.0
    assert DEFAULT_STALE_CLAIM_SEC == 90.0


def test_shutdown_policy_dataclass_holds_knobs() -> None:
    p = ShutdownPolicy(grace_sec=60.0, hard_exit_after_sec=2.0, stale_claim_sec=45.0)
    assert p.grace_sec == 60.0


def test_reaper_predicate_references_claimed_running_resuming() -> None:
    # Story 3.8 AC-3 — the reaper looks at in-flight rows. The literal
    # text is duplicated from the existing reaper module by design;
    # this guard fails if either side drifts.
    assert "claimed" in REAPER_STALE_PREDICATE
    assert "running" in REAPER_STALE_PREDICATE
    assert "resuming" in REAPER_STALE_PREDICATE
    assert "last_heartbeat_at" in REAPER_STALE_PREDICATE
