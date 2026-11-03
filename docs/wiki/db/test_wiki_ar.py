"""Tests for wiki_ar.py, the wiki's Arabic: the translation lookup and the drift check
(stdlib unittest; CI: ci.yml's wiki job).

    python3 -m unittest discover -s docs/wiki/db -p 'test_*.py'
"""

import io
import json
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import generate_wiki as g  # noqa: E402
import wiki_ar as w  # noqa: E402

STORY_EN = "Plays `video.mp4` from where you left it, on every screen."
STORY_AR = "يشغّل `video.mp4` من حيث توقّفت، على كل شاشة."


def story(**over) -> dict:
    e = {"id": "story-1.1", "type": "story", "epic": "01-scanner", "title": "Resume playback",
         "content": STORY_EN}
    e.update(over)
    return e


def stored(en: str, ar: str) -> dict:
    return {"hash": w.text_hash(en), "ar": ar}


def store_for(**fields: tuple[str, str]) -> dict:
    return {"epic-01.json": {"story-1.1": {f: stored(en, ar) for f, (en, ar) in fields.items()}}}


GOOD = store_for(title=("Resume playback", "استئناف التشغيل"), content=(STORY_EN, STORY_AR))


class Prose(unittest.TestCase):
    def test_code_paths_and_urls_are_not_prose(self) -> None:
        text = "Run `go test ./...` then see https://example.com/x and api/internal/x.go.\n\n```\nmake all\n```"
        p = w.prose(text)
        for gone in ("go test", "example.com", "internal", "make all"):
            self.assertNotIn(gone, p)
        self.assertIn("Run", p)

    def test_a_code_span_may_wrap_one_line(self) -> None:
        text = "Call `GET\n/api/x` now."
        self.assertEqual(w.code_pieces(text), ["`GET\n/api/x`"])
        self.assertNotIn("GET", w.prose(text))
        # a blank line ends it: two paragraphs, no span
        self.assertEqual(w.code_pieces("a `b\n\nc` d"), [])

    def test_a_cut_open_fence_or_span_is_not_prose(self) -> None:
        self.assertNotIn("SELECT", w.prose("Intro.\n\n```sql\nSELECT 1"))
        self.assertNotIn("half", w.prose("See `half"))

    def test_latin_share(self) -> None:
        self.assertEqual(w.latin_share("مكتبة"), 0.0)
        self.assertEqual(w.latin_share("abc"), 1.0)
        self.assertEqual(w.latin_share("`code` only"), 1.0)
        self.assertEqual(w.latin_share(""), 0.0)

    def test_code_alone_needs_no_arabic(self) -> None:
        self.assertFalse(w.needs_arabic("`GET /api/x`"))
        self.assertFalse(w.needs_arabic(""))
        self.assertTrue(w.needs_arabic("Lists the libraries."))

    def test_store_file(self) -> None:
        self.assertEqual(w.store_file(story()), "epic-01.json")
        self.assertEqual(w.store_file({"type": "epic", "epic": "54-plugins"}), "epic-54.json")
        self.assertEqual(w.store_file({"type": "phase", "epic": None}), "common.json")
        self.assertEqual(w.store_file({"type": "endpoint"}), "common.json")


class Lookup(unittest.TestCase):
    def test_from_the_store(self) -> None:
        ar, origin = w.lookup(story(), "content", STORY_EN, GOOD, {})
        self.assertEqual((ar, origin), (STORY_AR, "store"))

    def test_an_endpoint_reuses_the_openapi_overlay_while_its_english_matches(self) -> None:
        e = {"id": "endpoint-get-x", "type": "endpoint", "title": "GET /api/x",
             "content": "Lists the libraries.", "metadata": {"path": "/api/x/{id}", "method": "GET"}}
        overlay = {"/paths/~1api~1x~1{id}/get/summary": {"en": "Lists the libraries.", "ar": "يسرد المكتبات."}}
        self.assertEqual(w.lookup(e, "content", e["content"], {}, overlay), ("يسرد المكتبات.", "openapi"))
        # the English moved on: the overlay is not used, the store is
        self.assertEqual(w.lookup(e, "content", "Lists them all.", {}, overlay), (None, "store"))

    def test_a_plain_entity_has_a_fixed_sentence(self) -> None:
        e = {"id": "entity-jobs", "type": "entity", "title": "jobs"}
        en = "Database table `jobs`. Source: api/migrations/0001_init.sql."
        ar, origin = w.lookup(e, "content", en, {}, {})
        self.assertEqual(origin, "fixed")
        self.assertIn("`jobs`", ar)
        self.assertIn("api/migrations/0001_init.sql", ar)

    def test_openapi_pointer_escapes_the_path(self) -> None:
        self.assertEqual(w.openapi_pointer("/api/a~b/{id}", "POST"), "/paths/~1api~1a~0b~1{id}/post/summary")


class Drift(unittest.TestCase):
    def test_a_complete_translation_has_no_findings(self) -> None:
        res = w.translate([story()], GOOD, {})
        self.assertEqual(res.findings, [])
        self.assertEqual(res.translated, 1)
        e = res.entries[0]
        self.assertEqual(e["content"], STORY_AR)
        self.assertEqual(e["i18n"], {"lang": "ar", "source": {
            "title": w.text_hash("Resume playback"), "content": w.text_hash(STORY_EN)}})

    def test_missing_arabic_names_the_file_entry_and_field(self) -> None:
        store = store_for(title=("Resume playback", "استئناف التشغيل"))
        res = w.translate([story()], store, {})
        self.assertEqual(len(res.findings), 1)
        self.assertTrue(res.findings[0].startswith(
            "docs/wiki/db/ar/epic-01.json: story-1.1 content: no Arabic"), res.findings[0])
        self.assertEqual(res.translated, 0)
        # it stays in English in the Arabic database, and is in the to-do list with its hash
        self.assertEqual(res.entries[0]["content"], STORY_EN)
        self.assertEqual(res.todo["epic-01.json"]["story-1.1"]["content"],
                         {"hash": w.text_hash(STORY_EN), "en": STORY_EN})

    def test_stale_english_names_the_entry_and_the_new_hash(self) -> None:
        changed = STORY_EN.replace("every screen", "every screen and TV")
        res = w.translate([story(content=changed)], GOOD, {})
        self.assertEqual(len(res.findings), 1)
        f = res.findings[0]
        self.assertIn("docs/wiki/db/ar/epic-01.json: story-1.1 content: the English changed", f)
        self.assertIn(w.text_hash(changed), f)
        self.assertEqual(res.todo["epic-01.json"]["story-1.1"]["content"]["ar_before"], STORY_AR)

    def test_arabic_that_is_english_fails(self) -> None:
        store = store_for(title=("Resume playback", "استئناف التشغيل"), content=(STORY_EN, STORY_EN))
        res = w.translate([story()], store, {})
        self.assertEqual(res.findings, ["docs/wiki/db/ar/epic-01.json: story-1.1 content: the Arabic is not Arabic"])

    def test_dropped_or_changed_code_fails(self) -> None:
        store = store_for(title=("Resume playback", "استئناف التشغيل"),
                          content=(STORY_EN, "يشغّل `الفيديو` من حيث توقّفت، على كل شاشة."))
        res = w.translate([story()], store, {})
        self.assertEqual(len(res.findings), 1)
        self.assertIn("story-1.1 content: the Arabic drops or changes code", res.findings[0])
        self.assertIn("`video.mp4`", res.findings[0])

    def test_arabic_prose_left_mostly_in_latin_fails(self) -> None:
        en = "This story was written in English, every word of it, " * 8
        ar = "قصة: " + en
        store = store_for(title=("Resume playback", "استئناف التشغيل"), content=(en, ar))
        res = w.translate([story(content=en)], store, {})
        self.assertTrue(any("story-1.1:" in f and "Latin script" in f for f in res.findings), res.findings)
        self.assertTrue(any(f.startswith("docs/wiki/db/ar/epic-01.json: ") and "Latin" in f
                            for f in res.findings))

    def test_a_short_entry_with_a_product_name_is_fine(self) -> None:
        en = "Apple TV app."
        store = {"common.json": {"feature-x": {"title": stored("Apple TV", "تطبيق Apple TV"),
                                               "content": stored(en, "تطبيق Apple TV.")}}}
        e = {"id": "feature-x", "type": "feature", "title": "Apple TV", "content": en}
        # under MIN_LETTERS, the entry's own share is not judged (a file's still is)
        findings = w.translate([e], store, {}).findings
        self.assertFalse([f for f in findings if "feature-x" in f], findings)

    def test_an_orphan_store_entry_fails(self) -> None:
        store = {**GOOD, "common.json": {"phase-99": {"title": stored("Gone", "ذهب")}}}
        res = w.translate([story()], store, {})
        self.assertEqual(res.findings, [(
            "docs/wiki/db/ar/common.json: phase-99 title is not a text the wiki shows "
            "(the entry or field is gone, or belongs in another file): remove it")])

    def test_cross_refs_take_the_arabic_titles(self) -> None:
        xref = {"epics": [{"id": "01", "slug": "scanner", "title": "Scanner", "description": "Finds."}],
                "stories": [{"epic": 1, "num": "1", "id": "1.1", "title": "Resume playback"}]}
        ar_by_id = {"epic-01-scanner": {"title": "الماسح", "content": "يجد."},
                    "story-1.1": {"title": "استئناف التشغيل"}}
        out = w.cross_refs(xref, ar_by_id)
        self.assertEqual(out["lang"], "ar")
        self.assertEqual(out["epics"][0]["title"], "الماسح")
        self.assertEqual(out["stories"][0]["title"], "استئناف التشغيل")
        self.assertEqual(xref["epics"][0]["title"], "Scanner")  # the English is untouched


class TheArabicDatabase(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.built = g.build_all()

    def test_every_entry_is_translated_from_its_current_english(self) -> None:
        self.assertEqual(self.built.findings, [])
        self.assertEqual(self.built.translated, self.built.total)

    def test_the_arabic_files_mirror_the_english(self) -> None:
        en = json.loads(self.built.files[g.OUT_DB / "wiki.json"])
        ar = json.loads(self.built.files[g.OUT_DB_AR / "wiki.json"])
        self.assertEqual(ar["lang"], "ar")
        self.assertEqual(ar["parts"], en["parts"])
        self.assertEqual([e["id"] for e in ar["entries"]], [e["id"] for e in en["entries"]])
        for part in en["parts"]:
            a = json.loads(self.built.files[g.OUT_DB_AR / part])
            e = json.loads(self.built.files[g.OUT_DB / part])
            self.assertEqual(a["lang"], "ar")
            self.assertEqual([x["id"] for x in a["entries"]], [x["id"] for x in e["entries"]])

    def test_epics_1_to_54_read_in_arabic(self) -> None:
        epics = []
        for path, text in self.built.files.items():
            if path.parent == g.OUT_DB_AR:
                epics += [e for e in json.loads(text).get("entries", []) if e["type"] == "epic"]
        self.assertGreaterEqual(len(epics), 54)
        for e in epics:
            # a title that is a name alone (Maktaba.app) stays as written; the goal is Arabic
            self.assertRegex(e["content"], w.ARABIC_RE, e["id"])
            self.assertIn("i18n", e, e["id"])

    def test_the_store_has_a_file_per_epic(self) -> None:
        names = {p.name for p in g.AR_STORE.glob("*.json")}
        self.assertIn("common.json", names)
        for n in range(1, 55):
            self.assertIn(f"epic-{n:02d}.json", names)

    def test_ar_todo_is_empty_and_check_passes(self) -> None:
        out = io.StringIO()
        with redirect_stdout(out):
            self.assertEqual(g.main(["--ar-todo"]), 0)
        self.assertEqual(json.loads(out.getvalue()), {})
        with redirect_stdout(io.StringIO()) as o, redirect_stderr(io.StringIO()):
            self.assertEqual(g.main(["--check"]), 0)
        self.assertIn("every entry in English and Arabic", o.getvalue())


if __name__ == "__main__":
    unittest.main()
