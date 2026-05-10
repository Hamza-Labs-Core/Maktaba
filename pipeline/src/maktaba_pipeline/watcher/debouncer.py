"""Per-path debounce + size-stability gate.

Direct port of plan-01-03 §2.4 / §5 ("Debouncing Strategy"): the watcher
must never enqueue a file that is still being written. Three layered
mechanisms:

1. **Per-path timer.** Every raw event for ``path`` resets a timer set
   to ``debounce_sec``. Bursts of ``Modified`` events collapse into one
   tick — the AC-2 partial-write case from the story spec.
2. **Stable-size probe.** When the timer fires, ``os.stat`` the path.
   The file is "settled" only after :attr:`DebouncerConfig.settle_ticks`
   consecutive ticks return the *same* size.
3. **Mtime quarantine.** Even if size is stable, if the file's mtime is
   newer than ``time.time() - settle_sec`` we re-arm. Catches the
   pathological copy that stalls at the final byte count for one tick.

The debouncer is intentionally pure-Python (no asyncio, no IO besides
``os.stat``) so unit tests can drive it with a synthetic clock and
single-thread for determinism. Production callers wrap it inside the
:class:`maktaba_pipeline.watcher.LibraryObserver` adapter that bridges
to ``watchdog``'s own threads.

Threading note: the public API methods (:meth:`Debouncer.feed`,
:meth:`Debouncer.tick_now`, :meth:`Debouncer.shutdown`) are safe to call
concurrently. Callbacks fire on whichever thread invoked
:meth:`tick_now` (when polling) or on a :class:`threading.Timer` thread
(when running with the default real-time scheduler). The dispatcher
re-enters the asyncio loop on its own thread via the queue handoff in
:mod:`maktaba_pipeline.watcher.service`.
"""

from __future__ import annotations

import os
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any, Protocol

from .events import Op, RawEvent, SettledEvent

__all__ = ["Debouncer", "DebouncerConfig"]


class _Logger(Protocol):
    def debug(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...


class _StatLike(Protocol):
    """Minimal stat shape the debouncer probes.

    ``os.stat_result`` satisfies this; tests can hand in a hand-rolled
    object so they don't have to fight the read-only ``stat_result``
    constructor when programming nanosecond-precision mtimes.
    """

    @property
    def st_size(self) -> int: ...

    @property
    def st_mtime(self) -> float: ...

    @property
    def st_mtime_ns(self) -> int: ...


@dataclass(slots=True, frozen=True)
class DebouncerConfig:
    """Knobs for :class:`Debouncer`.

    The defaults match plan-01-03 §5 — ``debounce_sec=2``, ``settle_sec=1``,
    ``settle_ticks=2`` keep total worst-case latency at ``2 * debounce +
    settle = 5 s``, comfortably inside the AC-1 ``2 * debounce_sec + 1 = 5 s``
    budget. Tests override individual fields to drive the debouncer at
    millisecond timescales.
    """

    debounce_sec: float = 2.0
    settle_sec: float = 1.0
    settle_ticks: int = 2


@dataclass(slots=True)
class _Pending:
    """Per-path bookkeeping for an in-flight settle.

    ``timer`` is :class:`threading.Timer` in real-time mode and ``None``
    in synchronous-tick mode (tests). ``last_size`` tracks the size at
    the most recent probe; ``stable_ticks`` counts how many consecutive
    probes have seen ``last_size`` unchanged. ``library_id`` rides with
    the entry so the emitted :class:`SettledEvent` carries the same
    library identity as the original raw event.
    """

    library_id: str
    op: Op
    timer: threading.Timer | None = None
    last_size: int = -1
    stable_ticks: int = 0


@dataclass
class Debouncer:
    """Coalesces raw events into settled emissions per absolute path.

    The debouncer owns no DB connection and emits a settled event by
    invoking a caller-supplied ``on_settled`` callback. The callback
    runs on whatever thread the timer fired on (real-time mode) or on
    the thread that called :meth:`tick_now` (test mode). The callback
    must be cheap and reentrant — production wires it to a queue.put
    that hands off to the asyncio dispatcher.

    Set ``schedule=False`` for tests: in that mode :meth:`feed` arms a
    pending probe but does not start a timer, and the test calls
    :meth:`tick_now` to drive the settle check on its own clock.
    """

    on_settled: Callable[[SettledEvent], None]
    log: _Logger
    config: DebouncerConfig = field(default_factory=DebouncerConfig)
    schedule: bool = True
    _wall: Callable[[], float] = field(default=time.time)
    _stat: Callable[[str], _StatLike] = field(default=os.stat)
    _lock: threading.Lock = field(default_factory=threading.Lock)
    _pending: dict[str, _Pending] = field(default_factory=dict)
    _closed: bool = False

    def feed(self, ev: RawEvent) -> None:
        """Accept a raw event from the watchdog adapter.

        :attr:`Op.DELETED` cancels any pending settle and emits
        immediately — there is nothing to wait for. :attr:`Op.MOVED`
        also cancels any pending settle on the source path and emits
        the move synchronously; the dispatcher decides whether to
        update the path or dedupe by hash.
        :attr:`Op.CREATE` / :attr:`Op.MODIFY` arm or reset the per-path
        timer.
        """
        if self._closed:
            return

        if ev.op == Op.DELETED:
            self._cancel_pending(ev.path, drop=True)
            self._emit(SettledEvent(library_id=ev.library_id, op=Op.DELETED, path=ev.path))
            return

        if ev.op == Op.MOVED:
            # Cancel any pending settle on the source path: the bytes
            # have moved, the source is gone. The dispatcher resolves
            # the move via hash dedupe / path UPDATE (plan §2.5).
            self._cancel_pending(ev.path, drop=True)
            self._emit(
                SettledEvent(
                    library_id=ev.library_id,
                    op=Op.MOVED,
                    path=ev.path,
                    dest_path=ev.dest_path,
                )
            )
            return

        # CREATE / MODIFY: arm or reset the per-path timer.
        with self._lock:
            entry = self._pending.get(ev.path)
            if entry is None:
                entry = _Pending(library_id=ev.library_id, op=ev.op)
                self._pending[ev.path] = entry
            else:
                # A CREATE following an earlier MODIFY (rare but legal on
                # some filesystems when the inode was reused) promotes
                # the eventual settled op to CREATE so the dispatcher
                # treats the row as fresh discovery, not an update.
                if entry.op == Op.MODIFY and ev.op == Op.CREATE:
                    entry.op = Op.CREATE
                entry.library_id = ev.library_id
            self._cancel_timer_locked(entry)
            if self.schedule:
                entry.timer = threading.Timer(
                    self.config.debounce_sec, self._on_timer, args=(ev.path,)
                )
                entry.timer.daemon = True
                entry.timer.start()

    def tick_now(self, path: str) -> None:
        """Run the settle probe for ``path`` immediately. Test helper.

        Intended for ``schedule=False`` tests where the test drives the
        clock. In production the timer thread invokes the equivalent of
        this method via :meth:`_on_timer`.
        """
        self._on_timer(path)

    def shutdown(self) -> None:
        """Cancel every outstanding timer; future :meth:`feed` calls no-op.

        Idempotent. Safe to call from any thread. Pending settle events
        for paths that had not yet stabilised are *dropped* — the boot
        sweep on next start is the documented backstop.
        """
        with self._lock:
            self._closed = True
            for entry in self._pending.values():
                self._cancel_timer_locked(entry)
            self._pending.clear()

    def pending_count(self) -> int:
        """Number of paths with an in-flight settle. Test/metrics helper."""
        with self._lock:
            return len(self._pending)

    def _on_timer(self, path: str) -> None:
        """Timer callback: probe the file and either settle or re-arm."""
        try:
            st = self._stat(path)
        except FileNotFoundError:
            # Vanished between debounce and probe: the watcher will
            # receive the matching DELETED event on its own (or the
            # observer already swallowed it). Drop the pending entry
            # so we don't leak it.
            with self._lock:
                self._pending.pop(path, None)
            self.log.debug("watcher.debouncer.gone_during_settle", path=path)
            return
        except OSError as err:
            self.log.warning(
                "watcher.debouncer.stat_failed",
                path=path,
                err=str(err),
            )
            return

        size = int(st.st_size)
        mtime = float(st.st_mtime)
        mtime_age = max(0.0, self._wall() - mtime)

        with self._lock:
            entry = self._pending.get(path)
            if entry is None:
                # tick_now after shutdown / cancel race — nothing to do.
                return

            if size != entry.last_size:
                entry.last_size = size
                entry.stable_ticks = 1
            else:
                entry.stable_ticks += 1
            stable_enough = entry.stable_ticks >= self.config.settle_ticks

            mtime_quiet = mtime_age >= self.config.settle_sec

            if not (stable_enough and mtime_quiet):
                if self.schedule:
                    entry.timer = threading.Timer(
                        self.config.debounce_sec,
                        self._on_timer,
                        args=(path,),
                    )
                    entry.timer.daemon = True
                    entry.timer.start()
                self.log.debug(
                    "watcher.debouncer.re_armed",
                    path=path,
                    stable_ticks=entry.stable_ticks,
                    mtime_age=mtime_age,
                )
                return

            op = entry.op
            library_id = entry.library_id
            del self._pending[path]

        self._emit(
            SettledEvent(
                library_id=library_id,
                op=op,
                path=path,
                size_bytes=size,
                mtime_ns=int(st.st_mtime_ns),
            )
        )

    def _cancel_pending(self, path: str, *, drop: bool) -> None:
        with self._lock:
            entry = self._pending.get(path)
            if entry is None:
                return
            self._cancel_timer_locked(entry)
            if drop:
                del self._pending[path]

    @staticmethod
    def _cancel_timer_locked(entry: _Pending) -> None:
        if entry.timer is not None:
            entry.timer.cancel()
            entry.timer = None

    def _emit(self, ev: SettledEvent) -> None:
        try:
            self.on_settled(ev)
        except Exception as err:  # noqa: BLE001 — callback failures never tear down the watcher
            self.log.warning(
                "watcher.debouncer.callback_failed",
                path=ev.path,
                op=ev.op.value,
                err=str(err),
            )
