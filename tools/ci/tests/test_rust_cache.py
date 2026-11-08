"""Rust build caches: only main writes them, and the plugin host's ubuntu-24.04 entry is shared.

Swatinem/rust-cache keys each job apart by default. The plugind job, the sandbox's
ubuntu-24.04 leg and the update rehearsal's kit built the same workspace on the same image
into three entries (about 1.7 GB of the repository's 10 GB). They now share one, which only
the plugind job writes.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
MAIN = "${{ github.event_name == 'push' && github.ref == 'refs/heads/main' }}"


def _rust_caches() -> list[tuple[str, str, dict[str, Any]]]:
    found = []
    for path in WORKFLOWS:
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for name, job in (doc.get("jobs") or {}).items():
            for s in job.get("steps") or []:
                if str(s.get("uses", "")).startswith("Swatinem/rust-cache@"):
                    found.append((path.name, name, s.get("with") or {}))
    return found


def test_only_main_writes_rust_caches() -> None:
    caches = _rust_caches()
    assert len(caches) >= 7
    for workflow, job, w in caches:
        save_if = w.get("save-if")
        assert save_if is not None, f"{workflow} {job}: rust-cache without save-if saves on every ref"
        ok = (
            save_if is False
            or save_if == MAIN
            or str(save_if).startswith("${{ github.event_name == 'push' && github.ref == 'refs/heads/main' && ")
        )
        assert ok, f"{workflow} {job}: save-if {save_if!r}"


def test_the_plugin_host_shares_one_ubuntu_24_entry() -> None:
    by_job = {(wf, job): w for wf, job, w in _rust_caches()}
    writer = by_job[("_plugins.yml", "plugind")]
    sandbox = by_job[("_plugins.yml", "plugins-sandbox")]
    kit = by_job[("update-rehearsal.yml", "kit")]
    assert writer["shared-key"] == "plugind-ubuntu-24.04" and writer["save-if"] == MAIN
    assert kit["shared-key"] == "plugind-ubuntu-24.04" and kit["save-if"] is False
    # the sandbox's ubuntu-24.04 leg resolves to the same key and does not save
    assert sandbox["shared-key"] == "plugind-${{ matrix.os }}"
    assert "matrix.os == 'windows-2022'" in sandbox["save-if"]
    # the paths are part of an entry's version: every sharer caches the same workspaces
    assert writer["workspaces"] == sandbox["workspaces"] == kit["workspaces"]
    assert "key" not in writer and "key" not in sandbox and "key" not in kit


def test_the_desktop_shell_tests_keep_no_rust_cache() -> None:
    jobs = {job for wf, job, _ in _rust_caches() if wf == "_unit.yml"}
    assert not jobs & {"desktop-shell", "desktop-shell-linux", "desktop-shell-windows"}
