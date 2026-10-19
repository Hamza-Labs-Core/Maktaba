"""The second listen's model handed from the home to the workers through the share
(stt/model_share.py and its use in stt/cohere.py). No model is downloaded: a "snapshot" is
a folder of small files, and loading is faked."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import threading
import time
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.stt import cohere
from maktaba_pipeline.stt import model_share as ms

MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"
REV = "c3e911b42149bf7a1e53d5cef9878aee87515a23"


def hf_snapshot(tmp: Path, rev: str = REV) -> Path:
    """A Hugging Face cache snapshot: its files are links into ``blobs``."""
    blobs = tmp / "hf" / "blobs"
    snap = tmp / "hf" / "snapshots" / rev
    blobs.mkdir(parents=True)
    (snap / "sub").mkdir(parents=True)
    for name, body in {
        "config.json": b'{"model_type": "cohere_asr"}',
        "model.safetensors": os.urandom(4096),
        "sub/tokenizer.json": '{"ا": 1}'.encode(),
    }.items():
        blob = blobs / hashlib.sha256(body).hexdigest()
        blob.write_bytes(body)
        (snap / name).symlink_to(blob)
    return snap


def published(tmp: Path, rev: str = REV, at: str = "2026-10-19T10:00:00Z") -> Path:
    share = tmp / "share" / "maktaba" / "models"
    share.mkdir(parents=True, exist_ok=True)
    out = ms.publish(MODEL, hf_snapshot(tmp / rev, rev), share, now=lambda: at)
    assert out is not None
    return share


# --- publish ---------------------------------------------------------------------------


def test_publish_writes_the_files_and_a_manifest(tmp_path: Path) -> None:
    share = published(tmp_path)
    folder = share / "CohereLabs__cohere-transcribe-arabic-07-2026" / REV
    manifest = json.loads((folder / "manifest.json").read_text())
    assert manifest["model"] == MODEL and manifest["revision"] == REV
    files = {f["path"]: f for f in manifest["files"]}
    assert set(files) == {"config.json", "model.safetensors", "sub/tokenizer.json"}
    for rel, f in files.items():
        body = (folder / rel).read_bytes()
        assert not (folder / rel).is_symlink()  # the blob's bytes, not a link
        assert f["size"] == len(body) and f["sha256"] == hashlib.sha256(body).hexdigest()
    # nothing half-written is left beside it
    assert [p.name for p in folder.parent.iterdir()] == [REV]


def test_publish_is_once_per_revision(tmp_path: Path) -> None:
    share = published(tmp_path)
    again = ms.publish(MODEL, hf_snapshot(tmp_path / "again"), share)
    assert again is None


def test_a_failed_publish_leaves_nothing(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    share = tmp_path / "share"
    share.mkdir()
    snap = hf_snapshot(tmp_path)
    real = ms._copy_hashing
    calls = 0

    def flaky(src: Any, dest: Path) -> tuple[int, str]:
        nonlocal calls
        calls += 1
        if calls == 2:
            raise OSError("share went away")
        return real(src, dest)

    monkeypatch.setattr(ms, "_copy_hashing", flaky)
    with pytest.raises(OSError):
        ms.publish(MODEL, snap, share)
    assert list((share / ms.model_folder(MODEL)).iterdir()) == []
    assert ms.share_snapshots(share, MODEL) == []


def test_publish_leaves_it_to_a_process_already_copying(tmp_path: Path) -> None:
    share = tmp_path / "share"
    parent = share / ms.model_folder(MODEL)
    (parent / f".{REV}.incoming-99-abcdef01").mkdir(parents=True)
    assert ms.publish(MODEL, hf_snapshot(tmp_path), share) is None
    # a incoming folder left long ago by a crash does not hold it up
    old = time.time() - 2 * ms._BUSY_SEC
    os.utime(parent / f".{REV}.incoming-99-abcdef01", (old, old))
    assert ms.publish(MODEL, hf_snapshot(tmp_path / "2"), share) == parent / REV


def test_publish_from_cache_logs_rather_than_raises(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(ms, "hf_snapshot", lambda _m: tmp_path / "missing")
    assert ms.publish_from_cache(MODEL, tmp_path / "share") is None
    assert ms.publish_from_cache(MODEL, None) is None


# --- reading the share -----------------------------------------------------------------


def test_the_newest_complete_snapshot_wins(tmp_path: Path) -> None:
    share = published(tmp_path, "a" * 40, "2026-10-01T00:00:00Z")
    published(tmp_path, "b" * 40, "2026-10-19T00:00:00Z")
    assert [s.revision for s in ms.share_snapshots(share, MODEL)] == ["b" * 40, "a" * 40]


def test_a_partial_copy_is_ignored(tmp_path: Path) -> None:
    share = published(tmp_path, "a" * 40, "2026-10-01T00:00:00Z")
    parent = share / ms.model_folder(MODEL)
    # still being written: a hidden folder, even with a manifest in it
    newest = parent / ("b" * 40)
    (parent / f".{'b' * 40}.incoming-1-abc").mkdir()
    # renamed in place by hand but without its manifest
    newest.mkdir()
    (newest / "config.json").write_text("{}")
    # a manifest whose file is short (a copy cut off)
    cut = published(tmp_path / "cut", "c" * 40, "2026-10-30T00:00:00Z")
    cut_folder = cut / ms.model_folder(MODEL) / ("c" * 40)
    os.rename(cut_folder, parent / ("c" * 40))
    with open(parent / ("c" * 40) / "model.safetensors", "r+b") as fh:
        fh.truncate(10)
    assert [s.revision for s in ms.share_snapshots(share, MODEL)] == ["a" * 40]


def test_a_manifest_cannot_point_outside_its_folder(tmp_path: Path) -> None:
    share = published(tmp_path)
    folder = share / ms.model_folder(MODEL) / REV
    manifest = json.loads((folder / "manifest.json").read_text())
    manifest["files"][0]["path"] = "../../../etc/passwd"
    (folder / "manifest.json").write_text(json.dumps(manifest))
    assert ms.share_snapshots(share, MODEL) == []


def test_a_manifest_of_another_model_is_refused(tmp_path: Path) -> None:
    share = published(tmp_path)
    folder = share / ms.model_folder(MODEL) / REV
    manifest = json.loads((folder / "manifest.json").read_text())
    manifest["model"] = "someone/else"
    (folder / "manifest.json").write_text(json.dumps(manifest))
    assert ms.share_snapshots(share, MODEL) == []


# --- fetching to the local cache -------------------------------------------------------


def test_fetch_copies_once_and_checks_the_manifest(tmp_path: Path) -> None:
    share = published(tmp_path)
    local = tmp_path / "local"
    path = ms.from_share(MODEL, share, local)
    assert path == local / ms.model_folder(MODEL) / REV
    assert (path / "sub" / "tokenizer.json").read_text() == '{"ا": 1}'
    stamp = (path / "model.safetensors").stat().st_mtime_ns
    # the second time it is there already: not copied again
    assert ms.from_share(MODEL, share, local) == path
    assert (path / "model.safetensors").stat().st_mtime_ns == stamp


def test_a_newer_version_replaces_the_local_copy(tmp_path: Path) -> None:
    share = published(tmp_path, "a" * 40, "2026-10-01T00:00:00Z")
    local = tmp_path / "local"
    first = ms.from_share(MODEL, share, local)
    published(tmp_path, "b" * 40, "2026-10-19T00:00:00Z")
    second = ms.from_share(MODEL, share, local)
    assert second.name == "b" * 40 and not first.exists()


def test_a_copy_that_lost_files_is_replaced(tmp_path: Path) -> None:
    share = published(tmp_path)
    local = tmp_path / "local"
    path = ms.from_share(MODEL, share, local)
    (path / "model.safetensors").unlink()
    assert ms.from_share(MODEL, share, local) == path
    assert (path / "model.safetensors").is_file()


def test_a_file_that_does_not_match_its_hash_is_not_used(tmp_path: Path) -> None:
    share = published(tmp_path)
    blob = share / ms.model_folder(MODEL) / REV / "model.safetensors"
    size = blob.stat().st_size
    blob.write_bytes(b"\0" * size)  # same size, other bytes: only the hash catches it
    local = tmp_path / "local"
    with pytest.raises(ms.ShareModelError, match="does not match"):
        ms.from_share(MODEL, share, local)
    # nothing half-copied stays behind to be taken for a copy later
    assert list((local / ms.model_folder(MODEL)).iterdir()) == []


def test_the_local_copy_serves_when_the_share_is_away(tmp_path: Path) -> None:
    share = published(tmp_path)
    local = tmp_path / "local"
    path = ms.from_share(MODEL, share, local)
    assert ms.from_share(MODEL, None, local) == path
    assert ms.from_share(MODEL, tmp_path / "unmounted", local) == path


def test_no_share_and_no_copy_says_so(tmp_path: Path) -> None:
    with pytest.raises(ms.ShareModelError, match="no share here"):
        ms.from_share(MODEL, None, tmp_path / "local")


def test_the_models_folder_sits_beside_the_cluster_folder(tmp_path: Path) -> None:
    cluster = tmp_path / "maktaba" / "cluster"
    assert ms.models_dir(cluster, {}) == tmp_path / "maktaba" / "models"
    assert ms.models_dir(None, {}) is None
    assert ms.models_dir(cluster, {ms.ENV_MODELS_DIR: str(tmp_path / "m")}) == tmp_path / "m"


# --- the recogniser: Hugging Face first, then the share --------------------------------


@pytest.fixture
def fake_mlx(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """mlx-audio "installed"; a load from Hugging Face is refused unless ``hf_ok``."""
    state: dict[str, Any] = {"hf_ok": False, "loaded": []}
    real_find = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: object() if name == "mlx_audio" else real_find(name, *a),
    )
    monkeypatch.setattr(cohere, "_REC", {})
    monkeypatch.setattr(cohere, "_PUBLISHED", set())

    def load(self: cohere.CohereRecogniser) -> Any:
        if self._path is None and not state["hf_ok"]:
            raise RuntimeError("401 gated repo")
        state["loaded"].append(self._path or self.model_id)
        return object()

    monkeypatch.setattr(cohere.CohereRecogniser, "_load", load)
    return state


def test_a_worker_without_a_login_loads_the_share_copy(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, fake_mlx: dict[str, Any]
) -> None:
    share = published(tmp_path)
    monkeypatch.setattr(ms, "local_root", lambda: tmp_path / "local")
    rec = cohere.recogniser(MODEL, share)
    assert rec.source == "share"
    assert fake_mlx["loaded"] == [tmp_path / "local" / ms.model_folder(MODEL) / REV]


def test_no_login_and_no_share_copy_raises_both_reasons(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, fake_mlx: dict[str, Any]
) -> None:
    monkeypatch.setattr(ms, "local_root", lambda: tmp_path / "local")
    with pytest.raises(RuntimeError, match=r"401 gated repo .*share: no complete copy"):
        cohere.recogniser(MODEL, tmp_path / "empty")


def test_the_home_loads_from_hf_and_publishes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, fake_mlx: dict[str, Any]
) -> None:
    fake_mlx["hf_ok"] = True
    done = threading.Event()
    calls: list[tuple[str, Path | None]] = []

    def publish(model_id: str, share: Path | None) -> None:
        calls.append((model_id, share))
        done.set()

    monkeypatch.setattr(ms, "publish_from_cache", publish)
    rec = cohere.recogniser(MODEL, tmp_path / "share")
    assert rec.source == "hf" and fake_mlx["loaded"] == [MODEL]
    assert done.wait(5) and calls == [(MODEL, tmp_path / "share")]


def test_the_stage_records_the_source(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, fake_mlx: dict[str, Any]
) -> None:
    from maktaba_pipeline.engine.stages import transcribe as stage
    from maktaba_pipeline.stt import refine as rf

    class Stats:
        def as_dict(self) -> dict[str, Any]:
            return {"model": MODEL, "stretches": 1}

    share = published(tmp_path)
    monkeypatch.setattr(ms, "local_root", lambda: tmp_path / "local")
    monkeypatch.setattr(cohere, "dictionary", lambda: None)
    monkeypatch.setattr(rf, "refine", lambda *_a, **_k: Stats())
    out = stage._refine(Any, None, MODEL, share)  # type: ignore[arg-type]
    assert out == {"model": MODEL, "stretches": 1, "source": "share"}
