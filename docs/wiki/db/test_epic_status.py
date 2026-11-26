"""Tests for epic_status.py, the derived counts on docs/wiki/epic-status.md
(stdlib unittest; CI: ci.yml's wiki job).

    python3 -m unittest discover -s docs/wiki/db -p 'test_*.py'
"""

import io
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import epic_status as es  # noqa: E402

# A two-epic page in the real page's shape, its counts all stale.
PAGE = """\
# Epic Status

**Not every epic is done.** Of 9 stories across all 9 epics, 9 are done, 9 are partial, 9 are not started, and 9 were deferred or removed on purpose. Epic 02 is the open one.

## Summary

| Epic | Done | Partial | Not started | Deferred / removed | Verdict |
|---|---:|---:|---:|---:|---|
| 01 Scanner | 9* | 9 | 9 | 9 | Done |
| 02 Search | 9 | 9 | 9 | 9 | **Partial** (hand-written verdict) |
| **Total** | **9** | **9** | **9** | **9** | **Not all done** |

## What is left

### Not started

- **02.3 BM25** — nothing yet.
- **02.4–02.5, Phase 2:** both not built.

### Partial

| Story | Missing |
|---|---|
| 02.02 Suggestions | The facets. |

### Deferred or removed

| Story | Why |
|---|---|
| 02.R Old relay | Removed by #969. |

## Epics 01–25

### Epic 01 — Scanner

| Story | Status | Evidence |
|---|---|---|
| 01.01 File discovery | Done (sampled) | E01 |
| 01.02 Content identity | Done (sampled) | E01 |

### Epic 02 — Search

| Story | Status | Evidence |
|---|---|---|
| 02.1 Bundles | **Done** (#1441) | x |
| 02.2 Suggestions | **partial** | y |
| 02.3 BM25 | **Not started** | z |
| 02.4 Stage | **not started** | z |
| 02.5 Query | **not started** | z |
| 02.R Old relay | **removed by #969** | w |
"""


def fixed(page: str = PAGE) -> str:
    new, errors = es.render(page)
    if errors:
        raise AssertionError(errors)
    return new


class Counts(unittest.TestCase):
    def test_summary_rows_and_total_come_from_the_rows(self) -> None:
        new = fixed()
        self.assertIn("| 01 Scanner | 2* | 0 | 0 | 0 | Done |", new)
        self.assertIn("| 02 Search | 1 | 1 | 3 | 1 | **Partial** (hand-written verdict) |", new)
        self.assertIn("| **Total** | **3** | **1** | **3** | **1** | **Not all done** |", new)

    def test_the_sentence_comes_from_the_rows_and_keeps_its_prose(self) -> None:
        new = fixed()
        self.assertIn("**Not every epic is done.** Of 8 stories across all 2 epics, 3 are done, "
                      "1 are partial, 3 are not started, and 1 were deferred or removed on "
                      "purpose. Epic 02 is the open one.", new)

    def test_a_rewrite_is_stable_and_changes_only_numbers(self) -> None:
        new = fixed()
        self.assertEqual(fixed(new), new)
        changed = [(a, b) for a, b in zip(PAGE.split("\n"), new.split("\n")) if a != b]
        self.assertEqual(len(changed), 4)  # the sentence, two epics, the total
        strip = str.maketrans("", "", "0123456789*")
        for a, b in changed:
            self.assertEqual(a.translate(strip), b.translate(strip))

    def test_a_status_qualifier_and_case_do_not_matter(self) -> None:
        for cell in ("Done", "**Done**", "Done (device checks unverified)", "**Done** (#1445)"):
            self.assertEqual(es.status_of(cell), es.DONE, cell)
        self.assertEqual(es.status_of("**partial (acceptance target unmet)**"), es.PARTIAL)
        self.assertEqual(es.status_of("**deferred by design (removed)**"), es.DEFERRED)
        self.assertEqual(es.status_of("**Removed**"), es.DEFERRED)

    def test_emphasized_story_header_with_mixed_case(self) -> None:
        page = PAGE.replace("| Story |", "| **sToRy** |")
        self.assertIn("| 01 Scanner | 2* | 0 | 0 | 0 | Done |", fixed(page))

    def test_indented_story_rows(self) -> None:
        page = PAGE.replace("| 01.01 File discovery", "  | 01.01 File discovery")
        page = page.replace("| 02.02 Suggestions", "  | 02.02 Suggestions")
        self.assertIn("| 01 Scanner | 2* | 0 | 0 | 0 | Done |", fixed(page))

    def test_the_star_marks_only_an_epic_with_sampled_rows(self) -> None:
        page = PAGE.replace("Done (sampled)", "Done")
        self.assertIn("| 01 Scanner | 2 | 0 | 0 | 0 | Done |", fixed(page))


class Errors(unittest.TestCase):
    def errors(self, page: str) -> list[str]:
        return es.render(page)[1]

    def test_an_unknown_status_is_refused(self) -> None:
        with self.assertRaisesRegex(es.PageError, "02.3 BM25.*unknown status"):
            es.render(PAGE.replace("| 02.3 BM25 | **Not started** |", "| 02.3 BM25 | Maybe |"))

    def test_a_not_done_row_missing_from_what_is_left(self) -> None:
        page = PAGE.replace("- **02.3 BM25** — nothing yet.\n", "")
        self.assertEqual(len(errs := self.errors(page)), 1)
        self.assertIn("02.3 BM25 is not started, but \"What is left\" does not list it", errs[0])

    def test_what_is_left_naming_a_done_story(self) -> None:
        page = PAGE.replace("| 02.3 BM25 | **Not started** |", "| 02.3 BM25 | Done |")
        errs = self.errors(page)
        self.assertEqual(len(errs), 1)
        self.assertIn("lists 2.3 as not started, but its row", errs[0])
        self.assertIn("says done", errs[0])

    def test_what_is_left_under_the_wrong_heading(self) -> None:
        page = PAGE.replace("| 02.2 Suggestions | **partial** |", "| 02.2 Suggestions | **not started** |")
        errs = self.errors(page)
        self.assertEqual(len(errs), 1)
        self.assertIn("lists 2.2 as partial, but its row", errs[0])

    def test_a_range_names_each_story_in_it(self) -> None:
        self.assertEqual(es.left_keys("54.47–54.51, Epic 54 Phase 5:"),
                         ["54.47", "54.48", "54.49", "54.50", "54.51"])
        page = PAGE.replace("02.4–02.5", "02.4–02.6")
        self.assertIn("lists 2.6, which has no story row", " ".join(self.errors(page)))

    def test_a_listed_story_with_no_row(self) -> None:
        page = PAGE.replace("| 02.02 Suggestions | The facets. |",
                            "| 02.02 Suggestions | The facets. |\n| 02.9 Ghost | ? |")
        self.assertIn("lists 2.9, which has no story row", " ".join(self.errors(page)))

    def test_an_epic_with_no_summary_row(self) -> None:
        page = PAGE.replace("| 02 Search | 9 | 9 | 9 | 9 | **Partial** (hand-written verdict) |\n", "")
        self.assertIn("Epic 02 has no row in the Summary table", " ".join(self.errors(page)))

    def test_a_summary_row_without_an_epic_section(self) -> None:
        page = PAGE.replace("| **Total**", "| 03 Missing | 0 | 0 | 0 | 0 | Pending |\n| **Total**")
        self.assertIn("the Summary lists Epic 03, which has no `### Epic` section", self.errors(page)[0])

    def test_an_epic_without_story_rows(self) -> None:
        page = PAGE.replace("| 01.01 File discovery | Done (sampled) | E01 |\n", "")
        page = page.replace("| 01.02 Content identity | Done (sampled) | E01 |\n", "")
        with self.assertRaisesRegex(es.PageError, "Epic 01 has no story rows"):
            es.render(page)

    def test_a_story_with_two_rows(self) -> None:
        page = PAGE.replace("| 02.1 Bundles |", "| 02.01 Bundles again | Done | x |\n| 02.1 Bundles |")
        self.assertIn("story 2.1 has two rows", " ".join(self.errors(page)))

    def test_no_sentence_is_refused(self) -> None:
        with self.assertRaisesRegex(es.PageError, "summary sentence"):
            es.render(PAGE.replace("Of 9 stories across", "Of nine stories across"))


class Cli(unittest.TestCase):
    def run_main(self, page: str, *args: str) -> tuple[int, str, str, str]:
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "epic-status.md"
            path.write_text(page, encoding="utf-8")
            out, err = io.StringIO(), io.StringIO()
            with redirect_stdout(out), redirect_stderr(err):
                code = es.main([*args, str(path)])
            return code, out.getvalue(), err.getvalue(), path.read_text(encoding="utf-8")

    def test_check_fails_on_stale_counts_and_changes_nothing(self) -> None:
        code, out, err, after = self.run_main(PAGE, "--check")
        self.assertEqual(code, 1)
        self.assertEqual(after, PAGE)
        self.assertIn("-| **Total** | **9** | **9** | **9** | **9** |", out)
        self.assertIn("+| **Total** | **3** | **1** | **3** | **1** |", out)
        self.assertIn("the counts disagree with the story rows", err)

    def test_a_run_rewrites_then_check_passes(self) -> None:
        code, _, _, after = self.run_main(PAGE)
        self.assertEqual(code, 0)
        self.assertEqual(after, fixed())
        self.assertEqual(self.run_main(after, "--check")[0], 0)

    def test_check_fails_on_a_list_error_with_counts_right(self) -> None:
        page = fixed().replace("- **02.3 BM25** — nothing yet.\n", "")
        code, out, err, _ = self.run_main(page, "--check")
        self.assertEqual(code, 1)
        self.assertEqual(out, "")
        self.assertIn("does not list it", err)

    def test_the_committed_page_is_current(self) -> None:
        text = es.PAGE.read_text(encoding="utf-8")
        new, errors = es.render(text)
        self.assertEqual(errors, [])
        self.assertEqual(new, text, "run python3 docs/wiki/db/epic_status.py")


if __name__ == "__main__":
    unittest.main()
