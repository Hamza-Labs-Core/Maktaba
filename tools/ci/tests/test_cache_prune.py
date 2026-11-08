"""tools/ci/cache_prune.py: which Actions cache entries it deletes (a newer one replaced them).

GitHub evicts least recently used first past the repository's 10 GB; an entry a newer key
replaced was young enough to outlive small entries still in use (golangci-lint's, within the
hour, 2026-11-08). The prune deletes, on main only, every entry of a family but the newest,
once idle for an hour.
"""

from __future__ import annotations

import importlib.util
import sys
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
_spec = importlib.util.spec_from_file_location("cache_prune", ROOT / "tools/ci/cache_prune.py")
assert _spec and _spec.loader
cache_prune = importlib.util.module_from_spec(_spec)
sys.modules["cache_prune"] = cache_prune
_spec.loader.exec_module(cache_prune)

NOW = datetime(2026, 11, 8, 18, 0, tzinfo=UTC)
H64 = "0a7067ce90e74fd5d8526cecc9a4f6ffcb65eeba58ee25d11b98a973977ea7f3"
H64B = "b66c056afe4910125bb36e7d8b181f41568a32b571d3e9a6783aee0cbc14784e"


def _c(key: str, created_h: float, used_h: float, ref: str = "refs/heads/main") -> dict[str, Any]:
    iso = lambda h: (NOW - timedelta(hours=h)).isoformat().replace("+00:00", "Z")  # noqa: E731
    return {"id": hash(key) & 0xFFFF, "key": key, "ref": ref, "size_in_bytes": 1, "created_at": iso(created_h), "last_accessed_at": iso(used_h)}


def _keys(caches: list[dict[str, Any]]) -> list[str]:
    return [c["key"] for c in cache_prune.to_delete(caches, NOW)]


def test_families() -> None:
    f = cache_prune.family
    assert f(f"setup-go-Linux-x64-go-1.26.0-{H64}") == f(f"setup-go-Linux-x64-ubuntu22-go-1.26.0-{H64B}")
    assert f(f"setup-go-Linux-arm64-ubuntu22-arm64-go-1.26.0-{H64}") == f(f"setup-go-Linux-arm64-go-1.26.0-{H64B}")
    assert f(f"setup-go-Linux-x64-go-1.26.0-{H64}") != f(f"setup-go-Linux-x64-go-1.25.8-{H64}")
    assert f(f"setup-go-Windows-x64-go-1.26.0-{H64}") != f(f"setup-go-Linux-x64-go-1.26.0-{H64}")
    assert f("v0-rust-plugind-Linux-x64-01b81e85-33164258") == f("v0-rust-plugind-Linux-x64-fc985acc-11111111")
    assert f("v0-rust-plugind-Linux-x64-01b81e85-33164258") != f("v0-rust-kit-Linux-x64-01b81e85-33164258")
    assert f(f"rehearsal-seed-v0.3.106-restore-{H64}") == f(f"rehearsal-seed-v0.3.107-restore-{H64B}")
    assert f(f"rehearsal-seed-v0.3.106-restore-{H64}") != f(f"rehearsal-seed-v0.3.106-small-{H64}")
    assert f("rehearsal-pg-v0.3.106-X64") == f("rehearsal-pg-v0.3.107-X64")
    # pinned tools, payload downloads, CodeQL's, Gradle's: no family, left to GitHub
    for key in [
        "pinned-linux-golangci-lint-2.12.2-linux-amd64.tar.gz-8df580d2670fed8f",
        "win-app-downloads-Windows-X64-6417adcabd0471ac7a3b514d5d011fa94380e5c4fe24d1ac5ecf90f82e4a476c",
        "codeql-dependencies-1-Linux-go-d56fde1f4f167b8c953d3b69ad59646968a43747f2c777806992dd8a476503af",
        "gradle-dependencies-v2-491451a658d013ac05ea40592058e48d",
    ]:
        assert f(key) is None, key


def test_keeps_the_newest_and_deletes_the_idle_replaced_ones() -> None:
    caches = [
        _c(f"rehearsal-seed-v0.3.107-restore-{H64}", created_h=0.5, used_h=0.5),
        _c(f"rehearsal-seed-v0.3.106-restore-{H64B}", created_h=3, used_h=2),
        _c(f"rehearsal-seed-v0.3.105-restore-{H64}", created_h=6, used_h=0.2),  # used just now
        _c("pinned-linux-golangci-lint-2.12.2-linux-amd64.tar.gz-8df580d2670fed8f", created_h=9, used_h=8),
    ]
    assert _keys(caches) == [f"rehearsal-seed-v0.3.106-restore-{H64B}"]


def test_a_family_of_one_is_kept_however_old() -> None:
    assert _keys([_c("v0-rust-plugind-Linux-x64-01b81e85-33164258", created_h=200, used_h=100)]) == []


def test_a_closed_pull_requests_entries_go_an_open_ones_stay() -> None:
    dep = "codeql-dependencies-1-Linux-go-d56fde1f4f167b8c953d3b69ad59646968a43747f2c777806992dd8a476503af"
    caches = [
        _c(dep, created_h=2, used_h=2, ref="refs/pull/1034/head"),
        _c(dep, created_h=2, used_h=2, ref="refs/pull/1036/merge"),
        _c(dep, created_h=2, used_h=2),
    ]
    got = cache_prune.to_delete(caches, NOW, {1034})
    assert [c["ref"] for c in got] == ["refs/pull/1034/head"]
    assert cache_prune.pull_number("refs/heads/main") is None


def test_only_main_is_pruned() -> None:
    caches = [
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64}", created_h=1, used_h=1),
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64B}", created_h=5, used_h=5, ref="refs/pull/1/merge"),
    ]
    assert _keys(caches) == []


def test_the_old_per_job_go_entries_go_once_the_shared_one_is_there() -> None:
    caches = [
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64}", created_h=0.2, used_h=0.1),
        _c(f"setup-go-Linux-x64-ubuntu22-go-1.26.0-{H64}", created_h=30, used_h=5),
        _c(f"setup-go-Linux-x64-ubuntu24-go-1.26.0-{H64B}", created_h=20, used_h=3),
        _c(f"setup-go-Linux-x64-ubuntu22-go-1.25.8-{H64}", created_h=20, used_h=3),
    ]
    assert _keys(caches) == [
        f"setup-go-Linux-x64-ubuntu22-go-1.26.0-{H64}",
        f"setup-go-Linux-x64-ubuntu24-go-1.26.0-{H64B}",
    ]


def test_the_workflow_runs_it_with_the_least_privilege() -> None:
    doc = yaml.safe_load((ROOT / ".github/workflows/cache-prune.yml").read_text(encoding="utf-8"))
    assert doc["permissions"] == {"contents": "read"}
    job = doc["jobs"]["prune"]
    assert job["permissions"] == {"actions": "write", "contents": "read", "pull-requests": "read"}
    assert any(s.get("run") == "python3 tools/ci/cache_prune.py" for s in job["steps"])
    on = doc.get("on") or doc.get(True)
    assert "schedule" in on and "pull_request" not in on and "push" not in on


def test_it_deletes_only_closed_pull_requests_entries_and_replaced_main_entries() -> None:
    """Every other entry is kept: the newest of a family, one used in the last hour, one of
    no family, one on an open pull request's ref, one on any other branch or tag."""
    gl = "pinned-linux-golangci-lint-2.12.2-linux-amd64.tar.gz-8df580d2670fed8f"
    caches = [
        # deleted: a closed pull request's, whatever it is and however recent
        _c(gl, created_h=0.1, used_h=0.1, ref="refs/pull/7/merge"),
        # deleted: replaced on main and idle for an hour
        _c("v0-rust-plugind-ubuntu-24.04-Linux-x64-aaaaaaaa-11111111", created_h=10, used_h=9),
        # kept: the newest of that family
        _c("v0-rust-plugind-ubuntu-24.04-Linux-x64-bbbbbbbb-22222222", created_h=1, used_h=1),
        # kept: replaced but used in the last hour
        _c("v0-rust-plugind-ubuntu-24.04-Linux-x64-cccccccc-33333333", created_h=20, used_h=0.5),
        # kept: on main, old and idle, but of no family
        _c(gl, created_h=300, used_h=200),
        _c("codeql-overlay-base-database-1-433570c23e836bda-go-2.27.4-c06f247d", created_h=50, used_h=40),
        # kept: an open pull request's
        _c(gl, created_h=5, used_h=5, ref="refs/pull/8/merge"),
        # kept: replaced-looking entries off main (another branch, a tag)
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64}", created_h=1, used_h=1, ref="refs/heads/release/x"),
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64B}", created_h=9, used_h=9, ref="refs/heads/release/x"),
        _c(f"setup-go-Linux-x64-go-1.26.0-{H64B}", created_h=9, used_h=9, ref="refs/tags/v0.3.106"),
    ]
    got = {(c["key"], c["ref"]) for c in cache_prune.to_delete(caches, NOW, {7})}
    assert got == {
        (gl, "refs/pull/7/merge"),
        ("v0-rust-plugind-ubuntu-24.04-Linux-x64-aaaaaaaa-11111111", "refs/heads/main"),
    }
