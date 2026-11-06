"""Common Levantine and Egyptian clitics (Epic 52, story 52.4): a word the MSA analyser
lacks but knows once one of these is peeled is a dialect word, ``UNSURE``, never an error."""

from __future__ import annotations

__all__ = ["peeled"]

#: the present prefix ب (بيكتب، بتكتب، بنكتب), the future ح / ه (حيكتب، هيكتب)
_PREFIXES = ("بي", "بت", "بن", "با", "ب", "حي", "حت", "حن", "ح", "هي", "هت", "هن")
#: the negation ش (ما بكتبش), on its own or after the dialect prefixes
_SUFFIXES = ("ش",)


def peeled(word: str) -> list[str]:
    """The MSA forms ``word`` may be with one dialect clitic taken off (a prefix's letter
    of person kept: «بيكتب» → «يكتب»)."""
    out: list[str] = []
    for s in _SUFFIXES:
        if word.endswith(s) and len(word) > len(s) + 2:
            out.append(word[: -len(s)])
    bases = [word, *out]
    for base in bases:
        for p in _PREFIXES:
            if base.startswith(p) and len(base) > len(p) + 1:
                rest = base[len(p) :]
                # بيكتب: ب + يكتب (the person's letter stays)
                keep = base[1:] if len(p) == 2 else rest
                for cand in (keep, rest):
                    if cand not in out and cand != word:
                        out.append(cand)
    return out
