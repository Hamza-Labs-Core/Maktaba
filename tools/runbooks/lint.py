#!/usr/bin/env python3
"""The cloud runbooks' checks (Epic 50 story 50.12):

T01  every alert rule has a severity (sev1, sev2, sev3) and a runbook_url that exists under
     docs/runbooks/cloud/
T02  every runbook has When, Check, Do, Done when and Tell users, in that order
T03  every template for users has an English and an Arabic title and text
T07  no template or user-facing string offers a credit, refund or compensation (only the
     uptime.policy string may say there is none), and uptime.policy exists in English and
     Arabic for the status page and the cloud web app

    python3 tools/runbooks/lint.py [repo root]

tools/ci/tests/test_runbooks_lint.py runs it on the repository in the lint job.
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

import yaml

RULES = ["deploy/cloud-ha/monitoring/alerts.yml", "deploy/cloud-ha/monitoring/backup-rules.yml"]
RUNBOOKS = "docs/runbooks/cloud"
NOT_RUNBOOKS = {"README.md", "postmortem-template.md"}
SECTIONS = ["When", "Check", "Do", "Done when", "Tell users"]
SEVERITIES = {"sev1", "sev2", "sev3"}
# strings users read: the status page's and the cloud web app's
STRINGS = ["status/strings.json", "web/cloud/src/i18n/en.json", "web/cloud/src/i18n/ar.json"]
POLICY = "uptime.policy"
CREDIT = re.compile(r"\b(credit|refund|compensat)|رصيد|استرداد|تعويض", re.I)


def headings(text: str) -> list[str]:
    """The `## ` headings outside fenced code."""
    out, fenced = [], False
    for line in text.splitlines():
        if line.startswith("```"):
            fenced = not fenced
        elif not fenced and line.startswith("## "):
            out.append(line[3:].strip())
    return out


def check_rules(root: Path) -> list[str]:
    errs = []
    for rel in RULES:
        doc = yaml.safe_load((root / rel).read_text())
        for group in doc.get("groups", []):
            for rule in group.get("rules", []):
                name = rule.get("alert")
                if not name:
                    continue  # a recording rule
                sev = (rule.get("labels") or {}).get("severity")
                if sev not in SEVERITIES:
                    errs.append(f"{rel}: {name}: severity {sev!r}, want one of sev1, sev2, sev3")
                url = (rule.get("annotations") or {}).get("runbook_url", "")
                if not url.startswith(RUNBOOKS + "/") or not (root / url).is_file():
                    errs.append(f"{rel}: {name}: runbook_url {url!r} is not a file under {RUNBOOKS}/")
    return errs


def check_runbooks(root: Path) -> list[str]:
    errs = []
    files = sorted(p for p in (root / RUNBOOKS).glob("*.md") if p.name not in NOT_RUNBOOKS)
    if not files:
        errs.append(f"{RUNBOOKS}: no runbooks")
    for p in files:
        hs = [h for h in headings(p.read_text()) if h in SECTIONS]
        if hs != SECTIONS:
            errs.append(f"{p.relative_to(root)}: sections {hs}, want {SECTIONS} in that order")
    return errs


def section(text: str, name: str) -> str:
    m = re.search(rf"^## {re.escape(name)}\s*\n(.*?)(?=^## |\Z)", text, re.M | re.S)
    return m.group(1).strip() if m else ""


def check_templates(root: Path) -> list[str]:
    errs = []
    files = sorted((root / RUNBOOKS / "templates").glob("*.md"))
    if not files:
        errs.append(f"{RUNBOOKS}/templates: no templates")
    for p in files:
        text = p.read_text()
        rel = p.relative_to(root)
        fm = re.match(r"---\n(.*?)\n---\n", text, re.S)
        if not fm:
            errs.append(f"{rel}: no front matter at the top")
            continue
        keys = dict(line.split(":", 1) for line in fm.group(1).splitlines() if ":" in line)
        for k in ("title_en", "title_ar"):
            if not keys.get(k, "").strip():
                errs.append(f"{rel}: {k} is missing")
        if keys.get("title_ar") and not re.search(r"[؀-ۿ]", keys["title_ar"]):
            errs.append(f"{rel}: title_ar is not Arabic")
        body = text[fm.end():]
        for name in ("English", "العربية"):
            if not section(body, name):
                errs.append(f"{rel}: no {name} text")
        if section(body, "العربية") and not re.search(r"[؀-ۿ]", section(body, "العربية")):
            errs.append(f"{rel}: the Arabic section is not Arabic")
    return errs


def strings_of(path: Path) -> dict[str, str]:
    """A strings file's key -> text, for every language in it."""
    doc = json.loads(path.read_text())
    out = {}
    if all(isinstance(v, dict) for v in doc.values()):  # {"en": {...}, "ar": {...}}
        for lang, table in doc.items():
            for k, v in table.items():
                out[f"{lang}:{k}"] = v
    else:
        out.update(doc)
    return out


def check_credit(root: Path) -> list[str]:
    errs = []
    for p in sorted((root / RUNBOOKS / "templates").glob("*.md")):
        for m in CREDIT.finditer(p.read_text()):
            errs.append(f"{p.relative_to(root)}: {m.group(0)!r}: no template offers a credit or refund")
    policy_langs: dict[str, set[str]] = {}
    for rel in STRINGS:
        path = root / rel
        if not path.exists():
            errs.append(f"{rel}: missing")
            continue
        for key, text in strings_of(path).items():
            bare = key.split(":", 1)[-1]
            if bare == POLICY:
                lang = key.split(":", 1)[0] if ":" in key else ("ar" if rel.endswith("ar.json") else "en")
                policy_langs.setdefault(rel.split("/i18n/")[0] if "/i18n/" in rel else rel, set()).add(lang)
                continue
            if isinstance(text, str) and CREDIT.search(text):
                errs.append(f"{rel}: {bare}: {CREDIT.search(text).group(0)!r}: only uptime.policy may speak of credits")
    for where in ("status/strings.json", "web/cloud/src"):
        if policy_langs.get(where, set()) != {"en", "ar"}:
            errs.append(f"{where}: uptime.policy in {sorted(policy_langs.get(where, set()))}, want English and Arabic")
    return errs


def lint(root: Path) -> list[str]:
    return check_rules(root) + check_runbooks(root) + check_templates(root) + check_credit(root)


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else Path(__file__).resolve().parents[2])
    errs = lint(root)
    for e in errs:
        print(f"runbooks: {e}", file=sys.stderr)
    if not errs:
        print("runbooks: the alerts, runbooks, templates and strings are in order")
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
