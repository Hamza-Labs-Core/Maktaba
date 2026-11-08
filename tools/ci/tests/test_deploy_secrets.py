"""tools/ci/deploy_secrets.py, and the single VPS's deploy.sh writing the symbols token."""

from __future__ import annotations

import os
import shutil
import stat
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "ci"))

import deploy_secrets  # noqa: E402

TOKEN = "sym-0123456789abcdef-not-a-real-token"


def _tree(tmp_path: Path) -> Path:
    """A copy of what the guard reads."""
    for rel in ("deploy/cloud-relay", "deploy/cloud-ha", deploy_secrets.API_DIR):
        shutil.copytree(ROOT / rel, tmp_path / rel)
    (tmp_path / ".github/workflows").mkdir(parents=True)
    shutil.copy(ROOT / deploy_secrets.WORKFLOW, tmp_path / deploy_secrets.WORKFLOW)
    return tmp_path


def test_the_repository_wires_every_secret_on_both_paths() -> None:
    assert deploy_secrets.gaps() == []
    assert "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE" in deploy_secrets.api_token_files()


def test_a_secret_one_deploy_step_lacks_is_named(tmp_path: Path) -> None:
    root = _tree(tmp_path)
    wf = root / deploy_secrets.WORKFLOW
    text = wf.read_text()
    single = text.index("Deploy (the single VPS)")
    line = "          MAKTABA_SYMBOLS_TOKEN: ${{ secrets.MAKTABA_SYMBOLS_TOKEN }}\n"
    cut = text.index(line, single)
    wf.write_text(text[:cut] + text[cut + len(line) :])
    got = deploy_secrets.gaps(root)
    assert any("'Deploy (the single VPS)' is not" in g and "MAKTABA_SYMBOLS_TOKEN" in g for g in got), got


def test_a_token_file_one_path_never_sets_is_named(tmp_path: Path) -> None:
    root = _tree(tmp_path)
    for f in (root / "deploy/cloud-relay").rglob("*"):
        if f.is_file():
            f.write_text(f.read_text(errors="replace").replace("MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE", "X"))
    got = deploy_secrets.gaps(root)
    assert any("deploy/cloud-relay" in g and "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE" in g for g in got), got


def test_an_exemption_keeps_its_reason() -> None:
    for why in [*deploy_secrets.EXEMPT.values(), *deploy_secrets.PATH_ONLY.values()]:
        assert len(why) > 20


# --- deploy/cloud-relay/deploy.sh against a fake ssh that runs the remote command here


def _fake_ssh(tmp_path: Path) -> Path:
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    ssh = bin_dir / "ssh"
    # options until the target (user@host), then the remote command: the rollout's `bash -s`
    # payload is swallowed (no Docker here); anything else runs as a remote shell would
    ssh.write_text(
        """#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-p|-i) shift 2 ;;
    *@*) shift; break ;;
    *) shift ;;
  esac
done
if [ "$1" = "bash -s" ]; then cat >/dev/null; echo rollout >>"$SSH_LOG"; exit 0; fi
echo token-step >>"$SSH_LOG"
exec bash -c "$*"
"""
    )
    ssh.chmod(0o755)
    return bin_dir


def _deploy(tmp_path: Path, token: str | None) -> tuple[subprocess.CompletedProcess[str], Path]:
    stack = tmp_path / "stack dir"  # a space: the deploy dir is quoted end to end
    (stack / "secrets").mkdir(parents=True)
    (stack / "secrets/log.key").write_bytes(b"k" * 32)
    (stack / ".env").write_text("MAKTABA_CLOUD_TAG=v1\n")
    env = {
        "PATH": f"{_fake_ssh(tmp_path)}:{os.environ['PATH']}",
        "HOME": str(tmp_path),
        "RELAY_SSH_HOST": "vps.invalid",
        "RELAY_SSH_USER": "maktaba",
        "DEPLOY_DIR": str(stack),
        "MAKTABA_CLOUD_TAG": "v2",
        "SSH_LOG": str(tmp_path / "ssh.log"),
    }
    if token is not None:
        env["MAKTABA_SYMBOLS_TOKEN"] = token
    p = subprocess.run(
        ["bash", str(ROOT / "deploy/cloud-relay/deploy.sh")], env=env, capture_output=True, text=True
    )
    return p, stack


@pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")
def test_deploy_writes_the_symbols_token_file(tmp_path: Path) -> None:
    p, stack = _deploy(tmp_path, TOKEN)
    assert p.returncode == 0, p.stderr
    tok = stack / "secrets/symbols.token"
    assert tok.read_text() == TOKEN
    assert stat.S_IMODE(tok.stat().st_mode) == 0o600
    assert tok.stat().st_uid == (stack / "secrets/log.key").stat().st_uid
    env = (stack / ".env").read_text()
    assert "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=/etc/maktaba/cloud/secrets/symbols.token\n" in env
    assert TOKEN not in p.stdout + p.stderr, "the token was echoed"
    assert not list((stack / "secrets").glob(".symbols.token.*")), "a temp file was left"
    # the token step runs before the rollout, which restarts the api to read it
    assert (tmp_path / "ssh.log").read_text().split() == ["token-step", "rollout"]
    # a second deploy with the same token changes nothing and adds no second line
    p2, _ = _deploy_again(tmp_path, stack, TOKEN)
    assert p2.returncode == 0, p2.stderr
    assert (stack / ".env").read_text().count("MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=") == 1
    # a new token replaces the file; .env keeps its other lines
    p3, _ = _deploy_again(tmp_path, stack, TOKEN + "-rotated")
    assert p3.returncode == 0, p3.stderr
    assert tok.read_text() == TOKEN + "-rotated"
    assert stat.S_IMODE(tok.stat().st_mode) == 0o600
    env = (stack / ".env").read_text()
    assert env.startswith("MAKTABA_CLOUD_TAG=") and env.count("MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE=") == 1


def _deploy_again(tmp_path: Path, stack: Path, token: str) -> tuple[subprocess.CompletedProcess[str], Path]:
    env = {
        "PATH": f"{tmp_path / 'bin'}:{os.environ['PATH']}",
        "HOME": str(tmp_path),
        "RELAY_SSH_HOST": "vps.invalid",
        "RELAY_SSH_USER": "maktaba",
        "DEPLOY_DIR": str(stack),
        "MAKTABA_SYMBOLS_TOKEN": token,
        "SSH_LOG": str(tmp_path / "ssh.log"),
    }
    p = subprocess.run(
        ["bash", str(ROOT / "deploy/cloud-relay/deploy.sh")], env=env, capture_output=True, text=True
    )
    return p, stack


@pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")
def test_without_the_secret_no_file_is_written(tmp_path: Path) -> None:
    p, stack = _deploy(tmp_path, "")
    assert p.returncode == 0, p.stderr
    assert not (stack / "secrets/symbols.token").exists()
    assert "SYMBOLS" not in (stack / ".env").read_text()
    assert (tmp_path / "ssh.log").read_text().split() == ["rollout"]
