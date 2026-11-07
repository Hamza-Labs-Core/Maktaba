"""The parity proof proves itself (Epic 54, story 54.26 T01-T05): a toy first-party stage run
in-process on one copy of a database and through its plugin on the other, with the real
plugin API, its statements and its role. Identical, it passes; a planted row, fingerprint
or slowdown in the plugin path fails, naming what differs."""

from __future__ import annotations

import os
import sys
import tomllib
import uuid
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import diff_files, files_tree

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import to_ts
from tests.plugins.firstparty.conftest import ROOT, TOY, TOY_TABLE_PG
from tests.plugins.firstparty.harness import parity

pytestmark = [pytest.mark.integration]
asyncio_test = pytest.mark.asyncio

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
NS = uuid.UUID("6f1d0c35-55a4-4cb4-9d0d-4a7c6a3c5b10")
LIB = uuid.uuid5(NS, "library")
VIDS = [uuid.uuid5(NS, f"video-{i}") for i in range(4)]


async def seed(db: Any) -> None:
    from datetime import UTC, datetime

    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
        str(LIB),
        "مكتبة التكافؤ",
        ["/lib"],
    )
    for i, v in enumerate(VIDS):
        await db.execute(
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, title)"  # noqa: E501
            " VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
            str(v),
            str(LIB),
            f"{i:064x}",
            f"/lib/{i}.mp4",
            f"{i}.mp4",
            1000 + i,
            to_ts(db, datetime(2024, 1, 1, tzinfo=UTC)),
            f"حلقة {i + 1}",
        )
    await enqueue_job(db, subject_kind="library", subject_id=LIB, stage="fp_toy")


def roles_sql() -> str:
    sys.path.insert(0, str(ROOT / "tools" / "plugins"))
    import roles

    return str(
        roles.role_sql(tomllib.loads((TOY / "manifest.toml").read_text()))
        .replace("-- +goose StatementBegin\n", "")
        .replace("-- +goose StatementEnd\n", "")
    )


def run(toy: bytes, monkeypatch: pytest.MonkeyPatch, template: str) -> Any:
    from maktaba_pipeline.plugins import firstparty

    return parity(
        PG_URL,
        template,
        bundle=toy,
        stages=["fp_toy"],
        extra=[firstparty.stage_def("fp_toy")],
        setup_sql=[TOY_TABLE_PG],
        roles_sql=roles_sql(),
        seed=seed,
        monkeypatch=monkeypatch,
    )


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


@asyncio_test
async def test_an_identical_stage_passes(
    toy: bytes, monkeypatch: pytest.MonkeyPatch, template: str
) -> None:
    async with run(toy, monkeypatch, template) as p:
        await p.run()
        assert p.a.ran == p.b.ran == ["fp_toy"]
        problems = await p.compare(
            [("fp_toy_out", ("library_id", "video_id"))], stages=["fp_toy"], perf=False
        )
        assert problems == []
        assert await p.fingerprints("fp_toy", [Subject("library", LIB)]) == []
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.toy'"
        )
        assert n >= 1  # the job ran through a first-party token


@asyncio_test
async def test_a_row_that_differs_fails_naming_table_key_and_column(
    toy: bytes, monkeypatch: pytest.MonkeyPatch, template: str
) -> None:
    from fp_toy import stage

    stage.PLANT["row"] = True
    async with run(toy, monkeypatch, template) as p:
        await p.run()
        problems = await p.compare([("fp_toy_out", ("library_id", "video_id"))], perf=False)
    rows = [x for x in problems if x.startswith("fp_toy_out")]
    assert len(rows) == 4 and all(" n: in-process 100" in x for x in rows), problems
    assert str(VIDS[0]) in rows[0] or any(str(v) in rows[0] for v in VIDS)


@asyncio_test
async def test_a_fingerprint_that_differs_fails_naming_the_subject(
    toy: bytes, monkeypatch: pytest.MonkeyPatch, template: str
) -> None:
    from fp_toy import stage

    async with run(toy, monkeypatch, template) as p:
        stage.PLANT["fingerprint"] = True
        problems = await p.fingerprints("fp_toy", [Subject("library", LIB)])
    assert len(problems) == 1 and str(LIB) in problems[0], problems


@asyncio_test
async def test_a_slower_plugin_fails_n16_with_both_numbers(
    toy: bytes, monkeypatch: pytest.MonkeyPatch, template: str
) -> None:
    from fp_toy import stage

    stage.PLANT["slow"] = 0.3
    async with run(toy, monkeypatch, template) as p:
        await p.run()
        problems = await p.compare([], stages=["fp_toy"])
    assert any("fp_toy p50: plugin" in x and "x in-process" in x for x in problems), problems


def test_a_file_that_differs_fails_naming_the_file(tmp_path: Path) -> None:
    (tmp_path / "a" / "x").mkdir(parents=True)
    (tmp_path / "b" / "x").mkdir(parents=True)
    (tmp_path / "a" / "x" / "1.jpg").write_bytes(b"one")
    (tmp_path / "b" / "x" / "1.jpg").write_bytes(b"two")
    (tmp_path / "a" / "x" / "2.jpg").write_bytes(b"same")
    (tmp_path / "b" / "x" / "2.jpg").write_bytes(b"same")
    out = diff_files("data.thumbnails", files_tree(tmp_path / "a"), files_tree(tmp_path / "b"))
    assert len(out) == 1 and out[0].startswith("file data.thumbnails/x/1.jpg: in-process ")
