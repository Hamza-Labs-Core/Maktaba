"""The ``tv_staging`` rules (Epic 33, README "Settings"): every TV change goes through
staging. The defaults are the owner's decisions, not "the current behaviour": staging is
on, a watched channel switches at its programme boundary, the hot standby is on - and
"only manual needs approval" (2026-10-28): a change a person made (``origin = manual``:
the channel editor, a lineup or settings edit, an admin's change, a rollback an admin
pressed) always waits for an admin's approval; a change the system made (``origin =
auto``: the channel plan, the schedule, the updater's binary, an automatic rollback) goes
on the air by its kind's auto rule, ``immediate`` or ``quiet_window``, with no approval
and no limit on the channels it touches.

The README lists ``auto_promote`` as one map; here it is one setting per kind
(``auto_promote_<kind>``) so the settings page shows each with its choices."""

from __future__ import annotations

from dataclasses import dataclass

from ..settings import Help, register

__all__ = ["AUTO_RULES", "CHANGE_KINDS", "MaktabaTvStagingConfig", "auto_rule", "origin_for"]

#: what a TV change may carry (story 33.10 AC3)
CHANGE_KINDS = ("lineup", "schedule", "settings", "art", "live_slots", "binary", "rollback")
#: an automatic change's rules, strictest first (a manual change's is always "approval";
#: a stored "approval" from before reads as "immediate")
AUTO_RULES = ("quiet_window", "immediate")
#: the producers whose changes are automatic (the API's tvchange.OriginFor)
AUTO_SOURCES = ("write_channels", "schedule", "auto_update")

TST = "TV staging"
TSA = "TV staging approval"
TSS = "TV staging swap"


@dataclass(frozen=True)
class MaktabaTvStagingConfig:
    """How TV changes are staged, approved and put on the air (rules ``tv_staging``)."""

    #: changes go through staging (owner). Off: direct writes as before Epic 33
    enabled: bool = True
    #: an automatic change of each kind goes on the air once verified (owner, 2026-10-28)
    auto_promote_lineup: str = "immediate"
    auto_promote_settings: str = "immediate"
    auto_promote_live_slots: str = "immediate"
    auto_promote_art: str = "immediate"
    auto_promote_schedule: str = "immediate"
    auto_promote_binary: str = "immediate"
    auto_promote_rollback: str = "immediate"
    #: local window ("HH:MM-HH:MM") for an auto-promote rule switched on (and front-door
    #: restarts)
    quiet_window: str = "03:00-05:00"
    quiet_max_viewers: int = 0
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


def origin_for(source_kind: str, created_by: str | None = None) -> str:
    """Who made a change a producer of ``source_kind`` opens (``tv_changes.origin``, the
    API's tvchange.OriginFor): ``auto`` for the channel plan, the schedule and the
    updater's binary, and for a rollback or re-stage no person asked for; ``manual``
    otherwise (it waits for an approval)."""
    if source_kind in AUTO_SOURCES:
        return "auto"
    if source_kind in ("rollback", "restage") and not created_by:
        return "auto"
    return "manual"


def auto_rule(
    cfg: MaktabaTvStagingConfig,
    kinds: list[str] | tuple[str, ...],
    source_kind: str = "",
    origin: str | None = None,
) -> str:
    """A change's rule (the API's tvchange.ChangeRule; the owner's rule of 2026-10-28):
    ``approval`` for a manual change; for an automatic one the strictest auto rule among
    ``kinds`` (``quiet_window`` or ``immediate``; a stored ``approval`` reads as
    ``immediate``), and ``immediate`` for the updater's change of the streaming binary
    alone (Epic 35 story 35.6). ``origin`` None: the producer's (:func:`origin_for`)."""
    if (origin or origin_for(source_kind)) != "auto":
        return "approval"
    if source_kind == "auto_update" and list(kinds) == ["binary"]:
        return "immediate"
    rules = [str(getattr(cfg, f"auto_promote_{k}", "immediate")) for k in kinds]
    return "quiet_window" if "quiet_window" in rules else "immediate"


_RULE = (
    "When an automatic change of this kind (the channel plan, the schedule, the update's "
    "streaming software) goes on the air: immediate, as soon as it is verified; "
    "quiet_window, in the quiet window. A change a person made always waits for an "
    "admin's approval."
)

register(
    MaktabaTvStagingConfig,
    {
        "enabled": Help(
            TST,
            "Stage every TV change",
            "A lineup, schedule, settings or art change is prepared next to what is on the "
            "air and checked before it goes on the air. A change a person made waits for an "
            "admin's approval on the Changes page (Live TV -> Changes); an automatic one goes "
            "on by the rules below. On by default. Off: changes go on the air at once, as "
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
        "require_second_admin": Help(
            TSA,
            "A second admin approves",
            "A change a person made is approved by an admin other than its author.",
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
