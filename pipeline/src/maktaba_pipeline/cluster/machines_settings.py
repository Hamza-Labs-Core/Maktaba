"""``rules.cluster``: how the machines of a cluster share sign-ins and serve pages (Epic 35
stories 35.9, 35.10), and how a full machine runs on its own copy of the home's database
while the home is away (Epic 37). Read by the APIs (the home's and every full node's); the
pipeline only declares the section so the settings page shows it."""

from __future__ import annotations

from dataclasses import dataclass

from .. import settings

__all__ = ["ClusterMachinesConfig"]


@dataclass(frozen=True)
class ClusterMachinesConfig:
    """Sign-in across machines and what a full node serves for the home."""

    #: a signed-in user who opens another machine is offered "Continue with <home>"
    sso: bool = True
    #: after this long without an answer from the home, a node stops asking it per request
    #: (it signs people in and answers them from its own copy of the home's database)
    delegate_unreachable_grace_seconds: int = 30
    #: a node's copy answers for sign-ins with the home away only if it had followed the
    #: home up to this close to when the home was last heard
    offline_copy_max_lag_minutes: int = 10
    #: a sign-in made on a node while the home is away lasts this long
    offline_session_hours: int = 12
    #: and ends this long after the home answers again
    offline_session_grace_minutes: int = 30
    #: the home keeps this much of its changes for a machine that fell behind; past it the
    #: machine copies the home's database anew
    replica_wal_keep_gb: int = 20
    #: a node's disk cache for the pictures it fetches from the home (posters, sprites)
    proxy_cache_mb: int = 2048


_C = "Machines"
settings.register(ClusterMachinesConfig, {
    "sso": settings.Help(_C, "One sign-in for every machine",
                         "Opening another machine offers \"Continue with <home>\": a one-time "
                         "code carries the sign-in there. Off: each machine asks for its own "
                         "sign-in (still against the home's accounts)."),
    "delegate_unreachable_grace_seconds": settings.Help(
        _C, "Home unreachable after (s)",
        "A machine that cannot reach the home this long stops asking it for every request: "
        "it answers from its own copy of the home's database instead (sign-in, reading, "
        "playback, TV), and changes wait for the home.",
        minimum=5, maximum=600),
    "offline_copy_max_lag_minutes": settings.Help(
        _C, "Copy current enough to sign in (min)",
        "Every full machine keeps a live, read-only copy of the home's database and runs on it "
        "while the home is away. Its copy answers sign-ins only if it had followed the home up "
        "to this close to when the home was last heard: a copy further behind might not have a "
        "sign-out or a password change made since.",
        minimum=1, maximum=1440),
    "offline_session_hours": settings.Help(
        _C, "Sign-in without the home lasts (h)",
        "Signing in on a machine while the home is away checks the password against the "
        "machine's copy and is valid on that machine only, this long.",
        minimum=1, maximum=720),
    "offline_session_grace_minutes": settings.Help(
        _C, "Then ends after the home is back (min)",
        "Once the home answers again such a sign-in keeps reading and playing this long; a "
        "change asks for a sign-in with the home at once.",
        minimum=0, maximum=1440),
    "replica_wal_keep_gb": settings.Help(
        _C, "Changes kept for a machine behind (GB)",
        "The home keeps this much of its recent changes for a machine whose copy fell behind "
        "(it was off, or away). A machine further behind copies the home's database anew, on "
        "its own.",
        minimum=1, maximum=1024),
    "proxy_cache_mb": settings.Help(_C, "Picture cache per machine (MB)",
                                    "Posters and sprites a machine fetched from the home.",
                                    minimum=0, maximum=1_048_576),
})  # fmt: skip
