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
    #: after this long without an answer from the home, a node refuses changes
    delegate_unreachable_grace_seconds: int = 30
    #: meanwhile a node stays readable this long (pages open, changes refused); 0: refuse
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
        "A machine that cannot reach the home this long stops taking changes until it can "
        "again (reading follows the next setting); its own status pages keep working.",
        minimum=5, maximum=600),
    "offline_read_minutes": settings.Help(
        _C, "Stay readable without the home (min)",
        "While a machine cannot reach the home, people already signed in keep reading pages "
        "this long, with changes refused and a notice on top. 0: refuse everything at once. "
        "A sign-out made on the home during the outage reaches this machine only when it is back.",
        minimum=0, maximum=1440),
    "proxy_cache_mb": settings.Help(_C, "Picture cache per machine (MB)",
                                    "Posters and sprites a machine fetched from the home.",
                                    minimum=0, maximum=1_048_576),
})  # fmt: skip
