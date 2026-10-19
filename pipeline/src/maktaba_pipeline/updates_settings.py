"""The ``updates`` rules (Epic 35, story 35.3, README "Settings (rules.updates)"): how the
home finds new signed releases, when it installs them and how it rolls them out to the
machines. The pipeline never reads them; they live here because every rules section's
schema (``api/internal/handlers/rules/schema.json``) is generated from one place.

``auto_install`` defaults to true (owner decision, Epic 35 decision "Automatic"); until
the home's update orchestrator ships (stories 35.4-35.7) the API keeps automatic installs
off whatever this says (phase A), and only checks and notifies."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["UpdatesConfig"]

UPD = "Updates"
UPR = "Updates rollout"
UPS = "Updates safety"


@dataclass(frozen=True)
class UpdatesConfig:
    """Automatic updates of the home and its machines (rules ``updates``)."""

    #: install a new signed release in the quiet window (owner). Off: check and notify only
    auto_install: bool = True
    #: stable | beta. Switching to stable never installs a lower version
    channel: str = "stable"
    #: home's local time, HH:MM-HH:MM (may cross midnight); a run starts only inside it
    window: str = "03:00-05:00"
    window_overrun_minutes: int = 120
    #: a release must be this old before it installs by itself; 0: as soon as it is seen
    min_release_age_hours: int = 24
    #: stay at or below this version; empty: no pin
    pinned_version: str = ""
    #: no automatic run before this instant (RFC 3339); empty: not deferred
    deferred_until: str = ""
    #: never installed automatically (a newer one is)
    skipped_versions: tuple[str, ...] = ()
    #: hold_platform | halt | continue
    node_failure_policy: str = "hold_platform"
    node_health_timeout_minutes: int = 10
    drain_timeout_minutes: int = 10
    rehearse_migrations: bool = True
    rehearse_max_db_gb: int = 20
    keep_versions: int = 3
    backups_keep: int = 3
    #: immediate | window
    catch_up: str = "immediate"
    proxy_cache_mb: int = 2048


register(
    UpdatesConfig,
    {
        "auto_install": Help(
            UPD,
            "Install updates automatically",
            "A new signed release is installed on the home in the quiet window, then on "
            "every machine one by one, each checked before the next. Off: Maktaba only "
            "checks and tells you an update is available.",
        ),
        "channel": Help(
            UPD,
            "Release channel",
            "stable: releases only; beta: pre-releases too. Going back to stable never "
            "installs a lower version.",
            choices=("stable", "beta"),
        ),
        "window": Help(
            UPD,
            "Quiet window",
            "Home's local time, HH:MM-HH:MM (23:00-01:00 crosses midnight). An update starts "
            "only inside it, at least 30 minutes before its end.",
        ),
        "window_overrun_minutes": Help(
            UPD,
            "May run past the window (minutes)",
            "A started update may finish the TV swap and the machines this long after the window.",
            minimum=0,
            maximum=720,
        ),
        "min_release_age_hours": Help(
            UPD,
            "Release age before installing (hours)",
            "A release must be this old before it installs by itself (a release pulled "
            "within a day is never installed). 0: as soon as it is seen. Update now ignores "
            "it.",
            minimum=0,
            maximum=8760,
        ),
        "pinned_version": Help(
            UPD,
            "Stay at or below version",
            "For example 1.5.9. Never below the installed version. Empty: no pin.",
        ),
        "deferred_until": Help(
            UPD,
            "No automatic update before",
            "An instant (2026-10-24T03:00:00Z). Empty: not deferred.",
        ),
        "skipped_versions": Help(
            UPD, "Skipped versions", "Never installed automatically; a newer one is."
        ),
        "node_failure_policy": Help(
            UPR,
            "When a machine fails",
            "hold_platform: stop the machines of the same system (OS and processor) and go on "
            "with the others; halt: stop the rollout; continue: go on with every machine.",
            choices=("hold_platform", "halt", "continue"),
        ),
        "node_health_timeout_minutes": Help(
            UPR, "A machine is healthy within (minutes)", minimum=1, maximum=240
        ),
        "drain_timeout_minutes": Help(
            UPR,
            "Longest wait for running jobs (minutes)",
            "Transcriptions resume from their checkpoints.",
            minimum=0,
            maximum=240,
        ),
        "catch_up": Help(
            UPR,
            "A machine that was offline is updated",
            "immediate: as soon as it is back (streaming machines in the window); window: in "
            "the next quiet window.",
            choices=("immediate", "window"),
        ),
        "proxy_cache_mb": Help(UPR, "Image cache on a machine (MB)", minimum=0, maximum=1_048_576),
        "rehearse_migrations": Help(
            UPS,
            "Rehearse database changes first",
            "A release that changes the database tries the change on a copy first.",
        ),
        "rehearse_max_db_gb": Help(
            UPS,
            "Rehearse only up to (GB)",
            "A larger database skips the rehearsal (the backup still runs).",
            minimum=0,
            maximum=100_000,
        ),
        "keep_versions": Help(
            UPS,
            "Versions kept on each machine",
            "Never fewer than the running one and the one before.",
            minimum=2,
            maximum=20,
        ),
        "backups_keep": Help(UPS, "Backups before an update kept", minimum=1, maximum=50),
    },
)
