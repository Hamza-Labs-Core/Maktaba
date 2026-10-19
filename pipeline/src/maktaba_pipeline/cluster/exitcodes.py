"""Exit codes a service manager can act on (sysexits.h).

- ``EX_NOPERM`` (77): the home refused this machine for good, said by an authenticated
  source only — the sealed ``cmd disconnect {reason: "revoked"}`` on the channel, or the
  database (the node's row revoked, rejected or removed; its login refused). A bare
  WebSocket close (``4003``, ``4010``) is not authenticated and never leads here.
  Restarting will not help; a service unit should stop restarting.
- ``EX_CONFIG`` (78): ``run --node`` on a machine that has not joined a home.

The worker marks the refusal (:func:`mark_refused`) where it sees it and stops gracefully;
``runtime.run`` then returns :func:`exit_code`.
"""

from __future__ import annotations

__all__ = ["EX_CONFIG", "EX_NOPERM", "exit_code", "mark_refused", "refused", "reset"]

EX_NOPERM = 77
EX_CONFIG = 78

_REFUSED: list[str] = []


def mark_refused(reason: str) -> None:
    """The home refused this machine for good (the worker stops; the exit code is 77)."""
    if not _REFUSED:
        _REFUSED.append(reason)


def refused() -> str | None:
    return _REFUSED[0] if _REFUSED else None


def exit_code(default: int = 0) -> int:
    return EX_NOPERM if _REFUSED else default


def reset() -> None:
    """Tests: forget a refusal."""
    _REFUSED.clear()
