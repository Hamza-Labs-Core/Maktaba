"""``python -m maktaba_pipeline.word_review.eval --set ci|full --out report.json``
(Epic 52, story 52.2): word_check's scoring on the gold set, no model loaded.

A clip is ``<clip>.hyp.json`` (the stored transcript's sentences with their decoded word
evidence and ``alt``, its language, model and audio length, and the hearing test's spots
in it) and ``<clip>.ref.txt`` (the person's reference, one sentence per line). The
language packs come from ``--dicts`` (a folder of ``<code>/`` Hunspell pairs; the CI
subset uses the test fixtures). It prints a table and writes the JSON report the gate
(``tools/eval-gate``) compares with ``gold/baseline.json``.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

from ...language_packs.base import LanguagePack
from ...language_packs.fallback import FallbackPack
from ...language_packs.registry import packs_by_script
from ...language_packs.scripts import runs as script_runs
from ...stt.settings import Word
from .. import signals
from ..settings import WordReviewConfig, effective
from .align import align
from .metrics import Report, score

__all__ = ["evaluate", "main"]

_HERE = Path(__file__).resolve()
CI_SET = _HERE.parents[5] / "pipeline" / "tests" / "fixtures" / "word_review" / "gold"
CI_DICTS = _HERE.parents[5] / "pipeline" / "tests" / "fixtures" / "word_review" / "dicts"


def _sentences(hyp: dict[str, Any]) -> list[signals.SentenceIn]:
    out = []
    for s in hyp["sentences"]:
        words = [
            Word(float(a), float(b), str(t), p, int(f)) for a, b, t, p, f in s.get("words") or []
        ]
        out.append(
            signals.SentenceIn(
                seq=int(s["seq"]), start=float(s["start"]), end=float(s["end"]),
                text=str(s["text"]), words=words or None, alt=s.get("alt"),
                no_speech=float(s.get("no_speech", 0.0)),
            )
        )  # fmt: skip
    return out


def _word_spans(text: str) -> list[tuple[int, int]]:
    out, i = [], 0
    for w in text.split():
        at = text.find(w, i)
        out.append((at, at + len(w)))
        i = at + len(w)
    return out


def evaluate(gold: Path, dicts: Path) -> Report:
    packs = packs_by_script(dicts)
    report = Report()
    for hyp_path in sorted(gold.glob("*.hyp.json")):
        hyp = json.loads(hyp_path.read_text(encoding="utf-8"))
        ref_lines = hyp_path.with_name(hyp_path.name.replace(".hyp.json", ".ref.txt"))
        refs = ref_lines.read_text(encoding="utf-8").splitlines()
        lang = str(hyp["language"])
        sentences = _sentences(hyp)

        def pack_for(script: str, language: str) -> LanguagePack:
            return packs.get(script) or FallbackPack(language)

        inp = signals.Inputs(
            sentences=sentences,
            language=lang,
            settings=effective(WordReviewConfig(), {"budget_per_hour": 200}),
            pack_for=pack_for,
            model=str(hyp.get("model", "")),
            audio_sec=float(hyp.get("audio_sec", 0.0)),
        )
        flags = [f for f in signals.check(inp) if f.kind == "word"]
        report.audio_sec += inp.audio_sec
        for s, ref in zip(sentences, refs, strict=False):
            spans = _word_spans(s.text)
            flagged: dict[int, list[str]] = {}
            for f in flags:
                if f.segment_seq != s.seq:
                    continue
                for i, (a, b) in enumerate(spans):
                    if a < f.char_start + f.char_len and b > f.char_start:
                        flagged.setdefault(i, []).extend(f.reasons)
            words = s.text.split()
            # each word is compared with its own script's pack key
            first = script_runs(s.text)[0].script if s.text else "Zyyy"
            key = (packs.get(first) or FallbackPack(lang)).key
            al = align(words, ref.split("\t")[-1].split(), key)
            score(report, lang, al.labels, al.deletions_near, flagged)
            for spot in hyp.get("spots", []):
                if int(spot["seq"]) != s.seq:
                    continue
                report.spots_total += 1
                hit = any(
                    f.segment_seq == s.seq
                    and f.char_start <= int(spot["char_start"]) < f.char_start + max(1, f.char_len)
                    for f in flags
                )
                report.spots_hit += hit
                if not hit:
                    report.spots_missed.append(str(spot["name"]))
    return report


def _say(text: str) -> None:
    sys.stdout.write(text + "\n")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="python -m maktaba_pipeline.word_review.eval")
    ap.add_argument("--set", choices=("ci", "full"), default="ci")
    ap.add_argument("--gold", type=Path, default=None, help="the gold folder (full: required)")
    ap.add_argument("--dicts", type=Path, default=None)
    ap.add_argument("--out", type=Path, default=None)
    a = ap.parse_args(argv)
    gold = a.gold or (CI_SET if a.set == "ci" else None)
    if gold is None or not gold.is_dir():
        sys.stderr.write("eval: the full set needs --gold <folder> (it stays on the home)\n")
        return 2
    report = evaluate(gold, a.dicts or CI_DICTS).as_dict()
    _say(f"{'language':10} {'words':>6} {'wrong':>6} {'flagged':>8} {'precision':>9} {'recall':>7}")
    for lang, c in report["languages"].items():
        _say(
            f"{lang:10} {c['words']:6} {c['wrong']:6} {c['flagged']:8} {c['precision']:9.3f} {c['recall']:7.3f}"
        )
    for reason, c in report["reasons"].items():
        _say(f"  {reason:24} flagged {c['flagged']:4}  precision {c['precision']:.3f}")
    ht = report["hearing_test"]
    _say(f"hearing-test spots flagged: {ht['flagged']} of {ht['spots']}"
          + (f" (missed: {', '.join(ht['missed'])})" if ht["missed"] else ""))  # fmt: skip
    if a.out:
        a.out.write_text(json.dumps(report, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
