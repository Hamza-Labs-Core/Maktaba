"""The cluster LLM door's errors; ``code`` is the ``llm.done.error`` value on the wire."""

from __future__ import annotations

__all__ = [
    "Cancelled",
    "ClusterUnavailable",
    "LLMError",
    "MachineGone",
    "NoCapacity",
    "NoModel",
    "RuntimeFailed",
    "TooLarge",
    "error_for",
]


class LLMError(Exception):
    code = "error"


class NoModel(LLMError):
    """No machine in the cluster (or on this machine, without a channel) has the model."""

    code = "no_model"


class NoCapacity(LLMError):
    """Every machine with the model stayed busy past the queue timeout."""

    code = "no_capacity"


class MachineGone(LLMError):
    """The machine running the request left mid-request."""

    code = "machine_gone"


class ClusterUnavailable(LLMError):
    """No channel to the home: the cluster cannot be asked."""

    code = "no_channel"


class RuntimeFailed(LLMError):
    """The runtime refused or failed the request."""

    code = "runtime_error"


class Cancelled(LLMError):
    """The request was cancelled (its requester gave up)."""

    code = "cancelled"


class TooLarge(LLMError):
    """A request or an answer over the channel's frame limit (4 MiB)."""

    code = "too_large"


_BY_CODE: dict[str, type[LLMError]] = {
    c.code: c
    for c in (
        NoModel,
        NoCapacity,
        MachineGone,
        ClusterUnavailable,
        RuntimeFailed,
        Cancelled,
        TooLarge,
    )
}


def error_for(error: str) -> LLMError:
    """An ``llm.done.error`` (``"no_model"`` or ``"no_model: detail"``) as an exception."""
    code, _, detail = error.partition(":")
    cls = _BY_CODE.get(code.strip(), RuntimeFailed)
    return cls(detail.strip() or error)
