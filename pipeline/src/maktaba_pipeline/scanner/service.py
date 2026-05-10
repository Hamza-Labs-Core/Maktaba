"""Scanner orchestrator — composes walker + hasher + store (Story 1.1).

The :class:`Scanner` runs one bootstrap pass over a library's roots and
turns every supported file into a ``videos`` row plus a ``probe`` job:

1. Look up the library; abort with a WARN log if the library has zero
   roots (Story 1.1 edge case "Library with zero roots").
2. For each root, drive :func:`maktaba_pipeline.scanner.walker.walk` to
   stream :class:`Candidate` records.
3. For each candidate, consult the store: if a video at the same path
   already has the same ``(size_bytes, mtime)`` signature, skip the
   rehash entirely (the "Reuse on (path, size, mtime) unchanged"
   optimisation called out in Story 1.2).
4. Otherwise hash via :mod:`maktaba_pipeline.identity` and call
   :meth:`ScanStore.save_candidate`. The store inserts the video row
   and (when the library is enabled) enqueues the probe job inside one
   transaction; the slot 0005 trigger fires
   ``pg_notify('videos.new', …)`` from inside that same transaction so
   the API's WebSocket fan-out sees exactly one frame per insert.
5. On SQLite the orchestrator publishes the same payload on the
   in-process :class:`maktaba_pipeline.db.PubsubBus` after the commit —
   matching how slot 0002's ``jobs.new`` channel is mirrored across
   dialects.

The store is a thin :class:`ScanStore` protocol; the production
implementation backs onto Story 1.5's connection wrapper. Tests stub it
directly so the scanner orchestrator runs against an in-memory fake.
"""

from __future__ import annotations

import os
from collections.abc import Iterable, Iterator
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any, Protocol, cast
from uuid import UUID

from ..db.pubsub import VIDEOS_NEW, get_bus
from ..identity import hash_file
from .walker import (
    DEFAULT_IGNORE_BASENAMES,
    DEFAULT_IGNORE_DIRNAMES,
    DEFAULT_VIDEO_EXTENSIONS,
    Candidate,
    WalkConfig,
    walk,
)

__all__ = [
    "ExistingVideo",
    "LibraryRecord",
    "SaveCandidateParams",
    "SaveCandidateResult",
    "ScanCancelled",
    "ScanConfig",
    "ScanControl",
    "ScanError",
    "ScanLibraryDeleted",
    "ScanOptions",
    "ScanResult",
    "ScanStore",
    "Scanner",
]


class ScanCancelled(RuntimeError):
    """Raised by :meth:`Scanner.run` when ``cancel_requested`` flips true.

    The orchestrator catches nothing; callers (the API handler, the
    CLI, integration tests) get a clean exception they can map to a
    ``cancelling → idle`` transition. The :class:`ScanResult` collected
    so far is attached for observability.
    """

    def __init__(self, result: ScanResult) -> None:
        super().__init__(f"scan cancelled at files_walked={result.files_walked}")
        self.result = result


class ScanLibraryDeleted(RuntimeError):
    """Raised when ``libraries.deleted_at`` flips during a scan.

    Mirrors :class:`ScanCancelled` for the "library deleted mid-scan"
    edge case from Story 1.4. Carries the partial :class:`ScanResult`.
    """

    def __init__(self, result: ScanResult) -> None:
        super().__init__(f"library deleted; scan stopped at files_walked={result.files_walked}")
        self.result = result


@dataclass(slots=True, frozen=True)
class LibraryRecord:
    """Minimal projection the scanner needs from the ``libraries`` row.

    ``disabled`` and ``follow_symlinks`` come from ``settings`` JSONB —
    the store decodes them before returning so the orchestrator never
    sees raw JSON. ``roots`` is the per-library array column from slot
    0001 (the canonical store moves to ``library_roots`` in plan-09-16,
    at which point this field is repopulated from the join).
    """

    id: UUID
    name: str
    roots: tuple[str, ...]
    disabled: bool = False
    follow_symlinks: bool = False


@dataclass(slots=True, frozen=True)
class ExistingVideo:
    """Read-back shape for the "skip rehash if unchanged" lookup.

    ``mtime`` is microsecond-precision UTC. The orchestrator compares
    against the candidate's ``mtime_ns`` truncated to microseconds — see
    :func:`_mtime_ns_to_db` for why microseconds is the resolution we
    chose and where the truncation lives.
    """

    id: UUID
    size_bytes: int
    mtime: datetime
    content_hash: str


@dataclass(slots=True, frozen=True)
class SaveCandidateParams:
    """Inputs to :meth:`ScanStore.save_candidate`.

    ``last_seen_at`` is filled in by the scanner so ``last_seen_at``
    sweeps (slot 0007 / Story 1.5) see the row immediately. ``mtime`` is
    microsecond-precision UTC. ``enqueue_probe`` is ``False`` when the
    library is disabled — the row still lands but the probe job does
    not, mirroring AC5 of Story 1.1.
    """

    library_id: UUID
    content_hash: str
    path: str
    filename: str
    size_bytes: int
    mtime: datetime
    last_seen_at: datetime
    enqueue_probe: bool


@dataclass(slots=True, frozen=True)
class SaveCandidateResult:
    """Outcome of one :meth:`ScanStore.save_candidate` call."""

    video_id: UUID
    inserted: bool
    job_id: int | None


class ScanStore(Protocol):
    """Persistence boundary the orchestrator depends on.

    ``dialect`` is one of ``"postgres"`` or ``"sqlite"``; it drives the
    one place the orchestrator branches on backend (the SQLite
    pubsub-fanout fallback). All other behaviour is portable.
    """

    dialect: str

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        """Return the library projection or ``None`` if absent."""

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        """Look up the video row at ``(library_id, path)``, if any."""

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        """Insert (video, probe-job) atomically. Idempotent on conflict."""


@dataclass(slots=True, frozen=True)
class ScanControl:
    """Snapshot of one ``library_scan_state`` poll round-trip.

    ``cancel_requested`` drives the orchestrator's exit path; the rest
    is observability the API surfaces verbatim. ``library_deleted``
    rides on the same poll because both ``cancel_requested`` and
    ``deleted_at`` change asynchronously, and re-querying for the
    deletion check would double the per-poll cost.
    """

    cancel_requested: bool
    progress_pct: float
    library_deleted: bool


class ScanControlStore(Protocol):
    """Optional capability layered on top of :class:`ScanStore`.

    Stores that implement this protocol participate in cancellation
    polling and progress write-back; stores that don't (like the
    in-memory ``FakeScanStore`` in many tests) skip the poll entirely.

    The orchestrator branches on ``isinstance``-style duck typing —
    not on the dialect — so the same code path covers Postgres,
    SQLite, and the dry-run store without explicit dispatch.
    """

    async def clear_scan_control(self, library_id: UUID) -> None:
        """Reset ``cancel_requested`` and ``progress_pct`` at start of run.

        Called once before the walk begins so a stale ``cancel_requested
        = true`` from a previous cancelled run does not abort the next
        attempt on its very first poll.
        """

    async def poll_scan_control(
        self,
        library_id: UUID,
        files_walked: int,
        files_inserted: int,
    ) -> ScanControl:
        """Single round-trip: write progress, read cancel + deletion flags."""

    async def record_scan_error(
        self,
        library_id: UUID,
        message: str,
    ) -> None:
        """Persist a ``last_error`` string visible to the GET handler."""


@dataclass(slots=True, frozen=True)
class ScanOptions:
    """Per-run knobs distinct from the persistent :class:`ScanConfig`.

    ``dry_run`` skips the cancel-control round-trip (a dry-run is a
    read-only preview that must not fight a concurrent real scan over
    the same ``library_scan_state`` row). ``cancel_poll_every`` is the
    number of files between control polls — at the default 50 files
    and a typical hash throughput of 100 files/s this lands the cancel
    SLA well inside the story's 5 s requirement.
    """

    dry_run: bool = False
    cancel_poll_every: int = 50


@dataclass(slots=True, frozen=True)
class ScanConfig:
    """Knobs for one :meth:`Scanner.run` invocation.

    Defaults match Story 1.1's spec exactly. Tests override individual
    fields by constructing fresh :class:`ScanConfig` instances rather
    than mutating a shared global.
    """

    extensions: frozenset[str] = DEFAULT_VIDEO_EXTENSIONS
    ignore_basenames: tuple[str, ...] = DEFAULT_IGNORE_BASENAMES
    ignore_dirnames: frozenset[str] = DEFAULT_IGNORE_DIRNAMES


@dataclass(slots=True, frozen=True)
class ScanError:
    """One per-file failure (hash IO error, save error, etc.).

    The scan does not abort on a single error: the orchestrator records
    the error and continues with the next candidate. Callers that want
    "all-or-nothing" semantics inspect ``ScanResult.errors`` after the
    run completes.
    """

    path: str
    error: str


@dataclass(slots=True)
class ScanResult:
    """End-of-run summary returned by :meth:`Scanner.run`.

    The counts are mutated in place during the walk so a long scan can
    expose progress through a shared reference. ``finished_at`` is
    populated by the orchestrator before the result is returned.
    """

    library_id: UUID
    started_at: datetime
    finished_at: datetime | None = None
    files_walked: int = 0
    files_inserted: int = 0
    files_unchanged: int = 0  # path+signature matched an existing row
    files_skipped: int = 0  # ON CONFLICT swallowed the insert
    files_ignored: int = 0  # zero-byte
    errors: list[ScanError] = field(default_factory=list)


class _Logger(Protocol):
    """The structlog-shaped methods the orchestrator calls."""

    def info(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def debug(self, event: str, **kwargs: Any) -> Any: ...
    def error(self, event: str, **kwargs: Any) -> Any: ...


class Scanner:
    """Walk a library's roots and persist what we find.

    One :class:`Scanner` instance is reused across many
    :meth:`Scanner.run` calls (one per library). The instance owns no
    per-scan state; concurrency is the caller's problem (typically: one
    in-flight scan per library, enforced by Story 1.4's scan-control
    columns).
    """

    def __init__(
        self,
        store: ScanStore,
        config: ScanConfig | None = None,
        *,
        log: _Logger,
    ) -> None:
        self._store = store
        self._config = config or ScanConfig()
        self._log = log

    async def run(
        self,
        library_id: UUID,
        options: ScanOptions | None = None,
    ) -> ScanResult:
        """Run one bootstrap pass over ``library_id``.

        Raises :class:`LookupError` if the library does not exist,
        :class:`ScanCancelled` if ``library_scan_state.cancel_requested``
        flips during the walk, or :class:`ScanLibraryDeleted` if
        ``libraries.deleted_at`` flips. Other per-file failures are
        aggregated into :attr:`ScanResult.errors`.

        Stores that implement the optional :class:`ScanControlStore`
        protocol participate in cancellation polling and progress
        write-back. Stores that do not (in-memory test fakes, the
        :class:`DryRunStore`) skip the poll path entirely. ``options``
        is also where callers turn off the poll explicitly via
        ``ScanOptions(dry_run=True)``.
        """
        opts = options or ScanOptions()
        started = _utcnow()
        lib = await self._store.get_library(library_id)
        if lib is None:
            raise LookupError(f"library {library_id} not found")

        result = ScanResult(library_id=lib.id, started_at=started)

        if not lib.roots:
            self._log.warning(
                "scanner.no_roots",
                library_id=str(lib.id),
                name=lib.name,
            )
            result.finished_at = _utcnow()
            return result

        if lib.disabled:
            self._log.info(
                "scanner.library_disabled",
                library_id=str(lib.id),
                name=lib.name,
            )

        walk_cfg = WalkConfig(
            extensions=self._config.extensions,
            ignore_basenames=self._config.ignore_basenames,
            ignore_dirnames=self._config.ignore_dirnames,
            follow_symlinks=lib.follow_symlinks,
        )

        control_store: ScanControlStore | None = (
            cast(ScanControlStore, self._store) if _has_scan_control(self._store) else None
        )
        if control_store is not None and not opts.dry_run:
            await control_store.clear_scan_control(lib.id)

        poll_every = max(1, opts.cancel_poll_every)
        files_since_poll = 0

        try:
            for candidate in self._candidates(lib.roots, walk_cfg):
                result.files_walked += 1
                await self._process_candidate(lib, candidate, result)

                files_since_poll += 1
                if (
                    control_store is not None
                    and not opts.dry_run
                    and files_since_poll >= poll_every
                ):
                    files_since_poll = 0
                    flags = await control_store.poll_scan_control(
                        lib.id,
                        result.files_walked,
                        result.files_inserted,
                    )
                    if flags.cancel_requested:
                        result.finished_at = _utcnow()
                        await control_store.record_scan_error(lib.id, "cancelled")
                        self._log.info(
                            "scanner.cancelled",
                            library_id=str(lib.id),
                            files_walked=result.files_walked,
                        )
                        raise ScanCancelled(result)
                    if flags.library_deleted:
                        result.finished_at = _utcnow()
                        await control_store.record_scan_error(lib.id, "library_deleted")
                        self._log.warning(
                            "scanner.library_deleted",
                            library_id=str(lib.id),
                            files_walked=result.files_walked,
                        )
                        raise ScanLibraryDeleted(result)
        except (ScanCancelled, ScanLibraryDeleted):
            raise

        result.finished_at = _utcnow()
        self._log.info(
            "scanner.completed",
            library_id=str(lib.id),
            name=lib.name,
            files_walked=result.files_walked,
            files_inserted=result.files_inserted,
            files_unchanged=result.files_unchanged,
            files_skipped=result.files_skipped,
            files_ignored=result.files_ignored,
            errors=len(result.errors),
            dry_run=opts.dry_run,
        )
        return result

    def _candidates(
        self,
        roots: Iterable[str],
        walk_cfg: WalkConfig,
    ) -> Iterator[Candidate]:
        """Yield candidates from every root in turn.

        Sequential rather than fan-out: one library scan staying on a
        single CPU is fine — the bottleneck is hashing IO, and we want
        the per-file transactions to land in a deterministic-ish order
        for log readability. The watcher in Story 1.3 handles
        concurrency at the file-event level.
        """
        for root in roots:
            try:
                yield from walk(root, walk_cfg, self._log)
            except FileNotFoundError:
                self._log.warning("scanner.root_missing", path=root)
            except PermissionError:
                self._log.warning("scanner.root_permission_denied", path=root)
            except OSError as err:
                self._log.warning("scanner.root_failed", path=root, err=str(err))

    async def _process_candidate(
        self,
        lib: LibraryRecord,
        candidate: Candidate,
        result: ScanResult,
    ) -> None:
        """Persist one candidate, updating ``result`` in place."""
        # Skip-on-unchanged: if a row already exists at this path with
        # the same (size_bytes, mtime) signature, the bytes are the
        # same and we don't need to re-open or re-hash. This is the
        # FileSignature optimisation called out in the user prompt.
        existing = await self._store.find_video_by_path(lib.id, candidate.path)
        if existing is not None and _signature_matches(existing, candidate):
            result.files_unchanged += 1
            self._log.debug(
                "scanner.unchanged",
                video_id=str(existing.id),
                path=candidate.path,
            )
            return

        if candidate.size_bytes == 0:
            # Story 1.1 edge case: zero-byte files are noise — no row,
            # no error, DEBUG only.
            result.files_ignored += 1
            self._log.debug("scanner.zero_byte_skipped", path=candidate.path)
            return

        try:
            hash_result = hash_file(candidate.path)
        except (OSError, ValueError) as err:
            result.errors.append(
                ScanError(path=candidate.path, error=f"{type(err).__name__}: {err}")
            )
            self._log.error(
                "scanner.hash_failed",
                path=candidate.path,
                err=str(err),
            )
            return

        mtime_dt = _mtime_ns_to_db(candidate.mtime_ns)
        params = SaveCandidateParams(
            library_id=lib.id,
            content_hash=hash_result.content_hash,
            path=candidate.path,
            filename=os.path.basename(candidate.path),
            size_bytes=hash_result.size_bytes,
            mtime=mtime_dt,
            last_seen_at=_utcnow(),
            enqueue_probe=not lib.disabled,
        )

        try:
            save = await self._store.save_candidate(params)
        except Exception as err:  # noqa: BLE001 — store failures are logged + aggregated
            result.errors.append(
                ScanError(path=candidate.path, error=f"{type(err).__name__}: {err}")
            )
            self._log.error(
                "scanner.save_failed",
                path=candidate.path,
                err=str(err),
            )
            return

        if save.inserted:
            result.files_inserted += 1
            self._log.debug(
                "scanner.video_inserted",
                video_id=str(save.video_id),
                path=candidate.path,
                content_hash=hash_result.content_hash,
            )
            # Postgres has the slot 0005 AFTER INSERT trigger and fires
            # the NOTIFY at SQL level inside the save transaction; SQLite
            # has no LISTEN/NOTIFY so we publish manually so subscribers
            # in the same process see the same event shape.
            if self._store.dialect == "sqlite":
                get_bus().publish(
                    VIDEOS_NEW,
                    {
                        "id": str(save.video_id),
                        "library_id": str(lib.id),
                        "content_hash": hash_result.content_hash,
                        "path": candidate.path,
                        "filename": params.filename,
                        "state": "discovered",
                    },
                )
        else:
            # ON CONFLICT (library_id, content_hash) swallowed the
            # insert: another walker run, or a duplicate file at a
            # different path within the same library, already owns the
            # row. Story 1.2 grows the rename/move detection that
            # updates `path` here; for now we just bookkeep.
            result.files_skipped += 1
            self._log.debug(
                "scanner.video_already_present",
                video_id=str(save.video_id),
                path=candidate.path,
                content_hash=hash_result.content_hash,
            )


def _signature_matches(existing: ExistingVideo, candidate: Candidate) -> bool:
    """Compare ``existing`` against ``candidate`` at microsecond resolution.

    Postgres ``TIMESTAMPTZ`` rounds to microseconds (SQLite stores
    ISO-8601 text we round the same way), so the canonical comparison
    is at microsecond precision. Nanosecond differences below the
    rounding threshold are intentionally treated as equal — they cannot
    be observed through the schema.
    """
    if existing.size_bytes != candidate.size_bytes:
        return False
    candidate_mtime = _mtime_ns_to_db(candidate.mtime_ns)
    return existing.mtime == candidate_mtime


def _mtime_ns_to_db(mtime_ns: int) -> datetime:
    """Convert a nanosecond mtime to a UTC datetime at microsecond precision."""
    seconds, ns_remainder = divmod(int(mtime_ns), 1_000_000_000)
    micros = ns_remainder // 1_000
    base = datetime.fromtimestamp(seconds, tz=UTC)
    return base.replace(microsecond=micros)


def _utcnow() -> datetime:
    """Wall-clock UTC, microsecond precision. Tests can monkeypatch."""
    return datetime.now(tz=UTC)


def _has_scan_control(store: ScanStore) -> bool:
    """Duck-type check for the optional :class:`ScanControlStore` protocol."""
    return all(
        callable(getattr(store, name, None))
        for name in ("clear_scan_control", "poll_scan_control", "record_scan_error")
    )
