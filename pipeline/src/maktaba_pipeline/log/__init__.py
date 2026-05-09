"""Maktaba structured logging for Python services (Story 21.1).

A thin ``structlog`` wrapper that matches the field contract used by the
Go ``shared/log/go`` package: every emitted record carries
``ts, level, service, msg, version, env`` plus whichever contextual ids
have been bound for the current task (``request_id, session_id, job_id,
video_id, user_id``).

Production uses ``structlog.processors.JSONRenderer``; dev defaults to
``structlog.dev.ConsoleRenderer``. Sensitive field names listed in
:data:`DEFAULT_REDACTED_FIELDS` are masked before emission.

Usage::

    from maktaba_pipeline.log import init, get_logger, bind_request_id

    init(service="pipeline", env="prod", version="v1.2.3")
    log = get_logger()

    with bind_request_id("req-123"):
        log.info("video imported", duration_s=12.4)

The pipeline is currently the only Python service in the tree; if other
Python services emerge this module is the obvious thing to lift into
``shared/log/py``.
"""

from __future__ import annotations

import logging
import sys
from collections.abc import Iterator, MutableMapping
from contextlib import contextmanager
from typing import Any

import structlog

__all__ = [
    "DEFAULT_REDACTED_FIELDS",
    "MAX_MSG_BYTES",
    "REDACTED_VALUE",
    "TRUNCATION_SUFFIX",
    "bind_job_id",
    "bind_request_id",
    "bind_session_id",
    "bind_user_id",
    "bind_video_id",
    "get_logger",
    "init",
    "redact_processor",
    "set_level",
    "truncate_msg",
]


#: Sensitive attribute keys (case-insensitive) whose values are masked
#: before emission. Mirrors ``shared/log/go``'s ``DefaultRedactedFields``.
DEFAULT_REDACTED_FIELDS: tuple[str, ...] = (
    "password",
    "passwd",
    "pwd",
    "secret",
    "token",
    "access_token",
    "refresh_token",
    "id_token",
    "api_key",
    "apikey",
    "authorization",
    "auth",
    "cookie",
    "set_cookie",
    "session_cookie",
    "private_key",
    "client_secret",
    "credit_card",
    "card_number",
    "cvv",
    "ssn",
)

REDACTED_VALUE = "***REDACTED***"

#: Upper bound on the ``msg`` (a.k.a. structlog ``event``) field. The OS
#: pipe buffer is 64 KiB on Linux; we leave ~4 KiB for the surrounding
#: JSON envelope.
MAX_MSG_BYTES = 60_000
TRUNCATION_SUFFIX = " ...[truncated]"


def init(
    *,
    service: str,
    env: str = "dev",
    version: str = "unknown",
    redacted_fields: tuple[str, ...] | None = None,
    level: int = logging.INFO,
) -> Any:
    """Configure the global structlog instance and return a logger.

    Calling :func:`init` again is allowed and re-applies the
    configuration; this matches the Go ``Init`` contract closely enough
    for tests and re-init scenarios.
    """
    fields = redacted_fields if redacted_fields is not None else DEFAULT_REDACTED_FIELDS
    redact_set = frozenset(f.lower() for f in fields)

    timestamper = structlog.processors.TimeStamper(fmt="iso", utc=True, key="ts")

    processors: list[structlog.types.Processor] = [
        structlog.contextvars.merge_contextvars,
        structlog.processors.add_log_level,
        timestamper,
        _make_redact_processor(redact_set),
        truncate_msg,
        structlog.processors.StackInfoRenderer(),
        structlog.processors.format_exc_info,
    ]
    if env == "prod":
        processors.append(_rename_event_to_msg)
        processors.append(structlog.processors.JSONRenderer())
    else:
        processors.append(structlog.dev.ConsoleRenderer())

    structlog.configure(
        processors=processors,
        wrapper_class=structlog.make_filtering_bound_logger(level),
        context_class=dict,
        logger_factory=structlog.PrintLoggerFactory(file=sys.stdout),
        cache_logger_on_first_use=False,
    )
    # Stash the per-service base fields on the contextvar scope so every
    # logger returned by get_logger() — not just the one returned here —
    # picks them up via the merge_contextvars processor.
    structlog.contextvars.bind_contextvars(service=service, version=version, env=env)
    return structlog.get_logger()


def get_logger(**initial: Any) -> Any:
    """Return the configured logger, optionally bound with extra fields."""
    log = structlog.get_logger()
    if initial:
        log = log.bind(**initial)
    return log


def set_level(level: int) -> None:
    """Re-bind structlog with a new minimum level.

    structlog itself has no live-mutable level: the filtering wrapper is
    pinned at configure() time. We rebuild the wrapper class here to
    keep the operator UX consistent with the Go logger's ``SetLevel``.
    """
    structlog.configure(wrapper_class=structlog.make_filtering_bound_logger(level))


@contextmanager
def bind_request_id(request_id: str) -> Iterator[None]:
    """Attach a request id to the structlog contextvar scope."""
    yield from _bind("request_id", request_id)


@contextmanager
def bind_session_id(session_id: str) -> Iterator[None]:
    yield from _bind("session_id", session_id)


@contextmanager
def bind_job_id(job_id: str) -> Iterator[None]:
    yield from _bind("job_id", job_id)


@contextmanager
def bind_video_id(video_id: str) -> Iterator[None]:
    yield from _bind("video_id", video_id)


@contextmanager
def bind_user_id(user_id: str) -> Iterator[None]:
    yield from _bind("user_id", user_id)


def _bind(key: str, value: str) -> Iterator[None]:
    token = structlog.contextvars.bind_contextvars(**{key: value})
    try:
        yield
    finally:
        structlog.contextvars.reset_contextvars(**token)


def truncate_msg(
    _logger: Any,
    _name: str,
    event_dict: MutableMapping[str, Any],
) -> MutableMapping[str, Any]:
    """Cap oversized ``event`` strings (EC1)."""
    msg = event_dict.get("event")
    if isinstance(msg, str) and len(msg) > MAX_MSG_BYTES:
        event_dict["event"] = msg[: MAX_MSG_BYTES - len(TRUNCATION_SUFFIX)] + TRUNCATION_SUFFIX
    return event_dict


def redact_processor(
    _logger: Any,
    _name: str,
    event_dict: MutableMapping[str, Any],
) -> MutableMapping[str, Any]:
    """Default redaction processor — uses :data:`DEFAULT_REDACTED_FIELDS`.

    Exposed so callers can compose their own processor list. :func:`init`
    builds an equivalent processor with the resolved redact set baked
    in.
    """
    redact_set = frozenset(f.lower() for f in DEFAULT_REDACTED_FIELDS)
    return _redact_in_place(event_dict, redact_set)


def _make_redact_processor(redact_set: frozenset[str]) -> structlog.types.Processor:
    def processor(
        _logger: Any,
        _name: str,
        event_dict: MutableMapping[str, Any],
    ) -> MutableMapping[str, Any]:
        return _redact_in_place(event_dict, redact_set)

    return processor


def _redact_in_place(
    event_dict: MutableMapping[str, Any],
    redact_set: frozenset[str],
) -> MutableMapping[str, Any]:
    for key in list(event_dict.keys()):
        if key.lower() in redact_set:
            event_dict[key] = REDACTED_VALUE
    return event_dict


def _rename_event_to_msg(
    _logger: Any,
    _name: str,
    event_dict: MutableMapping[str, Any],
) -> MutableMapping[str, Any]:
    """Rename the structlog ``event`` field to ``msg`` to match the Go contract.

    In dev mode the ConsoleRenderer formats ``event`` natively, so the
    rename only runs ahead of the JSONRenderer.
    """
    if "event" in event_dict and "msg" not in event_dict:
        event_dict["msg"] = event_dict.pop("event")
    return event_dict
