# Story 52.05 — English language pack

> Epic 52 · Phase 1 · Depends on: 52.3 · Needed by: 52.6
> Installed as an Epic 51 component (`langpack-en`). Deterministic Hunspell checks only (README §8,
> decision 3, 2026-11-03: no LLM judge). Other languages use 52.3's generic Hunspell pack; English
> has its own pack for its tokenizer rules (contractions, possessives, acronyms).

## Why

English is the library's second language, both in English talks and inside Arabic lectures
(code-switching, PR #556's two-track files). English needs no morphology engine. Hunspell's affix
rules cover its inflection, and its dictionaries are mature. The pack proves the contract works
for a second script before any third language arrives.

## Description

### 1. Normalising and keys

- `normalise`: NFC; curly quotes and apostrophes (U+2018/2019) → `'`.
- `key`: casefold, strip leading/trailing apostrophes, and fold diacritics (`café` → `cafe`) with
  NFD + removal of combining marks.

### 2. Tokens

- Regex words over Latin letters, apostrophes inside words (`don't`, `Ahmad's`) and hyphens inside
  words (`well-known`). A hyphenated word is checked part by part.
- Numbers, ordinals (`3rd`) and times (`10:30`) are `number`.
- All-caps acronyms of 2-6 letters (`NASA`, `PhD`) are `word` with a hint flag, checked against a
  small acronym list, else `UNSURE`.

### 3. Known

1. Hunspell `en_US`, then `en_GB` (SCOWL-based dictionaries from LibreOffice), on the token as
   written, then in lower case: `KNOWN`.
2. Possessive `'s` stripped, then checked: `KNOWN`.
3. Capitalised mid-sentence and unknown: `UNSURE` (likely a name; 52.6's gazetteer decides).
4. Otherwise `UNKNOWN`.

Hunspell through `spylls` (pure Python, slow but portable) or `cyhunspell` (C, faster) where its
wheel installs. The pack row records which. A per-key LRU cache as in 52.4.

### 4. Suggest

Hunspell suggestions, then words from `wordfreq` within edit distance 1 sorted by frequency,
deduplicated by key.

### 5. Protected

Nothing by default. A quoted Arabic transliteration in Latin letters (`subhanallah`,
`insha'Allah`) is common in the library's English talks. A built-in list of ~200 such
transliterations ships with the pack as `KNOWN` (and is the seed of the English vocabulary).

### 6. Data and licences

LibreOffice `en_US`/`en_GB` dictionaries (SCOWL licence, permissive) in the pack's data folder,
`data_sha256` pinned, `NOTICE` recorded.

## Acceptance criteria

1. **Given** `running`, `ran`, `runs`, **when** `known()` runs, **then** all are `KNOWN`.
2. **Given** `colour` and `color`, **when** checked, **then** both are `KNOWN` (US then GB).
3. **Given** `teh`, **when** `known()` and `suggest()` run, **then** `UNKNOWN`, and `the` is the
   first suggestion.
4. **Given** `Ahmad's` mid-sentence and not in the dictionary, **when** checked, **then** `UNSURE`.
5. **Given** `insha'Allah`, **when** checked, **then** `KNOWN` from the transliteration list.
6. **Given** `don't`, `well-known`, `10:30`, **when** tokenised, **then** they are one word, a
   two-part word, and a number.
7. **Given** an English run inside an Arabic sentence, **when** 52.3's router sends it, **then**
   offsets are code points of the whole sentence.
8. **Given** the conformance suite, **when** run on `en`, **then** it passes.
9. **Given** the gold set's English hour, **when** `not_in_dictionary` alone is evaluated, **then**
   precision is reported (target ≥ 0.5 on its own).

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/language_packs/en/__init__.py`, `pack.py`, `acronyms.txt`,
  `transliterations.txt`, `samples.txt`, `NOTICE` (new).
- `pipeline/src/maktaba_pipeline/model_dirs.py`: data fetch for `en`.

### Tests

- `pipeline/tests/language_packs/en/test_pack.py`: the criteria above against a small Hunspell
  fixture (a 2,000-word `.dic` + the real `.aff`), both `spylls` and `cyhunspell` paths when
  installed.
- The conformance suite on `en`.
- Eval: `eval --set ci` English numbers.

### Rollout

Same as 52.4. Back out: disable `en`. English runs fall back to the fallback pack.

## Edge cases

- Whisper outputs British and American spellings in one file. Both are `KNOWN`, never flagged.
- Proper nouns at the start of a sentence look like any capitalised word. Only the gazetteer
  (52.6) and vocabulary (52.10) tell names apart.
- Numbers written out (`twenty-three`) are words and are known.
- Profanity or slang absent from SCOWL: `UNKNOWN`. Vocabulary teaches it per library.
