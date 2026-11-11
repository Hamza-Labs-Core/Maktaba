"""The update rehearsal's container legs (story 59.12): the harness's own checks.

Every leg in update_legs.json names steps and assertions update_leg.py has, runs on the
architectures the workflow gives it, and the line the harness waits for is the one the
entrypoint writes. Standard library only (nas.yml's plan job runs these on the runner).
"""

from __future__ import annotations

import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE))

import update_leg  # noqa: E402

WORKFLOW = ROOT / ".github/workflows/update-rehearsal.yml"
CONTAINER_GO = ROOT / "cmd/maktaba-server/cmd_container.go"


class Legs(unittest.TestCase):
    def setUp(self) -> None:
        self.legs = update_leg.load_legs()["legs"]

    def test_the_story_legs(self) -> None:
        self.assertEqual([leg["name"] for leg in self.legs],
                         ["nas-568", "nas-4g", "nas-arm64", "nas-restart", "nas-image-older"])

    def test_every_leg_updates_rolls_back_and_restores(self) -> None:
        for leg in self.legs:
            steps = leg["steps"]
            self.assertEqual(steps[:1], ["update"], leg["name"])
            for s in ("rollback", "restore"):
                self.assertIn(s, steps, leg["name"])
            self.assertLess(steps.index("rollback"), steps.index("restore"), leg["name"])

    def test_every_step_and_assertion_exists(self) -> None:
        for leg in self.legs:
            for a in leg["steps"] + leg.get("asserts", []):
                self.assertTrue(hasattr(update_leg.UpdateLeg, "assert_" + a), f"{leg['name']}: no assert_{a}")

    def test_the_shapes(self) -> None:
        by = {leg["name"]: leg for leg in self.legs}
        self.assertEqual(by["nas-568"]["user"], "568:568")
        self.assertTrue(by["nas-568"]["media"]["ro"])
        self.assertEqual(by["nas-568"]["data"]["mode"][-2], "7")  # group-writable
        self.assertIn("--memory=4g", by["nas-4g"]["docker"])
        self.assertEqual(by["nas-4g"]["items"], 20000)
        self.assertEqual(by["nas-arm64"]["arches"], ["arm64"])
        self.assertEqual(by["nas-restart"]["restart_at"], ["staged", "moved", "before_confirm"])
        self.assertIn("restart", by["nas-restart"]["steps"])
        self.assertIn("older", by["nas-image-older"]["steps"])

    def test_the_workflow_runs_every_leg_on_its_architectures(self) -> None:
        text = WORKFLOW.read_text(encoding="utf-8")
        got = set(re.findall(r"- \{ leg: ([\w-]+), arch: (\w+), runner: [\w.-]+ \}", text))
        want = {(leg["name"], a) for leg in self.legs for a in leg["arches"]}
        self.assertEqual(got, want)
        for job in ("nas-images", "update-rehearsal-container"):
            self.assertRegex(text, rf"for j in kit seed scenario[\w -]*\b{job}\b", f"the verdict lacks {job}")


class Lines(unittest.TestCase):
    def test_the_older_line_is_the_entrypoints(self) -> None:
        go = CONTAINER_GO.read_text(encoding="utf-8")
        fmt = re.search(r'"(The container image \(%s\) is older[^"]+)"', go)
        assert fmt is not None, "cmd_container.go no longer says the older image's line"
        self.assertEqual(fmt.group(1).replace("%s", "{}").format("1.4.0", "1.5.0"),
                         update_leg.older_line("1.4.0", "1.5.0"))

    def test_the_lines_waited_for(self) -> None:
        go = CONTAINER_GO.read_text(encoding="utf-8")
        for needle in ("putting Maktaba %s in", "Maktaba %s replaces", "Maktaba %s runs: kept",
                       "Maktaba %s could not start here: the launcher went back",
                       "this image's Maktaba %s could not start here before"):
            self.assertIn(needle, go)

    def test_versions_in_order(self) -> None:
        self.assertTrue(update_leg.order_ok(["v0.3.122", "v0.3.123-4-gabcdef1234",
                                             "v0.3.123-1004-gabcdef1234", "v0.3.123-2004-gabcdef1234"]))
        self.assertTrue(update_leg.order_ok(["v0.3.122", "v0.3.123", "v0.3.123-1000-gab12", "v0.3.123-2000-gab12"]))
        self.assertFalse(update_leg.order_ok(["v0.3.123", "v0.3.122-4-gab12", "v0.3.123-1000-gab12", "v0.3.124"]))
        self.assertFalse(update_leg.order_ok(["dev", "v0.3.123", "v0.3.124", "v0.3.125"]))


class Bulk(unittest.TestCase):
    def test_distinct_files(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "en").mkdir()
            (Path(d) / "en" / "Lesson 01.mp4").write_bytes(b"\0" * 64)
            subprocess.run(["sh", "-c", update_leg.bulk_script(d, 1203)], check=True)
            files = sorted((Path(d) / update_leg.BULK).rglob("*.mp4"))
            self.assertEqual(len(files), 1203)
            self.assertEqual(len({f.read_bytes() for f in files}), 1203)
            self.assertLessEqual(max(len(list(p.iterdir())) for p in (Path(d) / update_leg.BULK).iterdir()), 1000)


if __name__ == "__main__":
    unittest.main()
