"""Tests for generate_wiki.py (stdlib unittest; CI: ci.yml's wiki job).

    python3 -m unittest discover -s docs/wiki/db -p 'test_*.py'
"""

import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import generate_wiki as g  # noqa: E402


class Helpers(unittest.TestCase):
    def test_reads_the_repos_own_specs(self) -> None:
        self.assertEqual(g.SPECS, g.REPO / "specs")
        self.assertTrue((g.SPECS / "epics").is_dir())
        self.assertNotIn("/tmp", str(g.SPECS))

    def test_natural_order(self) -> None:
        ids = ["story-10.1", "story-9.2", "story-31.relay-service-door", "story-31.3"]
        self.assertEqual(sorted(ids, key=g.nat_key),
                         ["story-9.2", "story-10.1", "story-31.3", "story-31.relay-service-door"])

    def test_doc_names(self) -> None:
        self.assertEqual(g.parse_doc_name("story", "story-07-01-http-server.md"), (7, 1, "http-server"))
        self.assertEqual(g.parse_doc_name("story", "story-31-relay-service-door.md"), (31, None, "relay-service-door"))
        self.assertIsNone(g.parse_doc_name("plan", "plan-agent.md"))

    def test_epic_name_and_goal(self) -> None:
        readme = "# Epic 50: Cloud with no downtime: from one VPS\n\n> status\n\nIntro: \n\nThe plan is one VPS.\n"
        self.assertEqual(g.epic_name_from_readme(readme, "x"), "Cloud with no downtime")
        self.assertEqual(g.epic_goal(readme), "Cloud with no downtime: from one VPS.\n\nThe plan is one VPS.")
        self.assertEqual(g.epic_goal("# Epic 01 — Scanner\n\n**Goal.** Detect every\nvideo file.\n\nMore."),
                         "Detect every video file.")

    def test_inline_stories(self) -> None:
        readme = "\n".join([
            "# Epic 45: Libraries",
            "| 45.9 | not a story section | x |",
            "## Stories",
            "",
            "| # | Story | Scope |",
            "|---|---|---|",
            "| 45.1 | **Path resolver** | the folder check |",
            "- **45.2** `GET /api/libraries/manage`, conflicts;",
            "  admin role tests for each.",
            "1. **45.3 Home picker:** mDNS, by address, QR.",
            "## Acceptance",
            "- **45.4** after the section: not a story",
        ])
        got = {s["id"]: s for s in g.inline_stories(readme, 45)}
        self.assertEqual(sorted(got), ["45.1", "45.2", "45.3"])
        self.assertEqual(got["45.1"]["title"], "Path resolver")
        self.assertEqual(got["45.1"]["text"], "Path resolver. the folder check")
        self.assertEqual(got["45.2"]["title"], "`GET /api/libraries/manage`, conflicts")
        self.assertIn("admin role tests for each.", got["45.2"]["text"])
        self.assertEqual(got["45.3"]["title"], "Home picker")

    def test_short_title(self) -> None:
        long = "Schema (slot 0158): " + "word " * 30
        self.assertEqual(g.short_title(long), "Schema (slot 0158)")
        self.assertLessEqual(len(g.short_title("a" * 10 + " b" * 80)), 91)

    def test_no_owner_quotes(self) -> None:
        text = "\n\n".join([
            "# Epic 1",
            "The owner, 2026-11-03: *\"all of it\"*.",
            "The owner's decisions (2026-10-19):",
            "1. one",
            "## Design",
            "As the owner, I want a button.",
        ])
        out = g.drop_owner_quotes(text)
        self.assertNotIn("all of it", out)
        self.assertNotIn("decisions", out)
        self.assertNotIn("1. one", out)
        self.assertIn("## Design", out)
        self.assertIn("As the owner, I want a button.", out)

    def test_scrub(self) -> None:
        s = g.scrub_text("/Users/alice/x at 192.168.1.20 on mac-mini.local and maktaba.local")
        self.assertEqual(s, "~/x at [private address] on [name].local and maktaba.local")
        self.assertEqual(g.scrub({"a": ["10.0.0.1"]}), {"a": ["[private address]"]})


class Build(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.out = g.build()

    def test_committed_database_matches_the_specs(self) -> None:
        for path, text in self.out.items():
            self.assertEqual(path.read_text(encoding="utf-8"), text,
                             f"{path} is stale: run python3 docs/wiki/db/generate_wiki.py")

    def test_every_epic_folder_is_in_the_database(self) -> None:
        wiki = json.loads(self.out[g.OUT_DB / "wiki.json"])
        entries = list(wiki["entries"])
        for p in wiki["parts"]:
            entries += json.loads(self.out[g.OUT_DB / p])["entries"]
        epics = {e["epic"] for e in entries if e["type"] == "epic"}
        dirs = {d.name for d in (g.SPECS / "epics").iterdir() if d.is_dir() and d.name[:2].isdigit()}
        self.assertEqual(epics, dirs)
        self.assertEqual(len({e["id"] for e in entries}), len(entries))
        for e in entries:
            for r in e.get("related", []):
                self.assertNotEqual(r, e["id"])

    def test_the_hand_kept_data_is_merged(self) -> None:
        wiki = json.loads(self.out[g.OUT_DB / "wiki.json"])
        ids = {e["id"] for e in wiki["entries"]}
        self.assertIn("phase-01", ids)
        self.assertIn("feature-wiki-glossary", ids)
        part = json.loads(self.out[g.OUT_DB / "wiki-epics-01-09.json"])
        story = next(e for e in part["entries"] if e["id"] == "story-1.1")
        self.assertEqual(story["linear"], "HLB-5")


if __name__ == "__main__":
    unittest.main()
