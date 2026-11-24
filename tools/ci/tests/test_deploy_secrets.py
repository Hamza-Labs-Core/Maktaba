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
    for wf in deploy_secrets.WORKFLOWS.values():
        shutil.copy(ROOT / wf, tmp_path / wf)
    return tmp_path


def test_the_repository_wires_every_secret_on_both_paths() -> None:
    assert deploy_secrets.gaps() == []
    assert "MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE" in deploy_secrets.api_token_files()


def test_a_secret_one_deploy_step_lacks_is_named(tmp_path: Path) -> None:
    root = _tree(tmp_path)
    wf = root / deploy_secrets.WORKFLOWS["single VPS"]
    text = wf.read_text()
    single = text.index("Deploy (deploy.sh)")
    line = "          MAKTABA_SYMBOLS_TOKEN: ${{ secrets.MAKTABA_SYMBOLS_TOKEN }}\n"
    cut = text.index(line, single)
    wf.write_text(text[:cut] + text[cut + len(line) :])
    got = deploy_secrets.gaps(root)
    assert any("'Deploy (deploy.sh)' is not" in g and "MAKTABA_SYMBOLS_TOKEN" in g for g in got), got


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
    # options until the target (user@host), then the remote command: the rollout
    # (`maktaba-rollout job`, no Docker here) is answered with the end marker a whole one
    # prints (FAKE_CUT_SHORT=1: a script cut short, no marker; FAKE_JOB: a script that answers);
    # anything else runs as a remote shell would
    ssh.write_text(
        """#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-p|-i) shift 2 ;;
    *@*) shift; break ;;
    *) shift ;;
  esac
done
case "$1" in
  *"/usr/local/sbin/maktaba-rollout job "*)
    cat >/dev/null; echo rollout >>"$SSH_LOG"
    if [ -n "${FAKE_JOB:-}" ]; then set -- ${1##*maktaba-rollout job }; exec bash "$FAKE_JOB" "$@"; fi
    [ "${FAKE_CUT_SHORT:-}" = 1 ] || echo __maktaba_deploy_complete__
    exit 0 ;;
esac
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
        "SSH_LOG": str(tmp_path / "ssh.log"),
        "DEPLOY_ID": "1-1",
        "FAKE_CUT_SHORT": os.environ.get("FAKE_CUT_SHORT", ""),
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
        "DEPLOY_ID": "1-1",
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


@pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")
def test_a_remote_script_cut_short_fails_the_deploy(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # a command on the node that read the script's own stdin ends bash at exit 0 before the
    # rollout: only the end marker says the script ran to its end
    monkeypatch.setenv("FAKE_CUT_SHORT", "1")
    p, _ = _deploy(tmp_path, "")
    assert p.returncode != 0
    assert "stopped before its end" in p.stderr


# --- the node's units, followed over a session that can be cut


def _follow(tmp_path: Path, action: str, job: str, follow_seconds: str = "60") -> subprocess.CompletedProcess[str]:
    (tmp_path / "job.sh").write_text(job)
    return subprocess.run(
        ["bash", str(ROOT / "deploy/cloud-relay/deploy.sh"), action],
        env={
            "PATH": f"{_fake_ssh(tmp_path)}:{os.environ['PATH']}",
            "HOME": str(tmp_path),
            "RELAY_SSH_HOST": "vps.invalid",
            "RELAY_SSH_USER": "root",
            "DEPLOY_ID": "77-1",
            "SSH_LOG": str(tmp_path / "ssh.log"),
            "FAKE_JOB": str(tmp_path / "job.sh"),
            "FOLLOW_SECONDS": follow_seconds,
            "RETRY_SECONDS": "0",
            "T": str(tmp_path),
        },
        capture_output=True,
        text=True,
        timeout=60,
    )


# a unit with ten lines, the session cut (255, as ssh ends) after three more each time
CUT = """id=$1 cmd=$2 from=$3
echo "asked $id $cmd $from" >>"$T/asked"
for n in 1 2 3; do
  from=$((from + 1))
  [ "$from" -le 10 ] && echo "line $from"
  [ "$from" -lt 10 ] || { [ "$cmd" = apply ] && echo __maktaba_deploy_complete__; exit 0; }
done
exit 255
"""


@pytest.mark.parametrize("action", ["apply", "commit", "rollback"])
def test_a_cut_session_is_picked_up_where_it_ended(tmp_path: Path, action: str) -> None:
    p = _follow(tmp_path, action, CUT)
    assert p.returncode == 0, p.stderr
    assert (tmp_path / "asked").read_text().split("\n")[:-1] == [f"asked 77-1 {action} {n}" for n in (0, 3, 6, 9)]
    lines = [x for x in p.stdout.splitlines() if x.startswith("line ")]
    assert lines == [f"line {n}" for n in range(1, 11)], "a line was lost or repeated"


def test_the_units_own_failure_is_the_steps_and_is_not_tried_again(tmp_path: Path) -> None:
    p = _follow(tmp_path, "rollback", 'echo "asked $*" >>"$T/asked"; echo HELD; exit 3\n')
    assert p.returncode == 3 and "HELD" in p.stdout
    assert (tmp_path / "asked").read_text().count("asked") == 1


def test_a_node_that_is_never_reached_fails_the_step_at_its_deadline(tmp_path: Path) -> None:
    p = _follow(tmp_path, "rollback", "exit 255\n", follow_seconds="1")
    assert p.returncode == 255 and "comes to a known state by itself" in p.stderr


def test_apply_needs_its_end_marker_even_after_a_cut(tmp_path: Path) -> None:
    p = _follow(tmp_path, "apply", 'echo "line"; [ "$3" -gt 0 ] || exit 255; exit 0\n')
    assert p.returncode == 1 and "stopped before its end" in p.stderr


def test_every_action_names_its_deploy() -> None:
    env = {"PATH": os.environ["PATH"], "RELAY_SSH_HOST": "h", "RELAY_SSH_USER": "u"}
    p = subprocess.run(["bash", str(ROOT / "deploy/cloud-relay/deploy.sh"), "apply"], env=env, capture_output=True, text=True)
    assert p.returncode != 0 and "DEPLOY_ID" in p.stderr
    p = subprocess.run(["bash", str(ROOT / "deploy/cloud-relay/deploy.sh"), "begin"], env={**env, "DEPLOY_ID": "1"}, capture_output=True, text=True)
    assert p.returncode == 2
