"""``maktaba-pipeline doctor`` (stub audit #25): real checks, non-zero exit on a failure."""

from __future__ import annotations

import io
import os
import sys
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline import __main__ as cli
from maktaba_pipeline.cluster.schema import SCHEMA_REV
from maktaba_pipeline.doctor import check_binary, check_storage, run_doctor, storage_dirs
from maktaba_pipeline.runtime import Database


class FakePG:
    """Just enough of a Postgres Database for the doctor's queries."""

    dialect = "postgres"

    def __init__(self, applied: int | None, *, queue_ok: bool = True) -> None:
        self.applied = applied
        self.queue_ok = queue_ok
        self.closed = False

    async def fetchval(self, sql: str, *_a: Any) -> Any:
        if "goose_db_version" in sql:
            if self.applied is None:
                raise RuntimeError('relation "goose_db_version" does not exist')
            return self.applied
        if "to_regproc" in sql:
            return False
        return 1

    async def fetch(self, sql: str, *_a: Any) -> list[tuple[str, int]]:
        if not self.queue_ok:
            raise RuntimeError('relation "processing_jobs" does not exist')
        return [("queued", 3), ("running", 1), ("done", 9)]

    async def close(self) -> None:
        self.closed = True


def _connect_to(db: Any) -> Any:
    async def connect(_url: str) -> Any:
        return db

    return connect


@pytest.fixture
def tools(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """ffmpeg / ffprobe stand-ins that exist and are executable."""
    bindir = tmp_path / "bin"
    bindir.mkdir()
    for name in ("ffmpeg", "ffprobe"):
        exe = bindir / (name + (".bat" if sys.platform == "win32" else ""))
        exe.write_text("@echo off\n" if sys.platform == "win32" else "#!/bin/sh\n")
        exe.chmod(0o755)
        monkeypatch.setenv(f"MAKTABA_{name.upper()}", str(exe))
    return bindir


@pytest.mark.asyncio
async def test_all_checks_pass_on_a_migrated_postgres(tmp_path: Path, tools: Path) -> None:
    db = FakePG(SCHEMA_REV)
    store = tmp_path / "store"
    store.mkdir()
    out = io.StringIO()
    code = await run_doctor(
        "postgres://x", connect=_connect_to(db), dirs=[("chroma", str(store))], out=out
    )
    text = out.getvalue()
    assert code == 0, text
    for name in ("database", "schema", "queue", "ffmpeg", "ffprobe", "storage"):
        assert f"ok    {name}" in text
    assert "3 queued, 1 running" in text
    assert db.closed
    assert not list(store.iterdir()), "the storage probe left a file behind"


@pytest.mark.asyncio
async def test_schema_behind_fails(tmp_path: Path, tools: Path) -> None:
    out = io.StringIO()
    code = await run_doctor(
        "postgres://x",
        connect=_connect_to(FakePG(SCHEMA_REV - 1)),
        dirs=[("chroma", str(tmp_path))],
        out=out,
    )
    assert code == 1
    assert "FAIL  schema" in out.getvalue() and "update the home" in out.getvalue()


@pytest.mark.asyncio
async def test_unmigrated_database_and_missing_queue_fail(tmp_path: Path, tools: Path) -> None:
    out = io.StringIO()
    code = await run_doctor(
        "postgres://x",
        connect=_connect_to(FakePG(None, queue_ok=False)),
        dirs=[("chroma", str(tmp_path))],
        out=out,
        quiet=True,
    )
    text = out.getvalue()
    assert code == 1
    assert "FAIL  schema" in text and "FAIL  queue" in text
    assert "ok  " not in text, "quiet prints only failures"


@pytest.mark.asyncio
async def test_unreachable_database_fails(tmp_path: Path, tools: Path) -> None:
    async def refuse(_url: str) -> Any:
        raise ConnectionRefusedError("connection refused")

    out = io.StringIO()
    code = await run_doctor(
        "postgres://x", connect=refuse, dirs=[("chroma", str(tmp_path))], out=out
    )
    assert code == 1
    assert "FAIL  database  unreachable (ConnectionRefusedError" in out.getvalue()


@pytest.mark.asyncio
async def test_no_database_configured_fails(tmp_path: Path, tools: Path) -> None:
    out = io.StringIO()
    assert await run_doctor(None, dirs=[("chroma", str(tmp_path))], out=out) == 1
    assert "no database configured" in out.getvalue()


@pytest.mark.asyncio
async def test_real_sqlite_database(tmp_path: Path, tools: Path) -> None:
    path = tmp_path / "m.db"
    db = await Database.connect(f"sqlite:///{path}")
    await db.execute("CREATE TABLE processing_jobs (state TEXT NOT NULL)")
    await db.execute("INSERT INTO processing_jobs (state) VALUES ('queued')")
    await db.close()
    out = io.StringIO()
    code = await run_doctor(f"sqlite:///{path}", dirs=[("chroma", str(tmp_path))], out=out)
    assert code == 0, out.getvalue()
    assert "sqlite: not judged" in out.getvalue()
    assert "1 queued, 0 running" in out.getvalue()


def test_missing_binary_fails(tmp_path: Path) -> None:
    c = check_binary("ffmpeg", str(tmp_path / "no-ffmpeg-here"))
    assert not c.ok and "not found" in c.detail


def test_storage_checks(tmp_path: Path) -> None:
    assert check_storage("chroma", str(tmp_path)).ok
    # not created yet, but its parent is writable: fine, and nothing is created
    later = tmp_path / "a" / "b"
    assert check_storage("chroma", str(later)).ok
    assert not later.exists() and not (tmp_path / "a").exists()
    f = tmp_path / "file"
    f.write_text("x")
    assert not check_storage("chroma", str(f)).ok


@pytest.mark.skipif(
    sys.platform == "win32" or os.geteuid() == 0, reason="needs POSIX permissions as non-root"
)
def test_read_only_storage_fails(tmp_path: Path) -> None:
    ro = tmp_path / "ro"
    ro.mkdir()
    ro.chmod(0o500)
    try:
        c = check_storage("chroma", str(ro))
        assert not c.ok and "not writable" in c.detail
    finally:
        ro.chmod(0o700)


def test_storage_dirs_reads_env(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MAKTABA_CHROMA_DIR", str(tmp_path / "chroma"))
    dirs = dict(storage_dirs({"MAKTABA_THUMBNAIL_ROOT": str(tmp_path / "thumbs")}))
    assert dirs == {"chroma": str(tmp_path / "chroma"), "thumbnails": str(tmp_path / "thumbs")}


def test_cli_exits_non_zero_on_failure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setenv("MAKTABA_FFMPEG", str(tmp_path / "missing-ffmpeg"))
    monkeypatch.setenv("MAKTABA_CHROMA_DIR", str(tmp_path))
    path = tmp_path / "m.db"
    code = cli.main(["doctor", "--database-url", f"sqlite:///{path}"])
    assert code == 1
    out = capsys.readouterr().out
    assert "FAIL  ffmpeg" in out and "FAIL  queue" in out
