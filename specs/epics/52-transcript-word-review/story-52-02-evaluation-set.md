# Story 52.02 — Evaluation set, metrics and calibration

> Epic 52 · Phase 1 · Depends on: — (uses 52.1's output for confidence curves once it lands) ·
> Needed by: 52.6, 52.15 (52.7, the LLM judge, is removed)

## Why

A flagger that is never measured drifts into one of two failures: it flags too much (reviewers
give up) or it misses the words that matter. The hearing tests (2026-10-19, [#113]) showed that
judgement without measurement goes wrong: an LLM judge preferred turbo's tidy spelling, and the
owner's ears found Cohere right 17 times out of 20. This story builds the yardstick first:
a gold set, the metrics, and per-language calibration curves for Whisper's word probability.

## Description

### 1. The gold set

| Part | Size | Source |
|---|---|---|
| Arabic lectures (MSA + Levantine/Egyptian speech) | 3 h, from at least 8 speakers | The home's library, picked by the owner |
| Quran recitation inside lectures | 15 min | The same files |
| English talks | 1 h | The home's library |
| Code-switched Arabic/English | 30 min | The same |
| The owner's 20 disputed hearing-test spots | 20 clips | The 2026-10-19 test notes |

Each clip has a **reference transcript** corrected by a person (the owner or a reviewer they
choose), in the library's own spelling: hamza written, «ة» and «ى» as the dictionary spells them.
The reference is made in the review UI's own format once 52.11 exists. Until then it is a plain
text file per clip, one sentence per line, with times.

Stored under `pipeline/tests/fixtures/word_review/` as:

- `gold/<clip>.ref.txt`: the reference.
- `gold/<clip>.hyp.json`: the stored transcript's sentences with `word_data` decoded (52.1) and
  `metadata.alt`. These are taken once from the live DB read-only, or from a scratch transcription.
- No audio in git. The audio stays on the home. The CI subset needs only text and evidence.

### 2. Word error labels

`word_review/eval/align.py` aligns hypothesis words to reference words (Levenshtein on the
pack's `key()`, 52.3), so a hamza or harakat-only difference is a **spelling** error and a
different word is a **substitution**. Each hypothesis word gets a label: `ok`, `spelling`,
`substitution`, `insertion`. Deletions (words the reference has and the hypothesis lacks) are
counted, but cannot be flagged at a word, so they count against recall only through the
nearest hypothesis word, within ±1 word.

### 3. Metrics

For any flagger output (a set of flagged spans):

- **precision** = flagged words labelled wrong / flagged words;
- **recall** = wrong words that are flagged / wrong words;
- **flag rate** = flagged words / all words, and flags per audio hour;
- **hearing-test hits** = of the 20 spots, how many are flagged;
- all of these per language and per reason (`low_confidence`, `listeners_disagree`, …).

`python -m maktaba_pipeline.word_review.eval --set ci|full --out report.json` prints a table and
writes JSON. The CI subset (40 min of evidence, the 20 spots) runs in seconds with no models.

### 4. Calibration

Whisper's word probability is miscalibrated: over-confident on fluent hallucinations,
under-confident on short function words. For each language (and each STT model, since the
curve changes with the model):

- fit **isotonic regression** from `prob` to P(wrong), on the gold set's labels
  (scikit-learn's `IsotonicRegression`, or a 30-line pool-adjacent-violators to avoid the
  dependency). Report the expected calibration error (ECE, 10 bins) before and after;
- store the curve as JSON next to the pack: `calibration/<model>.json` with `{x: [...], y:
  [...], n, ece_before, ece_after, fitted_at}`;
- `word_check` (52.6) interpolates it. A language or model without a curve uses the identity
  map and a stricter default threshold (0.3 raw probability).

### 5. The gate

CI runs the CI subset on every PR that touches `word_review/`, `language_packs/` or the scoring.
It fails when precision or recall for any language drops by more than 0.02 against
`gold/baseline.json`, unless the commit has an `Eval-Change: <reason>` trailer (same idea as
`No-Test-Change`). A PR that improves the numbers updates the baseline.

## Acceptance criteria

1. **Given** the gold fixtures, **when** `eval --set ci` runs, **then** it finishes in under 30 s
   with no model loaded and prints precision, recall and flag rate per language and per reason.
2. **Given** a hypothesis «اسلام» and a reference «إسلام», **when** aligned, **then** the word is
   labelled `spelling`, not `substitution`.
3. **Given** an English word inside an Arabic sentence, **when** aligned, **then** it is compared
   with the English pack's `key()`.
4. **Given** a flagger that flags every word, **when** evaluated, **then** recall is 1.0 and
   precision equals the error rate (a sanity check in the tests).
5. **Given** the 20 hearing-test spots, **when** evaluated, **then** the report says how many were
   flagged, and each missed spot by name.
6. **Given** Arabic gold labels and Whisper probabilities, **when** the calibration fits, **then**
   the curve is monotonic non-increasing in `prob`, and its ECE is lower than the raw
   probability's.
7. **Given** a stored curve, **when** a newer model has no curve, **then** `word_check` uses the
   identity map with the stricter default threshold and logs `calibration_missing` once.
8. **Given** a PR that lowers Arabic precision by 0.05, **when** CI runs without an
   `Eval-Change` trailer, **then** the eval job fails and names the metric.
9. **Given** the full set on a Mac with the models, **when** `eval --set full` runs, **then** it
   re-transcribes the clips (optionally) and reports the same table.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/word_review/__init__.py` (new package).
- `pipeline/src/maktaba_pipeline/word_review/eval/align.py`, `metrics.py`, `calibrate.py`,
  `__main__.py`.
- `pipeline/tests/fixtures/word_review/gold/` (CI subset), `baseline.json`.
- `pipeline/tests/word_review/test_align.py`, `test_metrics.py`, `test_calibrate.py`.
- `.github/workflows/ci.yml`: an `eval` step in the pipeline job, path-filtered (CI runs only
  related jobs).
- `tools/eval-gate/` (new): reads the trailer and compares to the baseline.

### Tests

- Unit: alignment labels on hand-made pairs (hamza, harakat, ة/ه, clitics, insertions,
  deletions), metrics on toy flaggers, PAV fit on a known curve.
- The gate script: a fake report with a drop fails, with the trailer passes.
- No model loads (conftest guard). The full set is never run in CI.

### Rollout

- No live change. The live DB is read only once, to take the hypothesis evidence for the gold
  clips (read-only, the owner's OK first).
- The reference transcripts need a person's time: about 4-6 hours for 4.75 h of audio. Asked of
  the owner in the PR. Until done, the CI subset uses the 20 hearing-test spots and a 20-minute
  sample the session corrects and the owner checks.

## Edge cases

- Two references disagree on a spelling the dictionary allows both ways (e.g. «مسؤول» /
  «مسئول»): the pack's `key()` folds them, so they score as `ok`.
- A reference sentence with no hypothesis words (Whisper skipped it): all its words are
  deletions, reported apart.
- Quran: labels inside verse runs are reported apart, as the Quran check's own precision and
  recall (52.15), since `quran_mismatch` replaces the ordinary signals there.
- A gold clip whose file is later replaced: its fixture keeps the evidence it was made from, so
  the set stays stable.

[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
