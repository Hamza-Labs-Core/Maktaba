# Story 52.07 — The LLM judge that flags, not rewrites (REMOVED)

> Epic 52 · **Removed 2026-11-03** by decision 3 ([README §8](README.md#8-decisions-2026-11-03)).
> The number is kept so references stay valid; no story replaces it, and nothing is renumbered.

## Why it was removed

The first draft added an optional `word_judge` stage: an LLM on the cluster would read flagged
and borderline sentences and add an `llm_disagrees` verdict and an `llm` candidate. Decision 3:
there is **no LLM judge**. The hearing test (2026-10-19) had already shown an LLM judge
preferring tidy wrong spelling, and deterministic checks are cheaper, repeatable and testable.

## What goes with it

Nothing from this story is built. Removed across the epic:

- the `word_judge` stage, its `llm` pool claim and its idle-only rule;
- the reason code `llm_disagrees`, the candidate source `llm`, and `scores.llm`;
- the library settings `llm_judge.enabled`, `llm_judge.model`, `llm_judge.alone_may_flag`;
- the failure class `llm_unavailable` and the `queue.stage.word_judge` strings;
- the glossary feed from the vocabulary (52.10) to the judge;
- rollout step R6 and the phase-3 judge work.

## What does the job instead

Every check is deterministic: CAMeL Tools morphology for Arabic (52.4), Hunspell for English
(52.5) and for the ~100 languages it has dictionaries for (52.3's generic Hunspell pack), the
fallback pack for any other language (52.3), calibrated confidence and the second listen (52.6),
and the Quran check against the canonical text (52.15).
