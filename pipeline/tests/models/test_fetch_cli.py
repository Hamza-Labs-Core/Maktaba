"""``maktaba-pipeline models status|download`` and MAKTABA_MODEL_CACHE_DIR (story 36.4).
The downloaders are stood in for: nothing reaches Hugging Face."""

from __future__ import annotations

import contextlib
import importlib
import json
from collections.abc import Callable
from pathlib import Path
from typing import Any

import huggingface_hub.errors  # noqa: F401 - imported once here, not inside a timed test
import pytest

from maktaba_pipeline import __main__ as cli
from maktaba_pipeline import model_dirs
from maktaba_pipeline.engine.stages import transcribe  # noqa: F401 - imported once here
from maktaba_pipeline.models import fetch_cli
from maktaba_pipeline.stt import model_share

pytestmark = pytest.mark.unit

# items() reads mlx-audio's download patterns where the stt extra is installed: imported
# once here (half a second), not inside a timed test
with contextlib.suppress(ImportError):
    importlib.import_module("mlx_audio.utils")

COHERE = "CohereLabs/cohere-transcribe-arabic-07-2026"


@pytest.fixture(autouse=True)
def _settings(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    for var in (
        "MAKTABA_STT_MODEL",
        "MAKTABA_STT_REFINE",
        "MAKTABA_STT_REFINE_MODEL",
        "MAKTABA_EMBED_MODEL",
        "MAKTABA_MODELS_DIR",
    ):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setenv(model_dirs.ENV_MODEL_CACHE_DIR, str(tmp_path / "models"))


@pytest.fixture(autouse=True)
def _keep_log_config() -> Any:
    """The command sends the log to stderr: the next tests get the config back."""
    import structlog

    old = structlog.get_config()
    yield
    structlog.configure(**old)


class Refused(Exception):
    """What Hugging Face raises for a gated repo without a sign-in (a 401)."""

    class _Response:
        status_code = 401

    response = _Response()


def _fake_hub(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    *,
    cached: set[str] | None = None,
    refuse: set[str] | None = None,
) -> list[tuple[str, bool]]:
    calls: list[tuple[str, bool]] = []
    cached = set() if cached is None else cached

    def download(
        item: fetch_cli.Item, on_bytes: Callable[[int, int], None], local_only: bool
    ) -> Path:
        calls.append((item.model, local_only))
        if local_only and item.model not in cached:
            raise FileNotFoundError(item.model)
        if item.model in (refuse or set()):
            raise Refused("401 gated")
        snap = tmp_path / "hub" / item.model.replace("/", "--")
        if local_only:
            return snap
        snap.mkdir(parents=True, exist_ok=True)
        on_bytes(50, 100)
        on_bytes(100, 100)
        cached.add(item.model)
        return snap

    monkeypatch.setattr(fetch_cli, "hf_download", download)
    camel: dict[str, bool] = {"installed": False}
    monkeypatch.setattr(fetch_cli, "camel_installed", lambda: camel["installed"])
    monkeypatch.setattr(fetch_cli, "camel_download", lambda: camel.update(installed=True))
    return calls


def _run(capsys: pytest.CaptureFixture[str], *argv: str) -> tuple[int, list[dict[str, Any]]]:
    code = cli.main(["models", *argv, "--json"])
    lines = capsys.readouterr().out.splitlines()
    return code, [json.loads(line) for line in lines if line.startswith("{")]


def _final(events: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    return {e["name"]: e for e in events if e["event"] == "model"}


# --- MAKTABA_MODEL_CACHE_DIR -------------------------------------------------------------


def test_cache_dir_points_hf_home_and_camel_data() -> None:
    env = {model_dirs.ENV_MODEL_CACHE_DIR: "~/m"}
    model_dirs.apply_env(env)
    home = str(Path("~/m").expanduser())
    assert env["HF_HOME"] == f"{home}/huggingface"
    assert env["CAMELTOOLS_DATA"] == f"{home}/camel_tools"


def test_cache_dir_keeps_what_is_set_and_does_nothing_unset() -> None:
    env = {model_dirs.ENV_MODEL_CACHE_DIR: "/m", "HF_HOME": "/elsewhere"}
    model_dirs.apply_env(env)
    assert env["HF_HOME"] == "/elsewhere"
    assert env["CAMELTOOLS_DATA"] == "/m/camel_tools"
    empty: dict[str, str] = {}
    model_dirs.apply_env(empty)
    assert empty == {}


def test_share_copies_land_under_the_cache_dir(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    assert model_share.local_root() == tmp_path / "models" / "share-copies"
    monkeypatch.delenv(model_dirs.ENV_MODEL_CACHE_DIR)
    assert model_share.local_root() == Path.home() / ".maktaba" / "models"


# --- the items ---------------------------------------------------------------------------


def test_items_follow_the_workers_settings(monkeypatch: pytest.MonkeyPatch) -> None:
    its = {i.name: i for i in fetch_cli.items()}
    assert its["whisper"].model == "mlx-community/whisper-large-v3-turbo"
    assert its["cohere"].model == COHERE and its["cohere"].gated
    assert its["embedder"].model == "intfloat/multilingual-e5-small"
    assert all(i.skip is None for i in its.values())

    monkeypatch.setenv("MAKTABA_STT_MODEL", "mlx-community/whisper-large-v3-mlx")
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    its = {i.name: i for i in fetch_cli.items()}
    assert its["whisper"].model == "mlx-community/whisper-large-v3-mlx"
    assert its["cohere"].skip and its["dictionary"].skip
    assert [i.name for i in fetch_cli.items({"whisper"})] == ["whisper"]


# --- download / status -------------------------------------------------------------------


def test_download_fetches_everything_with_progress(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    calls = _fake_hub(monkeypatch, tmp_path)
    code, events = _run(capsys, "download")
    assert code == fetch_cli.EXIT_OK
    assert all(not local for _m, local in calls)
    final = _final(events)
    assert {n: e["state"] for n, e in final.items()} == dict.fromkeys(fetch_cli.NAMES, "ready")
    assert final["whisper"]["source"] == "hf"
    # (progress in between is throttled to two a second)
    assert final["whisper"]["bytes"] == 100 and final["whisper"]["total_bytes"] == 100
    assert events[-1] == {
        "event": "done",
        "ok": True,
        "exit": 0,
        "items": dict.fromkeys(fetch_cli.NAMES, "ready"),
    }
    code, events = _run(capsys, "status")
    assert code == fetch_cli.EXIT_OK
    assert {e["state"] for e in _final(events).values()} == {"ready"}


def test_status_reports_what_is_missing(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    _fake_hub(monkeypatch, tmp_path)
    assert _run(capsys, "download", "--only", "whisper")[0] == fetch_cli.EXIT_OK
    code, events = _run(capsys, "status")
    assert code == fetch_cli.EXIT_MISSING
    final = _final(events)
    assert final["whisper"]["state"] == "ready" and final["whisper"]["source"] == "cache"
    assert final["cohere"]["state"] == "missing"
    assert final["dictionary"]["state"] == "missing"


def test_status_does_not_trust_a_snapshot_without_the_marker(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    """Hugging Face writes the snapshot's ref before its files: a download cut short (or
    one a worker made) is not called ready until ``download`` has seen it through."""
    _fake_hub(monkeypatch, tmp_path, cached={"mlx-community/whisper-large-v3-turbo"})
    code, events = _run(capsys, "status", "--only", "whisper")
    assert code == fetch_cli.EXIT_MISSING
    assert _final(events)["whisper"]["state"] == "missing"


def test_gated_model_without_sign_in_asks_for_login(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    _fake_hub(monkeypatch, tmp_path, refuse={COHERE})
    code, events = _run(capsys, "download")
    assert code == fetch_cli.EXIT_NEEDS_LOGIN
    final = _final(events)
    assert final["cohere"]["state"] == "needs_login"
    assert "Hugging Face" in final["cohere"]["message"]
    assert final["whisper"]["state"] == "ready"


def test_gated_model_comes_from_the_share_when_refused(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    snapshot = tmp_path / "hf-snapshot" / "abc123"
    snapshot.mkdir(parents=True)
    (snapshot / "model.safetensors").write_bytes(b"weights")
    (snapshot / "config.json").write_text("{}", encoding="utf-8")
    share = tmp_path / "share" / "maktaba" / "models"
    share.mkdir(parents=True)
    assert model_share.publish(COHERE, snapshot, share) is not None
    _fake_hub(monkeypatch, tmp_path, refuse={COHERE})

    code, events = _run(capsys, "download", "--only", "cohere", "--share", str(share))
    assert code == fetch_cli.EXIT_OK
    cohere = _final(events)["cohere"]
    assert cohere["state"] == "ready" and cohere["source"] == "share"
    copied = Path(cohere["path"])
    assert copied.is_relative_to(tmp_path / "models" / "share-copies")
    assert (copied / "model.safetensors").read_bytes() == b"weights"
    # and status finds the copy without the share
    code, events = _run(capsys, "status", "--only", "cohere")
    assert code == fetch_cli.EXIT_OK and _final(events)["cohere"]["source"] == "share-copy"


def test_other_failures_exit_one(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    _fake_hub(monkeypatch, tmp_path, refuse={"intfloat/multilingual-e5-small"})
    code, events = _run(capsys, "download", "--only", "embedder")
    assert code == fetch_cli.EXIT_FAILED
    assert _final(events)["embedder"]["state"] == "failed"


def test_unknown_item_is_refused(capsys: pytest.CaptureFixture[str]) -> None:
    assert cli.main(["models", "download", "--only", "llama"]) == 2


def test_progress_bar_reports_snapshot_bytes() -> None:
    seen: list[tuple[int, int]] = []
    bar_cls = fetch_cli._bar_class(lambda n, t: seen.append((n, t)))
    # as snapshot_download drives it: a byte bar whose total grows per file
    bar = bar_cls(name="huggingface_hub.snapshot_download", total=0, initial=0, unit="B")
    bar.total += 1000
    bar.update(400)
    bar.update(600)
    files = bar_cls(total=3, desc="Fetching 3 files")
    files.update(1)
    assert seen == [(400, 1000), (1000, 1000)]
