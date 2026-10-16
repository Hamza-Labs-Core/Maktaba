"""Error-cause normalisation (plan 06-11 section 6.3).

``error_cause`` is computed once, at failure time, so the issues panel can
GROUP BY ``(stage, error_cause)`` and keep stable groups across message noise:
paths, numbers and ids must not split a group.
"""

from __future__ import annotations

import re

__all__ = ["classify", "norm"]

_MAX_LEN = 120

# Service-specific patterns come first: a Plex timeout is a Plex problem, not a
# NAS one, even though both say "timed out".
_KNOWN: tuple[tuple[str, re.Pattern[str]], ...] = tuple(
    (cause, re.compile(pattern, re.IGNORECASE))
    for cause, pattern in (
        ("youtube_bot_check", r"confirm you.?re not a bot|sign in to confirm"),
        (
            "youtube_unavailable",
            r"video unavailable|this channel does not exist|http error 404.*youtube"
            r"|youtube.*http error 404",
        ),
        ("plex_auth", r"plex.*(?:\b401\b|\b403\b|unauthori[sz]ed|forbidden)"),
        (
            "plex_unreachable",
            r"plex.*(?:connection refused|timed out|unreachable"
            r"|connect call failed|name or service)",
        ),
        (
            "dizquetv_unreachable",
            r"dizque\w*.*(?:connection refused|timed out|unreachable"
            r"|connect call failed|name or service)",
        ),
        ("readback_mismatch", r"read-?back mismatch|readback_mismatch"),
        ("no_handler", r"dispatch_unknown_stage|no handler registered"),
        ("graph_violation", r"graph_violation|undeclared request|not in may_request"),
        ("nas_partial_read", r"fp_partial|nas_partial_read"),
        (
            "nas_timeout",
            r"timed out|etimedout|winerror 121|semaphore timeout|status_io_timeout",
        ),
        ("nas_invalid_argument", r"invalid argument(?=.*(?:ffmpeg|input|opening|read failed))"),
        ("file_missing", r"no such file|filenotfounderror|winerror [23]\b"),
        ("ffmpeg_decode", r"invalid data found|moov atom not found|error while decoding"),
        (
            "db_error",
            r"asyncpg\.|sqlite3\.|psycopg|operationalerror|integrityerror|deadlock detected",
        ),
    )
)

_QUOTED = re.compile(r"""'[^']*'|"[^"]*\"""")
_WIN_PATH = re.compile(r"[a-z]:[\\/][^\s'\"]*")
_UNC = re.compile(r"\\\\[^\s'\"]+")
_POSIX = re.compile(r"(?<![\w<>])/[^\s'\"():,]+")
_UUID = re.compile(r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b")
_HEX = re.compile(r"\b[0-9a-f]{8,}\b")
_NUM = re.compile(r"\d+(?:\.\d+)?")
_WS = re.compile(r"\s+")


def norm(message: str) -> str:
    """Strip volatile detail from a message. Idempotent."""
    s = message.lower()
    s = _QUOTED.sub("<path>", s)
    s = _WIN_PATH.sub("<path>", s)
    s = _UNC.sub("<path>", s)
    s = _POSIX.sub("<path>", s)
    s = _UUID.sub("<id>", s)
    s = _HEX.sub("<hex>", s)
    s = _NUM.sub("<n>", s)
    s = _WS.sub(" ", s).strip()
    return s[:_MAX_LEN]


def classify(kind: str, message: str) -> str:
    """Normalised cause code for a failure ``(kind, message)``."""
    text = f"{kind} {message}"
    for cause, pattern in _KNOWN:
        if pattern.search(text):
            return cause
    return f"{kind}:{norm(message)}"[: _MAX_LEN + len(kind) + 1]
