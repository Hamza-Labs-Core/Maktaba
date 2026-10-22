"""The ``tv_staging`` rules (Epic 33, README "Settings"): every TV change goes through
staging and waits for an admin's approval (owner decisions). The defaults are the owner's
decisions, not "the current behaviour": staging is on, every kind of change needs an
approval except a small schedule change (it goes on the air once verified), a watched
channel switches at its programme boundary, the hot standby is on.

The README lists ``auto_promote`` as one map; here it is one setting per kind
(``auto_promote_<kind>``) so the settings page shows each with its choices."""

from __future__ import annotations

from dataclasses import dataclass

from ..settings import Help, register

__all__ = ["AUTO_RULES", "CHANGE_KINDS", "MaktabaTvStagingConfig", "auto_rule"]

#: what a TV change may carry (story 33.10 AC3)
CHANGE_KINDS = ("lineup", "schedule", "settings", "art", "live_slots", "binary", "rollback")
#: auto-promote rules, strictest first
AUTO_RULES = ("approval", "quiet_window", "immediate")

TST = "TV staging"
TSA = "TV staging approval"
TSS = "TV staging swap"


@dataclass(frozen=True)
class MaktabaTvStagingConfig:
    """How TV changes are staged, approved and put on the air (rules ``tv_staging``)."""

    #: changes go through staging (owner). Off: direct writes as before Epic 33
    enabled: bool = True
    auto_promote_lineup: str = "approval"
    auto_promote_settings: str = "approval"
    auto_promote_live_slots: str = "approval"
    auto_promote_art: str = "approval"
    #: the planner's daily schedule refreshes go on the air by themselves (owner, Oct 2026)
    auto_promote_schedule: str = "immediate"
    auto_promote_binary: str = "approval"
    auto_promote_rollback: str = "approval"
    #: local window ("HH:MM-HH:MM") for an auto-promote rule switched on (and front-door
    #: restarts)
    quiet_window: str = "03:00-05:00"
    quiet_max_viewers: int = 0
    #: a change an auto rule would put on the air waits for an admin when it touches
    #: more channels than this (or the planner left conflicts out); 0: no limit
    auto_max_channels: int = 3
    #: boundary: a watched channel switches at its next programme boundary; immediate
    swap_wait: str = "boundary"
    boundary_max_wait_minutes: int = 30
    #: a swap no front door took within this fails and is rolled back (the API's
    #: change controller); 0: it waits for the front door however long
    swap_start_timeout_minutes: int = 10
    keep_warm_minutes: int = 15
    rollback_retention_hours: int = 24
    verify_soak_seconds: int = 120
    verify_probe_seconds: int = 30
    #: slug; empty: the lowest-numbered live channel
    probe_channel: str = ""
    staging_max_extra_encodes: int = 0
    rolling_swap: str = "auto"
    auto_rollback: bool = True
    hot_standby: bool = True
    #: viewers (default, the owner's call for story 33.1: the standby never spends an
    #: encoder on a channel nobody watches) | demand
    standby_warm: str = "viewers"
    require_second_admin: bool = False
    param_change_push: str = "approve"


def auto_rule(cfg: MaktabaTvStagingConfig, kinds: list[str] | tuple[str, ...]) -> str:
    """The strictest auto-promote rule among ``kinds`` (story 33.10 AC6)."""
    rules = [str(getattr(cfg, f"auto_promote_{k}", "approval")) for k in kinds] or ["approval"]
    return min(rules, key=lambda r: AUTO_RULES.index(r) if r in AUTO_RULES else 0)


_RULE = (
    "approval: an admin approves it; quiet_window: put on the air in the quiet window "
    "with no warnings; immediate: as soon as it is verified with no warnings."
)

register(
    MaktabaTvStagingConfig,
    {
        "enabled": Help(
            TST,
            "Stage every TV change",
            "A lineup, schedule, settings or art change is prepared next to what is on the "
            "air, checked, and put on the air only when an admin approves it on the Changes "
            "page (Live TV -> Changes). On by default. Off: changes go on the air at once, as "
            "before Epic 33.",
        ),
        **{
            f"auto_promote_{k}": Help(
                TSA, f"{k.replace('_', ' ').capitalize()} changes", _RULE, choices=AUTO_RULES
            )
            for k in CHANGE_KINDS
        },
        "quiet_window": Help(
            TSA, "Quiet window", "Local time, HH:MM-HH:MM, for a rule switched to quiet_window."
        ),
        "quiet_max_viewers": Help(
            TSA, "Viewers at most in the quiet window", minimum=0, maximum=1000
        ),
        "auto_max_channels": Help(
            TSA,
            "Changed channels at most for an automatic change",
            "A change a rule above would put on the air by itself waits for an admin when it "
            "touches more channels than this, or the planner left a conflict out. 0: no limit.",
            minimum=0,
            maximum=1000,
        ),
        "require_second_admin": Help(
            TSA, "A second admin approves", "The approver must not be the change's author."
        ),
        "swap_wait": Help(
            TSS,
            "When a channel switches",
            "boundary: a watched channel switches at the end of the programme on the air; "
            "immediate: every channel at once.",
            choices=("boundary", "immediate"),
        ),
        "boundary_max_wait_minutes": Help(
            TSS, "Longest wait for a boundary (minutes)", minimum=0, maximum=600
        ),
        "swap_start_timeout_minutes": Help(
            TSS,
            "Swap not started after (minutes)",
            "A swap no front door has started by then is rolled back, with a notice. "
            "0: wait for the front door.",
            minimum=0,
            maximum=1440,
        ),
        "keep_warm_minutes": Help(
            TSS, "Previous version kept warm (minutes)", "For an instant rollback.", minimum=0,
            maximum=1440,
        ),
        "rollback_retention_hours": Help(
            TSS, "Previous version kept (hours)", "For a rollback after the warm period.",
            minimum=1, maximum=720,
        ),
        "verify_soak_seconds": Help(TSS, "Checks run for (seconds)", minimum=0, maximum=3600),
        "verify_probe_seconds": Help(
            TSS, "Each changed channel probed for (seconds)", minimum=5, maximum=600
        ),
        "probe_channel": Help(TSS, "Probe channel", "A slug; empty: the lowest-numbered."),
        "staging_max_extra_encodes": Help(
            TSS, "Extra encodes while staging", "0: what the machine has free.", minimum=0,
            maximum=64,
        ),
        "rolling_swap": Help(
            TSS, "Rolling swap", "When there are not enough encoders to run both versions.",
            choices=("auto", "always", "never"),
        ),
        "auto_rollback": Help(
            TSS, "Roll back automatically", "A crash or a failed check just after a switch."
        ),
        "hot_standby": Help(
            TSS, "Hot standby", "A second copy of what is on the air takes over at once on a crash."
        ),
        "standby_warm": Help(
            TSS,
            "What the standby keeps warm",
            "viewers: the channels people watch first, then the remote pushes; demand: also "
            "what the live process keeps warm after its last viewer. Live gets the encoders "
            "first; a channel the standby does not hold fails over with one short stall.",
            choices=("viewers", "demand"),
        ),
        "param_change_push": Help(
            TSS,
            "Picture changes on pushed channels",
            "approve: the approval acknowledges that simulcast outputs may reconnect; defer: "
            "that channel switches when its push ends.",
            choices=("approve", "defer"),
        ),
    },
)  # fmt: skip
