"""This process's own files on the share (design-agent.md section 4).

``<cluster dir>/<ip>/``::

    processes/<worker_id>.json             identity, rewritten at start
    logs/<YYYY-MM-DD>/<worker_id>.jsonl    {ts, level, event, logger, fields}
    metrics/<YYYY-MM-DD>/<worker_id>.jsonl a metrics frame every 10 s, and the final one
    llm/<YYYY-MM-DD>/<worker_id>.jsonl     requests this process ran (usage, never text)
    jobs/<YYYY-MM-DD>/<worker_id>.jsonl    every job event this process had

One writer per file (an append from two processes to one file is not safe on NFS/SMB).
Records are queued in memory (bounded; past it they are counted) and written by a
daemon thread at least every 2 s, so a slow or hung share never blocks the worker. The
day directory is the record's UTC date. Each file is capped at ``MAKTABA_CLUSTER_FILE_MB``
(past it records are counted, not written); the machine's day folders older than
``MAKTABA_CLUSTER_RETENTION_DAYS`` are removed (:meth:`ClusterFiles.prune`). Directories
are created ``0775`` (the hub and the home's workers share a group), files ``0644``.
A failed write is tried once more with fresh handles (an SMB session that dropped and
reconnected leaves every open handle dead). When the share cannot be written the files go
to ``~/.maktaba/cluster`` (the records of the failed write too) and
:meth:`ClusterFiles.status` says so (the metrics ``files`` key); the share is tried again
every 5 minutes, 15 s after a share in use failed a write.
"""

from __future__ import annotations

import contextlib
import json
import os
import re
import threading
import time
from collections import Counter
from collections.abc import Callable, Mapping
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import IO, Any

from ...log import get_logger
from .. import safefs
from .logship import AGENT_LOGGER

__all__ = [
    "STREAMS",
    "ClusterFiles",
    "safe_name",
]

STREAMS = ("logs", "metrics", "llm", "jobs")
FLUSH_SEC = 1.0
MAX_PENDING = 100_000
RETRY_SHARE_SEC = 300.0
RETRY_AFTER_WRITE_SEC = 15.0  #: after a share that was in use failed a write
PRUNE_SEC = 86_400.0  #: retention runs at start and once a day
IDENTITY_SEC = 60.0
ERROR_LOG_SEC = 60.0

_log = get_logger(logger=AGENT_LOGGER)


def safe_name(worker_id: str) -> str:
    """A worker id as a file name on any OS and share."""
    name = re.sub(r"[^A-Za-z0-9._-]", "_", worker_id).strip("._")
    return name[:120] or "worker"


def _env_int(name: str, default: int) -> int:
    try:
        return max(0, int(os.environ.get(name, "") or default))
    except ValueError:
        return default


_DAY = re.compile(r"\d{4}-\d{2}-\d{2}")


def _last_seen(path: Path) -> datetime | None:
    """A ``processes/*.json``'s ``last_seen`` (else its modification time)."""
    try:
        with safefs.open_read(path) as fh:
            data = json.load(fh)
        text = str(data.get("last_seen") or data.get("started_at") or "")
        if text:
            seen = datetime.fromisoformat(text.replace("Z", "+00:00"))
            return seen if seen.tzinfo else seen.replace(tzinfo=UTC)
    except (OSError, ValueError, AttributeError):
        pass
    try:
        return datetime.fromtimestamp(os.lstat(path).st_mtime, tz=UTC)
    except OSError:
        return None


def _ends_with_newline(path: Path) -> bool:
    """The file's last byte is ``\\n`` (true when it cannot be read: nothing is added)."""
    try:
        with safefs.open_read_binary(path) as fh:
            fh.seek(-1, os.SEEK_END)
            return fh.read(1) == b"\n"
    except OSError:
        return True


def _iso_now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


def _utc_day(ts: Any = None) -> str:
    if isinstance(ts, str) and _DAY.fullmatch(ts[:10]):
        return ts[:10]
    return datetime.now(UTC).strftime("%Y-%m-%d")


class ClusterFiles:
    def __init__(
        self,
        share_dir: Path | None,
        ip: str,
        worker_id: str,
        *,
        reason: str = "",
        fallback_dir: Path | None = None,
        retention_days: int | None = None,
        file_cap_bytes: int | None = None,
        flush_sec: float = FLUSH_SEC,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        from ..share import local_dir

        self.share_dir = share_dir
        self.fallback_dir = fallback_dir if fallback_dir is not None else local_dir()
        self.ip = ip
        self.worker_id = worker_id
        self.name = safe_name(worker_id)
        self.retention_days = (
            retention_days
            if retention_days is not None
            else _env_int("MAKTABA_CLUSTER_RETENTION_DAYS", 14)
        )
        self.file_cap = (
            file_cap_bytes
            if file_cap_bytes is not None
            else _env_int("MAKTABA_CLUSTER_FILE_MB", 256) * 1024 * 1024
        )
        self.flush_sec = flush_sec
        self.clock = clock
        self._lock = threading.Lock()
        self._pending: list[tuple[str, str, str]] = []  # (stream, day, line)
        self._files: dict[tuple[str, str], IO[str]] = {}
        self._sizes: dict[tuple[str, str], int] = {}
        self.dropped: Counter[str] = Counter()  # queue full
        self.capped: Counter[str] = Counter()  # past the file cap
        self.failed = 0  # records a write error lost
        self.reason = reason
        self.on_share = False
        self.dir: Path = self.fallback_dir
        self._identity: dict[str, Any] | None = None
        self._last_share_try = float("-inf")
        self._last_prune = float("-inf")
        self._last_seen = clock()
        #: one thread at a time moves, writes or closes the files
        self._io = threading.RLock()
        self._last_error_log = float("-inf")
        self._stop = threading.Event()
        self._wake = threading.Event()
        self._thread: threading.Thread | None = None
        self._choose_dir()

    # -- where -------------------------------------------------------------------

    @property
    def machine_dir(self) -> Path:
        return self.dir / self.ip

    def _probe(self, base: Path) -> str | None:
        """None when ``base/<ip>`` can be written, else why not."""
        try:
            base.mkdir(parents=True, exist_ok=True)
            d = safefs.safe_dir(base, [self.ip, "processes"])
            safefs.write_replace(d, f".{self.name}.probe", "ok")
            safefs.remove_real_file(d / f".{self.name}.probe")
            return None
        except OSError as exc:
            return f"{type(exc).__name__}: {exc}"

    def _choose_dir(self) -> None:
        self._last_share_try = self.clock()
        if self.share_dir is not None:
            why = self._probe(self.share_dir)
            if why is None:
                self._switch(self.share_dir, on_share=True)
                return
            self.reason = f"share not writable ({self.share_dir}): {why}"
        elif not self.reason:
            self.reason = "no share"
        self._switch(self.fallback_dir, on_share=False)
        self._probe(self.fallback_dir)

    def _switch(self, base: Path, *, on_share: bool) -> None:
        self._close_files()
        self.dir = base
        self.on_share = on_share
        if on_share:
            self.reason = ""
        if self._identity is not None:
            self._write_identity()

    def _under(self, parts: list[str]) -> Path:
        """``<dir>/parts…`` as real directories (the cluster directory itself is
        configuration and may be created; nothing under it is followed if a link)."""
        self.dir.mkdir(parents=True, exist_ok=True)
        return safefs.safe_dir(self.dir, parts)

    def status(self) -> dict[str, Any]:
        """The metrics frame's ``files`` key: ``{ip, dir, on_share, reason, dropped}``
        (``dropped``: records not written, for any reason)."""
        return {
            "ip": self.ip,
            "dir": str(self.machine_dir),
            "on_share": self.on_share,
            "reason": self.reason or None,
            "dropped": sum(self.dropped.values()) + sum(self.capped.values()) + self.failed,
        }

    def set_share_dir(self, share_dir: Path | None, reason: str = "") -> None:
        """The hub sent the share root (``welcome.settings``): its value wins. Takes
        effect at once when it names another directory."""
        if share_dir == self.share_dir:
            return
        with self._io:
            self.share_dir = share_dir
            self.reason = reason
            self._choose_dir()

    def set_ip(self, ip: str) -> None:
        """The hub names this process's IP folder (``welcome.settings.ip``): from now on
        the files go there (what was written stays where it is)."""
        with self._io:
            if ip == self.ip:
                return
            self._close_files()
            self.ip = ip
            if self._identity is not None:
                self._identity["ip"] = ip
            self._choose_dir()

    def apply_settings(
        self, *, retention_days: int | None = None, file_mb: int | None = None
    ) -> None:
        """The hub's retention and cap, unless this machine's environment sets them."""
        if retention_days is not None and not os.environ.get("MAKTABA_CLUSTER_RETENTION_DAYS"):
            self.retention_days = max(0, int(retention_days))
        if file_mb is not None and not os.environ.get("MAKTABA_CLUSTER_FILE_MB"):
            self.file_cap = max(0, int(file_mb)) * 1024 * 1024

    # -- writing (any thread) ------------------------------------------------------

    def write(self, stream: str, record: Mapping[str, Any]) -> None:
        try:
            line = json.dumps(record, separators=(",", ":"), ensure_ascii=False, default=str)
        except (TypeError, ValueError):
            self.failed += 1
            return
        day = _utc_day(record.get("ts"))
        with self._lock:
            if len(self._pending) >= MAX_PENDING:
                self.dropped[stream] += 1
                return
            self._pending.append((stream, day, line))

    def sink(self, stream: str) -> Callable[[dict[str, Any]], None]:
        return lambda rec: self.write(stream, rec)

    def set_identity(self, identity: Mapping[str, Any]) -> None:
        """``processes/<worker_id>.json``: written now, its ``last_seen`` every 60 s,
        ``stopped_at`` at a clean stop."""
        now = _iso_now()
        self._identity = {"started_at": now, **dict(identity), "last_seen": now}
        self._identity.setdefault("stopped_at", None)
        self._write_identity()

    def _write_identity(self) -> None:
        if self._identity is None:
            return
        try:
            d = self._under([self.ip, "processes"])
            body = json.dumps(self._identity, indent=2, default=str)
            safefs.write_replace(d, f"{self.name}.json", body)
        except OSError as exc:
            self._error("cluster_files_identity_failed", exc)

    # -- the writer thread ----------------------------------------------------------

    def start(self) -> None:
        if self._thread is not None:
            return
        self._thread = threading.Thread(target=self._run, name="cluster-files", daemon=True)
        self._thread.start()

    def stop(self, timeout: float = 3.0) -> None:
        self._stop.set()
        self._wake.set()
        if self._thread is not None:
            self._thread.join(timeout)
            self._thread = None
        else:
            self.flush()
        with self._io:
            self._close_files()
            if self._identity is not None:
                now = _iso_now()
                self._identity.update(last_seen=now, stopped_at=now)
                self._write_identity()

    def _run(self) -> None:
        while not self._stop.is_set():
            self._wake.wait(self.flush_sec)
            self._wake.clear()
            try:
                self.tick()
            except Exception as exc:  # noqa: BLE001 - the writer thread never dies
                self._error("cluster_files_failed", exc)
        with contextlib.suppress(Exception):
            self.flush()

    def tick(self) -> None:
        now = self.clock()
        with self._io:
            if (
                not self.on_share
                and self.share_dir is not None
                and now - self._last_share_try >= RETRY_SHARE_SEC
            ):
                self._choose_dir()
            if self._identity is not None and now - self._last_seen >= IDENTITY_SEC:
                self._last_seen = now
                self._identity["last_seen"] = _iso_now()
                self._write_identity()
        self.flush()
        if now - self._last_prune >= PRUNE_SEC:
            self._last_prune = now
            self.prune()

    def flush(self) -> int:
        with self._io:
            return self._flush()

    def _flush(self) -> int:
        with self._lock:
            batch, self._pending = self._pending, []
        if not batch:
            return 0
        written = 0
        grouped: dict[tuple[str, str], list[str]] = {}
        for stream, day, line in batch:
            grouped.setdefault((stream, day), []).append(line)
        for key, lines in grouped.items():
            try:
                written += self._append(key, lines)
                continue
            except OSError:
                # An SMB session that dropped and came back (a NAS's I/O timeout) leaves
                # every open handle dead; on Windows such a write fails with a bare
                # "[Errno 22] Invalid argument" (the C runtime's default for a network
                # error it has no errno for). Fresh handles, one more try.
                self._close_files()
            try:
                written += self._append(key, lines)
                continue
            except OSError as exc:
                self._close_files()
                self._error("cluster_files_write_failed", exc)
                if not self.on_share:
                    self.failed += len(lines)
                    continue
                self.reason = f"write failed: {exc}"
                self._switch(self.fallback_dir, on_share=False)
                # a dropped share usually comes back within seconds: try it again soon
                self._last_share_try = self.clock() - RETRY_SHARE_SEC + RETRY_AFTER_WRITE_SEC
            try:  # the records go to the local folder, not lost
                written += self._append(key, lines)
            except OSError as exc:
                self.failed += len(lines)
                self._close_files()
                self._error("cluster_files_write_failed", exc)
        # days no longer written: close their handles
        today = {k for k in grouped}
        for key in [k for k in self._files if k not in today and k[1] < _utc_day()]:
            self._drop_handle(key)
        return written

    def _append(self, key: tuple[str, str], lines: list[str]) -> int:
        """Append ``lines`` (within the file cap) to ``key``'s file; the number written."""
        stream = key[0]
        fh = self._open(key)
        size = self._sizes[key]
        out: list[str] = []
        capped = 0
        for line in lines:
            n = len(line.encode("utf-8")) + 1
            if self.file_cap and size + n > self.file_cap:
                capped += 1
                continue
            size += n
            out.append(line)
        if out:
            fh.write("\n".join(out) + "\n")
            fh.flush()
        self._sizes[key] = size
        self.capped[stream] += capped  # counted once the write went through
        return len(out)

    def _open(self, key: tuple[str, str]) -> IO[str]:
        fh = self._files.get(key)
        if fh is not None:
            return fh
        stream, day = key
        d = self._under([self.ip, stream, day])
        path = d / f"{self.name}.jsonl"
        fh = safefs.open_append(path)  # never through a link
        try:
            size = os.fstat(fh.fileno()).st_size
        except OSError:
            size = 0
        if size and not _ends_with_newline(path):
            # a write cut short (the share dropped mid-write): end that line, so the
            # next record starts on its own
            try:
                fh.write("\n")
                fh.flush()
            except OSError:
                with contextlib.suppress(OSError):
                    fh.close()
                raise
            size += 1
        self._files[key] = fh
        self._sizes[key] = size
        return fh

    def _drop_handle(self, key: tuple[str, str]) -> None:
        fh = self._files.pop(key, None)
        self._sizes.pop(key, None)
        if fh is not None:
            with contextlib.suppress(OSError):
                fh.close()

    def _close_files(self) -> None:
        for key in list(self._files):
            self._drop_handle(key)

    # -- retention ---------------------------------------------------------------

    def prune(self, today: str | None = None) -> int:
        """Retention for this machine's folder: every process file in an expired day
        folder goes (an expired UTC day is never written again; worker ids are new at
        every start, so the files of earlier processes would otherwise stay for ever),
        then the emptied folder. In the local fallback, which the hub never sweeps,
        the ``processes/*.json`` of processes not seen within the retention go too.
        Real directories and regular files only: a link is never followed or removed.
        Returns the number of files removed."""
        if self.retention_days <= 0:
            return 0
        day = datetime.strptime(today or _utc_day(), "%Y-%m-%d").replace(tzinfo=UTC)
        cutoff_day = day - timedelta(days=self.retention_days)
        cutoff = cutoff_day.strftime("%Y-%m-%d")
        removed = 0
        for base in {self.dir, self.fallback_dir} | (
            {self.share_dir} if self.share_dir is not None else set()
        ):
            if not safefs.real_dir(base / self.ip):
                continue  # missing, or a link: never followed
            for stream in STREAMS:
                # real directories only, each checked without following links
                for ddir in safefs.list_real_dirs(base / self.ip / stream):
                    if not _DAY.fullmatch(ddir.name) or ddir.name >= cutoff:
                        continue
                    # macOS's "._<name>" sidecars go with the files they stand beside
                    for f in safefs.list_real_files(ddir, ".jsonl", hidden=True):
                        try:
                            if safefs.remove_real_file(f):
                                removed += 1
                        except OSError as exc:
                            self._error("cluster_files_prune_failed", exc)
                    with contextlib.suppress(OSError):
                        ddir.rmdir()  # only when empty (a link or a stray file stays)
        removed += self._prune_processes(self.fallback_dir, cutoff_day)
        return removed

    def _prune_processes(self, base: Path, cutoff: datetime) -> int:
        """``processes/*.json`` last seen before ``cutoff`` (this process's own stays)."""
        pdir = base / self.ip / "processes"
        if not (safefs.real_dir(base / self.ip) and safefs.real_dir(pdir)):
            return 0
        removed = 0
        for p in safefs.list_real_files(pdir, ".json"):
            if p.stem == self.name:
                continue
            seen = _last_seen(p)
            if seen is None or seen >= cutoff:
                continue
            try:
                if safefs.remove_real_file(p):
                    removed += 1
                    safefs.remove_real_file(p.with_name("._" + p.name))  # macOS's sidecar
            except OSError as exc:
                self._error("cluster_files_prune_failed", exc)
        return removed

    def _error(self, event: str, exc: BaseException) -> None:
        now = self.clock()
        if now - self._last_error_log >= ERROR_LOG_SEC:
            self._last_error_log = now
            _log.warning(event, error=str(exc), dir=str(self.dir))
