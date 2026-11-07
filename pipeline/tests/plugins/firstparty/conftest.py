"""Fixtures of the Phase 4 tests (Epic 54, stories 54.22-54.26): the toy first-party plugin
installed into the pipeline's view of the built-in plugins, and its table."""

from __future__ import annotations

import json
import sys
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from tests.engine.fixtures_db import db, pg_template, sqlite_template  # noqa: F401

HERE = Path(__file__).resolve().parent
TOY = HERE / "toy"
ROOT = HERE.parents[3]
sys.path.insert(0, str(ROOT / "tools" / "plugins"))


def bundle_of(*folders: Path) -> bytes:
    """The bundle tools/plugins/bundle.py writes, for these plugin folders."""
    import bundle

    plugins = [bundle.plugin(f) for f in folders]
    doc: dict[str, Any] = {"schema": 1, "plugins": plugins}
    doc["digest"] = __import__("hashlib").sha256(bundle.canonical(plugins).encode()).hexdigest()
    return (json.dumps(doc, sort_keys=True, indent=1, ensure_ascii=False) + "\n").encode()


def install(monkeypatch: pytest.MonkeyPatch, raw: bytes) -> None:
    from maktaba_pipeline.engine.stages import registry as reg
    from maktaba_pipeline.plugins import firstparty, native

    monkeypatch.setattr(firstparty, "_raw", lambda: raw)
    for f in (firstparty._load, firstparty._stage_index, firstparty.registry):
        f.cache_clear()
    reg._IMPLS.pop("fp_toy", None)
    native.forget_hello()


@pytest.fixture
def toy(monkeypatch: pytest.MonkeyPatch) -> Iterator[bytes]:
    """The toy plugin as a built-in one; its code importable as ``fp_toy``."""
    from maktaba_pipeline.engine.stages import registry as reg
    from maktaba_pipeline.plugins import firstparty, native

    monkeypatch.syspath_prepend(str(TOY / "src"))
    raw = bundle_of(TOY)
    install(monkeypatch, raw)
    from fp_toy import stage

    stage.PLANT.update(row=False, fingerprint=False, slow=0.0)
    yield raw
    stage.PLANT.update(row=False, fingerprint=False, slow=0.0)
    monkeypatch.undo()
    for f in (firstparty._load, firstparty._stage_index, firstparty.registry):
        f.cache_clear()
    reg._IMPLS.pop("fp_toy", None)
    native.forget_hello()


TOY_TABLE_PG = (
    "CREATE TABLE IF NOT EXISTS fp_toy_out (library_id uuid NOT NULL, video_id uuid NOT NULL,"
    " title text, n bigint, PRIMARY KEY (library_id, video_id))"
)
TOY_TABLE_SQLITE = (
    "CREATE TABLE IF NOT EXISTS fp_toy_out (library_id TEXT NOT NULL, video_id TEXT NOT NULL,"
    " title TEXT, n INTEGER, PRIMARY KEY (library_id, video_id))"
)


async def toy_table(database: Any) -> None:
    await database.execute(TOY_TABLE_PG if database.dialect == "postgres" else TOY_TABLE_SQLITE)
