"""Epic 3 — speech-to-text backends, registry, and the segment hot path.

Module map:

- :mod:`.protocol`        Story 3.1 — :class:`STTBackend` Protocol + ``Segment`` schema
- :mod:`.mlx`             Story 3.2 — Whisper MLX backend
- :mod:`.faster_whisper`  Story 3.3 — Faster-Whisper CUDA / CPU backend
- :mod:`.openai_api`      Story 3.4 — OpenAI Whisper API backend (chunked, budget-capped)
- :mod:`.registry`        Story 3.5 — health-filtered registry + fallback walk
- :mod:`.segment_commit`  Story 3.6 — atomic per-segment DB commit
- :mod:`.pause_resume`    Story 3.7 — pause/resume helpers (prompt rebuild, seek)
- :mod:`.crash_recovery`  Story 3.8 — graceful shutdown + reaper integration
- :mod:`.diarization`     Story 3.9 — opt-in pyannote diarization stub

Heavy backend imports (``mlx_whisper``, ``faster_whisper``, ``openai``,
``pyannote``) are lazy: importing this package or :mod:`.protocol`
costs nothing beyond the dataclass definitions.
"""

from __future__ import annotations

from .protocol import (
    AudioSource,
    BackendHealth,
    Segment,
    STTBackend,
    TranscriptionHints,
    Word,
)
from .registry import BackendRegistry, NoBackendReady, pick_backend
from .segment_commit import CommitResult, commit_segment

__all__ = [
    "AudioSource",
    "BackendHealth",
    "BackendRegistry",
    "CommitResult",
    "NoBackendReady",
    "STTBackend",
    "Segment",
    "TranscriptionHints",
    "Word",
    "commit_segment",
    "pick_backend",
]
