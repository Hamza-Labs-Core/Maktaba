"""Backup / restore planner (Epic 24 plan-24-05).

The pipeline's data lives in three places:

* Postgres (canonical) — backed up via ``pg_dump`` / WAL archiving.
* Media files on disk — Maktaba treats these as immutable inputs; the
  user's existing backup story owns them.
* Derived artifacts (HLS segments, generated subtitles, embeddings) —
  recomputable on restore, so the backup planner enumerates *which*
  artifacts need to be rebuilt and writes a manifest.

This module owns the manifest format. The actual rsync / pg_dump
invocation is owned by ``tools/backup/`` shell scripts.
"""

from __future__ import annotations

import dataclasses
import datetime as dt
import json
import pathlib
from typing import Any


@dataclasses.dataclass
class BackupManifest:
    """Manifest written alongside every ``pg_dump`` snapshot."""

    snapshot_id: str
    created_at: dt.datetime
    schema_rev: int
    video_count: int
    job_count: int
    notes: str = ""

    def to_json(self) -> str:
        return json.dumps(
            {
                "snapshot_id": self.snapshot_id,
                "created_at": self.created_at.isoformat(),
                "schema_rev": self.schema_rev,
                "video_count": self.video_count,
                "job_count": self.job_count,
                "notes": self.notes,
            },
            sort_keys=True,
        )

    @classmethod
    def from_json(cls, text: str) -> BackupManifest:
        data: dict[str, Any] = json.loads(text)
        return cls(
            snapshot_id=str(data["snapshot_id"]),
            created_at=dt.datetime.fromisoformat(str(data["created_at"])),
            schema_rev=int(data["schema_rev"]),
            video_count=int(data["video_count"]),
            job_count=int(data["job_count"]),
            notes=str(data.get("notes", "")),
        )


class BackupPlanner:
    """Plans + records snapshots. Production is driven by a cron job
    that calls :meth:`record` after ``pg_dump`` succeeds."""

    def __init__(self, manifest_dir: pathlib.Path | str) -> None:
        self.manifest_dir = pathlib.Path(manifest_dir)

    def record(self, manifest: BackupManifest) -> pathlib.Path:
        self.manifest_dir.mkdir(parents=True, exist_ok=True)
        path = self.manifest_dir / f"{manifest.snapshot_id}.json"
        path.write_text(manifest.to_json())
        return path

    def list_snapshots(self) -> list[BackupManifest]:
        if not self.manifest_dir.is_dir():
            return []
        out: list[BackupManifest] = []
        for p in sorted(self.manifest_dir.glob("*.json")):
            try:
                out.append(BackupManifest.from_json(p.read_text()))
            except (json.JSONDecodeError, KeyError, ValueError):
                # Stray / corrupt manifest. Skip but don't die — disaster
                # recovery would have us reading even partial dirs.
                continue
        return out

    def latest(self) -> BackupManifest | None:
        snaps = self.list_snapshots()
        if not snaps:
            return None
        return max(snaps, key=lambda m: m.created_at)
