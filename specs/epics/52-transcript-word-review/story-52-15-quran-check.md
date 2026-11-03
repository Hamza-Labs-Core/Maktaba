# Story 52.15 — Quran check: verse runs against the Quran text

> Epic 52 · Phase 1 · Depends on: 52.4, 52.6 · Needed by: 52.11 (reason label), 52.14 (switch)
> Decision 4 (2026-11-03, [README §8](README.md#8-decisions-2026-11-03)).

## Why

Lectures quote the Quran and some files are recitation. The first draft protected every word
inside a detected verse run, so it never flagged one. But the run detector
(`classify/quran.verse_runs`) needs every folded word to match the text exactly, so a misheard
word ends the run, and the rest of the verse falls to the ordinary dictionary checks. Those know
nothing about the verse. Decision 4 is to **check** verse runs against the Quran text: compare
each word with the matched verse and flag only real mismatches. A correctly heard verse must never
appear in the queue, and a misheard verse word must, with the verse's word as the suggestion.

## Description

### 1. The canonical text and its licence

- Source: the **Tanzil Quran Text, "Simple Clean", version 1.1**, from tanzil.net. It is already
  bundled in `pipeline/src/maktaba_pipeline/data/quran/quran-simple-clean.txt` (one verse per line,
  `surah|aya|text`), with `surahs.json` (Tanzil Quran Metadata) for names and verse counts.
- Licence: **Creative Commons Attribution 3.0**, as recorded in `data/quran/LICENSE-TANZIL.txt`.
  Tanzil's terms ask for verbatim copies: the file stays byte-for-byte unchanged, including its
  copyright block, and Maktaba never edits the text. All normalisation is done in memory, on the
  fly, never written back.
- Attribution: wherever a verse word or reference is shown (the candidate and the verse line in
  the review screen, 52.11 and 52.13), a credit line reads "Quran text: Tanzil Project
  (tanzil.net)" / «نص القرآن: مشروع تنزيل (tanzil.net)». The About screen lists the Tanzil notice
  with the other third-party notices.
- `LICENSE-TANZIL.txt`'s last paragraph, which says the text is used only for display titles, is
  updated to name this use too: verse words are compared with transcripts and shown, unchanged, as
  suggestions to a home admin.
- The Uthmani script text (Tanzil publishes it under the same terms) is **not** added in this
  story. Simple Clean's modern spelling is closer to what Whisper and Cohere write, and the rasm key
  (§2) absorbs Uthmani-style spellings a transcript might use. A later pack version may add Uthmani
  for display only.

### 2. Normalisation

Two keys per word, both computed from the same function on the verse side and the transcript side:

1. **Match key** = the Arabic pack's `key()` (52.4) plus the Quranic extras: Quranic annotation
   marks (U+06D6-U+06ED: small high letters, pause marks, sajda and rub' el hizb signs), the
   superscript (dagger) alef U+0670, alef wasla ٱ → ا, small waw/ya removed; then harakat and
   tatweel removed, أ/إ/آ → ا, ة → ه, ى → ي, ؤ → و, ئ → ي. Two words are **equal** when their match
   keys are equal.
2. **Rasm key** = the match key with the dots removed (each letter mapped to its undotted skeleton
   class: ب/ت/ث/ن/ي (medial) → one class, ج/ح/خ, د/ذ, ر/ز, س/ش, ص/ض, ط/ظ, ع/غ, ف/ق, ه/ة) and every
   long alef ا dropped. Two unequal words with equal rasm keys are a **variant**: an orthographic
   difference (الصلوة / الصلاة, ملك / مالك) or a canonical reading difference (يعلمون / تعلمون),
   which is not an error of hearing for this check.

The normaliser is `language_packs/ar/quran.py` `match_key()` / `rasm_key()`, versioned with the
pack (`QURAN_CHECK_VERSION` in the pack's `version`), and shares `text/arabic.fold` so search
folding and this check never disagree on the basics.

### 3. Alignment

1. **Anchor:** the existing index (`classify/quran._index`, words keyed by their first two
   folded words) finds candidate starts. A run is anchored where ≥ 4 consecutive transcript words
   equal the text (today's `MIN_RUN_WORDS`). An anchor that matches more than one place in the
   Quran is extended at every place, and the place with the best score wins; a tie leaves the run
   unchecked (no flags, words not protected).
2. **Extend:** from the anchor, both ways, a word-level edit-distance alignment (Levenshtein over
   match keys, substitution cost 0 for equal, 0.3 for variant, 1 otherwise; insertion and deletion
   1) follows the text across verse ends. It stops when the last 5 transcript words cost more than
   1, i.e. it tolerates about 1 wrong word in 5. The run is kept when it has ≥ 4 words and ≥ 80% of
   its words are equal or variant.
3. **What a reciter does that is not an error:** going back to repeat a phrase (a jump back within
   the last 2 verses re-anchors without a penalty); the isti'adha «أعوذ بالله من الشيطان الرجيم» and
   the basmala before a run (matched as their own phrases, never flagged); «صدق الله العظيم» after
   it; stopping mid-verse and starting the next verse.
4. Runs are computed from the sentence text, so they work on old transcripts without `word_data`
   too (52.6 §7). New transcripts' `word_data` bit 2 is a hint for the anchor, not required.

### 4. Flags

Inside a kept run, each aligned position gives:

| Alignment | Result |
|---|---|
| Equal | Protected: never flagged by any signal |
| Variant (rasm equal) | Protected, `scores.quran.variant = true` for the record. Not flagged |
| Substitution | Flag, reason `quran_mismatch`, candidate the verse's word (source `quran`, score 0.9), error probability 0.8 (fitted on the gold set) |
| Inserted transcript word | Flag `quran_mismatch` on that word, no candidate («a word not in the verse») |
| Missing verse word | Flag `quran_mismatch` on the following transcript word with `scores.quran.missing` = the verse's word, offered as an "insert before" candidate |

`quran_mismatch` **replaces** the ordinary signals for that word (`not_in_dictionary`,
`low_confidence`): the verse decides, not the dictionary. `scores.quran` holds `{surah, aya,
word_index_in_aya, expected, kind: sub|ins|del}`. The verse reference is shown in the queue as
«البقرة ١٨٣» / "Al-Baqara 2:183" (`VerseRef.label`). Adjacent mismatches merge into one span as
in 52.6, at most 4 words. A run with more than 3 mismatches per 10 words is not flagged word by
word: it is taken as not really a verse run, and its words go back to the ordinary signals.

### 5. Where it runs

In `word_check` (52.6), as a step before the per-word signals: the Arabic pack's `scripture()`
returns the runs and their alignments; `protected` covers equal and variant words; mismatches are
emitted as `quran_mismatch` flags. It counts against the budget like any flag, ranked by its
priority. The library setting `word_review.reasons.quran_mismatch` (52.14, default on) turns it
off; off means verse runs are protected as in the first draft, never checked by the ordinary
signals.

### 6. Strings (English + Arabic)

`review.reason.quran_mismatch`: "Differs from the Quran text" / «يختلف عن نص القرآن».
`review.quran.expected`: "The verse has: {word}" / «في الآية: {word}».
`review.quran.missing`: "A word of the verse may be missing: {word}" / «قد تكون كلمة من الآية ناقصة: {word}».
`review.quran.inserted`: "Not in the verse" / «ليست في الآية».
`review.quran.credit`: "Quran text: Tanzil Project (tanzil.net)" / «نص القرآن: مشروع تنزيل (tanzil.net)».
`wordReview.settings.reason.quran_mismatch`: "Check Quran verses against the Quran text" /
«مطابقة آيات القرآن مع نص القرآن».

## Acceptance criteria

1. **Given** a sentence reciting Al-Fatiha 1:2-4 correctly, **when** checked, **then** no word of
   it is flagged, by this check or any other.
2. **Given** the same verses with «العالمين» heard as «العالمون», **when** checked, **then** the run
   is still found, exactly that word is flagged `quran_mismatch`, the candidate is «العالمين» with
   source `quran`, and `scores.quran` says 1:2.
3. **Given** a verse word written with a different orthography that has the same rasm (الصلوة for
   الصلاة) or a canonical reading variant (يعلمون for تعلمون), **when** checked, **then** it is not
   flagged and `scores.quran.variant` is recorded.
4. **Given** a verse word missing from the transcript, **when** checked, **then** the next word is
   flagged with `scores.quran.missing` and the missing word offered as a candidate.
5. **Given** a reciter who repeats the last half of a verse, **when** checked, **then** the repeat
   is not flagged.
6. **Given** the isti'adha and basmala before a recitation, **when** checked, **then** neither is
   flagged and the run starts after them.
7. **Given** a lecture sentence with 3 words that happen to match a verse and then diverge,
   **when** checked, **then** no run is kept (fewer than 4 words) and ordinary signals apply.
8. **Given** an anchor that matches two places in the Quran equally well, **when** checked,
   **then** the run is left unchecked: no `quran_mismatch`, no protection.
9. **Given** an old transcript without `word_data`, **when** checked, **then** verse runs are found
   from the text and checked the same way.
10. **Given** `reasons.quran_mismatch = false`, **when** checked, **then** verse runs are protected
    and no `quran_mismatch` flag is made.
11. **Given** the shipped `quran-simple-clean.txt`, **when** the test suite runs, **then** its
    SHA-256 equals the pinned value (the text is never modified), and every surah's verse count
    matches `surahs.json`.
12. **Given** the review screen of a `quran_mismatch` flag, **then** the verse reference, the
    expected word and the Tanzil credit show in English and Arabic.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/language_packs/ar/quran.py` (new): `match_key`, `rasm_key`,
  the anchored aligner, `scripture()` returning `ScriptureRun(start, end, ref, ops)`.
- `pipeline/src/maktaba_pipeline/language_packs/base.py`: the optional `scripture()` method and
  `ScriptureRun` (packs without one return `[]`).
- `pipeline/src/maktaba_pipeline/classify/quran.py`: expose the index for the aligner (no change to
  `verse_runs` or title matching).
- `pipeline/src/maktaba_pipeline/word_review/signals.py`: the `quran_mismatch` signal and its
  precedence over the others.
- `pipeline/src/maktaba_pipeline/data/quran/LICENSE-TANZIL.txt`: the use paragraph (the text file
  itself is not touched).
- Reason code `quran_mismatch` and candidate source `quran` in the shared contract: Go constants
  (`api/internal/handlers/review/`), `web/src/lib/reviewApi.ts`, the Python writer.
- Strings in `web/src/i18n/en.json` / `ar.json`, Android `values/strings.xml` /
  `values-ar/strings.xml`, Apple `Localizable.xcstrings` (en + ar).

### Tests

- `pipeline/tests/language_packs/ar/test_quran_check.py`: the criteria above, on the real bundled
  text (small, already in the repo, no model needed, so it runs in Linux CI).
- Normaliser tests: every Quranic mark in U+06D6-U+06ED, dagger alef, alef wasla, and a table of
  known orthographic pairs (الصلوة/الصلاة, الزكوة/الزكاة, الربوا/الربا, ملك/مالك) giving equal rasm
  keys; and a table of real errors (ذ/ز, ث/س, ع/ء) giving unequal ones.
- Seeded-error fixtures: 200 verse runs from the text with one word changed by a confusion pair,
  dropped or inserted; the check must flag ≥ 0.80 of them and flag nothing in the 200 unchanged
  runs.
- Determinism: the same input twice gives identical flags.
- Licence guard: the SHA-256 pin and verse counts (criterion 11).
- Eval: 52.2's gold set reports Quran-run precision and recall separately (its Quran clips).
- Contract test: `quran_mismatch` and `quran` are known to Go, Python and the web.

### Rollout

- Ships in phase 1 with 52.6; on by default. The backfill (52.6 §7) checks old transcripts'
  verse runs too.
- Live check (read-only) after a day: `quran_mismatch` flags per audio hour on recitation-heavy
  libraries; if a library shows many on clearly correct recitations, lower its sensitivity or turn
  the reason off and fix the aligner before the queue opens (52.11).
- Back out: `reasons.quran_mismatch = false` (verse runs go back to protected).

## Edge cases

- **Other readings than Hafs:** Simple Clean follows the Hafs reading. Differences that keep the
  rasm are variants (not flagged); a reading difference that changes the rasm would be flagged,
  and the admin accepts it as is; "Add to dictionary" does not apply inside a verse, so accepting
  is per flag.
- **A verse quoted with a word paraphrased by the lecturer:** flagged once; the admin accepts it.
  A run with more than 3 mismatches in 10 words is not treated as a verse at all (§4).
- **Verses that repeat in the Quran (e.g. «فبأي آلاء ربكما تكذبان»):** many places match; the
  context words decide, else the run is unchecked (criterion 8). The words themselves are equal at
  every place, so no false flag either way.
- **Huruf muqatta'at (الم، حم):** Whisper may write the letter names («ألف لام ميم»). These are
  matched as a known variant list at the start of the surahs that have them.
- **English transcript of a recitation:** the Arabic pack is not used for Latin runs; no check.
