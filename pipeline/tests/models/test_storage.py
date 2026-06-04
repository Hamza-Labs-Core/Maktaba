"""Model file management: directory resolution, list/size/delete, active.

All filesystem operations run against a ``tmp_path`` root so nothing
touches the real ``~/.cache/maktaba/models`` and the suite stays
hermetic.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.models import storage as st

pytestmark = pytest.mark.unit


def _install(root: Path, model_id: str, files: dict[str, bytes]) -> st.Storage:
    s = st.Storage(root=root)
    d = s.model_path(model_id)
    d.mkdir(parents=True, exist_ok=True)
    for name, data in files.items():
        (d / name).write_bytes(data)
    s.mark_installed(model_id)
    return s


def test_default_root_from_env(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.setenv("MAKTABA_MODELS_DIR", str(tmp_path / "custom"))
    s = st.Storage()
    assert s.models_dir() == tmp_path / "custom"


def test_default_root_falls_back_to_cache(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_MODELS_DIR", raising=False)
    s = st.Storage()
    assert s.models_dir() == Path.home() / ".cache" / "maktaba" / "models"


def test_model_path_is_under_root(tmp_path: Path) -> None:
    s = st.Storage(root=tmp_path)
    assert s.model_path("foo") == tmp_path / "foo"


def test_not_installed_when_absent(tmp_path: Path) -> None:
    s = st.Storage(root=tmp_path)
    assert s.is_installed("foo") is False


def test_installed_after_mark(tmp_path: Path) -> None:
    s = _install(tmp_path, "foo", {"a.bin": b"hello"})
    assert s.is_installed("foo") is True


def test_files_present_but_unmarked_is_not_installed(tmp_path: Path) -> None:
    # A half-finished download (files exist, no completion marker) must
    # not count as installed.
    s = st.Storage(root=tmp_path)
    d = s.model_path("foo")
    d.mkdir(parents=True)
    (d / "a.bin").write_bytes(b"partial")
    assert s.is_installed("foo") is False


def test_installed_size_sums_files(tmp_path: Path) -> None:
    s = _install(tmp_path, "foo", {"a.bin": b"x" * 100, "b.bin": b"y" * 50})
    # The completion marker is tiny but counted; assert at least the data.
    assert s.installed_size("foo") >= 150


def test_list_installed_returns_marked_models(tmp_path: Path) -> None:
    s = _install(tmp_path, "foo", {"a.bin": b"x" * 10})
    _install(tmp_path, "bar", {"b.bin": b"y" * 20})
    # An unmarked dir should be ignored.
    (s.model_path("ghost")).mkdir(parents=True)
    (s.model_path("ghost") / "junk").write_bytes(b"z")

    ids = {m.id for m in s.list_installed()}
    assert ids == {"foo", "bar"}
    for m in s.list_installed():
        assert m.size_bytes > 0
        assert isinstance(m.size_human, str)


def test_delete_removes_files_and_frees_space(tmp_path: Path) -> None:
    s = _install(tmp_path, "foo", {"a.bin": b"x" * 10})
    assert s.is_installed("foo") is True
    assert s.delete("foo") is True
    assert s.is_installed("foo") is False
    assert not s.model_path("foo").exists()


def test_delete_absent_returns_false(tmp_path: Path) -> None:
    s = st.Storage(root=tmp_path)
    assert s.delete("nope") is False


def test_active_model_roundtrip(tmp_path: Path) -> None:
    s = st.Storage(root=tmp_path)
    assert s.active_for("stt") is None

    s.set_active("stt", "mlx-whisper-large-v3")
    assert s.active_for("stt") == "mlx-whisper-large-v3"
    assert s.is_active("mlx-whisper-large-v3") is True
    assert s.is_active("some-other") is False


def test_active_persists_across_instances(tmp_path: Path) -> None:
    st.Storage(root=tmp_path).set_active("embedding", "all-minilm-l6-v2")
    # A fresh Storage over the same root must see the persisted choice.
    assert st.Storage(root=tmp_path).active_for("embedding") == "all-minilm-l6-v2"


def test_set_active_overwrites_same_type(tmp_path: Path) -> None:
    s = st.Storage(root=tmp_path)
    s.set_active("stt", "mlx-whisper-large-v3")
    s.set_active("stt", "faster-whisper-large-v3")
    assert s.active_for("stt") == "faster-whisper-large-v3"
    assert s.is_active("mlx-whisper-large-v3") is False


def test_delete_active_model_clears_active(tmp_path: Path) -> None:
    s = _install(tmp_path, "foo", {"a.bin": b"x" * 10})
    s.set_active("embedding", "foo")
    s.delete("foo")
    assert s.active_for("embedding") is None
