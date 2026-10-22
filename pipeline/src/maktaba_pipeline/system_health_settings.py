"""The ``system_health`` rules: when the System health page (``/admin/health``, the API's
``GET /api/admin/system/status``) calls a worker late or gone, a database slow, a
dependency check stale, and how long one check may take. The pipeline never reads them;
they live here because every rules section's schema
(``api/internal/handlers/rules/schema.json``) is generated from one place. The API keeps
the same defaults (``api/internal/handlers/systemstatus/rules.go``, checked by a test)."""

from __future__ import annotations

from dataclasses import dataclass

from .settings import Help, register

__all__ = ["SystemHealthConfig"]

_G = "System health"


@dataclass(frozen=True)
class SystemHealthConfig:
    """When the System health page turns a part of Maktaba yellow or red."""

    #: a worker or streaming machine silent this long is late (degraded)
    heartbeat_late_seconds: int = 60
    #: silent this long it is gone (down)
    heartbeat_gone_seconds: int = 300
    #: a worker silent this long, or replaced by a newer one, is history: never a failure
    worker_forget_hours: float = 24.0
    #: a database round trip slower than this is degraded
    db_latency_ms: int = 250
    #: a dependency check (the model, a share, the disk) older than this is stale
    dependency_stale_seconds: int = 600
    #: one check may take this long; slower is reported as not answering
    check_timeout_ms: int = 2000
    #: answers are reused for this long, so many open pages cost one check
    cache_seconds: int = 5


register(
    SystemHealthConfig,
    {
        "heartbeat_late_seconds": Help(
            _G,
            "Heartbeat late after (seconds)",
            "A worker or a streaming machine reports every few seconds. Silent this long, "
            "it shows as late (yellow).",
            minimum=5,
            maximum=3600,
        ),
        "heartbeat_gone_seconds": Help(
            _G,
            "Heartbeat gone after (seconds)",
            "Silent this long, a worker or streaming machine shows as gone (red). When every "
            "worker is gone, the whole page is Down.",
            minimum=10,
            maximum=86400,
        ),
        "worker_forget_hours": Help(
            _G,
            "Forget a worker after (hours)",
            "A worker silent this long is kept as history, not shown as a failure: a machine "
            "that was switched off for good, or a worker that restarted under a new name. A "
            "worker replaced by a newer one on the same machine is history at once.",
            minimum=1,
            maximum=8760,
        ),
        "db_latency_ms": Help(
            _G,
            "Database slow from (ms)",
            "A database round trip slower than this shows the database as slow (yellow).",
            minimum=1,
            maximum=60000,
        ),
        "dependency_stale_seconds": Help(
            _G,
            "Dependency check stale after (seconds)",
            "The model, each share and the free disk space are checked every minute "
            "(Queue health). A check older than this shows as stale (yellow): the workers "
            "that check them may be stopped.",
            minimum=30,
            maximum=86400,
        ),
        "check_timeout_ms": Help(
            _G,
            "One check waits (ms)",
            "How long the page waits for one part to answer before it shows it as not "
            "answering. Every part is checked at the same time.",
            minimum=100,
            maximum=30000,
        ),
        "cache_seconds": Help(
            _G,
            "Reuse an answer for (seconds)",
            "Answers are reused this long, so many open pages cost one round of checks.",
            minimum=0,
            maximum=300,
        ),
    },
)
