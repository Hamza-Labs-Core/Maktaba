# Story 52.01 — Keep every word's time, confidence and second listen

> Epic 52 · Phase 1 · Depends on: — · Needed by: 52.2, 52.6, 52.9

## Why

Whisper works out how sure it is of each word (`stt/align.py:175`, `probability`), then
`stt/batched.py:281-283` turns each word into `(start, end, text)` and the score is gone. Word
times are stored only with `MAKTABA_STT_STORE_WORDS=1` (default `0`,
`engine/stages/transcribe.py:1230`), and never with a probability. The second listen
(`stt/refine.py` `merge_words`) knows exactly which words it replaced and what Whisper had
heard there, but keeps only counts in `transcripts.metadata.refine`.

That evidence exists only while the transcribe job runs. This story keeps it, at a cost small
enough for the whole library, so `word_check` (52.6) can decide later without the GPU.

## Description

### 1. Carry the probability through

- `Sentence.words` (`stt/settings.py`) becomes `list[Word]`, with `Word` a slotted dataclass
  `(start, end, text, prob, flags)`. `prob` is Whisper's word probability (0-1, `None` when
  the engine gives none). The old tuple shape is kept as `Word.as_tuple()` for callers that
  only need time and text (`refine.TimedWord`).
- `stt/batched.py` keeps `w["probability"]`. `_plain` in `stt/settings.py` saves it in the
  checkpoint too, so a resumed job gives the same evidence as an uninterrupted one.
- The CTranslate2 path (`stt/ct2.py`) passes `Word.probability` from faster-whisper the same
  way.

### 2. Keep what the second listen heard

`refine.merge_words` marks each output word:

- bit 0 `heard_differently`: Cohere's word replaced Whisper's here;
- bit 1 `inserted`: Cohere heard a word Whisper missed (no Whisper probability, `prob = None`);
- bit 2 `quran`: inside a verse run (`classify/quran.verse_runs`), never replaced. 52.15's Quran
  check uses it as a hint for its anchors, but finds runs from the text too.

For each replaced span it returns `{i, n, whisper, cohere}`: the word index in the sentence, the
number of words, what Whisper heard and what Cohere heard. These go to
`transcript_segments.metadata.alt` (a list, empty lists not stored). A replaced word keeps the
probability of the Whisper word it replaced, and gets bit 0, so a low score on a replaced word
still counts.

### 3. The packed `word_data`

One `BYTEA` per sentence, on `transcript_segments.word_data` (new nullable column):

```
version:u8 (=1) | n:u16
n × { char_start:u16, char_len:u8, start_cs:u16, dur_cs:u16, prob:u8, flags:u8 }
```

- Little-endian. 10 bytes a word, 3-byte header.
- `char_start`, `char_len`: the word's place in `transcript_segments.text`, counted in **Unicode
  code points** (not bytes, not UTF-16 units). Python indexes `str` by code point. Go converts
  with `[]rune`. The web converts from UTF-16 with `Array.from`.
- `start_cs`: centiseconds from the sentence's `start_sec` (a sentence is under 655 s).
  `dur_cs`: centiseconds long.
- `prob`: `round(prob × 255)`; `255` with `flags` bit 3 `no_prob` set when the engine gave none.
- A word whose text cannot be found in the sentence text, in order, is skipped and counted in
  `metadata.word_misses`. The text is the source of truth. `word_data` points into it.
- Sentences of over 65,535 code points or 65,535 words (none seen) store no `word_data`.

`MAKTABA_STT_STORE_WORDS` is retired. Words are always kept. `metadata.words` is no longer
written (old rows keep theirs). Readers prefer `word_data`, then `metadata.words`, then nothing.

### 4. One format, two languages

- Python: `pipeline/src/maktaba_pipeline/stt/word_data.py` has `pack(text, words) -> bytes` and
  `unpack(text, data) -> list[Word]`.
- Go: `api/internal/transcript/worddata` has `Decode`, `Encode` and `ShiftAfter(data,
  charStart, delta)` for 52.9's edits.
- Golden fixtures in `shared/testdata/word_data/` (`*.json` input, `*.bin` expected) are read by
  both test suites: Arabic with harakat, mixed Arabic/English, an emoji (a non-BMP code point),
  an inserted word, a Quran run, an empty sentence.

### 5. Transcribe version

`TRANSCRIBE_VERSION` does **not** change. Old transcripts stay valid. They simply have no
`word_data`, and 52.6 handles that with text-only checks.

## Acceptance criteria

1. **Given** a file transcribed on MLX with word timestamps on, **when** the transcript is stored,
   **then** every `transcript_segments` row with words has a non-null `word_data` whose decoded
   words cover the sentence text in order, with `prob` equal to Whisper's probability ± 1/255.
2. **Given** the same file on the CTranslate2 engine, **when** it is stored, **then**
   `word_data` is filled the same way from faster-whisper's `Word.probability`.
3. **Given** a stretch where Cohere replaced two Whisper words, **when** it is stored, **then**
   those words have flag bit 0, and `metadata.alt` holds `{i, n: 2, whisper, cohere}` with both
   texts.
4. **Given** Cohere inserted a word Whisper missed, **when** stored, **then** that word has bit 1
   and `no_prob` set.
5. **Given** a verse run, **when** stored, **then** its words have bit 2 and no `alt` entry.
6. **Given** a job stopped and resumed from its checkpoint, **when** it finishes, **then** its
   `word_data` is byte-identical to an uninterrupted run's.
7. **Given** a sentence containing an emoji and Arabic harakat, **when** Python packs it and Go
   decodes it, **then** both give the same code-point offsets (the shared golden fixture).
8. **Given** a word whose text is not found in the sentence, **when** packed, **then** it is
   skipped, `metadata.word_misses` counts it, and the job does not fail.
9. **Given** the second listen is off or skipped, **when** stored, **then** `word_data` is still
   written with Whisper's words and no flag bits 0-1.
10. **Given** 1 hour of Arabic lecture audio, **when** transcribed with and without this story,
    **then** the job's wall time differs by under 1%, and `word_data` averages ≤ 12 bytes a word.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/stt/settings.py`: `Word`, `Sentence.words`, `_plain`.
- `pipeline/src/maktaba_pipeline/stt/batched.py`: keep `probability`.
- `pipeline/src/maktaba_pipeline/stt/ct2.py`: the same on CTranslate2.
- `pipeline/src/maktaba_pipeline/stt/refine.py`: flags and `alt` from `merge_words`;
  `RefineStats` unchanged.
- `pipeline/src/maktaba_pipeline/stt/word_data.py` (new): `pack`, `unpack`.
- `pipeline/src/maktaba_pipeline/engine/stages/transcribe.py` `_store`: write `word_data` and
  `metadata.alt`; drop `MAKTABA_STT_STORE_WORDS`.
- `shared/db/migrations/` next free slot, 0182+: `ALTER TABLE transcript_segments ADD COLUMN
  word_data BYTEA` (metadata-only, no rewrite of the 23 M rows), Postgres and SQLite twins.
- `api/internal/transcript/worddata/` (new): `Decode`, `Encode`, `ShiftAfter`.
- `shared/testdata/word_data/` (new): golden fixtures.

### Tests

- `pipeline/tests/stt/test_word_data.py`: round-trip, golden fixtures, misses, limits.
- `pipeline/tests/stt/test_refine.py`: flags and `alt` for replace, insert, verse run,
  low-agreement stretch (no flags).
- `pipeline/tests/stt/test_batched.py`: probability carried; checkpoint resume identical
  (fake decoder, no model).
- `pipeline/tests/stages/test_transcribe.py`: `_store` writes `word_data` on SQLite and on a
  scratch Postgres.
- `api/internal/transcript/worddata/worddata_test.go`: the same golden fixtures; `ShiftAfter`.
- conftest's guard (PR #565) stays: no test loads the real Whisper or Cohere model.
- Bench (manual, on the Mac, recorded in the PR): 1 h file, wall time and bytes per word.

### Rollout

- Ships on by default for new transcripts. Nothing reads `word_data` until 52.6.
- After a day live: read-only query of `avg(octet_length(word_data))` per word. If over 12
  bytes, stop and revisit before 52.6 ships.
- Back out: revert the code. The column stays nullable and unused.

## Edge cases

- Whisper splits an Arabic word with a clitic oddly (`و` alone): the pack's tokens (52.3) may
  differ from Whisper's words. `word_data` keeps Whisper's words. 52.6 maps between them by
  code-point overlap.
- A word's probability can be high on a hallucinated sentence. `no_speech_prob` and
  `avg_logprob` stay in the segment row for 52.6 to use.
- A sentence longer than 655 s cannot fit `start_cs`. This never happens with Whisper's 30 s
  windows, but the packer refuses it rather than wrapping around.
- An engine that gives no word timestamps (`openai-api`) stores no `word_data`.
