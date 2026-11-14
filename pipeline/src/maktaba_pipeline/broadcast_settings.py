"""The ``broadcast`` rules (Epic 50, story 50.22): how the home broadcasts its published Live
TV channels to the broadcast edge. The pipeline never reads them; they live here because
every rules section's schema (``api/internal/handlers/rules/schema.json``) is generated from
one place. The API's publish checks and streaming's broadcaster follow them."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["BroadcastConfig"]

SEC = "Broadcasting"


@dataclass(frozen=True)
class BroadcastConfig:
    """Home broadcasting (rules ``broadcast``)."""

    #: the share of the measured upload that published channels may use
    uplink_share: float = 0.5
    #: how much of a channel slot each encoded lower quality counts for
    rung_slot_weight: float = 0.5
    #: encode the lower qualities in software when there is no hardware encoder
    allow_software_rungs: bool = False


register(
    BroadcastConfig,
    {
        "uplink_share": Help(
            SEC,
            "Share of the upload for broadcasting",
            "Published channels together may use at most this share of the measured internet "
            "upload; the rest stays free for the home's other traffic.",
            minimum=0.2,
            maximum=0.8,
        ),
        "rung_slot_weight": Help(
            SEC,
            "Slot weight of a lower quality",
            "Each lower quality a published channel encodes counts as this much of a channel "
            "slot when checking whether the machine has room.",
            minimum=0.1,
            maximum=1.0,
        ),
        "allow_software_rungs": Help(
            SEC,
            "Encode lower qualities in software",
            "Without a hardware encoder, the lower qualities are encoded on the processor, "
            "about one core each. Off: a machine without one publishes only the channel's own "
            "quality.",
        ),
    },
)
