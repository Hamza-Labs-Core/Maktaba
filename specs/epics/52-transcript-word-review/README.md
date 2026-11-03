# Epic 52: Word review: suspect words go to a person, in every language

> Status: spec (2026-11-03). Depends on: Epic 03 (transcription, plan 03-10), Epic 04
> (subtitles), Epic 49 (Arabic-aware search, for the normaliser it shares), Epic 51
> (configurable components: language packs install as components). Research:
> [research.md](research.md). Build order and DAG: [plan.md](plan.md).

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
| There is no LLM anywhere in the transcript path; `llm.cluster_llm()` (Ollama / OpenAI-compatible runtimes on the cluster) is used only by enrich | `llm/__init__.py`, `enrich/reader.py:258` | "The 2nd LLM review" the owner names is today the second *listen*; an LLM judge is new and optional |
| Roles: `users.is_admin` and `library_acl.role IN ('read','write','admin')` | `0029_users.sql:31`, `0072_library_acl_role.sql:20` | Reviewer rights fit the per-library role, no new user type |

So the epic is in three parts: **keep the evidence** (each word's time, confidence and the second
listener's version), **point at suspect words** with language packs that know each language's
spelling, and **let a person fix them fast**, with every fix teaching the next transcript.

## 2. Goals

1. Every new transcript keeps, for every word, its time, Whisper's confidence and what the second
   listener heard there, at a storage cost small enough for the whole library.
2. A queue job checks each transcript and saves each suspect word's place (transcript, sentence,
   word, start and end time) with the reasons, scores and suggested spellings.
3. A reviewer works through suspect words in a keyboard-first queue: hears the clip, sees the
   sentence, and accepts, corrects, marks as a name or adds to the dictionary. A fix updates the
   transcript, its subtitle files and search at once, without running speech recognition again,
   and can be undone.
4. Every fix teaches: a per-library and per-language vocabulary that stops the same word from
   being flagged again, checks other files for it, and feeds the second listen's speller, the LLM
   judge and new transcriptions. It never re-transcribes the library by itself.
5. Nothing is hard-coded to one language. A **language pack** (normaliser, tokenizer, dictionary,
   morphology, suggestions, names) is an add-on component. Arabic and English ship first. Any other
   language gets a fallback pack until its own is installed.
6. The second LLM is a **judge that flags**, never a writer: off by default, opt-in per library,
   and never the only reason a word is shown.

## 3. Non-goals

- Re-running STT on a corrected word or sentence. Editing whole sentences or re-timing subtitles
  is Epic 04's editor, if ever built.
- Speaker-name review (diarization labels). It could reuse the queue later.
- Auto-correcting without a person. Even "accept all suggestions of this word" is one person's
  click.
- Training or fine-tuning STT models on corrections. Corrections are exported for that
  (story 52.10) but no training happens here.

## 4. Design

### 4.1 Where detection runs: both, split by what each place has

| Place | What it has that nothing else has | What it does |
|---|---|---|
| **Inside `transcribe`** (GPU pool, same job as the second listen) | Whisper's per-word probability, Cohere's text for the stretch, the alignment between them. All in memory, gone after the job | **Capture only.** Keeps each word's probability and, where the listeners disagreed, both versions. No dictionary, no scoring. Adds < 1% to the job |
| **New `word_check` stage** (CPU pool, after `transcribe`) | Time, language packs, the library's vocabulary, names from metadata | **Decides.** Tokenizes with the pack, checks the dictionary, scores, and writes flags. Re-runnable without STT whenever a pack, the vocabulary or the thresholds change |
| **New `word_judge` stage** (LLM, optional, after `word_check`) | An LLM on the cluster | Adds a "the LLM thinks this is wrong" opinion to words already flagged or borderline. Never rewrites, never runs alone |

Why not only in the transcribe step: the GPU is the home's scarcest resource (every hour of audio costs GPU time),, dictionaries and vocabularies change weekly, and a re-check must not need
the GPU. Why not only a separate stage: the confidence and second-listen evidence die with the
transcribe job. So transcribe *captures*, `word_check` *decides*. This answers the owner's "a queue
review each transcript, or the same step that sends for the 2nd review": both, each doing the half
it is placed for.

Existing transcripts (45k videos, about 23 M sentences on the home) have no word evidence. They get
`word_check` with text-only signals (dictionary, names, vocabulary, optional LLM). The confidence
and listener signals arrive as files are transcribed again for other reasons. Nothing forces a
re-transcription.

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
| `reasons` | TEXT[] | Any of `low_confidence`, `not_in_dictionary`, `listeners_disagree`, `llm_disagrees`, `named_entity`, `near_vocabulary` (one edit away from a known vocabulary term), `manual` (a reviewer picked the word from the transcript, 52.12), `previous_fix` (a fix that could not be carried over, 52.9) |
| `scores` | JSONB | `{prob, prob_cal, oov, listen, llm, entity}`: each signal's raw value, for calibration |
| `priority` | REAL | The combined score that orders the queue (§4.6) |
| `candidates` | JSONB | `[{text, source: whisper/second_listen/dictionary/vocabulary/llm, score}]`, best first, at most 5 |
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

A pack is an add-on component (Epic 51 installs, enables and updates it; until Epic 51 lands, a
pack is a Python entry point in the `maktaba.language_packs` group plus a data folder under
`model_dirs`, fetched like the CAMeL data). Its contract (story 52.3):

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
    def protected(self, tokens: list[Token]) -> list[bool]: ...  # never flag (e.g. Quran verse runs)
```

- **Arabic** (`ar`, story 52.4): `text/arabic.py` for `normalise`/`key`; CAMeL Tools' MSA
  morphological analyser for `known` (a word is known when it has an analysis: this solves the
  morphology problem that a plain word list cannot, since one Arabic stem takes hundreds of clitic
  and inflected forms); Hunspell `ar` (Ayaspell) for `suggest`; `classify/quran.verse_runs` for
  `protected`. Dialect words the MSA analyser lacks come back `UNSURE`, not `UNKNOWN`.
- **English** (`en`, story 52.5): Hunspell `en_US` + `en_GB` (SCOWL-based) for `known` and
  `suggest`; contractions and possessives handled in `tokens`.
- **Fallback for every other language** (story 52.3): Unicode word segmentation (UAX #29 via ICU),
  NFC, a frequency list (`wordfreq`) as the "dictionary" (a word is `UNSURE` below a frequency
  floor, never `UNKNOWN`), and any LibreOffice Hunspell dictionary the home has installed. So any
  Whisper language gets confidence and listener flags from day one, and dictionary flags only as
  well as its data allows.
- **Mixed script and code-switching:** `word_check` splits a sentence into script runs and sends
  each run to the pack for that script (Latin inside Arabic goes to `en`, or to the fallback). A
  run whose script has no installed pack never gets `not_in_dictionary`.

### 4.5 Who reviews (enforced by the API)

| Who | May | Rule |
|---|---|---|
| Home admin (`users.is_admin`) | Review every library, change settings, manage vocabulary, undo anyone's fix | — |
| A user with `library_acl.role` `write` or `admin` on a library | Review that library's flags, undo their own fixes, add to that library's vocabulary | The "household reviewer". A home admin grants `write` per library in Manage libraries |
| A user with `read` (viewers, household members by default) | Nothing here. They never see flags or the queue | Viewers keep a clean player |
| Cloud-linked sign-ins | Nothing here (play-scoped, trail §1) | Unless a home admin grants manage |

Every route has a role test in the `TestAdminOnly` style: as nobody, as a viewer, as a reviewer of
another library (403), and as a reviewer of this one (200).

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
- `llm_disagrees`: the judge's verdict (§4.8).

`priority = 1 − Π(1 − p_i)` over the signals present, then × 0.5 for a protected or name-matched
word. A **budget** caps flags per hour of audio (default 40, settable 10-200) so a bad file cannot
flood the queue. Flags beyond the budget are kept as `skipped` with reason `over_budget`, and a
file whose budget overflows gets one file-level flag ("this transcript may be poor: N suspect
words"). Settings per library (story 52.14): on/off, sensitivity (low/normal/high moves every
threshold together), the budget, which reasons are on, the LLM judge on/off and its model, and the
reviewers.

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
   the sentence has changed since. Admins can undo anyone's fix; reviewers their own.
5. **A transcript replaced later** (a relisten, a new model) would lose the fixes. `transcribe`
   carries them over: each edit whose `before` text is found, folded, in the new transcript within
   ±2 s is applied again (edit row `carried_from`). Those it cannot place become flags with reason
   `previous_fix`, the old fix as a candidate.

### 4.8 The LLM judge (optional)

`word_judge` (story 52.7) sends the LLM only the sentences with a flagged or borderline word
(calibrated error 0.3-0.5), with one sentence of context either side, the library's glossary
(vocabulary names and terms), and asks for JSON (`[{word_index, verdict: ok|suspect,
suggestion?, why}]`) under a schema. It never sees or returns whole rewritten sentences. Its
suggestion becomes a candidate with source `llm`. A word whose *only* reason would be
`llm_disagrees` is not flagged unless the library's setting "LLM alone may flag" is on (off by
default, from the hearing-test result). It runs through `cluster_llm()` on whatever machine has the
model, in the `llm` pool, only when the queue is otherwise idle (§4.10).

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
  spellings. The judge gets the glossary. New transcriptions get a **derived prompt**: the
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
  per 10,000 hours of audio, run at low priority behind transcribe.
- `word_judge`: only flagged and borderline sentences (≈ 40-80 per audio hour × ~250 tokens in,
  ~60 out). On a local 8 B model that is ~1-3 GPU-minutes per audio hour. Off by default; when on,
  it runs only while `transcribe` has no pending jobs, so it never slows the main queue.
- Queue health (Epic 40): `word_check` and `word_judge` get tiles like every stage, and the review
  queue's open count shows on the Transcription page.

### 4.11 API surface

| Route | Who | Story |
|---|---|---|
| `GET /api/review/flags`, `GET /api/review/flags/{id}`, `GET /api/review/summary` | Admin; `write`/`admin` on the library | 52.8 |
| `GET /api/review/flags/{id}/clip` → signed `GET /stream/clip/{video}` | Same | 52.8 |
| `POST /api/review/flags/{id}/resolve` | Same | 52.8, 52.9 |
| `POST /api/review/edits/{id}/undo`, `POST /api/review/batches/{id}/undo` | Own fix; admin any | 52.9 |
| `GET /api/videos/{id}/transcript/edits` | Same as review | 52.9 |
| `POST /api/videos/{id}/transcript/words` | Same as review | 52.12 |
| `/api/vocabulary` (list, add, edit, delete, import), `GET /api/vocabulary/export`, `GET /api/review/corrections/export` | Admin; `write`/`admin` for their library's terms | 52.10 |
| `GET`/`PUT /api/libraries/{id}/word-review` | Admin | 52.14 |
| `GET /api/language-packs`, `PATCH /api/language-packs/{code}` | Admin | 52.14 |

## 5. Requirements

### Functional

| # | Requirement | Story |
|---|---|---|
| F1 | Each new transcript keeps every word's time, confidence and second-listen version in `word_data` / `metadata.alt` | 52.1 |
| F2 | An evaluation set with gold corrections measures flag precision/recall per language | 52.2 |
| F3 | Language packs plug in through one contract; any language has a fallback | 52.3 |
| F4 | Arabic and English packs | 52.4, 52.5 |
| F5 | `word_check` writes flags with place, reasons, scores, candidates and status | 52.6 |
| F6 | Names in metadata and vocabulary lower priority and suggest spellings | 52.6 |
| F7 | An optional LLM judge adds verdicts and suggestions, never rewrites, never flags alone by default | 52.7 |
| F8 | Review API with server-side roles, the audio clip, and same-word grouping | 52.8 |
| F9 | Fixes edit the transcript, subtitles and search without STT; history, undo; carried over to a newer transcript | 52.9 |
| F10 | Vocabulary from fixes: stops re-flagging, re-scores same-key flags, feeds speller, judge and new transcriptions without re-transcribing | 52.10 |
| F11 | Web review queue, keyboard-first, RTL-correct, English + Arabic | 52.11 |
| F12 | Fix any word from the transcript tab | 52.12 |
| F13 | Review in the native apps (Apple and Android) | 52.13 |
| F14 | Settings, thresholds, budget, queue health | 52.14 |
| F15 | Old transcripts are checked with text-only signals, without re-transcribing | 52.6 |

### Non-functional (targets, measured by story 52.2's harness)

| Metric | Target | How measured |
|---|---|---|
| Flag precision (flagged words that are really wrong) | Arabic ≥ 0.50, English ≥ 0.60 at phase 1; ≥ 0.65 / 0.70 after calibration (phase 2) | Gold set, word-level alignment of hypothesis to reference |
| Flag recall (wrong words that are flagged) | Arabic ≥ 0.60, English ≥ 0.60 at the default budget | Same |
| Flag rate | ≤ 3% of words (≤ 40 per audio hour at the default budget) | Live sample, read-only |
| Hearing-test spots | ≥ 18 of the owner's 20 disputed spots flagged | The 20-spot set, kept as a fixture |
| Queue latency | Flags visible ≤ 2 min after the transcript is stored (p95, idle queue) | Job timestamps |
| Fix latency | API fix p95 ≤ 300 ms; subtitle files rewritten ≤ 60 s after the fix | API metric, `subtitle_files.updated_at` |
| Clip start | Audio clip plays ≤ 700 ms after the flag opens (LAN) | Web timing |
| Cost | `transcribe` +< 1% wall time; `word_check` ≤ 10 CPU-s per audio hour; `word_judge` ≤ 3 GPU-min per audio hour, idle-only | Job metrics |
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
  hallucinates fluent wrong text. Our own hearing test saw exactly that. So the LLM flags and
  suggests, with structured output, and does not write.
- **Learning:** Whisper's prompt (224 tokens) and CTranslate2's `hotwords` bias decoding towards
  known terms. Commercial STT does the same with custom vocabularies and phrase hints. Active
  learning picks low-confidence words for people, which is what this queue does.

## 7. Phases

| Phase | Stories | Ships |
|---|---|---|
| **1. Evidence and flags** | 52.1, 52.2, 52.3, 52.4, 52.5, 52.6 | Words kept with confidence; Arabic + English + fallback packs; `word_check` writes flags; old transcripts checked text-only. Flags visible to admins as a read-only list |
| **2. Review and fixes** | 52.8, 52.9, 52.11, 52.12, 52.14 | The web queue, fixes with undo, subtitles and search refreshed, roles, settings, budget |
| **3. Learning and the judge** | 52.10, 52.7 | Vocabulary, same-word bulk fixes, derived prompt and speller, the optional LLM judge |
| **4. Native apps** | 52.13 | Review screens in the Apple and Android Remote and Server apps |

Packaging: phase 1 ships the packs as entry points. When Epic 51 lands, they move to its
component model with no change to the contract (story 52.3).

## 8. Open questions for the owner

1. **Household reviewers:** may a household member with `write` on a library review its words,
   or only admins? The spec allows `write` (§4.5); say if it should be admins only.
2. **Old transcripts:** check all 45k existing transcripts text-only (under 30 CPU-hours per 10,000 audio hours, low
   priority), or only new ones and libraries you pick?
3. **LLM judge:** worth turning on for a library once phase 3 lands, and which local model?
4. **Quran:** never flag words inside detected verse runs (current spec), or flag them for a
   separate Quran-aware check?

## 9. Stories

| # | Story | Phase |
|---|---|---|
| 52.1 | [Keep every word's time, confidence and second listen](story-52-01-word-evidence.md) | 1 |
| 52.2 | [Evaluation set, metrics and calibration](story-52-02-evaluation-set.md) | 1 |
| 52.3 | [The language pack contract, registry and fallback pack](story-52-03-language-pack-contract.md) | 1 |
| 52.4 | [Arabic language pack](story-52-04-arabic-pack.md) | 1 |
| 52.5 | [English language pack](story-52-05-english-pack.md) | 1 |
| 52.6 | [`word_check`: flags, names and old transcripts](story-52-06-word-check-stage.md) | 1 |
| 52.7 | [The LLM judge that flags, not rewrites](story-52-07-llm-judge.md) | 3 |
| 52.8 | [Review API, roles and the audio clip](story-52-08-review-api.md) | 2 |
| 52.9 | [Applying fixes: edits, undo, subtitles, search, carry-over](story-52-09-apply-fixes.md) | 2 |
| 52.10 | [Vocabulary: learning from fixes](story-52-10-vocabulary.md) | 3 |
| 52.11 | [Web review queue](story-52-11-web-review-queue.md) | 2 |
| 52.12 | [Fix a word from the transcript](story-52-12-fix-from-transcript.md) | 2 |
| 52.13 | [Review in the native apps](story-52-13-native-review.md) | 4 |
| 52.14 | [Settings, thresholds, budget and queue health](story-52-14-settings-and-health.md) | 2 |

[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
