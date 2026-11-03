# Epic 52 — Word review: suspect words go to a person, in every language

> **Status:** spec (2026-11-03). **Source:** [`specs/epics/52-transcript-word-review/`](../../../specs/epics/52-transcript-word-review/README.md).
> **Anchors:** `pipeline/src/maktaba_pipeline/stt/refine.py` (the second listen) ·
> `stt/align.py` (word probabilities) · `engine/stages/transcribe.py` (`_store`) ·
> `text/arabic.py` (folding) · `llm/` (`cluster_llm`).

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
   the dictionary, listeners disagree, a name, or (optionally) an LLM disagrees. It also saves
   suggested spellings.
3. **A person decides.** A keyboard-first review queue on the web and in the native apps: hear
   the clip, see the sentence, then accept, correct, mark as a name or add to the dictionary. A
   fix updates the transcript, subtitles and search at once, with history and undo, and without
   running speech recognition again.
4. **Every fix teaches.** A per-library and per-language vocabulary stops re-flagging, finds the
   same word elsewhere, and feeds the second listen's speller, the LLM judge and new
   transcriptions, without re-transcribing the library.
5. **Every language.** Language packs are add-on components (Epic 51): a normaliser, tokenizer,
   dictionary, morphology and suggestions. Arabic (CAMeL Tools + Hunspell) and English
   (Hunspell) ship first; any other language gets a fallback pack (Unicode word segmentation +
   word frequencies).

The LLM is a judge that flags, never a writer, and is off by default. In the hearing test an LLM
judge favoured turbo's tidy spelling and was wrong.

## Stories

| # | Story | Phase |
|---|---|---|
| 52.1 | Keep every word's time, confidence and second listen | 1 |
| 52.2 | Evaluation set, metrics and calibration | 1 |
| 52.3 | The language pack contract, registry and fallback pack | 1 |
| 52.4 | Arabic language pack | 1 |
| 52.5 | English language pack | 1 |
| 52.6 | `word_check`: flags, names and old transcripts | 1 |
| 52.7 | The LLM judge that flags, not rewrites | 3 |
| 52.8 | Review API, roles and the audio clip | 2 |
| 52.9 | Applying fixes: edits, undo, subtitles, search, carry-over | 2 |
| 52.10 | Vocabulary: learning from fixes | 3 |
| 52.11 | Web review queue | 2 |
| 52.12 | Fix a word from the transcript | 2 |
| 52.13 | Review in the native apps | 4 |
| 52.14 | Settings, thresholds, budget and queue health | 2 |

Build order and dependency DAG: [`plan.md`](../../../specs/epics/52-transcript-word-review/plan.md).
Research with sources: [`research.md`](../../../specs/epics/52-transcript-word-review/research.md).

## Targets

- Flag precision: Arabic ≥ 0.50 and English ≥ 0.60 at first, ≥ 0.65 / 0.70 after calibration.
  Recall ≥ 0.60. At most 3% of words flagged. At least 18 of the owner's 20 hearing-test spots
  flagged.
- Flags visible within 2 minutes of a transcript. A fix takes ≤ 300 ms, with subtitles rewritten
  within 60 s.
- `transcribe` + < 1% time, `word_check` ≤ 10 CPU-seconds per audio hour, the LLM judge ≤ 3
  GPU-minutes per audio hour (only when the queue is idle).

## Who reviews

Home admins review everything. A user with `write` or `admin` on a library reviews that library.
Viewers and cloud-linked sign-ins never see the queue. The API enforces it, with role tests.

## Open questions for the owner

Household reviewers (`write`) or admins only? Check the existing transcripts too? Turn the LLM
judge on, and with which model? Leave Quran verse runs out of review?
