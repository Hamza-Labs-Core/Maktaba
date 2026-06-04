"""Model RPCs on the pipeline gRPC service.

These exercise the JSON-dict in / JSON-dict out contract the Go API
relies on: each ``PipelineService`` model method takes a payload dict and
returns a flat dict, and application errors (unknown model, not
installed, unknown job) come back through ``_dispatch`` as an in-band
``{"error": ...}`` rather than crashing the RPC.

Not marked ``unit`` — the methods are driven via ``asyncio.run``.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Coroutine
from pathlib import Path
from typing import Any

from maktaba_pipeline.grpc_server import PipelineService, _dispatch
from maktaba_pipeline.models.registry import ModelSpec
from maktaba_pipeline.models.service import ModelService
from maktaba_pipeline.models.storage import Storage


class _FakeDownloader:
    def download(self, spec: ModelSpec, dest_dir: Path, *, progress_cb: Any = None) -> Path:
        dest_dir = Path(dest_dir)
        dest_dir.mkdir(parents=True, exist_ok=True)
        (dest_dir / "model.bin").write_bytes(b"weights")
        if progress_cb is not None:
            progress_cb(spec.size_bytes, spec.size_bytes)
        return dest_dir


def _svc(tmp_path: Path) -> PipelineService:
    ms = ModelService(storage=Storage(root=tmp_path), downloader=_FakeDownloader())
    return PipelineService(model_service=ms)


def _run(coro: Coroutine[Any, Any, Any]) -> Any:
    return asyncio.run(coro)


def test_list_models_returns_catalog(tmp_path: Path) -> None:
    svc = _svc(tmp_path)
    resp = _run(svc.list_models({}))
    assert "models" in resp
    assert any(m["id"] == "all-minilm-l6-v2" for m in resp["models"])


def test_download_then_progress(tmp_path: Path) -> None:
    svc = _svc(tmp_path)

    async def _scenario() -> None:
        resp = await svc.download_model({"id": "all-minilm-l6-v2"})
        job_id = resp["job_id"]
        assert job_id
        # ModelService tracks the task; await it before polling.
        await svc.model_service.wait(job_id)
        prog = await svc.download_progress({"job_id": job_id})
        assert prog["status"] == "done"
        assert prog["progress"] == 100

    _run(_scenario())


def test_delete_model_reports_deleted(tmp_path: Path) -> None:
    svc = _svc(tmp_path)

    async def _scenario() -> None:
        resp = await svc.download_model({"id": "all-minilm-l6-v2"})
        await svc.model_service.wait(resp["job_id"])
        deleted = await svc.delete_model({"id": "all-minilm-l6-v2"})
        assert deleted["deleted"] is True

    _run(_scenario())


def test_activate_model(tmp_path: Path) -> None:
    svc = _svc(tmp_path)

    async def _scenario() -> None:
        resp = await svc.download_model({"id": "all-minilm-l6-v2"})
        await svc.model_service.wait(resp["job_id"])
        activated = await svc.activate_model({"id": "all-minilm-l6-v2"})
        assert activated["active"] is True
        assert activated["type"] == "embedding"

    _run(_scenario())


def test_unknown_model_surfaces_as_in_band_error(tmp_path: Path) -> None:
    svc = _svc(tmp_path)

    async def _call(payload: bytes) -> bytes:
        return await _dispatch(payload, svc.download_model)

    raw = _run(_call(json.dumps({"id": "no-such-model"}).encode("utf-8")))
    decoded = json.loads(raw.decode("utf-8"))
    assert "error" in decoded
    assert "no-such-model" in decoded["error"]


def test_missing_id_surfaces_as_in_band_error(tmp_path: Path) -> None:
    svc = _svc(tmp_path)

    async def _call(payload: bytes) -> bytes:
        return await _dispatch(payload, svc.download_model)

    raw = _run(_call(json.dumps({}).encode("utf-8")))
    decoded = json.loads(raw.decode("utf-8"))
    assert "error" in decoded
