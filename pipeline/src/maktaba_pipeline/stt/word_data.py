"""A sentence's words, packed (Epic 52, story 52.1): ``transcript_segments.word_data``.

Each word is 9 bytes (the fields below; the spec rounded to 10), after a 3-byte header, little-endian::

    version:u8 (=1) | n:u16
    n x { char_start:u16, char_len:u8, start_cs:u16, dur_cs:u16, prob:u8, flags:u8 }

- ``char_start`` / ``char_len``: the word's place in the sentence's text, in Unicode code
  points (Python indexes ``str`` by code point; Go converts with ``[]rune``; the web from
  UTF-16 with ``Array.from``). The word's text is never stored twice: the text is the
  source of truth and ``word_data`` points into it.
- ``start_cs``: centiseconds from the sentence's ``start_sec``; ``dur_cs``: its length.
- ``prob``: ``round(prob x 255)``; 255 with :data:`~.settings.NO_PROB` when there was none.
- ``flags``: :data:`~.settings.HEARD_DIFFERENTLY`, :data:`~.settings.INSERTED`,
  :data:`~.settings.QURAN`, :data:`~.settings.NO_PROB`.

A word whose text is not found in the sentence, in order, is skipped and counted (the
caller stores the count as ``metadata.word_misses``). A sentence beyond the format's
limits (65 535 code points or words, 655 s) stores none. The Go twin is
``api/internal/transcript/worddata``; both read the golden fixtures in
``shared/testdata/word_data/``.
"""

from __future__ import annotations

import struct
from collections.abc import Sequence
from dataclasses import dataclass

from .settings import NO_PROB, QURAN, Word

__all__ = ["VERSION", "Packed", "Slot", "decode", "edit", "encode", "pack", "shift_after", "unpack"]

VERSION = 1
_HEAD = struct.Struct("<BH")
_WORD = struct.Struct("<HBHHBB")
_U16 = 0xFFFF


@dataclass(frozen=True, slots=True)
class Slot:
    """One packed word, as stored."""

    char_start: int
    char_len: int
    start_cs: int
    dur_cs: int
    prob: int
    flags: int


@dataclass(frozen=True, slots=True)
class Packed:
    data: bytes | None
    #: words not found in the text, in order (skipped)
    misses: int = 0


def encode(slots: Sequence[Slot]) -> bytes:
    """The bytes of ``slots`` (each field already in range)."""
    out = bytearray(_HEAD.pack(VERSION, len(slots)))
    for s in slots:
        out += _WORD.pack(s.char_start, s.char_len, s.start_cs, s.dur_cs, s.prob, s.flags)
    return bytes(out)


def decode(data: bytes) -> list[Slot]:
    """The slots of ``data``; ValueError on a version or length it does not know."""
    if len(data) < _HEAD.size:
        raise ValueError("word_data: too short")
    version, n = _HEAD.unpack_from(data, 0)
    if version != VERSION:
        raise ValueError(f"word_data: version {version}")
    if len(data) != _HEAD.size + n * _WORD.size:
        raise ValueError("word_data: length does not match its count")
    return [
        Slot(*_WORD.unpack_from(data, _HEAD.size + i * _WORD.size)) for i in range(n)
    ]


def _cs(seconds: float) -> int:
    return int(round(seconds * 100))


def pack(text: str, words: Sequence[Word], start: float) -> Packed:
    """``words`` (times on the transcript's timeline) as the ``word_data`` of a sentence
    with ``text`` that starts at ``start``."""
    if len(text) > _U16 or len(words) > _U16:
        return Packed(None)
    slots: list[Slot] = []
    pos = 0
    misses = 0
    for w in words:
        token = w.text.strip()
        if not token:
            continue
        at = text.find(token, pos)
        if at < 0 or len(token) > 0xFF:
            misses += 1
            continue
        s_cs = max(0, _cs(w.start - start))
        d_cs = max(0, _cs(w.end - w.start))
        if s_cs > _U16:
            return Packed(None, misses)  # longer than 655 s: never wraps around
        flags = int(w.flags) & 0xFF
        if w.prob is None:
            prob, flags = 255, flags | NO_PROB
        else:
            prob = min(255, max(0, int(round(float(w.prob) * 255))))
            flags &= ~NO_PROB
        slots.append(Slot(at, len(token), s_cs, min(d_cs, _U16), prob, flags))
        pos = at + len(token)
    return Packed(encode(slots), misses)


def unpack(text: str, data: bytes, start: float) -> list[Word]:
    """The words of a sentence's ``word_data``, their texts read from ``text``."""
    out: list[Word] = []
    for s in decode(data):
        prob = None if s.flags & NO_PROB else s.prob / 255
        a = round(start + s.start_cs / 100, 3)
        out.append(
            Word(
                a,
                round(a + s.dur_cs / 100, 3),
                text[s.char_start : s.char_start + s.char_len],
                prob,
                s.flags & ~NO_PROB,
            )
        )
    return out


def shift_after(data: bytes, char_start: int, delta: int) -> bytes:
    """Every word at or after ``char_start`` moved by ``delta`` code points."""
    return encode(
        [
            Slot(s.char_start + delta, *_rest(s)) if s.char_start >= char_start else s
            for s in decode(data)
        ]
    )


def _rest(s: Slot) -> tuple[int, int, int, int, int]:
    return (s.char_len, s.start_cs, s.dur_cs, s.prob, s.flags)


def _runs(text: str, a: int, b: int) -> list[tuple[int, int]]:
    """The whitespace-separated runs of ``text[a:b]``, as (start, end) code points."""
    out: list[tuple[int, int]] = []
    i = a
    while i < b:
        if text[i].isspace():
            i += 1
            continue
        j = i
        while j < b and not text[j].isspace():
            j += 1
        out.append((i, j))
        i = j
    return out


def _touches(s: Slot, a: int, b: int) -> bool:
    """The word overlaps the span ``[a, b)``; an empty span touches a word it is inside."""
    if b > a:
        return s.char_start < b and s.char_start + s.char_len > a
    return s.char_start < a < s.char_start + s.char_len


def edit(
    text: str, data: bytes | None, char_start: int, char_len: int, after: str
) -> tuple[str, bytes | None]:
    """A sentence's text and ``word_data`` after its span ``[char_start, char_start +
    char_len)`` (code points) reads ``after`` (Epic 52, story 52.9; the Go twin is
    ``worddata.Edit``, both run ``shared/testdata/word_data/edits``):

    - the words the span touches become the words of the new text over their union, which
      share the first one's start to the last one's end evenly, checked by a person
      (``prob`` 255, only the Quran mark kept);
    - the words after it move by ``len(after) - char_len``; their times stay.
    """
    if char_start < 0 or char_len < 0 or char_start + char_len > len(text):
        raise ValueError("edit: span outside the text")
    new_text = text[:char_start] + after + text[char_start + char_len :]
    if data is None:
        return new_text, None
    slots = decode(data)
    delta = len(after) - char_len
    end = char_start + char_len
    hit = [i for i, s in enumerate(slots) if _touches(s, char_start, end)]
    out: list[Slot] = []
    if not hit:
        # nothing heard there: the new words take no time, at the previous word's end
        before = [s for s in slots if s.char_start + s.char_len <= char_start]
        at = before[-1].start_cs + before[-1].dur_cs if before else 0
        added = [
            Slot(a, b - a, at, 0, 255, 0)
            for a, b in _runs(new_text, char_start, char_start + len(after))
        ]
        rest = [s for s in slots if s.char_start + s.char_len > char_start]
        out = before + added + [Slot(s.char_start + delta, *_rest(s)) for s in rest]
        return new_text, encode(out)
    first, last = slots[hit[0]], slots[hit[-1]]
    u_start = min(char_start, first.char_start)
    u_end = max(end, last.char_start + last.char_len) + delta
    t0 = first.start_cs
    t1 = max(t0, last.start_cs + last.dur_cs)
    quran = 0
    for i in hit:
        quran |= slots[i].flags & QURAN
    runs = [(a, b) for a, b in _runs(new_text, u_start, u_end) if b - a <= 0xFF]
    new: list[Slot] = []
    if runs:
        step = (t1 - t0) / len(runs)
        for k, (a, b) in enumerate(runs):
            s0 = t0 + int(round(k * step))
            s1 = t0 + int(round((k + 1) * step))
            new.append(Slot(a, b - a, s0, s1 - s0, 255, quran))
    out = (
        slots[: hit[0]]
        + new
        + [Slot(s.char_start + delta, *_rest(s)) for s in slots[hit[-1] + 1 :]]
    )
    return new_text, encode(out)
