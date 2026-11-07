"""tools/runbooks/lint.py (Epic 50 story 50.12, T01-T03, T07): on the repository, and on small
broken copies so each check is known to bite."""
from __future__ import annotations

import importlib.util
import json
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("runbooks_lint", ROOT / "tools/runbooks/lint.py")
lint = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lint)


def copy(tmp: Path) -> Path:
    """The files the lint reads, copied under tmp."""
    for rel in lint.RULES + lint.STRINGS:
        (tmp / rel).parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(ROOT / rel, tmp / rel)
    shutil.copytree(ROOT / lint.RUNBOOKS, tmp / lint.RUNBOOKS)
    return tmp


def test_the_repository_passes():
    assert lint.lint(ROOT) == []


def test_t01_an_alert_without_a_severity_or_a_runbook(tmp_path):
    root = copy(tmp_path)
    p = root / lint.RULES[0]
    text = p.read_text().replace("severity: sev1", "severity: page", 1)
    text = text.replace("runbook_url: docs/runbooks/cloud/canary-down.md", "runbook_url: docs/runbooks/cloud/nope.md", 1)
    p.write_text(text)
    errs = lint.check_rules(root)
    assert any("severity 'page'" in e for e in errs), errs
    assert any("nope.md" in e for e in errs), errs


def test_t02_a_runbook_missing_a_section(tmp_path):
    root = copy(tmp_path)
    p = root / lint.RUNBOOKS / "disk.md"
    p.write_text(p.read_text().replace("## Done when", "## Finished"))
    errs = lint.check_runbooks(root)
    assert any("disk.md" in e for e in errs), errs


def test_t02_headings_inside_code_are_not_sections():
    text = "## When\n```\n## Check\n```\n## Do\n"
    assert lint.headings(text) == ["When", "Do"]


def test_t03_a_template_without_arabic(tmp_path):
    root = copy(tmp_path)
    p = root / lint.RUNBOOKS / "templates" / "identified.md"
    text = p.read_text()
    p.write_text(text[: text.index("## العربية")])
    errs = lint.check_templates(root)
    assert any("identified.md: no العربية text" in e for e in errs), errs


def test_t07_a_credit_offered_anywhere_but_the_policy(tmp_path):
    root = copy(tmp_path)
    p = root / "web/cloud/src/i18n/en.json"
    d = json.loads(p.read_text())
    d["billing.sorry"] = "Sorry for the outage: here is a credit."
    p.write_text(json.dumps(d))
    t = root / lint.RUNBOOKS / "templates" / "resolved.md"
    t.write_text(t.read_text().replace("## العربية", "We will refund you.\n\n## العربية"))
    errs = lint.check_credit(root)
    assert any("billing.sorry" in e for e in errs), errs
    assert any("resolved.md" in e and "refund" in e for e in errs), errs


def test_t07_the_policy_must_exist_in_both_languages(tmp_path):
    root = copy(tmp_path)
    p = root / "web/cloud/src/i18n/ar.json"
    d = json.loads(p.read_text())
    del d["uptime.policy"]
    p.write_text(json.dumps(d))
    errs = lint.check_credit(root)
    assert any("web/cloud/src: uptime.policy in ['en']" in e for e in errs), errs


# the postmortem reminder (story 50.12): a SEV1 resolved over 5 days ago without one is due
pm_spec = importlib.util.spec_from_file_location("postmortems", ROOT / "tools/runbooks/postmortems.py")
pm = importlib.util.module_from_spec(pm_spec)
pm_spec.loader.exec_module(pm)


def test_a_sev1_without_a_postmortem_after_5_days_is_due(tmp_path):
    from datetime import datetime, timezone

    inc = tmp_path / pm.INCIDENTS
    inc.mkdir(parents=True)

    def post(name, **fm):
        body = "\n".join(f"{k}: {v}" for k, v in fm.items())
        (inc / f"{name}.md").write_text(f"---\n{body}\n---\n\n## English\n\nx\n\n## العربية\n\nس\n")

    post("2026-11-01-relay-down", severity="sev1", state="resolved", started="2026-11-01T10:00:00Z", resolved="2026-11-01T11:00:00Z")
    post("2026-11-01-api-slow", severity="sev2", state="resolved", started="2026-11-01T10:00:00Z", resolved="2026-11-01T11:00:00Z")
    post("2026-11-05-relay-down", severity="sev1", state="resolved", started="2026-11-05T10:00:00Z", resolved="2026-11-05T11:00:00Z")
    post("2026-11-06-open", severity="sev1", state="investigating", started="2026-11-06T10:00:00Z")
    now = datetime(2026, 11, 7, 12, tzinfo=timezone.utc)
    assert pm.due(tmp_path, now) == ["2026-11-01-relay-down"]
    (tmp_path / pm.POSTMORTEMS).mkdir(parents=True)
    (tmp_path / pm.POSTMORTEMS / "2026-11-01-relay-down.md").write_text("# Postmortem\n")
    assert pm.due(tmp_path, now) == []
