#!/usr/bin/env python3
"""The per-component SBOMs' two helpers (story 23.7 AC1, TC1), used by tools/sbom.sh.

    sbom.py pnpm LOCKFILE COMPONENT VERSION OUT     # web: CycloneDX 1.5 from pnpm-lock.yaml
    sbom.py check SBOM --expect FILE                # every expected name@version is in SBOM

`pnpm`: cyclonedx-npm reads npm's tree only, so the web SBOM is written from the lockfile
itself: every package pnpm-lock.yaml (lockfileVersion 9) resolves, the transitive ones
included, each with its version and purl; a package only the dev dependencies reach has
scope `excluded` (it builds the bundle, it is not in it).

`check`: FILE lists the dependencies the SBOM must hold, one `name@version` per line, or
`name@` for one whose version is the build's own (Go:
`go list -deps`, the pipeline: `uv export`'s `name==version`, web: the lockfile's
packages); a missing one fails, so a generator that drops transitive dependencies fails the
release instead of publishing a short SBOM.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import sys
import uuid
from pathlib import Path


def _unquote(s: str) -> str:
    s = s.strip()
    if len(s) >= 2 and s[0] == s[-1] and s[0] in "'\"":
        return s[1:-1]
    return s


def _split_key(key: str) -> tuple[str, str]:
    """`@scope/name@1.2.3(peer@x)` -> (`@scope/name`, `1.2.3`)."""
    key = key.split("(", 1)[0]
    at = key.rfind("@")
    if at <= 0:
        raise ValueError(f"not a package key: {key!r}")
    return key[:at], key[at + 1 :]


def lock_packages(text: str) -> dict[str, dict[str, bool]]:
    """The lockfile's resolved packages, `name@version` -> {"dev": only dev deps reach it}.

    Reads the `snapshots:` section of a lockfileVersion 9 file (each resolved instance and
    its dependencies) and the importers' dependencies, then marks what the production
    dependencies reach; a v9 file is plain enough to read line by line without a YAML
    library (the release runner has none).
    """
    if not re.search(r"^lockfileVersion: '?9", text, re.M):
        raise ValueError("pnpm-lock.yaml is not lockfileVersion 9")
    section, importer, group, current = "", "", "", ""
    roots_prod: set[str] = set()
    roots_all: set[str] = set()
    pending_name = ""
    edges: dict[str, set[str]] = {}
    for raw in text.splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        line = raw.strip()
        if indent == 0:
            section = line.rstrip(":")
            continue
        if section == "importers":
            if indent == 2:
                importer = line
            elif indent == 4:
                group = line.rstrip(":")
            elif indent == 6 and group in {"dependencies", "devDependencies", "optionalDependencies"}:
                pending_name = _unquote(line.rstrip(":"))
            elif indent == 8 and line.startswith("version:") and pending_name:
                ver = _unquote(line.split(":", 1)[1]).split("(", 1)[0]
                if not ver.startswith(("link:", "file:", "workspace:")):
                    key = f"{pending_name}@{ver}"
                    roots_all.add(key)
                    if group != "devDependencies":
                        roots_prod.add(key)
        elif section == "snapshots":
            if indent == 2:
                current = _unquote(line.rstrip(":").rstrip(" {}").rstrip(":"))
                name, ver = _split_key(current)
                current = f"{name}@{ver}"
                edges.setdefault(current, set())
                group = ""
            elif indent == 4:
                group = line.rstrip(":")
            elif indent == 6 and group in {"dependencies", "optionalDependencies"}:
                dep, _, ver = line.partition(":")
                ver = _unquote(ver).split("(", 1)[0]
                dname = _unquote(dep)
                if ver.startswith("link:"):
                    continue
                # an alias (`npm:real@1.0.0`) or a plain version
                if ver.startswith("npm:"):
                    dname, ver = _split_key(ver[4:])
                edges[current].add(f"{dname}@{ver}")
    _ = importer

    def reach(roots: set[str]) -> set[str]:
        seen, todo = set(), list(roots)
        while todo:
            k = todo.pop()
            if k in seen:
                continue
            seen.add(k)
            todo.extend(edges.get(k, ()))
        return seen

    prod = reach(roots_prod)
    every = reach(roots_all) | set(edges)
    return {k: {"dev": k not in prod} for k in sorted(every)}


def lock_package_keys(text: str) -> list[str]:
    """The `packages:` section's keys (each resolved package once), read apart from
    lock_packages: the check's expected list, so the SBOM is not checked against itself."""
    body = text.split("\npackages:\n", 1)
    if len(body) < 2:
        return []
    body_text = body[1].split("\nsnapshots:\n", 1)[0]
    keys = []
    for ln in body_text.splitlines():
        if ln.startswith("  ") and not ln.startswith("   ") and ln.rstrip().endswith(":"):
            keys.append(_unquote(ln.strip()[:-1]))
    return sorted(set(keys))


def _purl(name: str, version: str) -> str:
    if name.startswith("@"):
        scope, _, base = name[1:].partition("/")
        return f"pkg:npm/%40{scope}/{base}@{version}"
    return f"pkg:npm/{name}@{version}"


def pnpm_sbom(lock_text: str, component: str, version: str) -> dict:
    comps = []
    for key, info in lock_packages(lock_text).items():
        name, ver = _split_key(key)
        comps.append(
            {
                "type": "library",
                "bom-ref": _purl(name, ver),
                "name": name,
                "version": ver,
                "purl": _purl(name, ver),
                "scope": "excluded" if info["dev"] else "required",
            }
        )
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "serialNumber": f"urn:uuid:{uuid.uuid5(uuid.NAMESPACE_URL, f'maktaba/{component}/{version}')}",
        "version": 1,
        "metadata": {
            "timestamp": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            "tools": [{"vendor": "Maktaba", "name": "tools/security/sbom.py", "version": "1"}],
            "component": {"type": "application", "name": f"maktaba-{component}", "version": version},
        },
        "components": comps,
    }


def sbom_entries(doc: dict) -> set[str]:
    out = set()

    def walk(cs: list[dict]) -> None:
        for c in cs or []:
            out.add(f"{c.get('name', '').lower()}@{str(c.get('version', '')).lstrip('v')}")
            # cyclonedx-gomod names a module by its path; `group` holds nothing extra
            walk(c.get("components") or [])

    walk(doc.get("components") or [])
    return out


def _norm(entry: str) -> str:
    name, _, ver = entry.strip().rpartition("@")
    # PEP 503 for Python names (cyclonedx-py writes the distribution's own spelling)
    return f"{name.lower()}@{ver.lstrip('v')}"


def check(sbom_path: Path, expect_path: Path) -> list[str]:
    doc = json.loads(sbom_path.read_text(encoding="utf-8"))
    problems = []
    if doc.get("bomFormat") != "CycloneDX":
        problems.append("bomFormat is not CycloneDX")
    have = sbom_entries(doc)
    names = {e.rpartition("@")[0] for e in have}
    have_py = {re.sub(r"[-_.]+", "-", e) for e in have}
    expected = [ln for ln in expect_path.read_text(encoding="utf-8").splitlines() if ln.strip()]
    if not expected:
        problems.append("the expected list is empty")
    for e in expected:
        n = _norm(e)
        name, _, ver = n.rpartition("@")
        if not ver:
            # `name@`: a module replaced by a local directory, whose version is the build's
            if name not in names:
                problems.append(f"missing {e.strip().rstrip('@')}")
            continue
        if n not in have and re.sub(r"[-_.]+", "-", n) not in have_py:
            problems.append(f"missing {e.strip()}")
    return problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("pnpm")
    p.add_argument("lockfile", type=Path)
    p.add_argument("component")
    p.add_argument("version")
    p.add_argument("out", type=Path)
    p.add_argument("--expect-out", type=Path, help="also write the lockfile's packages, one name@version a line")
    c = sub.add_parser("check")
    c.add_argument("sbom", type=Path)
    c.add_argument("--expect", type=Path, required=True)
    a = ap.parse_args(argv)
    if a.cmd == "pnpm":
        text = a.lockfile.read_text(encoding="utf-8")
        doc = pnpm_sbom(text, a.component, a.version)
        a.out.write_text(json.dumps(doc, indent=2) + "\n", encoding="utf-8")
        if a.expect_out:
            a.expect_out.write_text("".join(f"{k}\n" for k in lock_package_keys(text)), encoding="utf-8")
        print(f"[sbom] {a.out}: {len(doc['components'])} packages")
        return 0
    problems = check(a.sbom, a.expect)
    for prob in problems[:50]:
        print(f"{a.sbom.name}: {prob}", file=sys.stderr)
    if len(problems) > 50:
        print(f"{a.sbom.name}: ... and {len(problems) - 50} more", file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
