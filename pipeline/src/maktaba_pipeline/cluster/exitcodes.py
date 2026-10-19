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

__all__ = [
    "EX_CONFIG",
    "EX_NOPERM",
    "exit_code",
    "is_auth_error",
    "login_revoked",
    "mark_refused",
    "refused",
    "reset",
]

EX_NOPERM = 77
EX_CONFIG = 78

_REFUSED: list[str] = []


#: Postgres' authentication failures (asyncpg's names): 28P01, 28000
_AUTH_ERRORS = ("InvalidPasswordError", "InvalidAuthorizationSpecificationError")


def is_auth_error(exc: BaseException) -> bool:
    """The database refused the login, for whatever reason."""
    return type(exc).__name__ in _AUTH_ERRORS


def login_revoked(exc: BaseException) -> bool:
    """The database refused the login *because the home revoked this machine*.

    Revocation (phase 1) rotates the role's password and sets ``NOLOGIN``, so the signals
    are ``28P01 invalid_password`` and a ``28000`` saying the role "is not permitted to
    log in". Any other ``28000`` — "no pg_hba.conf entry for host", say — is a problem
    on the home's side that can be fixed: not a reason to stop for good."""
    name = type(exc).__name__
    if name == "InvalidPasswordError":
        return True
    if name == "InvalidAuthorizationSpecificationError":
        return "is not permitted to log in" in str(exc)
    return False


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
