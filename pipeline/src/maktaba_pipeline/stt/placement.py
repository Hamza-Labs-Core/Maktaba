"""``rules.transcription``: which machines transcribe the home's files (review of story
38.8).

The second listen (Cohere Transcribe Arabic, :mod:`.refine`) runs where its engine does:
Apple Silicon (MLX). While it is on (``MAKTABA_STT_REFINE``), a machine that cannot run
it leaves ``transcribe`` to the machines that can, so every transcript gets it. These
rules let such a machine transcribe too: its transcripts then keep Whisper's words and
say why, and a machine that runs the second listen transcribes them again later
(:mod:`maktaba_pipeline.engine.stages.transcribe`, ``relisten``).
"""

from __future__ import annotations

from dataclasses import dataclass

from .. import settings

__all__ = ["TranscriptionConfig"]


@dataclass(frozen=True)
class TranscriptionConfig:
    """Which machines transcribe."""

    #: a machine that cannot run the second listen transcribes too (Whisper's words,
    #: given their second listen later by a machine that can)
    without_second_listen: bool = False


_T = "Transcription"
settings.register(TranscriptionConfig, {
    "without_second_listen": settings.Help(
        _T, "Transcribe without the second listen",
        "Off: while the second listen is on, only the machines that run it (Apple "
        "Silicon) transcribe, so every transcript has it. On: a Windows or Linux machine "
        "transcribes too, with Whisper alone, faster; a machine that runs the second "
        "listen gives those transcripts theirs later, a few at a time, after the new "
        "files."),
})  # fmt: skip
