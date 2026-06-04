"""Downloader behaviour: resume, verify, atomic move, disk-space check.

The downloader never touches the network in these tests — a fake
``fetcher`` is injected, so the unit netguard (Story 20.1) stays happy.
The fake lets us drive every branch: clean download, checksum mismatch,
resume-from-partial, range-not-honored restart, and the pre-flight
disk-space gate.
"""

from __future__ import annotations

import hashlib
from collections.abc import Iterable
from pathlib import Path

import pytest

from maktaba_pipeline.models import downloader as dl
from maktaba_pipeline.models.registry import ModelFile, ModelSpec

pytestmark = pytest.mark.unit


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _spec(files: tuple[ModelFile, ...]) -> ModelSpec:
    return ModelSpec(
        id="test-model",
        type="embedding",
        name="Test Model",
        repo_id="acme/test-model",
        revision="main",
        files=files,
        platform="any",
    )


class _FakeFetcher:
    """Records calls and serves canned bytes keyed by filename.

    ``content`` maps filename -> full file bytes. When ``honor_range`` is
    True the fetch starts at ``resume_from`` (server supports Range);
    otherwise it always returns the whole file from byte 0.
    """

    def __init__(self, content: dict[str, bytes], *, honor_range: bool = True) -> None:
        self.content = content
        self.honor_range = honor_range
        self.calls: list[tuple[str, int]] = []

    def __call__(self, url: str, *, resume_from: int = 0) -> dl.Fetch:
        filename = url.rsplit("/", 1)[-1]
        self.calls.append((filename, resume_from))
        data = self.content[filename]
        if self.honor_range and resume_from:
            return dl.Fetch(
                total_size=len(data),
                chunks=_chunked(data[resume_from:]),
                start_byte=resume_from,
            )
        return dl.Fetch(total_size=len(data), chunks=_chunked(data), start_byte=0)


def _chunked(data: bytes, size: int = 8) -> Iterable[bytes]:
    for i in range(0, len(data), size):
        yield data[i : i + size]


def _fixed_disk(free: int) -> dl.DiskUsage:
    class _Usage:
        def __init__(self) -> None:
            self.total = free * 10
            self.used = self.total - free
            self.free = free

    def _usage(_path: str) -> _Usage:
        return _Usage()

    return _usage


def test_clean_download_writes_files_and_removes_part(tmp_path: Path) -> None:
    body = b"hello world, this is a model weight blob" * 4
    spec = _spec((ModelFile("weights.bin", len(body), _sha256(body)),))
    fetcher = _FakeFetcher({"weights.bin": body})
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    d.download(spec, tmp_path)

    out = tmp_path / "weights.bin"
    assert out.read_bytes() == body
    assert not (tmp_path / "weights.bin.part").exists()


def test_checksum_mismatch_raises_and_leaves_no_final_file(tmp_path: Path) -> None:
    body = b"the real bytes"
    spec = _spec((ModelFile("weights.bin", len(body), _sha256(b"different bytes")),))
    fetcher = _FakeFetcher({"weights.bin": body})
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    with pytest.raises(dl.ChecksumMismatch):
        d.download(spec, tmp_path)

    assert not (tmp_path / "weights.bin").exists()
    assert not (tmp_path / "weights.bin.part").exists()


def test_resume_appends_from_existing_partial(tmp_path: Path) -> None:
    body = b"0123456789abcdefghijklmnopqrstuvwxyz"
    half = len(body) // 2
    part = tmp_path / "weights.bin.part"
    part.write_bytes(body[:half])  # an interrupted prior download

    spec = _spec((ModelFile("weights.bin", len(body), _sha256(body)),))
    fetcher = _FakeFetcher({"weights.bin": body}, honor_range=True)
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    d.download(spec, tmp_path)

    assert (tmp_path / "weights.bin").read_bytes() == body
    # It must have requested a resume from the partial offset.
    assert fetcher.calls == [("weights.bin", half)]


def test_resume_restarts_when_range_not_honored(tmp_path: Path) -> None:
    body = b"0123456789abcdefghijklmnopqrstuvwxyz"
    half = len(body) // 2
    (tmp_path / "weights.bin.part").write_bytes(body[:half])

    spec = _spec((ModelFile("weights.bin", len(body), _sha256(body)),))
    fetcher = _FakeFetcher({"weights.bin": body}, honor_range=False)
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    d.download(spec, tmp_path)

    # Server ignored Range and sent the whole file from 0 -> still correct.
    assert (tmp_path / "weights.bin").read_bytes() == body


def test_disk_space_check_raises_before_fetching(tmp_path: Path) -> None:
    body = b"x" * 1000
    spec = _spec((ModelFile("weights.bin", 5_000_000_000, _sha256(body)),))
    fetcher = _FakeFetcher({"weights.bin": body})
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(1_000))  # only 1 KB free

    with pytest.raises(dl.InsufficientDiskSpace):
        d.download(spec, tmp_path)

    assert fetcher.calls == []  # nothing fetched


def test_progress_callback_is_monotonic_and_reaches_total(tmp_path: Path) -> None:
    body_a = b"a" * 40
    body_b = b"b" * 24
    spec = _spec(
        (
            ModelFile("a.bin", len(body_a), _sha256(body_a)),
            ModelFile("b.bin", len(body_b), _sha256(body_b)),
        )
    )
    fetcher = _FakeFetcher({"a.bin": body_a, "b.bin": body_b})
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    seen: list[tuple[int, int]] = []
    d.download(spec, tmp_path, progress_cb=lambda done, total: seen.append((done, total)))

    total = len(body_a) + len(body_b)
    assert seen, "progress callback was never invoked"
    assert all(t == total for _, t in seen), "total should be the combined size"
    downloaded = [done for done, _ in seen]
    assert downloaded == sorted(downloaded), "progress must be monotonic"
    assert downloaded[-1] == total, "final progress must reach the total"


def test_multiple_files_all_written(tmp_path: Path) -> None:
    a = b"config bytes"
    b = b"weight bytes" * 10
    spec = _spec(
        (
            ModelFile("config.json", len(a), _sha256(a)),
            ModelFile("model.bin", len(b), _sha256(b)),
        )
    )
    fetcher = _FakeFetcher({"config.json": a, "model.bin": b})
    d = dl.Downloader(fetcher=fetcher, disk_usage=_fixed_disk(10**12))

    d.download(spec, tmp_path)

    assert (tmp_path / "config.json").read_bytes() == a
    assert (tmp_path / "model.bin").read_bytes() == b
