# Epic 52 — Word review: suspect words go to a person, in every language

> **Status:** spec (2026-11-03), owner's decisions applied the same day. **Source:** [`specs/epics/52-transcript-word-review/`](../../../specs/epics/52-transcript-word-review/README.md).
> **Anchors:** `pipeline/src/maktaba_pipeline/stt/refine.py` (the second listen) ·
> `stt/align.py` (word probabilities) · `engine/stages/transcribe.py` (`_store`) ·
> `text/arabic.py` (folding) · `classify/quran.py` + `data/quran/` (the Tanzil Quran text).

## Goal

After the owner's hearing tests set how Maktaba transcribes (Whisper turbo, then a second listen
by Cohere Arabic, merged word by word), the remaining errors are single words: a misheard name,
a misspelt term, a word the two listeners disagree on. This epic points at those words and lets a
person fix them fast:

1. **Keep the evidence.** Each new transcript keeps every word's time, Whisper's confidence
   (computed today, then thrown away) and the second listener's version, packed at about 10 bytes
   a word.
2. **Point at suspect words.** A CPU queue stage, `word_check`, saves each suspect word's place
   (transcript, sentence, characters, start/end time) with its reasons: low confidence, not in
   the dictionary, listeners disagree, a name, or differs from the Quran text. It also saves
   suggested spellings. Every check is deterministic; there is no LLM judge.
3. **A person decides.** A keyboard-first review queue on the web and in the native apps, for
   home admins: hear the clip, see the sentence, then accept, correct, mark as a name or add to
   the dictionary. A fix updates the transcript, subtitles and search at once, with history and
   undo, and without running speech recognition again.
4. **Every fix teaches.** A per-library and per-language vocabulary stops re-flagging, finds the
   same word elsewhere, and feeds the second listen's speller and new transcriptions, without
   re-transcribing the library.
5. **Every language.** Language packs are installable Epic 51 components: a normaliser,
   tokenizer, dictionary, morphology and suggestions. Arabic (CAMeL Tools morphology + Hunspell)
   and English (Hunspell) ship first; a generic Hunspell pack covers about 100 more languages;
   any language without a full pack gets the fallback pack (Unicode word segmentation + word
   frequencies).
6. **Quran verses are checked, not skipped.** Verse runs are aligned with the bundled Tanzil text
   (CC BY 3.0, verbatim) and only words that really differ are flagged, as `quran_mismatch`.
7. **The whole library.** All ~45k existing transcripts are text-checked in the background at the
   lowest priority, only while transcription is idle.

## Stories

| # | Story | Phase |
|---|---|---|
| 52.1 | Keep every word's time, confidence and second listen | 1 |
| 52.2 | Evaluation set, metrics and calibration | 1 |
| 52.3 | The language pack contract, registry, generic Hunspell and fallback packs | 1 |
| 52.4 | Arabic language pack | 1 |
| 52.5 | English language pack | 1 |
| 52.6 | `word_check`: flags, names and the backfill of old transcripts | 1 |
| ~~52.7~~ | ~~The LLM judge that flags, not rewrites~~ **Removed 2026-11-03** | — |
| 52.8 | Review API, roles (home admins only) and the audio clip | 2 |
| 52.9 | Applying fixes: edits, undo, subtitles, search, carry-over | 2 |
| 52.10 | Vocabulary: learning from fixes | 3 |
| 52.11 | Web review queue | 2 |
| 52.12 | Fix a word from the transcript | 2 |
| 52.13 | Review in the native apps | 4 |
| 52.14 | Settings, thresholds, budget and queue health | 2 |
| 52.15 | Quran check: verse runs against the Quran text | 1 |

14 stories to build (52.7 is a removed stub kept for its number).
Build order and dependency DAG: [`plan.md`](../../../specs/epics/52-transcript-word-review/plan.md).
Research with sources: [`research.md`](../../../specs/epics/52-transcript-word-review/research.md).

## Targets

- Flag precision: Arabic ≥ 0.50 and English ≥ 0.60 at first, ≥ 0.65 / 0.70 after calibration.
  Recall ≥ 0.60. At most 3% of words flagged. At least 18 of the owner's 20 hearing-test spots
  flagged.
- Quran check: no flag on a correctly heard verse run; ≥ 0.80 of seeded verse-word errors flagged.
- Flags visible within 2 minutes of a transcript. A fix takes ≤ 300 ms, with subtitles rewritten
  within 60 s.
- `transcribe` + < 1% time, `word_check` ≤ 10 CPU-seconds per audio hour. No GPU after
  `transcribe`. The same input always gives the same flags.

## Who reviews

Home admins only. A user with `write` or the library `admin` role on a library may not review its
words, see its flags or edit its vocabulary. Viewers and cloud-linked sign-ins never see the
queue. The API enforces it, with role tests that expect 403 for `write`.

## Decisions (2026-11-03)

1. Reviewers: home admins only.
2. Old transcripts: all ~45k are text-checked in the background, at low priority.
3. LLM judge: none. Story 52.7 removed; checks are deterministic (CAMeL Tools for Arabic,
   Hunspell for English and others); languages are installable packs with a fallback.
4. Quran: verse runs are checked against the Quran text with their own reason code,
   `quran_mismatch` (story 52.15).

Third-party language packs, beyond the built-in and Hunspell ones, will come through Epic 54's
plugin system and store (built on Epic 51's components); this epic does not design it.
