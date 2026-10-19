"""Exit codes a service manager can act on (sysexits.h).

- ``EX_NOPERM`` (77): the home refused this machine for good — the channel closed with
  ``4010 revoked`` or ``4003 not_member``, or the database login is refused (revoked
  credentials). Restarting will not help; a service unit should stop restarting.
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
