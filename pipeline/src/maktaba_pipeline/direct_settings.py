"""The ``direct`` rules (Epic 50, story 50.15): direct remote access, the home reached from
outside over HTTPS without the relay. The pipeline never reads them; they live here because
every rules section's schema (``api/internal/handlers/rules/schema.json``) is generated from
one place. The API's ``cloud.direct`` loops follow them.

``enabled`` starts off: a release ships direct access off, and it is turned on for new homes
once the direct-name domains are delegated (the story's rollout)."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["DirectConfig"]

DIR = "Direct access"


@dataclass(frozen=True)
class DirectConfig:
    """Direct remote access (rules ``direct``)."""

    #: let apps reach the home directly from outside, over HTTPS, without the relay
    enabled: bool = False
    #: ask the router to open the port (UPnP-IGD, NAT-PMP, then PCP)
    map_port: bool = True
    #: the public port the admin forwarded by hand (used when map_port is off); 0: none
    manual_port: int = 0


register(
    DirectConfig,
    {
        "enabled": Help(
            DIR,
            "Let apps reach this home directly from outside, without the relay",
            "The home opens a port on the router, gets its own HTTPS certificate and is "
            "checked from outside; apps then play from it directly. Free on every plan.",
        ),
        "map_port": Help(
            DIR,
            "Open the port on the router",
            "Asks the router (UPnP, NAT-PMP or PCP) to open a port to this home every 20 "
            "minutes. Off: forward a port by hand and give it below.",
        ),
        "manual_port": Help(
            DIR,
            "Public port",
            "The port forwarded by hand on the router to this home's direct port. 0: none.",
            minimum=0,
            maximum=65535,
        ),
    },
)
