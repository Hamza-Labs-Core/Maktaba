"""``scan`` on the engine (Epic 54 story 54.36 §2): walk a library and save what it holds.

Before, ``scan`` was a legacy handler (``handlers.scan_handler``) that queued each new
video's ``probe`` by hand from the scanner's store. Now the scanner reports the videos it
inserted (``ScanResult.to_probe``), the stage returns them as its changed subjects, and the
graph's declared edge (``scan -> probe``, map ``videos_changed``) queues their probes. The
walk, the hashing, the follow-up checks (copies, missing files) and the failure kinds are
the handler's, unchanged. A disabled library's new videos are saved and not probed, as
before."""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ...log import get_logger
from ...scanner.service import Scanner
from ...scanner.store import SqlScanStore
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL"]

_log = get_logger()


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id = job.library_id or job.subject.id
    if library_id is None:
        raise StageFailure(
            "scan_missing_library", f"scan job {job.id} has no library_id", retryable=False
        )
    scanner = Scanner(SqlScanStore(db), log=_log, queue_probe=False)
    try:
        result = await scanner.run(library_id)
    except LookupError as exc:  # the library vanished between enqueue and claim
        raise StageFailure("scan_library_vanished", str(exc), retryable=False) from exc
    except StageFailure:
        raise
    except Exception as exc:  # walk IO, a store write: another run may succeed
        raise StageFailure("scan_failed", str(exc), retryable=True) from exc
    _log.info(
        "scan_committed",
        job_id=job.id,
        library_id=str(library_id),
        files_walked=result.files_walked,
        files_inserted=result.files_inserted,
        files_unchanged=result.files_unchanged,
        files_skipped=result.files_skipped,
        errors=len(result.errors),
    )
    await _follow_up(db, library_id)
    probe = sorted(str(v) for v in result.to_probe)
    return StageResult(
        output_hash=canonical_hash({"new": probe}),
        outcome="changed" if probe else "unchanged",
        changed_subjects=tuple(("video", v) for v in result.to_probe),
        summary={
            "walked": result.files_walked,
            "inserted": result.files_inserted,
            "unchanged": result.files_unchanged,
            "skipped": result.files_skipped,
            "ignored": result.files_ignored,
            "errors": len(result.errors),
        },
    )


async def _follow_up(db: Any, library_id: Any) -> None:
    """The checks a scan runs after its walk (the handler's, unchanged): a copy whose file is
    gone is forgotten, a video whose own file is gone moves to its copy (story 47.5), and the
    files a job found missing are looked at again (Epic 40). Neither fails the scan."""
    try:
        from ...scanner.copies import reconcile_copies

        copies = await reconcile_copies(db, library_id)
        if copies.promoted or copies.forgotten:
            _log.info("scan_copies_reconciled", library_id=str(library_id),
                      promoted=len(copies.promoted), forgotten=len(copies.forgotten))  # fmt: skip
    except Exception:  # noqa: BLE001 - a follow-up check, never a reason to fail the scan
        _log.exception("scan_copies_failed", library_id=str(library_id))
    try:
        from ..missing_files import confirm_missing

        missing = await confirm_missing(db, library_id)
        if missing.healed or missing.confirmed:
            _log.info("scan_missing_files", library_id=str(library_id),
                      healed=missing.healed, confirmed=missing.confirmed)  # fmt: skip
    except Exception:  # noqa: BLE001 - a follow-up check, never a reason to fail the scan
        _log.exception("scan_missing_files_failed", library_id=str(library_id))


# no input fingerprint: every scan walks (the files are the input, read by walking them)
IMPL = StageImpl(run=run)
