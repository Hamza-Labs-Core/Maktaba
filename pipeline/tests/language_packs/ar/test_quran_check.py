"""The Quran check (Epic 52, story 52.15) on the real bundled Tanzil text."""

from __future__ import annotations

import hashlib
import json
import random
from pathlib import Path

from maktaba_pipeline.classify import quran as q
from maktaba_pipeline.language_packs.ar import quran as Q

DATA = Path(q.__file__).resolve().parent.parent / "data" / "quran"
#: the Tanzil Simple Clean v1.1 file as distributed: never modified (CC BY 3.0, verbatim)
TANZIL_SHA256 = "1862ee63c70fbfc6285e0bb0b9587b89beb5b7891109a4d543d9d2cf8283e8c8"


def _non_eq(words: str) -> list[tuple[str, int | None, str | None]]:
    out: list[tuple[str, int | None, str | None]] = []
    for r in Q.align(words.split()):
        if r.rejected or "phrase" in r.ref:
            continue
        out.extend((o.kind, o.token, o.expected) for o in r.ops if o.kind != "eq")
    return out


def _runs(words: str) -> list[Q.ScriptureRun]:  # type: ignore[name-defined]
    return [r for r in Q.align(words.split()) if not r.rejected and "phrase" not in r.ref]


def test_a_correct_recitation_has_no_mismatch() -> None:
    assert _non_eq("الحمد لله رب العالمين الرحمن الرحيم مالك يوم الدين") == []
    runs = _runs("الحمد لله رب العالمين الرحمن الرحيم مالك يوم الدين")
    assert runs and runs[0].ref["surah"] == 1 and runs[0].ref["first"] == 2


def test_one_misheard_word_is_the_only_mismatch_with_the_verse_word() -> None:
    got = _non_eq("الحمد لله رب العالمون الرحمن الرحيم مالك يوم الدين")
    assert got == [("sub", 3, "العالمين")]
    op = next(o for r in _runs("الحمد لله رب العالمون الرحمن الرحيم مالك يوم الدين")
              for o in r.ops if o.kind == "sub")  # fmt: skip
    assert op.ref["surah"] == 1 and op.ref["aya"] == 2


def test_orthographic_and_reading_variants_are_not_mismatches() -> None:
    got = _non_eq("الذين يؤمنون بالغيب ويقيمون الصلوة ومما رزقناهم ينفقون")
    assert got == [("var", 4, "الصلاة")]
    for a, b in (("الصلوة", "الصلاة"), ("الزكوة", "الزكاة"), ("الربوا", "الربا"), ("ملك", "مالك"),
                 ("يعلمون", "تعلمون")):  # fmt: skip
        assert Q.rasm_key(a) == Q.rasm_key(b), (a, b)
    for a, b in (("ذهب", "زهب"), ("ثمر", "سمر"), ("عمل", "ءمل")):
        assert Q.rasm_key(a) != Q.rasm_key(b), (a, b)


def test_a_missing_verse_word_flags_the_next_word() -> None:
    got = _non_eq("الذين يؤمنون بالغيب ويقيمون ومما رزقناهم ينفقون")
    assert got == [("del", 4, "الصلاة")]


def test_isti_adha_and_basmala_before_a_run_are_their_own_phrases() -> None:
    said = "أعوذ بالله من الشيطان الرجيم بسم الله الرحمن الرحيم"
    runs = Q.align(f"{said} والسماء ذات البروج واليوم الموعود".split())
    phrases = [r.ref.get("phrase") for r in runs if "phrase" in r.ref]
    assert phrases == ["istiadha", "basmala"]
    verse = [r for r in runs if "phrase" not in r.ref]
    assert verse and verse[0].start == 9 and verse[0].ref["surah"] == 85
    assert all(o.kind == "eq" for o in verse[0].ops)


def test_a_repeated_half_verse_is_not_a_mismatch() -> None:
    got = _non_eq("والسماء ذات البروج واليوم الموعود واليوم الموعود وشاهد ومشهود")
    assert got == []


def test_three_words_that_happen_to_match_are_no_run() -> None:
    assert _runs("قال الحمد لله رب كل شيء في الدنيا") == []


def test_words_found_in_many_places_are_left_unchecked() -> None:
    assert _runs("إن الله على كل شيء قدير") == []


def test_the_canonical_text_is_unchanged_and_complete() -> None:
    got = hashlib.sha256((DATA / "quran-simple-clean.txt").read_bytes()).hexdigest()
    assert got == TANZIL_SHA256
    meta = json.loads((DATA / "surahs.json").read_text(encoding="utf-8"))
    counts: dict[int, int] = {}
    for line in (DATA / "quran-simple-clean.txt").read_text(encoding="utf-8").splitlines():
        if line and not line.startswith("#"):
            s, _a, _t = line.split("|", 2)
            counts[int(s)] = counts.get(int(s), 0) + 1
    assert [counts[i + 1] for i in range(114)] == [int(s["ayas"]) for s in meta["surahs"]]


def test_seeded_errors_are_caught_and_clean_runs_never_flag() -> None:
    """200 verse runs from the text, each with one word changed by a confusion pair,
    dropped or inserted: at least 0.80 caught; the 200 unchanged runs flag nothing."""
    ix = q._index()
    rng = random.Random(52)
    swaps = {"ذ": "ز", "ز": "ذ", "ث": "س", "س": "ث", "ص": "س", "ض": "د", "ق": "ك", "ح": "ه",
             "ع": "ء", "ت": "ط", "ط": "ت"}  # fmt: skip
    caught = clean_flags = tried = 0
    while tried < 200:
        start = rng.randrange(0, len(ix.words) - 12)
        words = [Q.verse_word(i) for i in range(start, start + 10)]
        if len(set(words)) < 8:
            continue
        clean = _non_eq(" ".join(words))
        clean_flags += len([k for k, _t, _e in clean if k != "var"])
        k = rng.randrange(3, 7)
        kind = rng.choice(["swap", "drop", "insert"])
        bad = list(words)
        if kind == "swap":
            w = bad[k]
            pos = [i for i, c in enumerate(w) if c in swaps]
            if not pos:
                continue
            i = rng.choice(pos)
            bad[k] = w[:i] + swaps[w[i]] + w[i + 1 :]
        elif kind == "drop":
            del bad[k]
        else:
            bad.insert(k, "كلمة")
        tried += 1
        got = [x for x in _non_eq(" ".join(bad)) if x[0] in ("sub", "ins", "del")]
        caught += bool(got)
    assert clean_flags == 0
    assert caught / tried >= 0.80, caught


def test_the_check_is_deterministic() -> None:
    text = ["الحمد", "لله", "رب", "العالمون", "الرحمن", "الرحيم", "مالك", "يوم", "الدين"]
    assert Q.align(text) == Q.align(text)
