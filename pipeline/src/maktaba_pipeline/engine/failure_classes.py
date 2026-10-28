"""Failure classes (Epic 40, queue health): what a failure says about what to do next.

Every failure's normalised cause (``error_cause``, :mod:`.causes`) maps to one class:

* ``transient`` - a passing problem: retried with backoff within its budget;
* ``dependency:<name>`` - something the job needs is down (the model, a share): the job
  is parked without spending an attempt until a watcher sees the dependency healthy;
* ``resource:<name>`` - the machine ran out of something (disk, Postgres shared memory):
  parked until the resource frees;
* ``item_permanent`` - no retry can help (a damaged file, no permission): the item
  needs an admin;
* ``unknown`` - nothing knows it: retried within the stage's own ``max_attempts``.

The mapping is data: :data:`DEFAULT_TABLE`, after the admin's own rows
(``rules.queue_health.class_overrides``). A row matches a cause that starts with its
signature, so an unknown kind is matched by its normalised message
(``import:read failed: <n> frames``). The thresholds of the queues page are the same rules
section.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

from .. import settings

__all__ = [
    "DEFAULT_TABLE",
    "DEPENDENCY",
    "ITEM_PERMANENT",
    "MODEL",
    "QueueHealthConfig",
    "RESOURCE",
    "SHARE",
    "TRANSIENT",
    "UNKNOWN",
    "YT_RATE_LIMIT",
    "class_for",
    "is_parking",
    "valid_class",
]

TRANSIENT = "transient"
ITEM_PERMANENT = "item_permanent"
UNKNOWN = "unknown"
DEPENDENCY = "dependency"
RESOURCE = "resource"
#: ``dependency:model`` is made ``dependency:model:<rules section>`` (the stage's model)
MODEL = "dependency:model"
#: a missing file whose library root does not answer: ``dependency:share:<root>``
SHARE = "dependency:share"

#: YouTube's "too many requests": a resource that cools down
YT_RATE_LIMIT = "resource:youtube_rate_limit"

#: (signature prefix, class), first match wins
DEFAULT_TABLE: tuple[tuple[str, str], ...] = (
    ("model_unavailable", MODEL),
    # refined to dependency:share:<root> when the item's library root does not answer
    ("file_missing", ITEM_PERMANENT),
    ("shm_full", "resource:shm"),
    ("disk_full", "resource:disk"),
    ("nas_timeout", TRANSIENT),
    ("nas_partial_read", TRANSIENT),
    ("nas_invalid_argument", TRANSIENT),
    ("db_error", TRANSIENT),
    ("ffmpeg_rc", TRANSIENT),
    # mediaintel's imported fingerprint failures: a partial read over SMB
    ("import:read failed", TRANSIENT),
    ("ffmpeg_decode", ITEM_PERMANENT),
    ("permission_denied", ITEM_PERMANENT),
    ("youtube_unavailable", ITEM_PERMANENT),
    # Epic 47: YouTube's bot check waits for YouTube (or for the owner's cookies); a 429
    # cools down (the watchers release it an hour after the last one)
    ("youtube_bot_check", "dependency:youtube"),
    ("youtube_rate_limited", YT_RATE_LIMIT),
)


def valid_class(cls: str) -> bool:
    """A class an override row may name."""
    if cls in (TRANSIENT, ITEM_PERMANENT, UNKNOWN):
        return True
    head, _, name = cls.partition(":")
    return head in (DEPENDENCY, RESOURCE) and bool(name.strip())


def is_parking(cls: str) -> bool:
    """A dependency or resource class: the job waits instead of spending attempts."""
    return cls.startswith((DEPENDENCY + ":", RESOURCE + ":"))


def class_for(cause: str, overrides: Iterable[tuple[str, str]] = ()) -> str:
    """The class of a normalised cause: the admin's rows first, then the built-in table.

    An override naming a class that does not exist is skipped (the rules are checked on
    save, but a stored value outlives a code change)."""
    c = cause.strip().lower()
    for sig, cls in (*overrides, *DEFAULT_TABLE):
        s = sig.strip().lower()
        if s and c.startswith(s) and valid_class(cls.strip()):
            return cls.strip()
    return UNKNOWN


@dataclass(frozen=True)
class QueueHealthConfig:
    #: how far back a tile looks to decide its colour
    window_hours: float = 6.0
    #: below this share of failed jobs in the window a stage is never red
    ok_failure_share: float = 0.02
    #: one signature needs this many failures in the window to make a stage red
    failing_min_jobs: int = 10
    #: tries of a transient failure before the item needs an admin
    transient_attempts: int = 5
    #: a parked job is tried again after this even if no watcher released it
    park_max_hours: float = 24.0
    probe_interval_seconds: int = 60
    probe_timeout_seconds: float = 5.0
    min_free_disk_gb: float = 5.0
    resource_cooldown_minutes: float = 10.0
    #: (signature, class) rows checked before the built-in table
    class_overrides: tuple[tuple[str, str], ...] = ()


_G = "Queue health"
settings.register(
    QueueHealthConfig,
    {
        "window_hours": settings.Help(
            _G, "Look back (hours)", "How far back a queue tile looks to decide its colour.",
            minimum=0.25, maximum=168,
        ),
        "ok_failure_share": settings.Help(
            _G, "Failures that stay neutral (share)",
            "Below this share of failed jobs in the window a queue is never red; its failed "
            "count shows as small text (0.02 is 2%).", minimum=0, maximum=1,
        ),
        "failing_min_jobs": settings.Help(
            _G, "Red from (jobs)",
            "A queue turns red only when at least this many jobs failed the same new way in "
            "the window: a likely bug or a bad setting.", minimum=1,
        ),
        "transient_attempts": settings.Help(
            _G, "Tries of a passing failure",
            "How many times a passing failure (a NAS timeout, a partial read) is tried before "
            "the item needs you. A queue that runs once by design keeps its one try.",
            minimum=1, maximum=50,
        ),
        "park_max_hours": settings.Help(
            _G, "Waiting jobs retried after (hours)",
            "A job waiting on the model, a share or disk space is tried again after this even "
            "if no check released it. It spends no try while it waits.", minimum=0.25,
        ),
        "probe_interval_seconds": settings.Help(
            _G, "Check every (seconds)",
            "How often the model, the shares and the free disk space are checked; jobs waiting "
            "on one go back to work within this long of it coming back.", minimum=10,
        ),
        "probe_timeout_seconds": settings.Help(
            _G, "Check waits (seconds)", "How long one check waits for an answer.",
            minimum=1, maximum=120,
        ),
        "min_free_disk_gb": settings.Help(
            _G, "Free disk needed (GB)",
            "Below this much free space (temporary and data folders) jobs that ran out of disk "
            "keep waiting.", minimum=0,
        ),
        "resource_cooldown_minutes": settings.Help(
            _G, "Shared memory wait (minutes)",
            "Jobs that ran out of Postgres shared memory wait this long before they are tried "
            "again (it frees when the big queries end).", minimum=1,
        ),
        "class_overrides": settings.Help(
            _G, "Failure classes",
            "A failure whose signature starts with this is of this class: transient (tried "
            "again), item_permanent (needs you), unknown, dependency:<name> or resource:<name> "
            "(waits without spending tries). Checked before the built-in table.",
            columns=("signature", "class"),
        ),
    },
)  # fmt: skip
