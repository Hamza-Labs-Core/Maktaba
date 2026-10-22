"""``rules.transcription``: which machines transcribe the home's files (review of story
38.8).

The second listen (Cohere Transcribe Arabic, :mod:`.refine`) runs where its engine does:
Apple Silicon (MLX). While it is on (``MAKTABA_STT_REFINE``), a machine that cannot run
it leaves ``transcribe`` to the machines that can, so every transcript gets it. These
rules let such a machine transcribe too: its transcripts then keep Whisper's words and
say why, and a machine that runs the second listen transcribes them again later
(:mod:`maktaba_pipeline.engine.stages.transcribe`, ``relisten``): a few tries each,
further apart each time, then left as they are until something changes (the model, a
machine that runs it) or the operator asks again (``relisten_round``).
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
    #: the relisten tries a transcript made without the second listen gets before it is
    #: left as it is (said once, in its job's log)
    relisten_tries: int = 6
    #: the wait after a try that left it without the second listen (hours, from the end
    #: of the try), twice as long after each further one...
    relisten_wait_hours: float = 1.0
    #: ...at most this long
    relisten_wait_max_hours: float = 24.0
    #: raised by the operator: every transcript tried in an earlier round gets its tries
    #: again
    relisten_round: int = 0


_T = "Transcription"
settings.register(TranscriptionConfig, {
    "relisten_tries": settings.Help(
        _T, "Second-listen tries per transcript",
        "How many times a machine that runs the second listen tries again a transcript "
        "made without it, when it cannot give it its own (it cannot read the file, or "
        "its model does not load), before it leaves the transcript as it is and says so "
        "once in Processing, Activity.",
        minimum=1, maximum=50),
    "relisten_wait_hours": settings.Help(
        _T, "Wait after a try (hours)",
        "After a try that left the transcript without its second listen, the next one "
        "waits this long from the end of the try, and twice as long after each further "
        "try.",
        minimum=0.1, maximum=168),
    "relisten_wait_max_hours": settings.Help(
        _T, "Longest wait between tries (hours)",
        "The wait between two tries doubles up to this.",
        minimum=0.1, maximum=720),
    "relisten_round": settings.Help(
        _T, "Try the stopped transcripts again (round)",
        "Raise this by one to give every transcript the second listen stopped on its "
        "tries again, for example once its share or its model is reachable again. A new "
        "second-listen model, or a machine that runs it with a new GPU or new packages, "
        "does the same by itself.",
        minimum=0),
    "without_second_listen": settings.Help(
        _T, "Transcribe without the second listen",
        "Off: while the second listen is on, only the machines that run it (Apple "
        "Silicon) transcribe, so every transcript has it. On: a Windows or Linux machine "
        "transcribes too, with Whisper alone, faster; a machine that runs the second "
        "listen gives those transcripts theirs later, a few at a time, after the new "
        "files."),
})  # fmt: skip
