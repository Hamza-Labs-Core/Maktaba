"""Deletes the Actions cache entries a newer entry of the same family has replaced on main.

GitHub evicts the repository's caches least recently used first once they pass 10 GB. An
entry a newer key replaced (the last release's 1.4 GB rehearsal seed, a Go or Rust entry from
before a go.sum or Cargo.lock change) was still young, so it outlived small entries that
are still in use, such as a pinned tool's: golangci-lint's was evicted within the hour
(2026-11-08). This removes the replaced ones as soon as they are idle.

    cache_prune.py [--dry-run]     reads the repository from GITHUB_REPOSITORY, uses gh

A family is a key with its version or hash taken out (FAMILIES). In each family on main,
the newest entry is kept, and so is any entry used in the last hour (a run may still be
restoring it); the others are deleted. An entry on a pull request's ref (refs/pull/N/...) is
deleted once that pull request is closed or merged: nothing reads it again (CodeQL's default
setup wrote 0.6 GB per pull request). Everything else is left to GitHub's eviction.
tools/ci/tests/test_cache_prune.py tests the choice.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from datetime import UTC, datetime, timedelta
from typing import Any

MAIN = "refs/heads/main"
IDLE = timedelta(hours=1)

# each pattern's groups name the family; what it leaves out is the version or the hash
FAMILIES = [
    # .github/actions/go-cache: one per runner OS, arch and Go version (older keys had the
    # runner image and a per-job hash, which fold into the same family)
    re.compile(r"^setup-go-(\w+)-(\w+)-(?:[\w-]+-)?go-([\d.]+)-[0-9a-f]{64}$"),
    # Swatinem/rust-cache: v0-rust-<job or shared key>-<OS>-<arch>-<env hash>-<lock hash>
    re.compile(r"^v0-rust-(.+)-[0-9a-f]{8}-[0-9a-f]{8}$"),
    # update-rehearsal.yml: each release's seed, venv and Postgres, keyed on the release
    re.compile(r"^(rehearsal-(?:seed|venv|pg))-v\d+\.\d+\.\d+-([A-Za-z0-9]+)(?:-[0-9a-f]{64})?$"),
]


def family(key: str) -> tuple[str, ...] | None:
    for i, pattern in enumerate(FAMILIES):
        m = pattern.match(key)
        if m:
            return (str(i), *m.groups())
    return None


def _time(s: str) -> datetime:
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


PULL = re.compile(r"^refs/pull/(\d+)/")


def pull_number(ref: str) -> int | None:
    m = PULL.match(ref)
    return int(m.group(1)) if m else None


def to_delete(caches: list[dict[str, Any]], now: datetime, closed: set[int] | frozenset[int] = frozenset()) -> list[dict[str, Any]]:
    """The entries to delete: on main, of a family, not its newest, idle for an hour; and
    every entry on the ref of a pull request in CLOSED."""
    families: dict[tuple[str, ...], list[dict[str, Any]]] = {}
    out = []
    for c in caches:
        n = pull_number(str(c.get("ref", "")))
        if n is not None and n in closed:
            out.append(c)
            continue
        if c.get("ref") != MAIN:
            continue
        f = family(str(c["key"]))
        if f is not None:
            families.setdefault(f, []).append(c)
    for entries in families.values():
        entries.sort(key=lambda c: _time(c["created_at"]), reverse=True)
        for c in entries[1:]:
            if now - _time(c["last_accessed_at"]) >= IDLE:
                out.append(c)
    return sorted(out, key=lambda c: str(c["key"]))


def _caches(repo: str) -> list[dict[str, Any]]:
    out = subprocess.run(
        ["gh", "api", "--paginate", f"repos/{repo}/actions/caches?per_page=100", "--jq", ".actions_caches[]"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [json.loads(line) for line in out.splitlines() if line.strip()]


def _closed(repo: str, numbers: set[int]) -> set[int]:
    """The pull requests among NUMBERS that are closed (merged ones too)."""
    closed = set()
    for n in sorted(numbers):
        r = subprocess.run(
            ["gh", "api", f"repos/{repo}/pulls/{n}", "--jq", ".state"], capture_output=True, text=True
        )
        if r.returncode == 0 and r.stdout.strip() == "closed":
            closed.add(n)
    return closed


def main(argv: list[str]) -> int:
    dry = "--dry-run" in argv
    repo = os.environ["GITHUB_REPOSITORY"]
    caches = _caches(repo)
    before = sum(int(c["size_in_bytes"]) for c in caches)
    pulls = {n for c in caches if (n := pull_number(str(c.get("ref", "")))) is not None}
    doomed = to_delete(caches, datetime.now(UTC), _closed(repo, pulls))
    freed = 0
    for c in doomed:
        mb = int(c["size_in_bytes"]) / 1e6
        print(f"{'would delete' if dry else 'deleting'} {c['key']} ({mb:.0f} MB, last used {c['last_accessed_at']})")
        if not dry:
            r = subprocess.run(
                ["gh", "api", "-X", "DELETE", f"repos/{repo}/actions/caches/{c['id']}"],
                capture_output=True,
                text=True,
            )
            if r.returncode != 0:
                # gone already (evicted, or another run deleted it): not a failure
                print(f"::warning::{c['key']}: not deleted ({r.stderr.strip()[:200]})")
                continue
        freed += int(c["size_in_bytes"])
    print(f"cache_prune: {len(caches)} entries, {before / 1e9:.2f} GB; {len(doomed)} replaced or left by closed pull requests, {freed / 1e9:.2f} GB freed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
