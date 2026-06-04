"""Model service — the orchestrator the API drives over gRPC.

Ties the :mod:`registry` catalog, :mod:`storage`, and :mod:`downloader`
together behind the surface the Go API calls:

- :meth:`ModelService.list_models` — catalog + installed + active state.
- :meth:`ModelService.download_model` — start an async download, return a
  job id; the actual transfer runs off the event loop in a worker thread.
- :meth:`ModelService.download_progress` — poll a job by id.
- :meth:`ModelService.delete_model` — remove a model's files.
- :meth:`ModelService.activate_model` — set the active model for its type.
- :meth:`ModelService.test_model` — run a short sample through the model.

Downloads are tracked as :class:`Job` records. A download for a model
that is already in flight returns the existing job id (idempotent), and
a failed download records the error on the job without marking the model
installed.
"""

from __future__ import annotations

import asyncio
import inspect
import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from . import registry
from .downloader import Downloader
from .registry import ModelSpec
from .storage import Storage

__all__ = ["Job", "ModelNotInstalled", "ModelService", "UnknownJob", "registry"]

# A tester loads the model and runs a short sample, returning a result
# dict (e.g. {"latency_ms": .., "detail": ..}). May be sync or async.
Tester = Callable[[ModelSpec, Path], "dict[str, Any] | Awaitable[dict[str, Any]]"]


class ModelNotInstalled(RuntimeError):
    """Raised when an operation needs a model that isn't installed."""

    def __init__(self, model_id: str) -> None:
        super().__init__(f"model not installed: {model_id}")
        self.model_id = model_id


class UnknownJob(KeyError):
    """Raised by :meth:`ModelService.download_progress` for a bad job id."""

    def __init__(self, job_id: str) -> None:
        super().__init__(job_id)
        self.job_id = job_id

    def __str__(self) -> str:
        return f"unknown job: {self.job_id}"


@dataclass(slots=True)
class Job:
    """A download job's live state. Mutated by the worker thread."""

    id: str
    model_id: str
    total: int
    status: str = "queued"  # queued | downloading | done | error
    downloaded: int = 0
    progress: int = 0  # 0..100
    error: str | None = None

    def as_dict(self) -> dict[str, Any]:
        return {
            "job_id": self.id,
            "model_id": self.model_id,
            "status": self.status,
            "progress": self.progress,
            "downloaded": self.downloaded,
            "total": self.total,
            "error": self.error,
        }


class ModelService:
    """Stateful orchestrator over the catalog, storage, and downloader."""

    def __init__(
        self,
        *,
        storage: Storage | None = None,
        downloader: Any = None,
        tester: Tester | None = None,
    ) -> None:
        self._storage = storage if storage is not None else Storage()
        self._downloader = downloader if downloader is not None else Downloader()
        self._tester = tester
        self._jobs: dict[str, Job] = {}
        self._tasks: dict[str, asyncio.Task[None]] = {}

    # --- read ------------------------------------------------------------

    async def list_models(self) -> list[dict[str, Any]]:
        """Catalog entries with overlaid installed / active / progress."""
        out: list[dict[str, Any]] = []
        for spec in registry.list_models():
            installed = self._storage.is_installed(spec.id)
            active = self._storage.is_active(spec.id)
            job = self._latest_job_for(spec.id)
            in_flight = job is not None and job.status in ("queued", "downloading")

            if active:
                status = "active"
            elif in_flight:
                status = "downloading"
            elif installed:
                status = "downloaded"
            else:
                status = "available"

            out.append(
                {
                    "id": spec.id,
                    "type": spec.type,
                    "name": spec.name,
                    "size": spec.size_human,
                    "size_bytes": spec.size_bytes,
                    "platform": spec.platform,
                    "gated": spec.gated,
                    "installed": installed,
                    "active": active,
                    "status": status,
                    "progress": job.progress if (job and in_flight) else (100 if installed else 0),
                }
            )
        return out

    # --- download --------------------------------------------------------

    async def download_model(self, model_id: str) -> str:
        """Start an async download; return its job id.

        Idempotent: a download already queued or running for the same
        model returns the existing job id rather than starting a second.
        Raises :class:`registry.UnknownModel` for an unknown id.
        """
        spec = registry.get_model(model_id)

        existing = self._latest_job_for(model_id)
        if existing is not None and existing.status in ("queued", "downloading"):
            return existing.id

        job = Job(id=uuid.uuid4().hex, model_id=model_id, total=spec.size_bytes)
        self._jobs[job.id] = job
        self._tasks[job.id] = asyncio.create_task(self._run_download(job, spec))
        return job.id

    async def _run_download(self, job: Job, spec: ModelSpec) -> None:
        job.status = "downloading"
        dest = self._storage.model_path(spec.id)

        def _progress(done: int, total: int) -> None:
            job.downloaded = done
            job.total = total or job.total
            job.progress = int(done * 100 / total) if total else 0

        try:
            await asyncio.to_thread(self._downloader.download, spec, dest, progress_cb=_progress)
            self._storage.mark_installed(spec.id)
            job.downloaded = job.total
            job.progress = 100
            job.status = "done"
        except Exception as exc:  # noqa: BLE001 - surfaced on the job, not swallowed
            job.status = "error"
            job.error = str(exc)

    async def download_progress(self, job_id: str) -> dict[str, Any]:
        """Current state of a download job; raises :class:`UnknownJob`."""
        job = self._jobs.get(job_id)
        if job is None:
            raise UnknownJob(job_id)
        return job.as_dict()

    async def wait(self, job_id: str) -> None:
        """Await a download job's background task (used by callers/tests)."""
        task = self._tasks.get(job_id)
        if task is not None:
            await task

    # --- mutate ----------------------------------------------------------

    async def delete_model(self, model_id: str) -> bool:
        """Remove a model's files. Raises :class:`registry.UnknownModel`."""
        registry.get_model(model_id)  # validate it's a known id
        return self._storage.delete(model_id)

    async def activate_model(self, model_id: str, model_type: str | None = None) -> dict[str, Any]:
        """Set ``model_id`` active for its type (inferred if not given)."""
        spec = registry.get_model(model_id)
        if not self._storage.is_installed(model_id):
            raise ModelNotInstalled(model_id)
        resolved_type = model_type or spec.type
        self._storage.set_active(resolved_type, model_id)
        return {"id": model_id, "type": resolved_type, "active": True}

    async def test_model(self, model_id: str) -> dict[str, Any]:
        """Run a short sample through the model.

        Requires the model installed. Delegates to the injected tester;
        without one, reports an unsupported result rather than failing.
        """
        spec = registry.get_model(model_id)
        if not self._storage.is_installed(model_id):
            raise ModelNotInstalled(model_id)
        if self._tester is None:
            return {"ok": False, "detail": "model testing not available"}
        try:
            result = self._tester(spec, self._storage.model_path(model_id))
            if inspect.isawaitable(result):
                result = await result
            return {"ok": True, **(result or {})}
        except Exception as exc:  # noqa: BLE001 - reported in-band as a failed test
            return {"ok": False, "error": str(exc)}

    # --- helpers ---------------------------------------------------------

    def _latest_job_for(self, model_id: str) -> Job | None:
        match = [j for j in self._jobs.values() if j.model_id == model_id]
        return match[-1] if match else None
