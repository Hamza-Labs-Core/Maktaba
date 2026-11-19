#!/usr/bin/env python3
"""The CVE gate (story 23.7 AC2, EC1): fails when a shipped component has a known
vulnerability that blocks and no live suppression covers it.

    cve_gate.py --govulncheck api=api.json --govulncheck streaming=s.json \\
                --pip-audit pipeline=pip.json --pnpm-audit web=web.json \\
                [--suppressions security/suppressions] [--today YYYY-MM-DD] \\
                [--severities osv-severities.json] [--summary FILE]

What blocks:

- Go (govulncheck -json): every vulnerability whose vulnerable symbol the code calls (a
  finding whose trace reaches a function). The Go database has no severity, and
  govulncheck's call graph already leaves out what the code cannot reach, so each called
  one blocks. A module or package-level match alone (imported, never called) is reported,
  not blocking.
- Python (pip-audit -f json): a vulnerability whose severity is HIGH or CRITICAL. pip-audit
  gives no severity, so each id is looked up on OSV (api.osv.dev; `--severities` gives a
  file of {id: severity} instead, for tests and offline runs). An id OSV gives no
  severity for blocks: the gate fails closed.
- web (pnpm audit --json): an advisory of severity high or critical, or of none (fails
  closed, as Python).

A suppression is a file `security/suppressions/<id>.md` (the id is the advisory's, a CVE,
GHSA, GO- or PYSEC- id; any of a vulnerability's aliases matches) with front matter:

    ---
    id: GHSA-xxxx-xxxx-xxxx
    expires: 2027-02-01
    ---
    Why this does not affect Maktaba, and what removes it.

It must give a reason (the body) and an expiry date no further than MAX_DAYS from today.
An expired suppression fails the gate whether or not the vulnerability is still reported,
so an old one cannot be forgotten. A file whose name is not its `id` fails too.

Exit 0: nothing blocks. Exit 1: something blocks, or a suppression is invalid or expired.
Exit 2: a report could not be read.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import sys
import urllib.request
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SUPPRESSIONS = ROOT / "security" / "suppressions"
# the furthest a suppression's expiry may be from today (AC2's "date ceiling")
MAX_DAYS = 180
# what does not block; any other severity, none included, does (the gate fails closed)
NOT_BLOCKING = {"LOW", "MODERATE", "MEDIUM", "INFO"}
OSV = "https://api.osv.dev/v1/vulns/"


@dataclass
class Vuln:
    component: str
    ecosystem: str  # go | python | npm
    id: str
    aliases: set[str]
    package: str
    version: str
    fixed: str
    severity: str  # HIGH, CRITICAL, ...; "called" for a Go finding the code reaches
    summary: str
    blocks: bool

    @property
    def ids(self) -> set[str]:
        return {self.id, *self.aliases}


@dataclass
class Suppression:
    path: Path
    id: str
    expires: dt.date | None
    reason: str
    problems: list[str] = field(default_factory=list)


class ReportError(Exception):
    pass


# ---- the reports ------------------------------------------------------------------------


def _json_stream(text: str) -> Iterable[dict]:
    """govulncheck -json writes a stream of JSON objects, one after another."""
    dec, i = json.JSONDecoder(), 0
    while True:
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            return
        obj, i = dec.raw_decode(text, i)
        if isinstance(obj, dict):
            yield obj


def parse_govulncheck(component: str, text: str) -> list[Vuln]:
    osvs: dict[str, dict] = {}
    called: dict[str, dict] = {}
    seen: dict[str, dict] = {}
    for msg in _json_stream(text):
        if "osv" in msg:
            osvs[msg["osv"]["id"]] = msg["osv"]
        elif "finding" in msg:
            f = msg["finding"]
            trace = f.get("trace") or []
            target = called if trace and trace[0].get("function") else seen
            target.setdefault(f["osv"], f)
    out = []
    for vid, f in {**seen, **called}.items():
        o = osvs.get(vid, {})
        top = (f.get("trace") or [{}])[0]
        out.append(
            Vuln(
                component=component,
                ecosystem="go",
                id=vid,
                aliases=set(o.get("aliases") or []),
                package=top.get("module", ""),
                version=top.get("version", ""),
                fixed=f.get("fixed_version", ""),
                severity="called" if vid in called else "not called",
                summary=o.get("summary", ""),
                blocks=vid in called,
            )
        )
    return out


def parse_pip_audit(component: str, text: str, severity: Callable[[str, set[str]], str]) -> list[Vuln]:
    doc = json.loads(text)
    deps = doc["dependencies"] if isinstance(doc, dict) else doc
    out = []
    for dep in deps:
        for v in dep.get("vulns") or []:
            aliases = set(v.get("aliases") or [])
            sev = severity(v["id"], aliases)
            out.append(
                Vuln(
                    component=component,
                    ecosystem="python",
                    id=v["id"],
                    aliases=aliases,
                    package=dep["name"],
                    version=dep.get("version", ""),
                    fixed=", ".join(v.get("fix_versions") or []),
                    severity=sev,
                    summary=(v.get("description") or "").strip().split("\n")[0][:120],
                    blocks=sev not in NOT_BLOCKING,
                )
            )
    return out


def parse_pnpm_audit(component: str, text: str) -> list[Vuln]:
    doc = json.loads(text)
    if "error" in doc and "advisories" not in doc:
        raise ReportError(f"{component}: pnpm audit failed: {doc['error']}")
    out = []
    for adv in (doc.get("advisories") or {}).values():
        sev = str(adv.get("severity") or "none").upper()
        ghsa = adv.get("github_advisory_id") or ""
        aliases = set(adv.get("cves") or []) | ({ghsa} if ghsa else set())
        versions = sorted({f.get("version", "") for f in adv.get("findings") or []} - {""})
        out.append(
            Vuln(
                component=component,
                ecosystem="npm",
                id=ghsa or str(adv.get("id")),
                aliases=aliases,
                package=adv.get("module_name", ""),
                version=", ".join(versions),
                fixed=adv.get("patched_versions", ""),
                severity=sev,
                summary=adv.get("title", ""),
                # an advisory pnpm gives no severity blocks, as a Python one does
                blocks=sev not in NOT_BLOCKING,
            )
        )
    return out


# ---- severities (Python) ---------------------------------------------------------------


def osv_severity(cache: dict[str, str]) -> Callable[[str, set[str]], str]:
    """The severity OSV's GitHub-reviewed record gives the id or one of its aliases."""

    def lookup(vid: str, aliases: set[str]) -> str:
        for cand in [vid, *sorted(a for a in aliases if a.startswith("GHSA-")), *sorted(aliases)]:
            if cand in cache:
                return cache[cand]
            try:
                with urllib.request.urlopen(OSV + cand, timeout=30) as r:  # noqa: S310 (fixed https host)
                    rec = json.load(r)
            except Exception:  # noqa: BLE001 (an unknown id or a failed fetch: try the next alias)
                continue
            sev = str((rec.get("database_specific") or {}).get("severity") or "").upper()
            if sev:
                cache[cand] = sev
                return sev
        return "UNKNOWN"

    return lookup


def file_severity(path: Path) -> Callable[[str, set[str]], str]:
    table = {k: str(v).upper() for k, v in json.loads(path.read_text()).items()}

    def lookup(vid: str, aliases: set[str]) -> str:
        for cand in [vid, *sorted(aliases)]:
            if cand in table:
                return table[cand]
        return "UNKNOWN"

    return lookup


# ---- suppressions -----------------------------------------------------------------------

_FRONT = re.compile(r"\A---\n(.*?)\n---\n?(.*)\Z", re.S)


def load_suppressions(folder: Path, today: dt.date) -> list[Suppression]:
    out = []
    if not folder.is_dir():
        return out
    for path in sorted(folder.glob("*.md")):
        if path.name.lower() == "readme.md":
            continue
        s = Suppression(path=path, id="", expires=None, reason="")
        m = _FRONT.match(path.read_text(encoding="utf-8"))
        if not m:
            s.problems.append("no front matter (--- id: / expires: ---)")
            out.append(s)
            continue
        meta = dict(
            (k.strip(), v.strip()) for k, _, v in (ln.partition(":") for ln in m.group(1).splitlines()) if k.strip()
        )
        s.id, s.reason = meta.get("id", ""), m.group(2).strip()
        if not s.id:
            s.problems.append("no id")
        elif path.stem != s.id:
            s.problems.append(f"the file is named {path.name}, its id is {s.id}")
        try:
            s.expires = dt.date.fromisoformat(meta.get("expires", ""))
        except ValueError:
            s.problems.append("no expires date (YYYY-MM-DD)")
        if not s.reason:
            s.problems.append("no reason (the text after the front matter)")
        if s.expires is not None:
            if s.expires < today:
                s.problems.append(f"expired on {s.expires}")
            elif (s.expires - today).days > MAX_DAYS:
                s.problems.append(f"expires {s.expires}, more than {MAX_DAYS} days away")
        out.append(s)
    return out


# ---- the gate ---------------------------------------------------------------------------


@dataclass
class Result:
    vulns: list[Vuln]
    suppressions: list[Suppression]

    def suppressed_by(self, v: Vuln) -> Suppression | None:
        for s in self.suppressions:
            if not s.problems and s.id in v.ids:
                return s
        return None

    @property
    def blocking(self) -> list[Vuln]:
        return [v for v in self.vulns if v.blocks and self.suppressed_by(v) is None]

    @property
    def bad_suppressions(self) -> list[Suppression]:
        return [s for s in self.suppressions if s.problems]

    @property
    def ok(self) -> bool:
        return not self.blocking and not self.bad_suppressions


def report(res: Result) -> str:
    lines = ["## CVE gate (story 23.7)", ""]
    if res.ok:
        lines.append("Nothing blocks.")
    for s in res.bad_suppressions:
        lines.append(f"- **suppression {s.path.name}**: {'; '.join(s.problems)}")
    rows = sorted(res.vulns, key=lambda v: (not v.blocks, v.component, v.package, v.id))
    if rows:
        lines += ["", "| | component | package | version | advisory | severity | fixed in | summary |", "|---|---|---|---|---|---|---|---|"]
    for v in rows:
        sup = res.suppressed_by(v)
        mark = "suppressed" if v.blocks and sup else ("**blocks**" if v.blocks else "reported")
        alias = ", ".join(sorted(a for a in v.aliases if a.startswith(("CVE-", "GHSA-"))))
        adv = v.id + (f" ({alias})" if alias else "")
        summary = v.summary.replace("|", "/")
        lines.append(
            f"| {mark} | {v.component} | {v.package} | {v.version} | {adv} | {v.severity} | {v.fixed or 'none'} | {summary} |"
        )
    return "\n".join(lines) + "\n"


def _pairs(values: list[str] | None, flag: str) -> list[tuple[str, Path]]:
    out = []
    for item in values or []:
        name, sep, path = item.partition("=")
        if not sep or not name or not path:
            raise SystemExit(f"{flag} takes COMPONENT=FILE, got {item!r}")
        out.append((name, Path(path)))
    return out


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--govulncheck", action="append", metavar="COMPONENT=FILE")
    ap.add_argument("--pip-audit", action="append", metavar="COMPONENT=FILE")
    ap.add_argument("--pnpm-audit", action="append", metavar="COMPONENT=FILE")
    ap.add_argument("--suppressions", type=Path, default=SUPPRESSIONS)
    ap.add_argument("--today", type=dt.date.fromisoformat, default=dt.date.today())
    ap.add_argument("--severities", type=Path, help="a JSON {id: severity} instead of asking OSV")
    ap.add_argument("--summary", type=Path, help="also write the report here (GITHUB_STEP_SUMMARY)")
    a = ap.parse_args(argv)

    severity = file_severity(a.severities) if a.severities else osv_severity({})
    vulns: list[Vuln] = []
    try:
        for name, path in _pairs(a.govulncheck, "--govulncheck"):
            vulns += parse_govulncheck(name, path.read_text(encoding="utf-8"))
        for name, path in _pairs(a.pip_audit, "--pip-audit"):
            vulns += parse_pip_audit(name, path.read_text(encoding="utf-8"), severity)
        for name, path in _pairs(a.pnpm_audit, "--pnpm-audit"):
            vulns += parse_pnpm_audit(name, path.read_text(encoding="utf-8"))
    except (OSError, ValueError, KeyError, ReportError) as e:
        print(f"cve-gate: cannot read a report: {e}", file=sys.stderr)
        return 2

    res = Result(vulns=vulns, suppressions=load_suppressions(a.suppressions, a.today))
    text = report(res)
    print(text)
    if a.summary:
        with a.summary.open("a", encoding="utf-8") as f:
            f.write(text)
    for v in res.blocking:
        print(
            f"::error title=CVE gate::{v.component}: {v.package} {v.version} has {v.id} ({v.severity}), "
            f"fixed in {v.fixed or 'no release yet'}; upgrade it or add security/suppressions/{v.id}.md"
        )
    for s in res.bad_suppressions:
        print(f"::error title=CVE gate::{s.path.relative_to(ROOT) if s.path.is_relative_to(ROOT) else s.path}: {'; '.join(s.problems)}")
    return 0 if res.ok else 1


if __name__ == "__main__":
    sys.exit(main())
