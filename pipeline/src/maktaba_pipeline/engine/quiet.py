"""Quiet hours: heavy work waits for a window of the day (Epic 59 story 59.02).

On a NAS, heavy jobs (a library's first scan, a transcription backlog, convert and dedup,
thumbnail sprites in bulk) would run as soon as they are queued, while the NAS also serves
files to the household. The supervisor names this machine's window in
``MAKTABA_QUIET_HOURS`` ("01:00-06:00", the hardware tier's or ``[schedule] quiet_hours``);
outside it, a heavy job this worker claims is given back (``JobDeferred``: paused, no new
attempt) until the window opens, with the reason ``quiet_hours:<start>`` the queue page
shows as "Waiting for quiet hours (01:00)".

- Playback, a single new file's thumbnail (``thumb_small``) and its transcript (a backlog
  of 10 or fewer) never wait; on the Light tier (``MAKTABA_QUIET_SPEECH``, story 59.06) every
  transcription waits, a single new file's too.
- An admin's **Run now** on a job or a library sets ``run_now`` in the job's payload and
  clears its wait: the gate lets it through.
- Only jobs this machine claims wait: a node with no window of its own takes them (quiet
  hours protect the NAS, not the cluster).
- The window is in the home's time zone (``TZ``, the process's local time); one across
  midnight (22:00-06:00) works.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any

from ..db.jobs import Job
from .sql import is_pg, loads

__all__ = [
    "ENV_QUIET_HOURS",
    "ENV_QUIET_SPEECH",
    "ENV_QUIET_SPRITES",
    "QUIET_REASON",
    "QuietHours",
    "hold_seconds",
    "is_heavy",
    "parse_window",
]

ENV_QUIET_HOURS = "MAKTABA_QUIET_HOURS"
ENV_QUIET_SPRITES = "MAKTABA_QUIET_SPRITES"
ENV_QUIET_SPEECH = "MAKTABA_QUIET_SPEECH"
#: the paused reason of a held job: quiet_hours:<HH:MM>, the window's start
QUIET_REASON = "quiet_hours"

#: stages that are always heavy
ALWAYS_HEAVY = frozenset({"convert", "dedup_scan", "dedup_apply"})
#: a transcription backlog above this many jobs waits
TRANSCRIBE_BACKLOG = 10
#: thumbnail sprites for more than this many items wait
SPRITES_BACKLOG = 50


def _clock(s: str) -> int:
    h, _, m = s.strip().partition(":")
    hh, mm = int(h), int(m)
    if not (0 <= hh <= 23 and 0 <= mm <= 59 and len(m.strip()) == 2):
        raise ValueError(s)
    return hh * 60 + mm


def parse_window(s: str | None) -> tuple[int, int] | None:
    """``"01:00-06:00"`` as minutes of the day; ``None`` for none or a value that does
    not parse (the server checks it before it is saved)."""
    if not s or not s.strip():
        return None
    a, sep, b = s.partition("-")
    if not sep:
        return None
    try:
        start, end = _clock(a), _clock(b)
    except ValueError:
        return None
    return None if start == end else (start, end)


@dataclass(frozen=True, slots=True)
class QuietHours:
    start: int
    end: int
    #: thumbnail sprites always wait (the Light tier)
    sprites: bool = False
    #: every transcription waits, a single new file's too (the Light tier, story 59.06)
    speech: bool = False

    @classmethod
    def from_env(cls, env: Any = None) -> QuietHours | None:
        env = os.environ if env is None else env
        w = parse_window(env.get(ENV_QUIET_HOURS))
        if w is None:
            return None
        on = ("", "0", "false")
        return cls(
            w[0],
            w[1],
            sprites=env.get(ENV_QUIET_SPRITES, "") not in on,
            speech=env.get(ENV_QUIET_SPEECH, "") not in on,
        )

    @property
    def start_text(self) -> str:
        return f"{self.start // 60:02d}:{self.start % 60:02d}"

    def inside(self, at: datetime) -> bool:
        m = at.hour * 60 + at.minute
        if self.start < self.end:
            return self.start <= m < self.end
        return m >= self.start or m < self.end  # across midnight

    def until_open(self, at: datetime) -> float:
        """Seconds from ``at`` to the window's next start (0 inside it)."""
        if self.inside(at):
            return 0.0
        nxt = at.replace(hour=self.start // 60, minute=self.start % 60, second=0, microsecond=0)
        if nxt <= at:
            nxt += timedelta(days=1)
        return (nxt - at).total_seconds()


def _now() -> datetime:
    """The home's local time (tests replace it)."""
    return datetime.now().astimezone()


async def _count_waiting(db: Any, stage: str) -> int:
    row = await db.fetchrow(
        "SELECT count(*) AS n FROM processing_jobs WHERE stage = $1"
        " AND state IN ('pending', 'paused', 'claimed', 'running')",
        stage,
    )
    return int(row["n"]) if row is not None else 0


async def _first_scan(db: Any, job: Job) -> bool:
    if job.library_id is None:
        return False
    row = await db.fetchrow(
        "SELECT last_scan_at FROM library_scan_state WHERE library_id = $1",
        job.library_id if is_pg(db) else str(job.library_id),
    )
    return row is None or row["last_scan_at"] is None


def _run_now(job: Job) -> bool:
    p = job.payload
    if isinstance(p, str):
        p = loads(p)
    return isinstance(p, dict) and bool(p.get("run_now"))


async def is_heavy(db: Any, job: Job, quiet: QuietHours) -> bool:
    """Whether the job is heavy work that waits for the window."""
    stage = str(job.stage)
    if stage in ALWAYS_HEAVY:
        return True
    if stage == "scan":
        return await _first_scan(db, job)
    if stage == "transcribe":
        return quiet.speech or await _count_waiting(db, stage) > TRANSCRIBE_BACKLOG
    if stage == "thumbnail":
        return quiet.sprites or await _count_waiting(db, stage) > SPRITES_BACKLOG
    return False


async def hold_seconds(
    db: Any, job: Job, *, quiet: QuietHours | None = None, at: datetime | None = None
) -> float:
    """How long ``job`` waits for the quiet hours: 0 when it runs now (no window, inside
    it, Run now, or not heavy)."""
    quiet = QuietHours.from_env() if quiet is None else quiet
    if quiet is None or _run_now(job):
        return 0.0
    at = _now() if at is None else at
    wait = quiet.until_open(at)
    if wait <= 0 or not await is_heavy(db, job, quiet):
        return 0.0
    return wait
