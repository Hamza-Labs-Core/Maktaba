# Story 52.07 — The LLM judge that flags, not rewrites

> Epic 52 · Phase 3 · Depends on: 52.2, 52.6 (52.10's glossary when present) · Needed by: —

## Why

The owner asked about *"the same step that sends for the 2nd llm review"*. The hearing tests
taught the limit of that step: an LLM judge preferred Whisper turbo's tidy spelling, and the
owner's ears found Cohere right 17 times out of 20 ([#113]). LLMs correcting speech text are known
to "fix" words into fluent, wrong ones. So the LLM here is a **second opinion that points**: it
may say "this word looks wrong, maybe X", it never writes the transcript, and by default it can
only add weight to a word another signal already found.

## Description

### 1. The stage

- Name `word_judge`, after `word_check` (edge `word_check → word_judge`, `same`).
- Pool: a new `llm` pool (one slot by default). The job calls `llm.cluster_llm().chat(...)`, so
  the model runs on any machine that has it (Ollama or an OpenAI-compatible runtime,
  `llm/runtimes.py`).
- **Idle-only**: the stage's claim query skips while `transcribe` has pending or running jobs on
  the home, so the judge never slows the main queue.
- Off unless the library's `word_review.judge.enabled` is on (52.14), with a model name set.
- Input fingerprint: the transcript id and revision, `word_check`'s output hash, the model name,
  the prompt version and the glossary version.

### 2. What it sends

Only sentences with a flagged word (any status `open`) or a borderline word (`priority` 0.3 to
the threshold), each with one sentence of context on either side. Batches of up to 12 target
sentences per request. Per request:

- system prompt (versioned, in `word_review/judge_prompt.py`): "You check a speech transcript
  for words that were probably misheard. Do not rewrite sentences. For each marked word, answer
  ok or suspect. If suspect, give at most one replacement word in the same language and script.";
- the library's glossary: up to 200 vocabulary terms and names (52.10, gazetteer from 52.6);
- the sentences, with marked words as `⟦3:كلمة⟧` (word index and text).

`response_format` is a JSON schema (OpenAI `json_schema`; Ollama `format`):

```json
{"type":"object","properties":{"words":{"type":"array","items":{"type":"object",
 "properties":{"sentence":{"type":"integer"},"word":{"type":"integer"},
 "verdict":{"enum":["ok","suspect"]},"suggestion":{"type":"string","maxLength":40},
 "why":{"type":"string","maxLength":120}},"required":["sentence","word","verdict"]}}},
 "required":["words"]}
```

`temperature 0`, `max_tokens` sized to the batch.

### 3. What it does with the answer

- A reply that is not valid JSON, refers to an unmarked word, or suggests text in another script
  is dropped (logged `judge_invalid`, counted in the job summary). No retry loop: one retry, then
  the batch is left without a verdict.
- `suspect` on a flagged word: adds `llm_disagrees`, `scores.llm = 0.6` (fitted on the gold set),
  recomputes `priority`, adds the suggestion as a candidate with source `llm`.
- `ok` on a flagged word: `scores.llm_ok = true`. Priority is lowered by at most 0.1, and the
  word is never removed from the queue by the LLM alone.
- `suspect` on a borderline word that no other reason flagged: flagged **only** if the library's
  setting `judge.may_flag_alone` is on (default off). Otherwise it is kept in `scores` for the
  evaluation, not shown.
- It never edits `transcript_segments`. A test asserts no write to that table from the stage.

### 4. Cost

About 40-80 target sentences per audio hour × ~250 tokens in, ~60 out. On a local 8 B model that is
1-3 GPU-minutes per audio hour. The job summary records tokens in/out and seconds, and the
Transcription page shows the totals.

### 5. Measured before offered

The judge is offered in settings for a language only after the gold set shows that adding
`llm_disagrees` raises precision without lowering recall on that language with the chosen model.
The eval report names the model. Until then, the setting shows "not checked for this language".

## Acceptance criteria

1. **Given** a library with the judge off, **when** `word_check` finishes, **then** no `word_judge`
   job is queued.
2. **Given** the judge on and `transcribe` has pending jobs, **when** the scheduler runs, **then**
   `word_judge` jobs wait, and start once `transcribe` is empty.
3. **Given** a flagged word the model calls `suspect` with a suggestion, **when** the job finishes,
   **then** the flag has reason `llm_disagrees`, the suggestion is a candidate with source `llm`,
   and the sentence text is unchanged.
4. **Given** a borderline word the model calls `suspect` and `may_flag_alone` off, **when** the job
   finishes, **then** no new flag is opened.
5. **Given** the model replies with invalid JSON twice, **when** the job finishes, **then** the batch
   has no verdict, the job succeeds, and its summary counts `judge_invalid = 1`.
6. **Given** a suggestion in Latin script for an Arabic word, **when** parsed, **then** it is
   dropped.
7. **Given** no machine has the model (`NoModel`), **when** the job runs, **then** it parks with a
   clear reason on the failing-items page, not retried every minute.
8. **Given** the same input, **when** re-run, **then** it is `unchanged` (no LLM call).
9. **Given** the gold set and a model, **when** `eval --judge <model>` runs, **then** the report
   shows precision and recall with and without `llm_disagrees`, per language.
10. **Given** a code search, **when** CI's lint runs, **then** `word_judge.py` has no write to
    `transcript_segments` or `transcripts`.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/engine/stages/word_judge.py` (new).
- `pipeline/src/maktaba_pipeline/word_review/judge_prompt.py`, `judge_parse.py` (new).
- `pipeline/src/maktaba_pipeline/engine/graph.py`: the stage, edge, `llm` pool, idle-only claim
  rule (a stage attribute `waits_for=("transcribe",)`).
- `pipeline/src/maktaba_pipeline/word_review/eval/__main__.py`: `--judge`.

### Tests

- Parsing: valid, invalid, wrong indices, wrong script, oversize suggestion.
- Stage on SQLite and scratch Postgres with a fake `cluster_llm` (records the prompt, returns
  canned JSON): verdict effects, `may_flag_alone`, idle-only claim, `NoModel` parking,
  `unchanged` re-run, no transcript writes.
- Prompt snapshot test: the versioned prompt and schema; a change needs a version bump.
- No real LLM in CI.

### Rollout

- Phase 3, after 52.10. Off by default. The owner turns it on per library (README open question 3).
- Back out: turn it off. Its reasons and candidates stay on flags, harmless.

## Edge cases

- A model that answers in the transcript's language but not its script (Arabizi): dropped.
- The glossary would exceed the model's context: names by use, then terms, cut to fit.
- A long file with many flags: batches run one at a time, and the job yields between batches so
  a newly queued `transcribe` stops it after the current batch (it resumes later, done batches
  kept).
- Text the model refuses to judge (content filters): treated as no verdict.

[#113]: https://github.com/Hamza-Labs-Core/Maktaba/pull/113
