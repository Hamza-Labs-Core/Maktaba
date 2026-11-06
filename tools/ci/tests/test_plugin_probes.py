"""The plugins-sandbox job's probe checker (Epic 54, story 54.03 §5): a denied run with a
silent canary passes; an allowed probe, a canary hit, a broken control or a wrong sandbox
level fails."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "plugins"))

import check_probes  # noqa: E402

SANDBOXED = """SANDBOX full missing=
PROBE tcp_loopback4 network DENIED Operation not permitted (Some(1))
PROBE read_passwd file DENIED Permission denied (Some(13))
PROBE spawn process DENIED Operation not permitted (Some(1))
"""
CONTROL = """SANDBOX none missing=all
PROBE tcp_loopback4 network ALLOWED connected to 127.0.0.1:47391
PROBE read_passwd file ALLOWED read 16 bytes
PROBE spawn process ALLOWED exit status: 0
"""


def run(tmp: Path, sandboxed: str, control: str, before=0, after_s=0, after_c=3, missing="") -> list[str]:
    s, c = tmp / "s.txt", tmp / "c.txt"
    s.write_text(sandboxed)
    c.write_text(control)
    ns = argparse.Namespace(
        sandboxed=str(s), control=str(c), canary_before=before, canary_after_sandboxed=after_s,
        canary_after_control=after_c, expect_missing=missing,
    )
    return check_probes.check(ns)


def test_all_denied_passes(tmp_path: Path) -> None:
    assert run(tmp_path, SANDBOXED, CONTROL) == []


def test_an_allowed_probe_fails(tmp_path: Path) -> None:
    bad = SANDBOXED.replace("tcp_loopback4 network DENIED", "tcp_loopback4 network ALLOWED")
    assert any("tcp_loopback4" in e for e in run(tmp_path, bad, CONTROL))


def test_a_canary_hit_fails(tmp_path: Path) -> None:
    assert any("N8" in e for e in run(tmp_path, SANDBOXED, CONTROL, after_s=1, after_c=4))


def test_a_broken_control_fails(tmp_path: Path) -> None:
    broken = CONTROL.replace("read_passwd file ALLOWED", "read_passwd file DENIED")
    assert any("broken" in e for e in run(tmp_path, SANDBOXED, broken))
    assert any("canary" in e for e in run(tmp_path, SANDBOXED, CONTROL, after_c=0))


def test_partial_sandbox(tmp_path: Path) -> None:
    partial = SANDBOXED.replace("SANDBOX full missing=", "SANDBOX partial missing=namespaces")
    assert run(tmp_path, partial, CONTROL, missing="namespaces") == []
    assert run(tmp_path, partial, CONTROL) != []
    assert run(tmp_path, SANDBOXED, CONTROL, missing="namespaces") != []
