"""Cheap upper-bound token estimator.

The packer needs a token budget check before emitting a unit so we
don't blow the embedding model's context window. Production uses
``intfloat/multilingual-e5-*`` (sentencepiece, ~3-4 chars/token on
Arabic), so ``ceil(len/3)`` is a safe over-estimate for both Arabic
and English. No model load required.
"""

from __future__ import annotations

import math

__all__ = ["estimate_tokens"]


def estimate_tokens(text: str) -> int:
    """Return a conservative (upper-bound) token count for ``text``.

    ``ceil(len(text) / 3)`` — slightly pessimistic for English (one
    token is closer to 4 chars), tight for Arabic. The packer uses
    this as a hard cap before emitting a unit.
    """
    return math.ceil(len(text) / 3)
