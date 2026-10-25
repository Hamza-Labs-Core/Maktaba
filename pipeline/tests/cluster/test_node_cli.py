"""How `run` picks node mode; capabilities; the ffmpeg override."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from maktaba_pipeline import __main__ as cli
from maktaba_pipeline.binaries import ffmpeg_binary, ffprobe_binary
from maktaba_pipeline.cluster import config as node_config
from maktaba_pipeline.cluster.capabilities import capabilities
from maktaba_pipeline.cluster.config import HomeRef, NodeConfig

pytestmark = pytest.mark.unit

NODE = NodeConfig(
    node_id="6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7",
    name="linux-box",
    private_key="AQID",
    home=HomeRef(url="http://h:8080", name="home-mac"),
    database_url="postgres://maktaba_node_6f1c2a3b:x@h:5432/maktaba",
)


def _args(*argv: str) -> object:
    return cli._build_parser().parse_args(["run", *argv])


@pytest.fixture
def cfg_path(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    path = tmp_path / "node.json"
    monkeypatch.setenv(node_config.ENV_VAR, str(path))
    monkeypatch.delenv("MAKTABA_DATABASE_URL", raising=False)
    monkeypatch.delenv("MAKTABA_PIPELINE_WORKER_ID", raising=False)
    monkeypatch.delenv("MAKTABA_WORKER_HOST", raising=False)
    return path


def test_no_database_url_and_a_joined_config_is_node_mode(cfg_path: Path) -> None:
    node_config.save(NODE, cfg_path)
    args = _args()
    node = cli._node_for(args)  # type: ignore[arg-type]
    assert node == NODE
    rc = cli._build_runtime_config(args, node)  # type: ignore[arg-type]
    assert rc.role == "node" and rc.node_id == NODE.node_id
    assert rc.database_url == NODE.database_url
    assert rc.worker_id == f"node-6f1c2a3b-{os.getpid()}"
    assert rc.host


def test_an_explicit_database_url_is_a_home_worker(cfg_path: Path) -> None:
    node_config.save(NODE, cfg_path)
    args = _args("--database-url", "sqlite:///x.db")
    assert cli._node_for(args) is None  # type: ignore[arg-type]
    rc = cli._build_runtime_config(args)  # type: ignore[arg-type]
    assert rc.role == "home" and rc.node_id is None and rc.database_url == "sqlite:///x.db"


def test_no_config_or_not_yet_joined_keeps_the_old_default(cfg_path: Path) -> None:
    args = _args()
    assert cli._node_for(args) is None  # type: ignore[arg-type]
    node_config.save(NodeConfig(node_id=NODE.node_id, name="n", private_key="AQID"), cfg_path)
    assert cli._node_for(args) is None  # type: ignore[arg-type]
    assert cli._build_runtime_config(args).database_url == "sqlite:///:memory:"  # type: ignore[arg-type]


def test_join_is_a_subcommand() -> None:
    args = cli._build_parser().parse_args(["join", "--home", "http://h:8080", "--run"])
    assert (args.command, args.home, args.run, args.name) == ("join", "http://h:8080", True, None)


def test_capabilities_report_the_keys_the_page_reads(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MAKTABA_FFMPEG", "/nonexistent/ffmpeg")
    caps = capabilities()
    assert set(caps) == {
        "os", "os_version", "arch", "cpu_count", "memory_gb", "apple_silicon", "mlx",
        "gpus", "ffmpeg", "extras", "python", "pipeline_version",
    }  # fmt: skip
    assert caps["cpu_count"] >= 1 and isinstance(caps["gpus"], list)
    assert caps["ffmpeg"] is None  # the override names a binary that is not there
    assert caps["memory_gb"] is None or caps["memory_gb"] > 0


def test_ffmpeg_and_ffprobe_follow_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_FFMPEG", raising=False)
    monkeypatch.delenv("MAKTABA_FFPROBE", raising=False)
    assert (ffmpeg_binary(), ffprobe_binary()) == ("ffmpeg", "ffprobe")
    monkeypatch.setenv("MAKTABA_FFMPEG", r"C:\ffmpeg\bin\ffmpeg.exe")
    monkeypatch.setenv("MAKTABA_FFPROBE", "/opt/ff/ffprobe")
    assert ffmpeg_binary() == r"C:\ffmpeg\bin\ffmpeg.exe"
    assert ffprobe_binary() == "/opt/ff/ffprobe"
    assert ffmpeg_binary("/explicit") == "/explicit"

    from maktaba_pipeline.audio.chromaprint import ffmpeg_chromaprint_args
    from maktaba_pipeline.thumbnail.generator import build_poster_args

    assert ffmpeg_chromaprint_args("a.mp4", 0, 10)[0] == r"C:\ffmpeg\bin\ffmpeg.exe"
    assert build_poster_args("a.mp4", 1.0, "o.jpg")[0] == r"C:\ffmpeg\bin\ffmpeg.exe"


def test_join_run_starts_as_the_node_even_with_a_database_url_set(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import argparse

    import maktaba_pipeline.__main__ as cli
    from maktaba_pipeline.cluster import join as join_mod

    monkeypatch.setenv("MAKTABA_DATABASE_URL", "postgresql://local/standalone")
    monkeypatch.setattr(join_mod, "join", lambda **kw: None)
    seen: list[object] = []

    def serve(args: argparse.Namespace, log: object) -> int:
        seen.append(args.database_url)
        return 0

    monkeypatch.setattr(cli, "_serve", serve)
    monkeypatch.setattr("maktaba_pipeline.__main__.asyncio.run", lambda result: result)
    parser = cli._build_parser()
    args = parser.parse_args(["join", "--run", "--home", "http://h:8080"])
    assert cli._join(args, parser, None) == 0
    assert seen == [None]  # node mode: the joined config's database, not the env's


def test_the_index_worker_flags(cfg_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The supervisor's index worker runs with MAKTABA_PIPELINE_SHARE=off, the main
    worker beside it with MAKTABA_PIPELINE_SEMANTIC=off; unset, both are on."""
    monkeypatch.delenv("MAKTABA_PIPELINE_SHARE", raising=False)
    monkeypatch.delenv("MAKTABA_PIPELINE_SEMANTIC", raising=False)
    args = _args()
    assert cli._build_runtime_config(args).share is True  # type: ignore[arg-type]
    assert args.no_semantic is False  # type: ignore[attr-defined]
    monkeypatch.setenv("MAKTABA_PIPELINE_SHARE", "off")
    monkeypatch.setenv("MAKTABA_PIPELINE_SEMANTIC", "off")
    args = _args()
    assert cli._build_runtime_config(args).share is False  # type: ignore[arg-type]
    assert args.no_semantic is True  # type: ignore[attr-defined]
