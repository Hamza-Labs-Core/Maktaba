---
schema: 1
kind: pr
pr: 1525
title: "AI mode's API: every sentence cited; a real question found nothing until its content words were searched"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1525]
---

## What happened
- #1525 builds story 49.12, `POST /api/search/ai`. It answers as Server-Sent Events: `queue`,
  `sources` first, then one `text` event per checked sentence, then `done`. A sentence is
  sent only when it cites a passage the model was given. One answer is written at a time, and
  up to 8 questions wait in line. With no model the endpoint answers 409 with `help`; with AI
  mode turned off, 409 `ai-off`.
- The fake-model tests all passed, but the first run against a real model (qwen2.5:7b, the
  Mac app's bundled Ollama) found no passages for any question. The keyword search needs
  every word, and a question's own words ("what are", ما هي) are in no transcript. Now,
  when the question as written finds too few passages, each of its content words is searched
  as well (question and stop words left out), and the lists are fused.
- With the real model, three questions the passages do not answer were not answered: one with
  no passages, one whose passages name no narrator, and one whose passages never say what was
  asked. One sentence cited all 30 passages, so a sentence now keeps at most 5 citations.
- First token on an M3 Ultra: about 3.7 s for a new 30-passage prompt, 0.1 to 0.6 s for a
  short or cached prompt, and 7.8 s when the model was cold.
- CI and one GLM-5.3 review round found more, all fixed in
  f4e527750be2482f6b85b6349a211fe937efb82e:
  - The three new problem types were not in `shared/api/problems.json`. The API would have
    sent the generic types, and the unregistered-problems gate fails on them.
  - The parental bypass rehearsal failed: no probe reached `POST /api/search/ai`. It now
    asks with a model that answers with every word it read, so the leak scan covers the
    model's input as well as the sources.
  - The affected-tests gate failed on `p6.go`, `rules.py` and `search_settings.py`. The
    router's llm check is now `searchLLMOn`, with a test, and the pipeline tests the new
    rules (6c6daf2824d127e5028a808107e91f9e005cf3b6).
  - The website's manifest lists each page's anchors, so the help section needed a rebuild.
  - The splitter took an Arabic-Indic decimal (٣.٥) for a sentence end, and read an end
    after a marker byte by byte (؟ is two bytes). Digit conversion rewrote the sentence's
    own digits. The last, held sentence was lost when a timeout or a failed model ended the
    stream.
- With the Mac at a load average of about 40, the first token took 10 to 13 s.
- Local-only noise, not caused by the change:
  - `TestP57LiveTVPG` T02 fails against a scratch Postgres that runs in local time, on main
    as well; CI's server is in UTC.
  - A shared Go build cache emptied mid-run gave "no such file" link errors. A private
    `GOCACHE` avoided it.

## What we learned
- Test AI mode against a real model before trusting its retrieval. A fake model is handed the
  passages the test planted, so it never shows that the search finds none.
- The citation check proves a sentence cites a passage that exists, not that the passage says
  what the sentence says. The instructions and a low temperature carry that part.

- A new route needs four registrations beyond its handler: OpenAPI (en, ar), the problem
  types it sends, the parental surface registry, and a probe in the bypass rehearsal.

## Keep doing
- Keep `TestAI_RealModel` as the measure: run it with `MAKTABA_TEST_OLLAMA_URL` after any change
  to the prompt, the retrieval or the model.

## Open
- The first-token time on the home Mac with the home's own library (49.12 stays partial).
- 49.14, the AI mode tab in every client.
