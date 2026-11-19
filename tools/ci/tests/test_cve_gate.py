"""Story 23.7 AC2, TC2, EC1: the CVE gate (tools/security/cve_gate.py) blocks a called Go
vulnerability, a high or critical Python or npm one, and lets a recorded, unexpired
suppression through; an expired, undated, unreasoned or too-distant suppression fails it.

The reports are small copies of what govulncheck -json, pip-audit -f json and pnpm audit
--json write (shapes read from real runs on this repository).
"""

from __future__ import annotations

import datetime as dt
import json
import sys
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "security"))

import cve_gate  # noqa: E402

TODAY = dt.date(2026, 11, 19)

# TC2's pin: golang.org/x/net at an old version with a known vulnerability the code calls
GOVULN_CALLED = "\n".join(
    json.dumps(o, indent=2)
    for o in [
        {"config": {"protocol_version": "v1.0.0", "scanner_name": "govulncheck"}},
        {
            "osv": {
                "id": "GO-2026-6611",
                "aliases": ["CVE-2026-78669"],
                "summary": "HTTP/2 server crash in golang.org/x/net/http2",
            }
        },
        {
            "osv": {"id": "GO-2026-0001", "aliases": ["CVE-2026-00001"], "summary": "imported, never called"}
        },
        # a module-level match first, then the called one (govulncheck streams both)
        {
            "finding": {
                "osv": "GO-2026-6611",
                "fixed_version": "v0.60.0",
                "trace": [{"module": "golang.org/x/net", "version": "v0.58.0"}],
            }
        },
        {
            "finding": {
                "osv": "GO-2026-6611",
                "fixed_version": "v0.60.0",
                "trace": [
                    {"module": "golang.org/x/net", "version": "v0.58.0", "package": "golang.org/x/net/http2", "function": "ServeConn"},
                    {"module": "github.com/Hamza-Labs-Core/Maktaba/streaming", "package": "main", "function": "main"},
                ],
            }
        },
        {
            "finding": {
                "osv": "GO-2026-0001",
                "fixed_version": "v1.2.0",
                "trace": [{"module": "example.com/lib", "version": "v1.0.0", "package": "example.com/lib"}],
            }
        },
    ]
)

PIP = {
    "dependencies": [
        {"name": "dnspython", "version": "2.9.0", "vulns": [{"id": "GHSA-vvf4-9x63-8xvv", "fix_versions": ["2.9.2"], "aliases": ["CVE-2026-128089"], "description": "dnspython has excessive memory use"}]},
        {"name": "opentelemetry-api", "version": "1.45.1", "vulns": [{"id": "GHSA-mf5v-w3w3-f6cg", "fix_versions": ["1.45.2"], "aliases": ["CVE-2026-126360"], "description": "quadratic parse"}]},
        {"name": "mystery", "version": "1.0", "vulns": [{"id": "PYSEC-2026-9999", "fix_versions": [], "aliases": [], "description": "no severity anywhere"}]},
        {"name": "clean", "version": "1.0", "vulns": []},
    ],
    "fixes": [],
}
SEVERITIES = {"GHSA-vvf4-9x63-8xvv": "HIGH", "GHSA-mf5v-w3w3-f6cg": "MODERATE"}

PNPM = {
    "advisories": {
        "1": {"id": 1, "github_advisory_id": "GHSA-xffm-g5w8-qvg7", "severity": "low", "module_name": "@eslint/plugin-kit", "patched_versions": ">=0.3.4", "title": "ReDoS", "cves": [], "findings": [{"version": "0.3.1"}]},
        "2": {"id": 2, "github_advisory_id": "GHSA-aaaa-bbbb-cccc", "severity": "high", "module_name": "left-pad", "patched_versions": ">=1.3.1", "title": "prototype pollution", "cves": ["CVE-2026-11111"], "findings": [{"version": "1.3.0"}]},
    },
    "metadata": {"vulnerabilities": {"low": 1, "high": 1}},
}


def write_reports(tmp: Path) -> list[str]:
    (tmp / "go.json").write_text(GOVULN_CALLED)
    (tmp / "pip.json").write_text(json.dumps(PIP))
    (tmp / "pnpm.json").write_text(json.dumps(PNPM))
    (tmp / "sev.json").write_text(json.dumps(SEVERITIES))
    (tmp / "sup").mkdir(exist_ok=True)
    return [
        "--govulncheck", f"streaming={tmp / 'go.json'}",
        "--pip-audit", f"pipeline={tmp / 'pip.json'}",
        "--pnpm-audit", f"web={tmp / 'pnpm.json'}",
        "--severities", str(tmp / "sev.json"),
        "--suppressions", str(tmp / "sup"),
        "--today", TODAY.isoformat(),
    ]


def suppress(tmp: Path, vid: str, expires: str | None = "2027-01-31", reason: str = "Not reachable: see the issue.", name: str | None = None) -> None:
    front = f"id: {vid}\n" + (f"expires: {expires}\n" if expires else "")
    (tmp / "sup" / f"{name or vid}.md").write_text(f"---\n{front}---\n{reason}\n")


def blocking_ids(capsys: pytest.CaptureFixture[str]) -> set[str]:
    out = capsys.readouterr().out
    return {ln.split(" has ", 1)[1].split(" ", 1)[0] for ln in out.splitlines() if ln.startswith("::error title=CVE gate::") and " has " in ln}


def test_tc2_a_called_go_vulnerability_blocks_and_an_uncalled_one_is_only_reported(tmp_path: Path) -> None:
    (tmp_path / "go.json").write_text(GOVULN_CALLED)
    vulns = cve_gate.parse_govulncheck("streaming", GOVULN_CALLED)
    by_id = {v.id: v for v in vulns}
    assert by_id["GO-2026-6611"].blocks
    assert by_id["GO-2026-6611"].package == "golang.org/x/net"
    assert by_id["GO-2026-6611"].version == "v0.58.0"
    assert by_id["GO-2026-6611"].fixed == "v0.60.0"
    assert "CVE-2026-78669" in by_id["GO-2026-6611"].ids
    assert not by_id["GO-2026-0001"].blocks


def test_the_gate_blocks_called_go_high_python_unknown_python_and_high_npm(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    rc = cve_gate.main(write_reports(tmp_path))
    assert rc == 1
    # MODERATE Python and low npm are reported, not blocking; an id with no severity
    # anywhere blocks (fail closed)
    assert blocking_ids(capsys) == {"GO-2026-6611", "GHSA-vvf4-9x63-8xvv", "PYSEC-2026-9999", "GHSA-aaaa-bbbb-cccc"}


def test_a_recorded_unexpired_suppression_lets_its_vulnerability_through(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    args = write_reports(tmp_path)
    # by an alias (the CVE), by the GHSA id, by the PYSEC id
    suppress(tmp_path, "CVE-2026-78669")
    suppress(tmp_path, "GHSA-vvf4-9x63-8xvv")
    suppress(tmp_path, "PYSEC-2026-9999")
    suppress(tmp_path, "CVE-2026-11111")
    assert cve_gate.main(args) == 0
    out = capsys.readouterr().out
    assert "Nothing blocks." in out
    assert out.count("| suppressed |") == 4


@pytest.mark.parametrize(
    ("expires", "reason", "name", "problem"),
    [
        ("2026-11-18", "Not reachable.", None, "expired on 2026-11-18"),
        (None, "Not reachable.", None, "no expires date"),
        ("2027-01-31", "", None, "no reason"),
        ("2027-12-31", "Not reachable.", None, "more than 180 days away"),
        ("2027-01-31", "Not reachable.", "CVE-2026-1", "its id is GHSA-vvf4-9x63-8xvv"),
    ],
)
def test_ec1_an_invalid_or_expired_suppression_fails_the_gate(
    tmp_path: Path, capsys: pytest.CaptureFixture[str], expires: str | None, reason: str, name: str | None, problem: str
) -> None:
    args = write_reports(tmp_path)
    for vid in ("CVE-2026-78669", "PYSEC-2026-9999", "CVE-2026-11111"):
        suppress(tmp_path, vid)
    suppress(tmp_path, "GHSA-vvf4-9x63-8xvv", expires=expires, reason=reason, name=name)
    assert cve_gate.main(args) == 1
    out = capsys.readouterr().out
    assert problem in out
    # the invalid suppression covers nothing: its vulnerability blocks again
    assert "GHSA-vvf4-9x63-8xvv (HIGH)" in out


def test_an_expired_suppression_fails_even_when_nothing_is_reported_any_more(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    (tmp_path / "sup").mkdir()
    suppress(tmp_path, "GHSA-gone-gone-gone", expires="2026-10-01")
    (tmp_path / "pnpm.json").write_text(json.dumps({"advisories": {}, "metadata": {}}))
    rc = cve_gate.main(["--pnpm-audit", f"web={tmp_path / 'pnpm.json'}", "--suppressions", str(tmp_path / "sup"), "--today", TODAY.isoformat()])
    assert rc == 1
    assert "expired on 2026-10-01" in capsys.readouterr().out


def test_a_report_that_cannot_be_read_is_exit_2(tmp_path: Path) -> None:
    (tmp_path / "pnpm.json").write_text(json.dumps({"error": {"code": "ERR_PNPM_AUDIT_BAD_RESPONSE"}}))
    assert cve_gate.main(["--pnpm-audit", f"web={tmp_path / 'pnpm.json'}", "--suppressions", str(tmp_path)]) == 2
    (tmp_path / "pip.json").write_text("not json")
    assert cve_gate.main(["--pip-audit", f"pipeline={tmp_path / 'pip.json'}", "--suppressions", str(tmp_path), "--severities", str(tmp_path / "pnpm.json")]) == 2


def test_the_repository_suppressions_are_valid_today() -> None:
    sups = cve_gate.load_suppressions(cve_gate.SUPPRESSIONS, dt.date.today())
    assert [s.path.name for s in sups if s.problems] == []


def test_ci_runs_the_gate_on_every_push_to_main_and_in_ci_success() -> None:
    ci = yaml.safe_load((ROOT / ".github/workflows/ci.yml").read_text())
    job = ci["jobs"]["cve-gate"]
    assert job["uses"] == "./.github/workflows/_cve-gate.yml"
    assert "cve-gate" in ci["jobs"]["ci-success"]["needs"]
    wf = yaml.safe_load((ROOT / ".github/workflows/_cve-gate.yml").read_text())
    steps = wf["jobs"]["cve-gate"]["steps"]
    setup_go = [s for s in steps if str(s.get("uses", "")).startswith("actions/setup-go@")]
    # the Go that builds the release (release.yml's setup-go reads api/go.mod too)
    assert setup_go and setup_go[0]["with"]["go-version-file"] == "api/go.mod"
    assert any("tools/security/cve-gate.sh" in str(s.get("run", "")) for s in steps)
