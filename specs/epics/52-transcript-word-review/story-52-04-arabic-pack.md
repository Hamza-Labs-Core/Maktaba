# Story 52.04 — Arabic language pack

> Epic 52 · Phase 1 · Depends on: 52.3, 52.16 · Needed by: 52.6, 52.15
> Code in the pipeline package; data in the Epic 51 asset `lang.ar` (Ayaspell) plus the existing
> `model.refine` (CAMeL morphology DB), both from `shared/components/catalog.toml`.

## Why

Most of the library is Arabic, and Arabic is where a word list fails. One stem takes hundreds
of forms through prefixes (و، ف، ب، ك، ل، ال، س), suffixes (ه، ها، هم، كم، نا…) and inflection.
«وسيكتبونها» is a correct word that no plain list holds. The pipeline already has what solves
this: the CAMeL Tools MSA morphological analyser (`stt/cohere.py` `dictionary()`, data
`morphology-db-msa-r13`) and Arabic folding (`text/arabic.py`). This story wraps them in the
pack contract.

## Description

### 1. Normalising and keys

- `normalise(text)`: NFC, strip bidi controls and tatweel (U+0640). Harakat (U+064B-U+0652,
  U+0670) are **kept** for display. A reviewer sees what was written.
- `key(token)`: `text.arabic.fold`: harakat and tatweel gone, أ/إ/آ/ٱ → ا, ة → ه, ى → ي, ؤ → و,
  ئ → ي, Arabic-Indic digits → Latin. Also Persian look-alikes ی (U+06CC) → ي and ک (U+06A9) → ك,
  which Whisper sometimes emits. The pack's `version` includes `ARABIC_VERSION`.

### 2. Tokens

- Words are runs of Arabic letters plus harakat. Clitics stay attached (Whisper writes them
  attached). The analyser handles them.
- `kind=number` for digit runs (Arabic-Indic or Latin), `punct` for ، ؛ ؟ and Latin punctuation.
- The honorific ligatures ﷺ (U+FDFA) and ﷻ (U+FDFB) are `symbol`, never words.

### 3. Known

`known(token)`, on the token with harakat stripped:

1. A number or single letter: `KNOWN`.
2. The CAMeL analyser (`camel_tools.morphology.analyzer.Analyzer`, MSA DB r13, backoff
   `NOAN_PROP` off) returns analyses: `KNOWN`, with the top analysis's lemma and POS as the
   explanation shown to the reviewer (e.g. «كَتَبَ · verb»).
3. No analysis, but the token with a common dialect clitic peeled (Levantine/Egyptian `ب`
   present prefix, `ش` negation suffix, `ح`/`ه` future prefix) has one: `UNSURE`. Dialect words
   are not errors.
4. Hunspell `ar` (Ayaspell) knows it: `KNOWN`. This catches nouns the analyser lacks.
5. Otherwise `UNKNOWN`.

Results are cached per folded key (LRU, 200 k entries; an hour of lecture has ~1,500 distinct
keys, so ~95% hits after the first minutes).

### 4. Suggest

In order, deduplicated by `key`, at most `n`:

1. Spellings of the same `key` that the analyser accepts (restores hamza, «ة», «ى»: what
   `refine.Speller` already does).
2. Hunspell `ar` suggestions (edit distance on letters).
3. Analyser-accepted words one letter away on letters Whisper confuses in Arabic speech: ذ/ز/ظ,
   ث/س/ص, ض/د/ظ, ق/ك/ء, ح/ه, ع/ء/ا, ت/ط.

### 5. Protected and scripture (Quran)

README §8, decision 4, is to **check** verse runs against the Quran text rather
than never flag them. The pack's `scripture()` returns the verse runs found by the Quran aligner
of story 52.15 (tolerant alignment against the bundled Tanzil text). `protected()` covers the
words of those runs that equal the verse (or are a rasm variant of it); the words that differ are
reported by 52.15 as `quran_mismatch`, not by this pack's dictionary. When the library turns the
Quran check off, every word of a run is protected, as in the first draft.

### 5a. Deterministic

Every answer of this pack is deterministic: the same token, data files (`data_sha256`) and
`version` give the same `known()`, `suggest()` and keys. No model is sampled and no LLM is called
(README §8, decision 3).

### 6. Data and licences

- CAMeL Tools (MIT, in the `stt` extra that `runtime.ml` installs) and its MSA morphology DB:
  the existing catalog asset `model.refine` (`source = "pack:model.refine"`), today fetched by
  `model_dirs.ensure_camel_data`. Nothing new: `review.words` lists `model.refine` in `wants`, so
  a home without it (a Core home, no `runtime.ml`) runs the pack without the analyser (criterion 8)
  and `review.words` shows *Degraded* (`want_missing:model.refine`).
- Ayaspell Hunspell `ar` dictionary: the new asset `lang.ar` (`source = "pack:lang.ar"`, recipe
  `tools/release/packs/recipes/lang.ar.sh`, 52.16), from the LibreOffice dictionaries repository
  at a pinned commit. Its licence is checked and recorded in the pack's `NOTICE` before shipping
  (GPL/LGPL/MPL tri-licence, data only, loaded at run time, not linked).
- The pack finds its files with `components.packs.pack_dir("lang.ar")` (52.16). `data_sha256`
  (52.3) pins them.

## Acceptance criteria

1. **Given** «وسيكتبونها», **when** `known()` runs, **then** it returns `KNOWN` with a verb lemma.
2. **Given** «اسلام» and «إسلام», **when** keyed, **then** both give the same key.
3. **Given** «بيكتب» (Levantine present), **when** `known()` runs, **then** it returns `UNSURE`, not
   `UNKNOWN`.
4. **Given** a nonsense token «كتسبلمن», **when** `known()` runs, **then** it returns `UNKNOWN`.
5. **Given** «مدرسه», **when** `suggest()` runs, **then** «مدرسة» is first.
6. **Given** «ﷺ» in a sentence, **when** tokenised, **then** it is a `symbol`, never checked.
7. **Given** a sentence with a correctly heard verse run, **when** `protected()` runs, **then**
   exactly the run's tokens are protected; **given** the same run with one misheard word, **then**
   `scripture()` still returns the run and that word is not protected (52.15 flags it).
8. **Given** a worker without CAMeL data, **when** `known()` runs, **then** every word is `UNSURE`
   (no `not_in_dictionary` flags), and the pack row says `analyser=unavailable`.
9. **Given** 9,000 tokens of lecture text, **when** checked cold then warm, **then** warm takes
   under 1 CPU-second on the home Mac, and the conformance suite (52.3) passes.
10. **Given** the gold set (52.2), **when** `not_in_dictionary` alone is evaluated on Arabic,
    **then** its precision is reported (target ≥ 0.4 on its own; it is one signal of several).

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/language_packs/ar/__init__.py`, `pack.py`, `dialect.py`,
  `confusions.py`, `samples.txt`, `NOTICE` (new).
- `pipeline/src/maktaba_pipeline/text/arabic.py`: add ی/ک folding, bump `ARABIC_VERSION` (search
  rebuilds folded caches, the same rule as today; check Epic 49's `maktaba_search` config agrees).
- `pipeline/src/maktaba_pipeline/stt/cohere.py`: `dictionary()` reuses the pack's loaded analyser,
  so the process holds one copy.
- No download code: the data arrives as the `lang.ar` pack (52.16).

### Tests

- `pipeline/tests/language_packs/ar/test_pack.py`: the criteria above with a tiny fake analyser
  and a 200-word Hunspell fixture. Real CAMeL data only in a test marked `needs_camel_data`,
  skipped when absent (never in Linux CI).
- The conformance suite on `ar`.
- `pipeline/tests/text/test_arabic.py`: the new folds; search folding unchanged otherwise.
- Eval: `eval --set ci` reports `not_in_dictionary` precision for Arabic.

### Rollout

- Loaded only by `word_check`. The analyser is already in memory on transcribing machines.
- Back out: `maktaba-server components remove lang.ar` (or `DELETE /api/components/lang.ar`;
  an asset is on disk or not, it has no "off"). Arabic falls back to the fallback pack (no dictionary flags).

## Edge cases

- Names (people, places) that the analyser parses as a common word (e.g. «كريم» as an adjective)
  are `KNOWN`. Only 52.6's gazetteer can tell. That is accepted.
- Classical words absent from MSA DB r13: Hunspell may know them. If not, they are `UNKNOWN`,
  and the reviewer's "Add to dictionary" (52.10) teaches the library.
- Harakat that change meaning (كَتَبَ vs كُتُب): keys fold them. The reviewer sees the original.
- Transliterated English in Arabic letters («الكمبيوتر») may be `UNKNOWN`. Vocabulary fixes it
  per library.
