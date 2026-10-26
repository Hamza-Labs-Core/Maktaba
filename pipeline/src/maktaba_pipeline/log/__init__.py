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

import functools
import logging
import re
import sys
from collections.abc import Callable, Iterator, Mapping, MutableMapping
from contextlib import contextmanager, suppress
from typing import Any

import structlog

__all__ = [
    "DEFAULT_REDACTED_FIELDS",
    "LEVEL_NUMBERS",
    "add_sink",
    "remove_sink",
    "set_sink_level",
    "MAX_MSG_BYTES",
    "REDACTED_KEY_PARTS",
    "REDACTED_VALUE",
    "SCRUBBED_VALUE",
    "TRUNCATION_SUFFIX",
    "bind_job_id",
    "bind_request_id",
    "bind_session_id",
    "bind_user_id",
    "bind_video_id",
    "get_logger",
    "init",
    "redact_processor",
    "scrub_secrets",
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

#: A string-valued key *containing* one of these is masked too (``plex_token``,
#: ``x_plex_token``, ``db_password``, ``database_url``). Only string values: a numeric
#: ``max_tokens`` / ``tokens_used`` is a count, not a secret.
REDACTED_KEY_PARTS: tuple[str, ...] = (
    "token",
    "secret",
    "password",
    "passwd",
    "dsn",
    "database_url",
    "api_key",
    "apikey",
    "private_key",
)

#: What a secret inside a string value is replaced with (the key-level mask keeps
#: :data:`REDACTED_VALUE`).
SCRUBBED_VALUE = "***"

# ``X-Plex-Token=abc``, ``?token=abc``, ``&sig=abc``, ``access_token=abc``,
# ``password=abc`` in a URL query, a header dump or an exception text
_SECRET_PARAM_RE = re.compile(
    r"(?i)(?<![\w-])([\w-]*(?:token|password|passwd|secret|api_?key)|sig|signature)"
    r"=([\"']?)([^&\s\"'<>,;]+)"
)
# ``scheme://user:pass@host`` (a DSN, an authenticated share URL): the password goes
_URL_USERINFO_RE = re.compile(r"(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]*):([^/\s@]+)@")

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
    ring_capacity: int | None = None,
) -> Any:
    """Configure the global structlog instance and return a logger.

    Calling :func:`init` again is allowed and re-applies the
    configuration; this matches the Go ``Init`` contract closely enough
    for tests and re-init scenarios.

    ``ring_capacity`` sizes the in-memory ring sink used by the
    troubleshooting log export: ``None``/``0`` installs the default
    (:data:`~maktaba_pipeline.log.ring.DEFAULT_RING_CAPACITY`); a
    negative value disables the ring entirely.
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
        _scrub_exception,
        _sinks_processor,
    ]

    # Ring sink for the diagnostics export. Installed unless explicitly
    # disabled (negative capacity). The capture processor runs just
    # before the terminal renderer so the buffered copy is already
    # redacted + truncated.
    ring = None
    if ring_capacity is None or ring_capacity >= 0:
        from .ring import DEFAULT_RING_CAPACITY, LogRingBuffer, make_ring_processor, set_ring

        cap = ring_capacity if ring_capacity else DEFAULT_RING_CAPACITY
        ring = LogRingBuffer(cap)
        set_ring(ring)

    if env == "prod":
        processors.append(_rename_event_to_msg)
        if ring is not None:
            processors.append(make_ring_processor(ring))
        processors.append(structlog.processors.JSONRenderer())
    else:
        if ring is not None:
            processors.append(make_ring_processor(ring))
        processors.append(structlog.dev.ConsoleRenderer())

    global _console_level
    _console_level = level
    structlog.configure(
        processors=processors,
        wrapper_class=structlog.make_filtering_bound_logger(min(level, _sink_level)),
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
    global _console_level
    _console_level = level
    structlog.configure(
        wrapper_class=structlog.make_filtering_bound_logger(min(level, _sink_level))
    )


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
    for key, value in list(event_dict.items()):
        lowered = key.lower()
        if lowered in redact_set or (isinstance(value, str) and _key_looks_secret(lowered)):
            event_dict[key] = REDACTED_VALUE
        elif isinstance(value, str):
            scrubbed = scrub_secrets(value)
            if scrubbed is not value:
                event_dict[key] = scrubbed
    return event_dict


@functools.lru_cache(maxsize=1024)
def _key_looks_secret(lowered: str) -> bool:
    return any(part in lowered for part in REDACTED_KEY_PARTS)


def scrub_secrets(text: str) -> str:
    """``text`` with secret query/header parameters and URL passwords replaced by
    :data:`SCRUBBED_VALUE`; the very same object when there is nothing to scrub (the
    common case costs two ``in`` checks)."""
    if "=" not in text and "@" not in text:
        return text
    out = text
    if "=" in out:
        out = _SECRET_PARAM_RE.sub(lambda m: f"{m.group(1)}={m.group(2)}{SCRUBBED_VALUE}", out)
    if "@" in out:
        out = _URL_USERINFO_RE.sub(lambda m: f"{m.group(1)}:{SCRUBBED_VALUE}@", out)
    return text if out == text else out


def _scrub_exception(
    _logger: Any,
    _name: str,
    event_dict: MutableMapping[str, Any],
) -> MutableMapping[str, Any]:
    """The rendered traceback (``format_exc_info`` runs after the redaction) can quote a
    DSN or a tokenised URL too."""
    exc = event_dict.get("exception")
    if isinstance(exc, str):
        event_dict["exception"] = scrub_secrets(exc)
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


# --- sinks (the cluster agent's log shipper, design-agent.md section 3) --------------

#: level name -> number, for the sinks' gate and the console gate below
LEVEL_NUMBERS: dict[str, int] = {
    "debug": logging.DEBUG,
    "info": logging.INFO,
    "warn": logging.WARNING,
    "warning": logging.WARNING,
    "error": logging.ERROR,
    "exception": logging.ERROR,
    "critical": logging.CRITICAL,
    "fatal": logging.CRITICAL,
}

LogSink = Callable[[Mapping[str, Any]], None]
_SINKS: list[LogSink] = []
_console_level = logging.INFO
#: the lowest level any sink asked for (records below the console level still reach
#: the sinks, then stop before the ring and the console)
_sink_level = logging.INFO


def add_sink(sink: LogSink) -> None:
    """Receive every record (already redacted and truncated) before it is rendered.

    A sink runs on the logging thread and must be cheap and never raise (it is guarded:
    an exception is swallowed). It must not log itself."""
    if sink not in _SINKS:
        _SINKS.append(sink)


def remove_sink(sink: LogSink) -> None:
    if sink in _SINKS:
        _SINKS.remove(sink)


def set_sink_level(level: int) -> None:
    """Let records down to ``level`` through to the sinks (the console keeps its own)."""
    global _sink_level
    if level == _sink_level:
        return
    _sink_level = level
    structlog.configure(
        wrapper_class=structlog.make_filtering_bound_logger(min(_console_level, _sink_level))
    )


def _sinks_processor(
    _logger: Any,
    _name: str,
    event_dict: MutableMapping[str, Any],
) -> MutableMapping[str, Any]:
    if _SINKS:
        for sink in tuple(_SINKS):
            with suppress(Exception):  # a sink never breaks logging
                sink(event_dict)
    if _sink_level < _console_level:
        number = LEVEL_NUMBERS.get(str(event_dict.get("level", "info")).lower(), logging.INFO)
        if number < _console_level:
            raise structlog.DropEvent
    return event_dict
