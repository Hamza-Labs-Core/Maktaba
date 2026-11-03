# Epic 52: Word review: suspect words go to a person, in every language

> Status: spec (2026-11-03). Depends on: Epic 03 (transcription, plan 03-10), Epic 04
> (subtitles), Epic 49 (Arabic-aware search, for the normaliser it shares), Epic 51
> (configurable components: language packs install as components). Research:
> [research.md](research.md). Build order and DAG: [plan.md](plan.md).
> Owner's decisions of 2026-11-03 are in §8: admins only review, every existing transcript is
> checked in the background, there is **no LLM judge** (story 52.7 removed), and Quran verse runs
> are checked against the Quran text (story 52.15).

## 1. Why

The owner's words (2026-11-03): *"transcription was done after some hearing tests i made, we can
make this so that a queue review each transcript, or the same step that sends for the 2nd llm
review, to save the location of a word and if its not available in dictionary or think its not
correct, or not confident, to save for human review and to provide correct spelling, also this
needs to be done so that it can support all languages, added as addons or whatever."*

The hearing tests (2026-10-19, trail §2 "Transcription and search", [#113]) settled how the
library is transcribed: Whisper large-v3-turbo, then a **second listen** by Cohere Transcribe
Arabic over each stretch, merged word by word (`pipeline/src/maktaba_pipeline/stt/refine.py`). On
20 disputed spots the owner's ears scored Cohere 17, turbo 0. **An LLM judge had favoured turbo's
tidy spelling and was wrong.** That result is the first design rule of this epic: a machine may
*point at* a word; a person decides what it is.

What we have today, read from the code on 2026-11-03:

| Fact | Where | What it means here |
|---|---|---|
| Whisper computes a per-word `probability` during alignment | `stt/align.py:175` | The confidence signal exists |
| …and throws it away: words become `(start, end, text)` tuples | `stt/batched.py:281-283`, `stt/settings.py:53`, `_plain` in `stt/settings.py` | Today nothing can say "Whisper was unsure of this word" |
| Word timings are stored only with `MAKTABA_STT_STORE_WORDS=1` (default `0`), in `transcript_segments.metadata.words`, without probability | `engine/stages/transcribe.py:1230,1262` | Existing transcripts have no word times. A word can be located only by its sentence |
| The engine path never writes `transcript_words` (the legacy path did, with `confidence`) | `shared/db/migrations/0012_transcripts_is_active.sql:66`, `stt/segment_commit.py:109` | A per-word table exists but is empty for engine transcripts |
| Segment confidence is `exp(avg_logprob)` | `engine/stages/transcribe.py:1272` | Only a sentence-level signal is stored |
| The second listen knows where Whisper and Cohere disagreed, and which words it replaced, but keeps only counts (`metadata.refine.changed`) | `stt/refine.py` `merge_words`, `RefineStats` | The strongest "suspect" signal we have is computed and dropped |
| Spelling of Cohere's words uses the CAMeL Tools MSA analyser as a dictionary | `stt/cohere.py:312` `dictionary()` | Arabic morphology is already a dependency |
| Arabic folding (hamza forms, ة/ه, ى/ي, harakat, tatweel) | `text/arabic.py` `light`/`fold` | The first language pack's normaliser exists |
| Names and terms bias Whisper through the library's `stt.initial_prompt` | `engine/stages/transcribe.py:852` `library_prompt` | A vocabulary channel into STT exists, **but the prompt is part of the transcribe fingerprint** (`input_fingerprint`, line 836): changing it queues the whole library again |
| There is no LLM anywhere in the transcript path; `llm.cluster_llm()` (Ollama / OpenAI-compatible runtimes on the cluster) is used only by enrich | `llm/__init__.py`, `enrich/reader.py:258` | "The 2nd LLM review" the owner names is today the second *listen*. The owner decided (2026-11-03) that no LLM judges words: every check is deterministic |
| Roles: `users.is_admin` and `library_acl.role IN ('read','write','admin')` | `0029_users.sql:31`, `0072_library_acl_role.sql:20` | Reviewing is for home admins only (decision 1); `library_acl` roles grant nothing here |
| Verse runs are found by exact folded match against the bundled Tanzil "Simple Clean" text | `classify/quran.py` `verse_runs`, `data/quran/` (CC BY 3.0) | The canonical text is already on every home; one wrong word breaks today's exact run, so the Quran check (52.15) aligns with tolerance |

So the epic is in three parts: **keep the evidence** (each word's time, confidence and the second
listener's version), **point at suspect words** with language packs that know each language's
spelling, and **let a person fix them fast**, with every fix teaching the next transcript.

## 2. Goals

1. Every new transcript keeps, for every word, its time, Whisper's confidence and what the second
   listener heard there, at a storage cost small enough for the whole library.
2. A queue job checks each transcript and saves each suspect word's place (transcript, sentence,
   word, start and end time) with the reasons, scores and suggested spellings.
3. A reviewer (a home admin) works through suspect words in a keyboard-first queue: hears the clip, sees the
   sentence, and accepts, corrects, marks as a name or adds to the dictionary. A fix updates the
   transcript, its subtitle files and search at once, without running speech recognition again,
   and can be undone.
4. Every fix teaches: a per-library and per-language vocabulary that stops the same word from
   being flagged again, checks other files for it, and feeds the second listen's speller and new
   transcriptions. It never re-transcribes the library by itself.
5. Nothing is hard-coded to one language. A **language pack** (normaliser, tokenizer, dictionary,
   morphology, suggestions, names) is an installable Epic 51 component. Arabic and English ship
   first. Hunspell dictionaries cover about 100 languages, so most languages get a full pack from
   them; any language without a full pack gets the fallback pack.
6. Every check is **deterministic** (decision 3): CAMeL Tools morphology for Arabic, Hunspell for
   English and the other languages, the calibrated confidence and the second listen. No LLM reads
   or judges words.
7. Quran verse runs are **checked against the canonical text** (decision 4): a word that differs
   from the matched verse is flagged `quran_mismatch`; a word that matches is never flagged.

## 3. Non-goals

- Re-running STT on a corrected word or sentence. Editing whole sentences or re-timing subtitles
  is Epic 04's editor, if ever built.
- Speaker-name review (diarization labels). It could reuse the queue later.
- Auto-correcting without a person. Even "accept all suggestions of this word" is one person's
  click.
- Training or fine-tuning STT models on corrections. Corrections are exported for that
  (story 52.10) but no training happens here.
- An LLM judge or any LLM in the word path (owner, 2026-11-03). Story 52.7 is removed.
- Third-party add-ons beyond language packs. Plugins are Epic 54's (a third-party plugin system
  with no internet access and a store, built on Epic 51's component model); this epic only uses
  Epic 51 components for its packs.

## 4. Design

### 4.1 Where detection runs: both, split by what each place has

| Place | What it has that nothing else has | What it does |
|---|---|---|
| **Inside `transcribe`** (GPU pool, same job as the second listen) | Whisper's per-word probability, Cohere's text for the stretch, the alignment between them. All in memory, gone after the job | **Capture only.** Keeps each word's probability and, where the listeners disagreed, both versions. No dictionary, no scoring. Adds < 1% to the job |
| **New `word_check` stage** (CPU pool, after `transcribe`) | Time, language packs, the library's vocabulary, names from metadata, the Quran text | **Decides.** Tokenizes with the pack, checks the dictionary, checks verse runs against the Quran text (52.15), scores, and writes flags. Re-runnable without STT whenever a pack, the vocabulary or the thresholds change |

There is no third place: the `word_judge` LLM stage of the first draft is removed (decision 3).

Why not only in the transcribe step: the GPU is the home's scarcest resource (every hour of audio costs GPU time),, dictionaries and vocabularies change weekly, and a re-check must not need
the GPU. Why not only a separate stage: the confidence and second-listen evidence die with the
transcribe job. So transcribe *captures*, `word_check` *decides*. This answers the owner's "a queue
review each transcript, or the same step that sends for the 2nd review": both, each doing the half
it is placed for.

Existing transcripts (~45k videos, about 23 M sentences on the home) have no word evidence. **All
of them** are checked in the background at the lowest priority (decision 2) with text-only signals
(dictionary, names, vocabulary, the Quran check). The backfill pauses whenever `transcribe` has
work. The confidence and listener signals arrive as files are transcribed again for other reasons.
Nothing forces a re-transcription.

### 4.2 The evidence: words kept compactly

A row per word in `transcript_words` would cost roughly 60-70 bytes plus its index per word. On the
home's ~300 M words (23 M sentences × ~13) that is 20-25 GB. Instead each `transcript_segments` row
gets one packed `word_data BYTEA` (story 52.1):

```
version:u8 | n:u16 | n × { char_start:u16, char_len:u8, start_cs:u16 (from the sentence start),
                           dur_cs:u16, prob:u8 (0-255), flags:u8 (bit0 heard differently by the
                           second listen, bit1 inserted by it, bit2 a Quran verse run) }
```

That is 10 bytes a word (~3 GB for the whole library, less after TOAST compression). The word's
text is a span of the sentence text, so the text is never stored twice. Where the second listen
replaced words, `transcript_segments.metadata.alt` keeps what each listener heard: `[{i, n,
whisper, cohere}]` (word index, count, both texts). That gives the reviewer two candidates for free.
`MAKTABA_STT_STORE_WORDS` is retired. Words are always kept, and the legacy `transcript_words`
table stays for the legacy path only.

### 4.3 The flag model

One row per suspect word or short span (`word_flags`, slot 0173+, Postgres and SQLite twins):

| Column | Type | Notes |
|---|---|---|
| `id` | UUID | |
| `kind` | TEXT | `word`, or `transcript` for the one "this transcript may be poor" flag per over-budget transcript (52.6) |
| `transcript_id` | UUID FK `transcripts` ON DELETE CASCADE | |
| `video_id`, `library_id` | UUID | Denormalised for the queue's filters and ACL |
| `audio_track_id` | BIGINT | The track the transcript was heard from (PR #556's chosen stream) |
| `segment_seq` | INT | The sentence (`transcript_segments.seq`) |
| `word_index`, `word_count` | SMALLINT | The span inside the sentence, in the pack's tokens (for display) |
| `char_start`, `char_len` | SMALLINT | The same span as characters of the sentence text. Edits address characters, so the Go API never needs a language pack |
| `start_sec`, `end_sec` | REAL | From `word_data`. Without word data (old transcripts), estimated by character position and marked `time_estimated` |
| `original` | TEXT | The text as transcribed |
| `context` | TEXT | The sentence at flag time (a snapshot, so a later edit does not change what the flag said) |
| `language` | TEXT | BCP 47 of the *word* (an English word inside Arabic speech is `en`) |
| `reasons` | TEXT[] | Any of `low_confidence`, `not_in_dictionary`, `listeners_disagree`, `quran_mismatch` (differs from the matched verse, 52.15), `named_entity`, `near_vocabulary` (one edit away from a known vocabulary term), `manual` (a reviewer picked the word from the transcript, 52.12), `previous_fix` (a fix that could not be carried over, 52.9) |
| `scores` | JSONB | `{prob, prob_cal, oov, listen, quran, entity}`: each signal's raw value, for calibration (`quran` holds the verse reference and the alignment, 52.15) |
| `priority` | REAL | The combined score that orders the queue (§4.6) |
| `candidates` | JSONB | `[{text, source: whisper/second_listen/dictionary/vocabulary/quran, score}]`, best first, at most 5 |
| `key` | TEXT | The pack's folded form of `original`: groups the same word across files |
| `status` | TEXT | `open`, `accepted` (as is), `corrected`, `name`, `dictionary`, `skipped`, `stale` (the transcript changed under it) |
| `resolution` | TEXT | The text chosen, when corrected |
| `resolved_by`, `resolved_at` | UUID, TIMESTAMPTZ | |
| `locked_by`, `locked_until` | UUID, TIMESTAMPTZ | A 5-minute soft lock while one reviewer has the word open (52.8) |
| `pack`, `check_version` | TEXT, INT | Which pack and rules made it |
| `created_at` | TIMESTAMPTZ | |

Unique on `(transcript_id, segment_seq, char_start)`. Indexes: `(library_id, status, priority
DESC)` for the queue, `(key, language, status)` for "same word elsewhere".

### 4.4 Language packs

A pack is an installable component (Epic 51 installs, enables, updates and removes it; until Epic
51 lands, a pack is a Python entry point in the `maktaba.language_packs` group plus a data folder
under `model_dirs`, fetched like the CAMeL data). Every check a pack makes is deterministic: the
same text and the same pack version give the same answer. Hunspell dictionaries exist for about
100 languages (LibreOffice's dictionaries), so a full pack for most languages is Hunspell plus
that language's tokenizer rules; Arabic adds CAMeL Tools morphology. A language with no full pack
gets the fallback pack. Its contract (story 52.3):

```python
class LanguagePack(Protocol):
    code: str                       # BCP 47: "ar", "en", "fr", ...
    version: str                    # bumps re-check flags made by an older version
    scripts: frozenset[str]         # ISO 15924: {"Arab"}, {"Latn"}
    direction: Literal["rtl", "ltr"]
    def normalise(self, text: str) -> str: ...      # display-safe (NFC, marks that never change meaning)
    def key(self, token: str) -> str: ...           # the folded form two spellings share
    def tokens(self, text: str) -> list[Token]: ... # words with char offsets; punctuation, numbers marked
    def known(self, token: str) -> Known: ...       # KNOWN / UNKNOWN / UNSURE, with the analysis
    def suggest(self, token: str, n: int) -> list[str]: ...
    def protected(self, tokens: list[Token]) -> list[bool]: ...  # never flag with ordinary signals
    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]: ...  # optional: runs to check
                                    # against a canonical text (Arabic: Quran verses, 52.15)
```

- **Arabic** (`ar`, story 52.4): `text/arabic.py` for `normalise`/`key`; CAMeL Tools' MSA
  morphological analyser for `known` (a word is known when it has an analysis: this solves the
  morphology problem that a plain word list cannot, since one Arabic stem takes hundreds of clitic
  and inflected forms); Hunspell `ar` (Ayaspell) for `suggest`; the Quran verse aligner (52.15)
  for `scripture`: words that match the verse are `protected`, words that differ get
  `quran_mismatch` instead of the ordinary signals. Dialect words the MSA analyser lacks come back
  `UNSURE`, not `UNKNOWN`.
- **English** (`en`, story 52.5): Hunspell `en_US` + `en_GB` (SCOWL-based) for `known` and
  `suggest`; contractions and possessives handled in `tokens`.
- **Hunspell packs for other languages** (story 52.3): a generic Hunspell pack, installed per
  language as its own Epic 51 component (`langpack-fr`, `langpack-de`, …), takes that language's
  LibreOffice Hunspell dictionary for `known` and `suggest` and UAX #29 for `tokens`. About 100
  languages are covered this way without new code.
- **Fallback for any language without a full pack** (story 52.3): Unicode word segmentation
  (UAX #29 via ICU), NFC, and a frequency list (`wordfreq`) as the "dictionary" (a word is
  `UNSURE` below a frequency floor, never `UNKNOWN`). So any Whisper language gets confidence and
  listener flags from day one, and dictionary flags once its Hunspell pack is installed.
- **Mixed script and code-switching:** `word_check` splits a sentence into script runs and sends
  each run to the pack for that script (Latin inside Arabic goes to `en`, or to the fallback). A
  run whose script has no installed pack never gets `not_in_dictionary`.

### 4.5 Who reviews (enforced by the API): home admins only

The owner decided (2026-11-03, decision 1): **only home admins review.**

| Who | May | Rule |
|---|---|---|
| Home admin (`users.is_admin`) | Review every library, change settings, manage vocabulary, undo any fix | The only reviewer |
| A user with `library_acl.role` `write` or `admin` on a library | Nothing here. `write` lets them manage the library's files and metadata as today, but not review its words, see flags or edit its vocabulary | 403 on every review route |
| A user with `read` (viewers, household members by default) | Nothing here. They never see flags or the queue | Viewers keep a clean player |
| Cloud-linked sign-ins | Nothing here (play-scoped, trail §1) | 403 even when a home admin granted manage, unless the sign-in is itself a home admin |

Every route has a role test in the `TestAdminOnly` style: as nobody (401), as a viewer (403), as a
user with `write` on this library (403), as a user with library `admin` role on this library who
is not a home admin (403), as cloud-linked play-scoped (403), and as a home admin (200). The web
and native apps hide Word review from non-admins, but the API is the rule.

### 4.6 Scoring and thresholds

Each signal is turned into a probability that the word is wrong, per language, then combined:

- `low_confidence`: Whisper's word probability is miscalibrated (over-confident on hallucinations,
  under-confident on short function words). `word_check` maps it through a per-language
  calibration curve (isotonic regression fitted on the evaluation set, story 52.2). It flags
  below the language's threshold (default: calibrated error probability ≥ 0.5).
- `listeners_disagree`: the second listen replaced or inserted the word. Strongest signal on
  Arabic lectures (17/20 in the hearing test). The *replaced* Whisper word becomes a candidate,
  since the hearing test also found Cohere misspelling 3/20.
- `not_in_dictionary`: the pack says `UNKNOWN` (`UNSURE` adds a little, never alone).
- `named_entity`: the word matches the library's gazetteer (titles, speakers, series, TMDB cast,
  vocabulary names) or a capitalised/NER span. This *lowers* priority (a name is often right but
  unknown to the dictionary) and adds the gazetteer's spelling as a candidate.
- `quran_mismatch`: inside a matched verse run, the word differs from the verse's word (§4.8). It
  replaces the ordinary signals for that word and carries its own fixed error probability (0.8,
  fitted on the gold set's Quran clips).

`priority = 1 − Π(1 − p_i)` over the signals present, then × 0.5 for a name-matched word. Words of
a verse run that match the verse are protected and never flagged.

A **budget** caps flags per hour of audio (default 40, settable 10-200) so a bad file cannot
flood the queue. Flags beyond the budget are kept as `skipped` with reason `over_budget`, and a
file whose budget overflows gets one file-level flag ("this transcript may be poor: N suspect
words"). Settings per library (story 52.14): on/off, sensitivity (low/normal/high moves every
threshold together), the budget, which reasons are on (including the Quran check), and the
backfill switch.

### 4.7 Applying a fix

A fix is a **transcript edit**, never a new STT run (story 52.9):

1. In one transaction (in the Go API, so a fix takes milliseconds): replace the character span
   in `transcript_segments.text`, shift the `word_data` offsets of the words after it, write a
   `transcript_edits` row (`transcript_id, segment_seq, char_start, char_len, before, after, flag_id, user_id, created_at, undone_at, batch_id`),
   set the flag's status, and bump `transcripts.revision`.
2. `search_tsv` is a generated column (slot 0016), so search follows by itself. Embeddings for
   the sentence are re-queued (`index` stage, by revision).
3. `subtitle_gen` includes `transcripts.revision` in its input fingerprint, so the change-driven
   engine rewrites `Name.<lang>.srt/.vtt` (its "only overwrite what we wrote" rule stays).
4. **Undo** reverses one edit or a batch (a bulk "fix every place of this word"), and refuses if
   the sentence has changed since. Any home admin can undo any fix.
5. **A transcript replaced later** (a relisten, a new model) would lose the fixes. `transcribe`
   carries them over: each edit whose `before` text is found, folded, in the new transcript within
   ±2 s is applied again (edit row `carried_from`). Those it cannot place become flags with reason
   `previous_fix`, the old fix as a candidate.

### 4.8 The Quran check (story 52.15)

The first draft never flagged a word inside a verse run. The owner decided (decision 4) to
**check** verse runs against the Quran text instead, so a misheard word in a recitation is caught
while a correctly heard verse is never shown.

- **Canonical text:** the Tanzil Quran Text, "Simple Clean" (v1.1), already bundled in
  `pipeline/src/maktaba_pipeline/data/quran/` with its notice, licensed CC BY 3.0, distributed
  verbatim (Tanzil does not allow changing the text). Matching uses it as is; display of the
  verse word as a suggestion credits "Tanzil Project, tanzil.net" in English and Arabic.
- **Normalisation:** both sides go through the Arabic pack's `key` (harakat, Quranic marks,
  tatweel and small letters removed; alef/hamza forms, ة/ه, ى/ي unified), plus a **rasm key**
  (dots removed, long alef dropped) used only to tell an orthographic or reading variant from a
  real mismatch.
- **Alignment:** a run is anchored where the existing index finds ≥ 4 consecutive matching words,
  then extended word by word with an edit-distance alignment that tolerates up to 1 wrong word in
  every 5, so one misheard word no longer breaks the run. Repeated phrases (a reciter going back),
  the isti'adha and basmala before a run, and a run crossing verse ends are handled.
- **Flags:** a substituted, inserted or missing word inside the run is flagged `quran_mismatch`,
  with the verse's word as a candidate of source `quran` and the verse reference («البقرة ١٨٣» /
  "Al-Baqara 2:183") in `scores.quran`. A word whose key differs but whose rasm key matches the
  verse word is a reading or spelling variant and is **not** flagged.

### 4.9 Learning from corrections

`vocabulary` table (story 52.10): `id, scope (home|library), library_id, language, term, key, kind
(word|name|term), variants TEXT[], source (review|manual|import), uses INT, added_by,
created_at`.

- **Mark as name / Add to dictionary** adds the term. A correction adds the corrected spelling as
  a variant of nothing, and records `misheard: original → correction` pairs (in `vocab_pairs`).
- The pack's `known()` consults the vocabulary first: a vocabulary word is never
  `not_in_dictionary` again.
- **Re-scoring:** adding a term re-queues `word_check` only for transcripts with an open flag of
  the same `key` (indexed), not the whole library. **Same word elsewhere:** the reviewer sees
  "this word is flagged in 37 other places" and can apply the same fix to all of them at once (one
  batch, one undo).
- **Feeding STT without re-transcribing:** the second listen's `Speller` prefers vocabulary
  spellings. New transcriptions get a **derived prompt**: the
  library's `stt.initial_prompt` plus the top vocabulary names by use, cut to Whisper's 224-token
  prompt limit, and on CTranslate2, `hotwords`. The derived part is **left out of
  `input_fingerprint`** (only the owner-written prompt stays in it), so learning never queues the
  library for transcription. "Transcribe again with the vocabulary" is a per-video or
  per-library action, with an hours estimate shown first.
- **Export:** corrections and their audio spans export as a dataset (CSV + clip list) for anyone
  who later wants to fine-tune a model.

### 4.10 Throughput on the home Mac

- `transcribe`: + word capture only: packing `word_data`, keeping `alt`. Target < 1% of the job's
  wall time; no extra GPU work (the probabilities are already computed).
- `word_check`: CPU only, in the `cpu` pool, one job per transcript. A lecture hour is ~9,000
  Arabic words but only ~1,500 distinct folded types, and the pack caches `known()` per type
  (LRU, 200 k entries). Target **≤ 10 CPU-seconds per hour of audio** at p95, i.e. under 30 CPU-hours
  per 10,000 hours of audio, run at low priority behind transcribe. The Quran check adds an index
  lookup per word and an alignment only where a run is anchored: within the same budget.
- **Backfill of all ~45k existing transcripts** (decision 2): enqueued once at the lowest priority
  when the stage first ships, paused whenever `transcribe` has pending jobs. Text-only checks cost
  less than the full ones, so the whole library is a few hundred CPU-hours at most, spread over
  idle time. No GPU.
- Queue health (Epic 40): `word_check` gets a tile like every stage, with the backfill's progress
  (transcripts checked of total), and the review queue's open count shows on the Transcription
  page.

### 4.11 API surface

| Route | Who | Story |
|---|---|---|
| `GET /api/review/flags`, `GET /api/review/flags/{id}`, `GET /api/review/summary` | Home admin only | 52.8 |
| `GET /api/review/flags/{id}/clip` → signed `GET /stream/clip/{video}` | Home admin only | 52.8 |
| `POST /api/review/flags/{id}/resolve` | Home admin only | 52.8, 52.9 |
| `POST /api/review/edits/{id}/undo`, `POST /api/review/batches/{id}/undo` | Home admin only | 52.9 |
| `GET /api/videos/{id}/transcript/edits` | Home admin only | 52.9 |
| `POST /api/videos/{id}/transcript/words` | Home admin only | 52.12 |
| `/api/vocabulary` (list, add, edit, delete, import), `GET /api/vocabulary/export`, `GET /api/review/corrections/export` | Home admin only | 52.10 |
| `GET`/`PUT /api/libraries/{id}/word-review` | Admin | 52.14 |
| `GET /api/language-packs`, `PATCH /api/language-packs/{code}` | Admin | 52.14 |

## 5. Requirements

### Functional

| # | Requirement | Story |
|---|---|---|
| F1 | Each new transcript keeps every word's time, confidence and second-listen version in `word_data` / `metadata.alt` | 52.1 |
| F2 | An evaluation set with gold corrections measures flag precision/recall per language | 52.2 |
| F3 | Language packs are installable Epic 51 components through one contract; a generic Hunspell pack covers ~100 languages; any language without a full pack has the fallback | 52.3 |
| F4 | Arabic (CAMeL Tools morphology) and English (Hunspell) packs; every check deterministic | 52.4, 52.5 |
| F5 | `word_check` writes flags with place, reasons, scores, candidates and status | 52.6 |
| F6 | Names in metadata and vocabulary lower priority and suggest spellings | 52.6 |
| F7 | *Removed 2026-11-03 (was: optional LLM judge, story 52.7). No LLM in the word path* | — |
| F8 | Review API, **home admins only** (a library `write` or `admin` role grants nothing), the audio clip, and same-word grouping | 52.8 |
| F9 | Fixes edit the transcript, subtitles and search without STT; history, undo; carried over to a newer transcript | 52.9 |
| F10 | Vocabulary from fixes: stops re-flagging, re-scores same-key flags, feeds the speller and new transcriptions without re-transcribing | 52.10 |
| F11 | Web review queue, keyboard-first, RTL-correct, English + Arabic | 52.11 |
| F12 | Fix any word from the transcript tab | 52.12 |
| F13 | Review in the native apps (Apple and Android) | 52.13 |
| F14 | Settings, thresholds, budget, queue health | 52.14 |
| F15 | All ~45k existing transcripts are checked in the background at the lowest priority with text-only signals, without re-transcribing | 52.6, 52.14 |
| F16 | Words of a Quran verse run are compared with the matched verse of the canonical text; only real mismatches are flagged, with reason `quran_mismatch` | 52.15 |

### Non-functional (targets, measured by story 52.2's harness)

| Metric | Target | How measured |
|---|---|---|
| Flag precision (flagged words that are really wrong) | Arabic ≥ 0.50, English ≥ 0.60 at phase 1; ≥ 0.65 / 0.70 after calibration (phase 2) | Gold set, word-level alignment of hypothesis to reference |
| Flag recall (wrong words that are flagged) | Arabic ≥ 0.60, English ≥ 0.60 at the default budget | Same |
| Flag rate | ≤ 3% of words (≤ 40 per audio hour at the default budget) | Live sample, read-only |
| Hearing-test spots | ≥ 18 of the owner's 20 disputed spots flagged | The 20-spot set, kept as a fixture |
| Quran check | No flag on a correctly heard verse run (0 false flags on the gold set's matching runs); ≥ 0.80 of seeded word errors inside runs flagged `quran_mismatch` | Gold set's Quran clips plus seeded-error fixtures (52.15) |
| Backfill | All ~45k existing transcripts checked; never runs while `transcribe` has pending jobs | Backfill progress on the queue tile |
| Queue latency | Flags visible ≤ 2 min after the transcript is stored (p95, idle queue) | Job timestamps |
| Fix latency | API fix p95 ≤ 300 ms; subtitle files rewritten ≤ 60 s after the fix | API metric, `subtitle_files.updated_at` |
| Clip start | Audio clip plays ≤ 700 ms after the flag opens (LAN) | Web timing |
| Cost | `transcribe` +< 1% wall time; `word_check` ≤ 10 CPU-s per audio hour (Quran check included); no GPU after `transcribe` | Job metrics |
| Determinism | The same transcript, pack versions, vocabulary and settings give byte-identical flags | Stage test runs `word_check` twice and compares |
| Storage | `word_data` ≤ 12 bytes a word before compression | Measured on the live DB, read-only |
| Languages | No code path names a language except inside a pack; a CI check greps `stages/word_*` for `"ar"`/`"en"` literals | CI |
| Strings | Every string English + Arabic on web, Android and Apple, RTL checked | The existing missing-Arabic tests |

## 6. Research summary

Full notes with sources: [research.md](research.md).

- **Confidence:** Whisper-family decoders give per-word probabilities when word timestamps are on
  (openai-whisper, faster-whisper's `Word.probability`, mlx-whisper). Whisper's own
  `word_anomaly_score` treats `probability < 0.15` as suspect: our starting threshold. Segment `avg_logprob`,
  `no_speech_prob` and `compression_ratio` are coarse. Word probabilities are known to be poorly
  calibrated, so they are calibrated per language before use.
- **Dictionaries:** Hunspell covers ~100 languages through LibreOffice dictionaries. Arabic needs
  morphology (one stem, hundreds of forms), so a morphological analyser (CAMeL Tools, already
  used) decides "known", and Hunspell only suggests. Unicode normalisation (NFC, harakat,
  tatweel, alef/hamza/ta-marbuta/alef-maqsura) comes before any lookup. UAX #29 segmentation
  handles languages without spaces.
- **Names:** dictionaries lack names. Library metadata is the best gazetteer, NER the fallback.
- **Licences:** CAMeL Tools' code is MIT but its morphology data is GPL v2. Ayaspell is
  GPL/LGPL/MPL. Farasa is research-only and is not used. So packs fetch their data when they are
  installed and never bundle it (as `ensure_camel_data` already does).
- **Review tools:** Auphonic, Echo360, Sonix, Rev and Subtitle Edit show confidence as
  highlighting with click-to-play. AWS Transcribe and Google STT return word confidences. A CHI
  2025 study (36 people) found that confidence highlighting alone "neither improved correction
  efficiency nor was perceived as helpful". So the queue shows one word at a time, from several
  signals, with its reasons, its sentence and its clip, and a few keys.
- **LLM correction:** generative error correction can fix ASR text, but it also over-corrects and
  hallucinates fluent wrong text. Our own hearing test saw exactly that. The first draft let an
  LLM flag and suggest; the owner decided against any LLM judge (2026-11-03), so every check is
  deterministic.
- **Quran text:** Tanzil publishes verified Quran texts (Simple and Uthmani scripts) under CC BY
  3.0 with verbatim copying only; Maktaba already bundles "Simple Clean". Comparing on folded and
  rasm keys keeps orthographic and reading differences from counting as errors.
- **Learning:** Whisper's prompt (224 tokens) and CTranslate2's `hotwords` bias decoding towards
  known terms. Commercial STT does the same with custom vocabularies and phrase hints. Active
  learning picks low-confidence words for people, which is what this queue does.

## 7. Phases

| Phase | Stories | Ships |
|---|---|---|
| **1. Evidence and flags** | 52.1, 52.2, 52.3, 52.4, 52.5, 52.6, 52.15 | Words kept with confidence; Arabic + English + Hunspell + fallback packs; `word_check` writes flags; the Quran check; all existing transcripts checked text-only in the background. Flags visible to admins as a read-only list |
| **2. Review and fixes** | 52.8, 52.9, 52.11, 52.12, 52.14 | The web queue (admins only), fixes with undo, subtitles and search refreshed, roles, settings, budget |
| **3. Learning** | 52.10 | Vocabulary, same-word bulk fixes, derived prompt and speller |
| **4. Native apps** | 52.13 | Review screens in the Apple and Android Remote and Server apps |

Packaging: phase 1 ships the packs as entry points. When Epic 51 lands, they move to its
component model with no change to the contract (story 52.3).

## 8. Decisions (2026-11-03)

The owner answered the four open questions on 2026-11-03.

1. **Reviewers: home admins only.** A user with `write` (or the library `admin` role) on a
   library may **not** review its words, see its flags or edit its vocabulary. §4.5, §4.11, the
   role tests in 52.8 (and 52.9, 52.10, 52.12, 52.14), the web queue (52.11) and the native
   screens (52.13) follow this.
2. **Old transcripts: check all of them.** All ~45k existing transcripts are text-checked in the
   background at the lowest priority, paused whenever `transcribe` has work. `word_review.backfill`
   defaults to on (52.6, 52.14); an admin may still turn it off for a library. Rollout step R3 no
   longer waits for a yes.
3. **LLM judge: none.** Story 52.7 is removed (its number is kept and its file is a stub, so
   links and story numbers elsewhere stay valid; nothing is renumbered). Gone with it: the `word_judge` stage, the
   `llm` pool use, the `llm_disagrees` reason, the `llm` candidate source, the `llm_judge.*`
   settings, the `llm_unavailable` failure class and the phase-3 judge work. Dictionary and
   spelling checks are deterministic: **CAMeL Tools morphology for Arabic** (52.4), **Hunspell for
   English and the other languages** (52.5, 52.3). Languages stay installable packs (Epic 51
   components): Hunspell dictionaries cover about 100 languages, and the fallback pack covers any
   language without a full pack.
4. **Quran: check verse runs against the Quran text.** A new Quran-aware check (story 52.15)
   aligns each detected verse run with the matched verse of the bundled Tanzil text (CC BY 3.0,
   verbatim), compares word by word after normalisation (§4.8), and flags only real mismatches,
   with its own reason code `quran_mismatch`. Correctly heard words in a run stay protected.

## 9. Stories

| # | Story | Phase |
|---|---|---|
| 52.1 | [Keep every word's time, confidence and second listen](story-52-01-word-evidence.md) | 1 |
| 52.2 | [Evaluation set, metrics and calibration](story-52-02-evaluation-set.md) | 1 |
| 52.3 | [The language pack contract, registry and fallback pack](story-52-03-language-pack-contract.md) | 1 |
| 52.4 | [Arabic language pack](story-52-04-arabic-pack.md) | 1 |
| 52.5 | [English language pack](story-52-05-english-pack.md) | 1 |
| 52.6 | [`word_check`: flags, names and old transcripts](story-52-06-word-check-stage.md) | 1 |
| 52.7 | ~~[The LLM judge that flags, not rewrites](story-52-07-llm-judge.md)~~ **Removed 2026-11-03** (decision 3) | — |
| 52.8 | [Review API, roles and the audio clip](story-52-08-review-api.md) | 2 |
| 52.9 | [Applying fixes: edits, undo, subtitles, search, carry-over](story-52-09-apply-fixes.md) | 2 |
| 52.10 | [Vocabulary: learning from fixes](story-52-10-vocabulary.md) | 3 |
| 52.11 | [Web review queue](story-52-11-web-review-queue.md) | 2 |
| 52.12 | [Fix a word from the transcript](story-52-12-fix-from-transcript.md) | 2 |
| 52.13 | [Review in the native apps](story-52-13-native-review.md) | 4 |
| 52.14 | [Settings, thresholds, budget and queue health](story-52-14-settings-and-health.md) | 2 |
| 52.15 | [Quran check: verse runs against the Quran text](story-52-15-quran-check.md) | 1 |

[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
