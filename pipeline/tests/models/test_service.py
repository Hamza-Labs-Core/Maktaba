"""Full service lifecycle: list -> download -> activate -> test -> delete.

The service orchestrates the real :mod:`registry` against an injected
fake downloader and a ``tmp_path``-backed :class:`Storage`, so the whole
lifecycle runs offline and deterministically. Following the repo
convention, async methods are driven via ``asyncio.run`` from sync test
bodies rather than pytest-asyncio marks.
"""

from __future__ import annotations

import asyncio
from collections.abc import Coroutine
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.models import service as svc
from maktaba_pipeline.models.downloader import ChecksumMismatch
from maktaba_pipeline.models.registry import ModelSpec
from maktaba_pipeline.models.storage import Storage

# NOTE: deliberately *not* marked `unit`. These tests drive the service
# through ``asyncio.run``, and the unit-tier netguard (Story 20.1) forbids
# the sockets asyncio's event-loop self-pipe needs. The repo's other
# asyncio.run-based tests (e.g. stt) follow the same convention. The suite
# stays offline regardless — every dependency is an injected fake.


class _FakeDownloader:
    """Writes a marker file and reports progress to 100%.

    Set ``fail=True`` to simulate a mid-download verification failure.
    """

    def __init__(self, *, fail: bool = False) -> None:
        self.fail = fail
        self.downloaded: list[str] = []

    def download(
        self,
        spec: ModelSpec,
        dest_dir: Path,
        *,
        progress_cb: Any = None,
    ) -> Path:
        dest_dir = Path(dest_dir)
        dest_dir.mkdir(parents=True, exist_ok=True)
        total = spec.size_bytes
        if progress_cb is not None:
            progress_cb(total // 2, total)
        if self.fail:
            raise ChecksumMismatch("model.bin", "aa", "bb")
        (dest_dir / "model.bin").write_bytes(b"fake weights")
        if progress_cb is not None:
            progress_cb(total, total)
        self.downloaded.append(spec.id)
        return dest_dir


def _service(tmp_path: Path, **kw: Any) -> svc.ModelService:
    return svc.ModelService(
        storage=Storage(root=tmp_path),
        downloader=kw.pop("downloader", _FakeDownloader()),
        **kw,
    )


def _run(coro: Coroutine[Any, Any, Any]) -> Any:
    return asyncio.run(coro)


def test_list_models_reflects_available_state(tmp_path: Path) -> None:
    s = _service(tmp_path)
    models = _run(s.list_models())
    assert models, "catalog should not be empty"
    by_id = {m["id"]: m for m in models}
    minilm = by_id["all-minilm-l6-v2"]
    assert minilm["installed"] is False
    assert minilm["active"] is False
    assert minilm["status"] == "available"
    assert "size" in minilm and "type" in minilm


def test_download_then_installed_and_active_flow(tmp_path: Path) -> None:
    s = _service(tmp_path)

    async def _scenario() -> None:
        job_id = await s.download_model("all-minilm-l6-v2")
        assert isinstance(job_id, str) and job_id
        await s.wait(job_id)  # let the background task finish

        prog = await s.download_progress(job_id)
        assert prog["status"] == "done"
        assert prog["progress"] == 100

        models = {m["id"]: m for m in await s.list_models()}
        assert models["all-minilm-l6-v2"]["installed"] is True
        assert models["all-minilm-l6-v2"]["status"] == "downloaded"

        # Activate it -> status flips to active.
        await s.activate_model("all-minilm-l6-v2")
        models = {m["id"]: m for m in await s.list_models()}
        assert models["all-minilm-l6-v2"]["active"] is True
        assert models["all-minilm-l6-v2"]["status"] == "active"

    _run(_scenario())


def test_download_unknown_model_raises(tmp_path: Path) -> None:
    s = _service(tmp_path)
    with pytest.raises(svc.registry.UnknownModel):
        _run(s.download_model("no-such-model"))


def test_download_error_is_recorded_on_job(tmp_path: Path) -> None:
    s = _service(tmp_path, downloader=_FakeDownloader(fail=True))

    async def _scenario() -> None:
        job_id = await s.download_model("all-minilm-l6-v2")
        await s.wait(job_id)
        prog = await s.download_progress(job_id)
        assert prog["status"] == "error"
        assert prog["error"]
        # A failed download must not leave the model marked installed.
        models = {m["id"]: m for m in await s.list_models()}
        assert models["all-minilm-l6-v2"]["installed"] is False

    _run(_scenario())


def test_download_in_flight_is_idempotent(tmp_path: Path) -> None:
    s = _service(tmp_path)

    async def _scenario() -> None:
        j1 = await s.download_model("multilingual-e5-large")
        j2 = await s.download_model("multilingual-e5-large")
        assert j1 == j2  # re-requesting returns the same in-flight job
        await s.wait(j1)

    _run(_scenario())


def test_activate_requires_installed(tmp_path: Path) -> None:
    s = _service(tmp_path)
    with pytest.raises(svc.ModelNotInstalled):
        _run(s.activate_model("all-minilm-l6-v2"))


def test_activate_infers_type_from_catalog(tmp_path: Path) -> None:
    s = _service(tmp_path)

    async def _scenario() -> None:
        job_id = await s.download_model("pyannote-diarization-3.1")
        await s.wait(job_id)
        result = await s.activate_model("pyannote-diarization-3.1")
        assert result["type"] == "diarization"
        assert result["active"] is True

    _run(_scenario())


def test_delete_model_removes_install(tmp_path: Path) -> None:
    s = _service(tmp_path)

    async def _scenario() -> None:
        job_id = await s.download_model("all-minilm-l6-v2")
        await s.wait(job_id)
        assert await s.delete_model("all-minilm-l6-v2") is True
        models = {m["id"]: m for m in await s.list_models()}
        assert models["all-minilm-l6-v2"]["installed"] is False

    _run(_scenario())


def test_delete_unknown_model_raises(tmp_path: Path) -> None:
    s = _service(tmp_path)
    with pytest.raises(svc.registry.UnknownModel):
        _run(s.delete_model("no-such-model"))


def test_test_model_requires_installed(tmp_path: Path) -> None:
    s = _service(tmp_path)
    with pytest.raises(svc.ModelNotInstalled):
        _run(s.test_model("all-minilm-l6-v2"))


def test_test_model_runs_injected_tester(tmp_path: Path) -> None:
    def tester(spec, path):  # type: ignore[no-untyped-def]
        return {"latency_ms": 123, "detail": f"ran {spec.id}"}

    s = _service(tmp_path, tester=tester)

    async def _scenario() -> None:
        job_id = await s.download_model("all-minilm-l6-v2")
        await s.wait(job_id)
        result = await s.test_model("all-minilm-l6-v2")
        assert result["ok"] is True
        assert result["latency_ms"] == 123
        assert "ran all-minilm-l6-v2" in result["detail"]

    _run(_scenario())


def test_unknown_job_progress_raises(tmp_path: Path) -> None:
    s = _service(tmp_path)
    with pytest.raises(svc.UnknownJob):
        _run(s.download_progress("nope"))
