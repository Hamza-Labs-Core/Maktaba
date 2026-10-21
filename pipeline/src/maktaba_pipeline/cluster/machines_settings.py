"""``rules.cluster``: how the machines of a cluster share sign-ins and serve pages (Epic 35
stories 35.9, 35.10). Read by the APIs (the home's and every full node's); the pipeline
only declares the section so the settings page shows it."""

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
    #: (sign-in and changes refused at once)
    delegate_unreachable_grace_seconds: int = 30
    #: meanwhile a node serves reads and playback on the home's last answer this long; 0: none
    offline_read_minutes: int = 10
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
        "signing in and changes are refused at once until it answers again (reading and "
        "playback follow the next setting); its own status pages keep working.",
        minimum=5, maximum=600),
    "offline_read_minutes": settings.Help(
        _C, "Stay readable without the home (min)",
        "While a machine cannot reach the home, people the home already confirmed keep reading "
        "pages and playing the videos this machine can read, this long from the outage's start, "
        "under a notice. Signing in or out and changes are refused (not kept for later) until "
        "the home answers. 0: none of it. A sign-out made on the home during the outage reaches "
        "this machine only when it is back.",
        minimum=0, maximum=1440),
    "proxy_cache_mb": settings.Help(_C, "Picture cache per machine (MB)",
                                    "Posters and sprites a machine fetched from the home.",
                                    minimum=0, maximum=1_048_576),
})  # fmt: skip
