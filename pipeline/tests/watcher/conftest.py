"""Shared fixtures for the watcher test suite.

The fakes here mirror the ones in ``tests/scanner/test_scanner.py`` but
also implement the watcher-only protocol methods (``find_video_by_hash``,
``update_video_path``, ``rediscover``, ``soft_delete_by_path``). Keeping
them in one place lets the unit and integration test modules share an
identical store shape — the only thing that varies between tests is how
events reach the dispatcher.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any
from uuid import UUID, uuid4

from maktaba_pipeline.scanner import (
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
)


@dataclass
class StoredVideo:
    """One persisted ``videos`` row in the fake watcher store."""

    id: UUID
    library_id: UUID
    content_hash: str
    path: str
    filename: str
    size_bytes: int
    mtime: datetime
    last_seen_at: datetime
    state: str = "discovered"
    derived_kept: bool = True


@dataclass
class StoredJob:
    id: int
    video_id: UUID
    stage: str
    state: str = "pending"


@dataclass
class FakeWatcherStore:
    """In-memory :class:`WatcherStore` that mirrors the relevant Postgres semantics.

    Models per-library ``UNIQUE (library_id, content_hash)`` and tracks
    derived-data persistence on soft delete via ``StoredVideo.derived_kept``.
    """

    libraries: dict[UUID, LibraryRecord] = field(default_factory=dict)
    videos: dict[UUID, StoredVideo] = field(default_factory=dict)
    jobs: list[StoredJob] = field(default_factory=list)
    dialect: str = "postgres"
    _next_job: int = 1

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        return self.libraries.get(library_id)

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        for v in self.videos.values():
            if v.library_id == library_id and v.path == path:
                return ExistingVideo(
                    id=v.id,
                    size_bytes=v.size_bytes,
                    mtime=v.mtime,
                    content_hash=v.content_hash,
                )
        return None

    async def find_video_by_hash(
        self,
        library_id: UUID,
        content_hash: str,
    ) -> ExistingVideo | None:
        for v in self.videos.values():
            if v.library_id == library_id and v.content_hash == content_hash:
                return ExistingVideo(
                    id=v.id,
                    size_bytes=v.size_bytes,
                    mtime=v.mtime,
                    content_hash=v.content_hash,
                )
        return None

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        for v in self.videos.values():
            if v.library_id == params.library_id and v.content_hash == params.content_hash:
                return SaveCandidateResult(video_id=v.id, inserted=False, job_id=None)

        new_id = uuid4()
        self.videos[new_id] = StoredVideo(
            id=new_id,
            library_id=params.library_id,
            content_hash=params.content_hash,
            path=params.path,
            filename=params.filename,
            size_bytes=params.size_bytes,
            mtime=params.mtime,
            last_seen_at=params.last_seen_at,
        )
        job_id: int | None = None
        if params.enqueue_probe:
            job_id = self._next_job
            self._next_job += 1
            self.jobs.append(StoredJob(id=job_id, video_id=new_id, stage="probe"))
        return SaveCandidateResult(video_id=new_id, inserted=True, job_id=job_id)

    async def update_video_path(self, video_id: UUID, new_path: str) -> None:
        v = self.videos[video_id]
        v.path = new_path

    async def rediscover(self, video_id: UUID, new_path: str) -> None:
        v = self.videos[video_id]
        v.state = "discovered"
        v.path = new_path

    async def soft_delete_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> UUID | None:
        for v in self.videos.values():
            if v.library_id == library_id and v.path == path:
                if v.state == "missing":
                    return v.id
                v.state = "missing"
                # derived_kept stays True — the soft delete preserves
                # the row but does not touch downstream artefacts.
                return v.id
        return None


@dataclass
class LoggerSink:
    """Recording structlog double, identical in shape to scanner tests."""

    events: list[tuple[str, str, dict[str, Any]]] = field(default_factory=list)

    def info(self, event: str, **kwargs: Any) -> None:
        self.events.append(("info", event, kwargs))

    def warning(self, event: str, **kwargs: Any) -> None:
        self.events.append(("warning", event, kwargs))

    def debug(self, event: str, **kwargs: Any) -> None:
        self.events.append(("debug", event, kwargs))

    def error(self, event: str, **kwargs: Any) -> None:
        self.events.append(("error", event, kwargs))

    def names(self, level: str) -> list[str]:
        return [name for lvl, name, _ in self.events if lvl == level]


def make_lib(
    *roots: str,
    disabled: bool = False,
    follow_symlinks: bool = False,
    name: str = "test-lib",
) -> LibraryRecord:
    return LibraryRecord(
        id=uuid4(),
        name=name,
        roots=tuple(roots),
        disabled=disabled,
        follow_symlinks=follow_symlinks,
    )
