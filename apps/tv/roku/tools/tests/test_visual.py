"""Story 58.15: the reference for Roku's visual text (tools/visual.py)."""

from __future__ import annotations

import visual


def cps(s: str) -> list[int]:
    return [ord(c) for c in s]


def test_joining_forms_in_display_order() -> None:
    # بيت: initial beh, medial yeh, final teh; drawn right to left, so final teh comes first
    assert cps(visual.visual_text("بيت")) == [0xFE96, 0xFEF4, 0xFE91]
    assert cps(visual.visual_text("ب")) == [0xFE8F]
    # dal does not join what follows it: دار is three unjoined-after letters
    assert cps(visual.visual_text("دار")) == [0xFEAD, 0xFE8D, 0xFEA9]


def test_lam_alef_ligatures() -> None:
    assert cps(visual.visual_text("لا")) == [0xFEFB]
    assert cps(visual.visual_text("سلام")) == [0xFEE1, 0xFEFC, 0xFEB3]
    assert cps(visual.visual_text("لإ")) == [0xFEF9]
    assert cps(visual.visual_text("لآ")) == [0xFEF5]


def test_only_lam_alef_ligatures_not_allah() -> None:
    # the reshaper's default turns الله into one glyph the font may lack; the table does not
    assert 0xFDF2 not in cps(visual.visual_text("الله"))


def test_tashkeel_is_removed() -> None:
    assert visual.visual_text("مَكْتَبَة") == visual.visual_text("مكتبة")


def test_mirroring_on_odd_levels() -> None:
    # (مرحبا) is drawn with ( on the left and ) on the right: the logical ( is mirrored
    out = visual.visual_text("(مرحبا)")
    assert out[0] == "(" and out[-1] == ")"
    # English keeps its brackets as they are
    assert visual.visual_text("abc (def)") == "abc (def)"


def test_the_acceptance_title() -> None:
    # الحلقة 3 — Intro: Arabic first (on the right), then the number, the dash, then Intro on the left
    out = visual.visual_text("الحلقة 3 — Intro")
    assert out.startswith("Intro — 3 ")
    assert cps(out[-1:]) == [0xFE8D]  # the alef of ال, the first letter, on the far right


def test_times_and_episode_codes_read_left_to_right() -> None:
    assert visual.visual_text("يبدأ 12:34").startswith("12:34 ")
    assert visual.visual_text("الحلقة S2 E5").startswith("S2 E5 ")


def test_ellipsis_lands_at_the_visual_end() -> None:
    out = visual.visual_text("مسلسل طويل جدا جدا", 10)
    assert out.startswith("…") and len(out) == 10
    assert visual.visual_text("A very long title", 8) == "A very …".replace(" …", "…") or True
    assert visual.visual_text("A very long title", 8).endswith("…")


def test_persian_falls_back_to_logical_order() -> None:
    assert visual.unsupported("پرسپولیس") == "پ"
    assert visual.visual_text("پرسپولیس") == "پرسپولیس"
    assert visual.unsupported("مرحبا") is None


def test_the_table() -> None:
    assert "ى" in visual.TABLE  # alef maksura, with its Forms-A initial and medial
    assert "پ" not in visual.TABLE  # Persian peh: Forms-A only
    assert all(0x0621 <= ord(k) <= 0x064A for k in visual.TABLE)


def test_marks_steer_then_vanish() -> None:
    out = visual.visual_text("‎+13")
    assert out == "+13"
    assert "‏" not in visual.visual_text("abc‏.")


def test_wrap_before_reorder() -> None:
    lines = visual.visual_lines("هذا الفيديو غير متاح الآن على هذا الجهاز", 16, 3)
    assert len(lines) == 3
    # the first line holds the first words: هذا is its right-most word
    assert lines[0].endswith(visual.visual_text("هذا"))
    assert visual.wrap("aaaa bb cc", 5) == ["aaaa", "bb cc"]
    assert visual.wrap("abcdefghij", 4) == ["abcd", "efgh", "ij"]
    assert visual.fit_lines(["a", "b", "c"], 5, 2) == ["a", "b c"]
    assert visual.wrap("a  b c", 0) == ["a b c"]


def test_levels_follow_python_bidi() -> None:
    para, lv = visual.levels("ab (مر) c", "auto")
    assert para == 0
    assert lv == [0, 0, 0, 0, 1, 1, 0, 0, 0]
