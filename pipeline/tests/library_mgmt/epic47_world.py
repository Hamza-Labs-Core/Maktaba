"""A library on disk (tmp_path) with tracked videos, for the Epic 47 ledger, trash and
swap tests. Works on both dialects."""

from __future__ import annotations

import os
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.sql import dumps, is_pg, to_ts, uid
from maktaba_pipeline.identity.hasher import hash_file
from maktaba_pipeline.library_mgmt.verify import VerifyResult


class CrashInjected(Exception):
    """A crash at one step of the swap (the test's ``crash`` hook raises it)."""


def crash_at(step: str) -> Any:
    def hook(at: str) -> None:
        if at == step:
            raise CrashInjected(step)

    return hook


def same(p: str) -> str:
    """The identity path map: the database's paths are this machine's."""
    return p


async def ok_verify(path: str) -> VerifyResult:
    return VerifyResult(True, [], {"checked": os.path.basename(path)})


async def bad_verify(path: str) -> VerifyResult:
    return VerifyResult(False, ["decode errors: invalid NAL unit"], {"decode_errors": "boom"})


@dataclass
class World:
    db: Any
    root: Path
    library_id: UUID

    async def video(self, rel: str, content: bytes) -> tuple[UUID, Path]:
        """A file under the root and its tracked row (the real content hash)."""
        p = self.root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(content)
        vid = uuid.uuid4()
        h = hash_file(p)
        await self.db.execute(
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes,"
            " mtime, state) VALUES ($1,$2,$3,$4,$5,$6,$7,'ready')",
            str(vid),
            str(self.library_id),
            h.content_hash,
            str(p),
            p.name,
            h.size_bytes,
            to_ts(self.db, datetime.fromtimestamp(p.stat().st_mtime, UTC)),
        )
        return vid, p

    async def row(self, vid: UUID) -> Any:
        return await self.db.fetchrow(
            "SELECT id, path, filename, content_hash, size_bytes, state FROM videos WHERE id = $1",
            uid(vid),
        )

    async def assert_tracked(self, vid: UUID) -> Path:
        """Exactly what the invariant says: the row exists, its path is a file on disk
        and its content hash is that file's."""
        r = await self.row(vid)
        assert r is not None
        p = Path(str(r["path"]))
        assert p.is_file(), f"{p} is not on disk"
        assert hash_file(p).content_hash == r["content_hash"]
        assert int(r["size_bytes"]) == p.stat().st_size
        return p

    def staged_files(self) -> list[Path]:
        return [p for p in self.root.rglob(".*.maktaba-new*") if ".maktaba" not in p.parts]

    def trash_files(self) -> list[Path]:
        t = self.root / ".maktaba" / "trash"
        return sorted(p for p in t.rglob("*") if p.is_file()) if t.exists() else []


async def make_world(db: Any, tmp_path: Path, *, settings: dict[str, Any] | None = None) -> World:
    root = tmp_path / "lib"
    root.mkdir(parents=True, exist_ok=True)
    lib = uuid.uuid4()
    roots = [str(root)]
    await db.execute(
        "INSERT INTO libraries (id, name, roots, settings) VALUES ($1, $2, $3, $4)",
        str(lib),
        f"lib-{lib.hex[:6]}",
        roots if is_pg(db) else dumps(roots),
        dumps(settings or {}),
    )
    return World(db=db, root=root, library_id=lib)


def new_file(tmp_path: Path, name: str, content: bytes) -> Path:
    """The converted file in the local work folder."""
    work = tmp_path / "work"
    work.mkdir(exist_ok=True)
    p = work / name
    p.write_bytes(content)
    return p
