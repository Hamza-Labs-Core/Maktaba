"""How ``write_channels`` links a planned channel to the channel it wrote (Epic 54 wave 7,
story 54.34): an ``external_links`` row with this target and subject kind. In the core
because the TV release code (:mod:`maktaba_pipeline.tvrelease`) reads and writes these
links too; the stage itself is ``tv.maktaba.livetv``'s."""

from __future__ import annotations

__all__ = ["SUBJECT_KIND", "TARGET"]

TARGET = "maktaba_channel"
SUBJECT_KIND = "catalog"
