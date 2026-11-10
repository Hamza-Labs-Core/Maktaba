"""The nas-shapes harness's own tests (story 59.15): the matrix, the plan, the profiles, the
checklists' lint. Standard library only: nas.yml's plan job runs them with the runner's python3.

    python3 -m unittest discover -s tests/nas -p 'test_*.py' -v
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import cell  # noqa: E402
import check_checklists  # noqa: E402
import plan  # noqa: E402
import summary  # noqa: E402

MATRIX = cell.load_cells()
# Every story whose cells and assertions the harness knows (README §7 phases); a new needs
# value is a typo unless it is added here with its cell.
KNOWN_NEEDS = {"59.01", "59.02", "59.03", "59.04", "59.05", "59.06", "59.07", "59.08", "pg-stale-lock", "scan-grant-race", "plugin-cache-grant", "poster-queue"}
# The phase-1 cells story 59.15 lists (README §9): each must be in the matrix
PHASE_ONE = {"uid-1000", "uid-568", "uid-unraid", "uid-dsm", "media-ro", "media-none", "media-foreign",
             "mem-4g", "mem-6g", "no-landlock-enosys", "no-landlock-eperm"}


class Matrix(unittest.TestCase):
    def test_names_are_unique_and_docker_safe(self) -> None:
        names = [c["name"] for c in MATRIX["cells"]]
        self.assertEqual(len(names), len(set(names)))
        for n in names:
            self.assertRegex(n, r"^[a-z0-9][a-z0-9.-]*$")

    def test_phase_one_cells_are_there(self) -> None:
        self.assertLessEqual(PHASE_ONE, {c["name"] for c in MATRIX["cells"]})

    def test_uid_cells_run_on_both_architectures(self) -> None:
        for c in MATRIX["cells"]:
            if c["name"] in ("uid-1000", "uid-568", "uid-unraid", "uid-dsm"):
                self.assertEqual(sorted(c["arches"]), ["amd64", "arm64"], c["name"])

    def test_every_assertion_exists(self) -> None:
        for c in MATRIX["cells"]:
            self.assertTrue(c.get("asserts"), c["name"])
            for a in c["asserts"]:
                name = cell.assertion_name(a)
                pending = cell.assertion_needs(a) or c.get("needs")
                self.assertTrue(callable(getattr(cell.Cell, "assert_" + name, None)) or pending,
                                f"{c['name']}: no assert_{name} (only a pending assertion may wait for its code)")

    def test_needs_are_known_and_landed_is_a_subset(self) -> None:
        self.assertLessEqual(set(MATRIX["landed"]), KNOWN_NEEDS)
        for c in MATRIX["cells"]:
            needs = set(c.get("needs", []))
            for a in c["asserts"]:
                needs |= set(cell.assertion_needs(a))
            self.assertLessEqual(needs, KNOWN_NEEDS, c["name"])

    def test_every_landed_assertion_has_its_code(self) -> None:
        landed = set(MATRIX["landed"])
        for c in MATRIX["cells"]:
            if not set(c.get("needs", [])) <= landed:
                continue
            for a in c["asserts"]:
                if set(cell.assertion_needs(a)) <= landed:
                    self.assertTrue(hasattr(cell.Cell, "assert_" + cell.assertion_name(a)),
                                    f"{c['name']}: {cell.assertion_name(a)} landed with no code")

    def test_seccomp_profiles_exist(self) -> None:
        for c in MATRIX["cells"]:
            if c.get("seccomp"):
                self.assertTrue((HERE / c["seccomp"]).is_file(), c["seccomp"])

    def test_refused_cells_assert_a_refusal(self) -> None:
        for c in MATRIX["cells"]:
            if c.get("expect") == "refused":
                self.assertTrue(all(cell.assertion_name(a).startswith("refused_") for a in c["asserts"]), c["name"])


class Plan(unittest.TestCase):
    def test_a_cell_waits_for_its_story(self) -> None:
        m = {"landed": [], "cells": [{"name": "a", "arches": ["amd64"]}, {"name": "b", "needs": ["59.01"]}]}
        self.assertEqual([e["cell"] for e in plan.plan(m)], ["a"])
        m["landed"] = ["59.01"]
        self.assertEqual([e["cell"] for e in plan.plan(m)], ["a", "b"])

    def test_arm64_runner_and_no_arm64(self) -> None:
        m = {"landed": [], "cells": [{"name": "a", "arches": ["amd64", "arm64"]}]}
        self.assertEqual(plan.plan(m), [{"cell": "a", "arch": "amd64", "runner": "ubuntu-24.04"},
                                        {"cell": "a", "arch": "arm64", "runner": "ubuntu-24.04-arm"}])
        self.assertEqual([e["arch"] for e in plan.plan(m, arm64=False)], ["amd64"])

    def test_only(self) -> None:
        self.assertEqual({e["cell"] for e in plan.plan(MATRIX, only={"uid-1000"})}, {"uid-1000"})

    def test_the_real_matrix_has_a_baseline(self) -> None:
        cells = {(e["cell"], e["arch"]) for e in plan.plan(MATRIX)}
        self.assertIn(("uid-1000", "amd64"), cells)
        self.assertIn(("uid-1000", "arm64"), cells)


class Pending(unittest.TestCase):
    def test_an_assertion_whose_story_has_not_landed_is_pending(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x"}, image="img", fixtures=Path(d), out=Path(d), local_port=None, landed=[])
            c.check({"name": "tier_light", "needs": ["59.04"]})
            self.assertEqual(c.results[-1]["ok"], None)
            self.assertIn("59.04", c.results[-1]["detail"])

    def test_a_pending_cell_passes_without_docker(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x", "needs": ["59.01"], "asserts": ["ready"]}, image="img",
                          fixtures=Path(d), out=Path(d) / "out", local_port=None, landed=[])
            self.assertTrue(c.run())
            report = json.loads((Path(d) / "out" / "report.json").read_text(encoding="utf-8"))
            self.assertIsNone(report["results"][0]["ok"])

    def test_an_unknown_assertion_fails(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x"}, image="img", fixtures=Path(d), out=Path(d), local_port=None, landed=[])
            c.check("no_such_thing")
            self.assertIs(c.results[-1]["ok"], False)


class Summary(unittest.TestCase):
    def test_rows_link_each_cell_to_its_logs(self) -> None:
        jobs = summary.objects('{"jobs": [{"name": "cell uid-568 (arm64)", "conclusion": "failure", "html_url": "u"},'
                               ' {"name": "plan", "conclusion": "success"}]}\n{"jobs": [{"name": "cell mem-3.7g (amd64)",'
                               ' "conclusion": "success", "html_url": "v"}]}')
        arts = [{"name": "nas-cell-uid-568-arm64", "id": 7}]
        got = summary.rows([j for d in jobs for j in d["jobs"]], arts, "o/r", "9")
        self.assertEqual(got, ["| [cell mem-3.7g (amd64)](v) | success | none |",
                               "| [cell uid-568 (arm64)](u) | failure | [logs](https://github.com/o/r/actions/runs/9/artifacts/7) |"])


class Seccomp(unittest.TestCase):
    def load(self, name: str) -> dict:
        return json.loads((HERE / f"seccomp-no-landlock-{name}.json").read_text(encoding="utf-8"))

    def rule(self, profile: dict, syscall: str) -> dict:
        return next(r for r in profile["syscalls"] if syscall in r["names"])

    def test_landlock_answers(self) -> None:
        for name, errno in (("enosys", 38), ("eperm", 1)):
            p = self.load(name)
            self.assertEqual(p["defaultAction"], "SCMP_ACT_ALLOW")
            for sc in ("landlock_create_ruleset", "landlock_add_rule", "landlock_restrict_self"):
                r = self.rule(p, sc)
                self.assertEqual((r["action"], r["errnoRet"]), ("SCMP_ACT_ERRNO", errno), (name, sc))

    def test_no_user_namespaces(self) -> None:
        for name in ("enosys", "eperm"):
            p = self.load(name)
            self.assertEqual(self.rule(p, "unshare")["errnoRet"], 1)
            clone = self.rule(p, "clone")
            self.assertEqual(clone["args"][0]["value"], 0x10000000)  # CLONE_NEWUSER
            self.assertEqual(self.rule(p, "clone3")["errnoRet"], 38)


class Checklists(unittest.TestCase):
    STEP = "### 1. Do a thing\n- **Do:** press it\n- **See:** it is pressed\n- **Screenshots:** {shots}\n"

    def write(self, d: Path, body: str, status: str = "draft") -> None:
        (d / "x.md").write_text(f"---\nplatform: x\ndevices: one\nstatus: {status}\n---\n\n# X\n\n{body}",
                                encoding="utf-8")

    def test_the_real_checklists_pass(self) -> None:
        self.assertEqual(check_checklists.check_dir(HERE.parents[1] / "docs" / "nas" / "checklists"), [])

    def test_a_step_without_its_expected_result_fails(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, "### 1. Do a thing\n- **Do:** press it\n- **Screenshots:** none\n")
            self.assertTrue(any("no See" in e for e in check_checklists.check_dir(d)))

    def test_screenshots_come_in_both_languages(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="`x/1.en.png`"))
            self.assertTrue(any("pairs" in e for e in check_checklists.check_dir(d)))
            self.write(d, self.STEP.format(shots="`x/1.en.png`, `x/1.ar.png`"))
            self.assertEqual(check_checklists.check_dir(d), [])

    def test_a_ready_checklist_has_its_screenshots(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="`x/1.en.png`, `x/1.ar.png`"), status="ready")
            self.assertEqual(len(check_checklists.check_dir(d)), 2)
            (d / "screenshots" / "x").mkdir(parents=True)
            for lang in ("en", "ar"):
                (d / "screenshots" / "x" / f"1.{lang}.png").write_bytes(b"\x89PNG")
            self.assertEqual(check_checklists.check_dir(d), [])

    def test_steps_are_numbered_without_a_gap(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="none") + self.STEP.format(shots="none").replace("### 1.", "### 3."))
            self.assertTrue(any("expected 2" in e for e in check_checklists.check_dir(d)))


if __name__ == "__main__":
    unittest.main()
