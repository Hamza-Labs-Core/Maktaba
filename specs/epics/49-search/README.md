# Epic 49: Search that answers like Google: ranked, instant, and Arabic-aware

## 1. Why

Searching what was said is what Maktaba is for: type a word, get the best videos, jump to the
moment it is spoken. On the home (45,339 videos, 23.1 M transcript lines) it does not work:

- **It is slow, and sometimes fails.** A common word (الله) matches millions of lines. The keyword
  query ranks every matching line (`ts_rank` per segment, `max` per video) before it answers:
  23-60 s, and a 500 when the 60 s statement timeout cancels it (server log, 2026-11-01). The app
  waits it out, so "search doesn't work in the app". PR #513 caps the ranking at 5,000 matching
  lines as a stopgap (91 ms); this epic replaces the cap.
- **It ranks the wrong thing.** It ranks lines, and for one word every line scores about the
  same. What should differ is the video: a lecture that says الصبر 200 times against one that
  says it once, in a short talk against a three-hour one. That is what BM25 does, per document.
- **It misses Arabic spellings.** `maktaba_search` is `unaccent + simple`. `unaccent` does not
  touch Arabic, so اسلام does not find إسلام, هدي does not find هدى, مدرسه does not find مدرسة,
  and a line with harakat (اللَّهُ) does not match الله. Hamza forms (أ إ آ) appear in about
  half the lines (a sample of 20,000).
- **It is not "Google-like".** No did-you-mean, no suggestions as you type (the suggest
  endpoint returns only the caller's own history, and was empty on the home), no phrase
  preference, no facets.

## 2. Research (2026-11-01)

| Option | Ranked from the index | Verdict |
|---|---|---|
| Today: GIN + `ts_rank` on segments | No: ranking reads every matching row from the heap | The cause of the 60 s |
| [pg_textsearch](https://github.com/timescale/pg_textsearch) (Timescale, PostgreSQL licence, v1.5.1) | Yes: BM25, Block-Max WAND top-k | **Chosen.** Needs PostgreSQL 17/18 and `shared_preload_libraries`; uses a Postgres text search configuration, so our Arabic config applies |
| [RUM](https://supabase.com/docs/guides/database/extensions/rum) (PostgreSQL licence) | Yes: positions in the index, `<=>` order | Fallback: works on 16, but ranks with `ts_rank`-style scores (no IDF, no length normalisation), and is larger and slower to write |
| VectorChord-bm25, ParadeDB pg_search | Yes | No: AGPL / ELv2, and weak Arabic tokenisation |
| A custom per-video term table | Yes, ranked at build time | No: code we would own forever, for what pg_textsearch does |
| A separate engine (Meilisearch, Typesense, OpenSearch) | Yes | No: another process on every machine, a second copy to keep in sync, library ACLs mirrored outside Postgres, and nothing in switchover, node copies or backups covers it |

**Benchmark** (scratch PostgreSQL 18.6 + pg_textsearch 1.5.1, 5,000 of the home's videos, 2.6 M
lines, one document per video, `text_config = public.maktaba_search`):

| Query | Today's query on the same sample | Per-video BM25 |
|---|---|---|
| الله (one very common word), top 200 | 85 ms warm (23-60 s on the full, busy home) | 0.3-0.7 ms |
| الصبر عند المصيبة (a sentence) | not run | 0.3 ms |
| الاستغفار (rare) | not run | 0.2 ms |
| الله, one library's videos | not run | 1.0 ms |
| Index build | GIN 7.0 s, 85 MB | 7.0 s, 51 MB |

The plan is a plain index scan (`Order By: body <@> bm25query`, 377 buffers). At the home's size
(45 k documents) the build is about a minute and the index about 0.5 GB.

## 3. Goals

1. A keyword search answers in under 50 ms at p95 on the home at full size, for one word, a
   sentence or a rare word, filtered by the caller's libraries. No cap, no timeout, no 500.
2. Videos are ranked by BM25 over the whole transcript (plus title, description, speakers),
   with phrase and proximity preferred; within a video, passages are ranked and timed for the
   jump.
3. Arabic spelling variants match: hamza forms, alef maqsura and ya, ta marbuta and ha, harakat
   and tatweel, and Arabic-Indic digits. Word forms match too: الصابرين, صبروا and يصبر find الصبر,
   with the exact word ranked above its other forms.
4. Google-like help: suggestions as you type (under 50 ms), did-you-mean on a misspelling, and
   facets (library, language, speaker, year, duration) with counts.
5. Hybrid keeps working: BM25 and meaning search (pgvector) fused by RRF. Search itself never waits
   on a language model.
6. **AI mode**, like Google's: a separate tab the person chooses. The home's local language model
   reads the best passages for the question and writes an answer, every sentence linked to the
   video and moment it comes from.
7. Indexing goes through the queue: a `keyword_index` stage after transcription; the existing
   videos are backfilled through the queue at low priority, one at a time.
8. PostgreSQL 18 everywhere, upgraded in place by the updater, proven in update-rehearsal first.
9. Every client gets it with no new endpoint shape: web, Apple, Android, TV, big screen.

## 4. Non-goals

- A separate search service (see research).
- Typo tolerance inside the index (fuzzy BM25). Did-you-mean covers misspellings.
- Searching inside video frames (OCR) or audio events.
- SQLite installs get the new Arabic normalisation and the per-video document, but rank with
  FTS5's `bm25()`; no PostgreSQL extension there.

## 5. Design

### 5.1 PostgreSQL 18 and pg_textsearch in every bundle

- Pins move to PostgreSQL 18.x (theseus-rs binaries, as today) in `tools/release/{mac,linux,win}-app/pins.env`,
  Docker and CI images (`pgvector/pgvector:*-pg18`), deb/rpm `pgbin`.
- `payload.sh` builds pgvector and pg_textsearch from pinned source tags (SHA-256), `with_llvm=no`,
  as pgvector is built today; Windows with `Makefile.win`.
- `shared_preload_libraries = 'pg_textsearch'` in the bundled server's start flags (`pgbundle`).
- Licences: both PostgreSQL licence; added to `licenses/`.

### 5.2 Upgrading a home and its machines (16 → 18)

- The updater gains a major-version step: stop, `pg_upgrade --link` from the old bundle's
  binaries (kept for this step) to the new, `vacuumdb --analyze-in-stages`, start. Minutes, not a
  copy. The pre-update backup (`pgbundle`) runs first, and a failed upgrade restores it.
- Node replicas: logical replication works across 16 → 18, so the home upgrades first and the
  machines follow in the cluster rollout (Epic 35.12). A machine on 16 keeps serving; search on it
  uses the old path until it upgrades.
- Proven in CI before any live update: update-rehearsal gains "16 → 18 with data, then roll back".

### 5.3 Arabic normalisation (`maktaba_search` v2)

A text search configuration that every index and query share (BM25, the GIN on lines, titles,
suggestions):

- strips harakat (U+064B-U+0652, U+0670), tatweel (U+0640);
- folds أ إ آ ٱ → ا, ى → ي, ة → ه, ؤ → و, ئ → ي;
- Arabic-Indic and Persian digits → ASCII;
- keeps `unaccent` for Latin.

Implemented as a custom `unaccent` rules file (`maktaba_arabic.rules`, shipped with the bundle) in
front of `simple`, so it is a dictionary change, not code. Changing the config rebuilds the
generated `search_tsv` and the indexes once (a migration, run in the queue's quiet hours on the home).

**Word forms (stemming).** A second configuration, `maktaba_stem`, runs the same normalisation and
then PostgreSQL's built-in Snowball Arabic stemmer (`arabic_stem`; English words through
`english_stem`). Checked on the home: صبروا, يصبر and الصبر all become صبر; المسلم and المسلمون become
مسلم. It is not perfect (الصابرين becomes صابر, and a prefixed و is not always stripped: والصابرات
becomes والصابرا), so:

- the per-video document is indexed twice, exact (`maktaba_search`) and stemmed (`maktaba_stem`);
- a query scores against both, the exact score weighted higher, so the word as typed ranks first and
  its other forms follow;
- a small prefix rule list (و ف ب ك ل ال وال بال فال كال لل) is stripped before the stemmer in
  `maktaba_stem`, and the relevance check (49.9) measures whether a better stemmer is needed.

### 5.4 One search document per video

```sql
CREATE TABLE video_search_docs (
    video_id    UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id  UUID NOT NULL,
    language    TEXT,
    title       TEXT NOT NULL DEFAULT '',
    speakers    TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,            -- the active transcript, lines in order
    transcript_id UUID NOT NULL,          -- what body was built from
    built_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX video_search_docs_bm25 ON video_search_docs
    USING bm25 ((ARRAY[title, speakers, body])) WITH (text_config = 'public.maktaba_search');
CREATE INDEX ON video_search_docs (library_id);
```

- Title and speakers weigh more than body (pg_textsearch multi-field; weights tuned on the
  owner's test queries, story 49.9).
- Lines stay where they are (`transcript_segments` + GIN): they give the passages and the jump
  times for the top videos only, so that query is bounded by video ids.
- Not copied to the machines' replicas if they do not serve search (`cluster_copy_excluded`), as
  `segment_embeddings` is not; a machine that serves search builds its own through the queue.

### 5.5 The `keyword_index` stage (queue)

- A new stage in the stage graph, after `transcribe` (beside `index`, the meaning stage), pool
  `index`. It builds the video's document from the active transcript and upserts it.
- Enqueued when a transcript becomes active, when a title or speakers change, and when the config
  version changes (stage version bump re-queues done items, as other stages do).
- Backfill: a stage version bump on deploy queues every video with an active transcript at low
  priority (`priority 10`), one at a time; the Queues page shows it like any stage.
- Until a video has a document, its keyword results come from the line path (with the 5,000 cap
  from #513), so search works during the backfill. The cap is removed when the backfill is done.

### 5.6 The query

- **Keyword:** `ORDER BY doc <@> to_bm25query($q, 'video_search_docs_bm25') LIMIT $k` with the
  library filter (`library_id = ANY($libs)`); a non-admin with few libraries pre-filters by the
  `library_id` index (pg_textsearch's documented pattern).
- **Phrase and proximity:** for the top 100 videos, a phrase match (`<->`) on their lines adds a
  boost; a sentence whose words appear together ranks above one whose words are scattered.
- **Passages:** the top passages per shown video from their lines (GIN, `video_id = ANY`), with
  `<mark>` and times, as today.
- **Hybrid:** BM25 videos and meaning-search videos fused by RRF (k=60, today's weights).
- No language model in this path: the results tab is always the fast, ranked search.

### 5.7 Google-like help

- **Suggestions as you type:** a terms dictionary (`search_terms(term, df)`) maintained by the
  `keyword_index` stage, plus titles and speakers; prefix match with `pg_trgm`, ranked by document
  frequency; the caller's recent searches first. Under 50 ms.
- **Did-you-mean:** when a query has few results, the closest frequent term by trigram similarity
  per word (`similarity > 0.4`), shown as "Did you mean …" and one tap away.
- **Facets:** counts by library, language, speaker, year and duration band over the matching
  videos (the top 1,000), returned with the first page.
- **Sort:** relevance (default), newest, most matches.

### 5.8 AI mode (a separate tab)

The search page has two tabs: **Results** (the default, everything above) and **AI mode**. AI mode
runs only when the person opens that tab or submits from it; Results never waits on it.

- **Retrieval:** the same hybrid search, then the top 30 passages across the top videos (with the
  passages around each for context), filtered by the caller's libraries as everywhere.
- **Answer:** the home's local language model (Epic 36.11) is given the question and those
  passages, and writes a short answer in the question's language. Every sentence carries a
  citation: the video, the time, and the quoted words, which open the player at that moment.
- **Grounded only:** the model is told to answer only from the passages; a sentence it cannot cite
  is dropped, and when the passages do not answer the question it says so and shows the passages
  instead. Nothing from outside the library.
- **Streaming:** the answer appears as it is written (Server-Sent Events, as `/ws/jobs` does); the
  sources show first, so there is something to read while it writes.
- **Follow-ups:** the person can ask a follow-up in the same tab; the earlier question and answer
  are part of the next prompt, and retrieval runs again for the new question.
- **When there is no model:** a home without the language model shows the tab with a message
  saying how to enable it (Settings, help link), not an error.
- **Cost:** answers are not cached across people (permissions differ); one answer at a time per
  home, others wait in line with their position shown, so the transcription queue is not starved.
- **Roles:** the same library permissions as search; an admin can turn AI mode off for the home.

### 5.9 API and clients

- `POST /api/search` keeps its request and response; new optional fields: `facets`,
  `did_you_mean`, `took_ms` per leg. `GET /api/search/suggest` returns terms, titles and history.
- `POST /api/search/ai` (SSE): `{q, history?}` → `sources` event, then `text` events with citation
  markers, then `done`; `409 ai-unavailable` when the home has no model; `429` with queue position
  when another answer is being written.
- Web, Apple, Android, TV and big screen: suggestions list, did-you-mean row, facet chips, sort,
  and the AI mode tab with cited answers that open the player at the moment. English and Arabic for
  every string.

## 6. Stories

| # | Story | Acceptance |
|---|---|---|
| 49.1 | **PostgreSQL 18 + pg_textsearch in every bundle** (mac, linux x86_64/aarch64, windows, Docker, CI, deb/rpm) | Each payload's `postgres --version` is 18.x; `CREATE EXTENSION pg_textsearch` works; the bundle's start flags preload it; licences shipped |
| 49.2 | **Major upgrade in the updater (16 → 18)** with backup first and restore on failure | update-rehearsal upgrades a 16 home with data to 18 and rolls back, green on the exact commit before any live update |
| 49.3 | **Cluster: machines upgrade after the home**; a 16 machine keeps serving | two-machine e2e: home on 18, node on 16 replicating, then the node upgrades |
| 49.4 | **Arabic normalisation (`maktaba_search` v2) and word forms (`maktaba_stem`)** | اسلام finds إسلام, هدي finds هدى, مدرسه finds مدرسة, الله finds اللَّهُ; يصبر and صبروا find الصبر with the exact word first; tests on Postgres and SQLite; the migration rebuilds `search_tsv` |
| 49.5 | **`video_search_docs` + BM25 indexes, exact and stemmed** (migration, SQLite twin with FTS5) | schema up/down on scratch PG and SQLite; one index on `maktaba_search`, one on `maktaba_stem` |
| 49.6 | **`keyword_index` queue stage** and backfill through the queue | a new transcript queues it; a title change queues it; a version bump queues every video at priority 10; shown on the Queues page; one item at a time |
| 49.7 | **The query: BM25 videos, phrase boost, passages, hybrid** | p95 < 50 ms on a 45 k-document fixture; الله, a sentence and a rare word; ACL filter test for a limited user; hybrid order unchanged where BM25 and today agree on the obvious case |
| 49.8 | **Suggestions, did-you-mean, facets, sort** (API) | suggest < 50 ms on the fixture; a misspelled Arabic word gets the right did-you-mean; facet counts match the result set; role tests |
| 49.9 | **Relevance check** | 30 test searches (Arabic and English) drafted from the home's most searched words and the library's topics, each with the videos that should come first; the owner reviews the list once; field weights, the exact/stem balance and the phrase boost tuned until they pass; kept as a regression test |
| 49.10 | **Web search page**: suggestions, did-you-mean, facets, sort | English and Arabic; axe; screenshots dark |
| 49.11 | **Apple and Android apps, TV, big screen** | same features; UI tests on a throwaway simulator / emulator |
| 49.12 | **AI mode: API** (retrieval, grounded answer, citations, streaming, follow-ups, one at a time) | every sentence has a citation that exists in the passages; an unanswerable question says so; no model → 409 with help; a limited user never gets a passage from another library (role test); measured first-token time on the home Mac |
| 49.14 | **AI mode: the tab in every client** (web, Apple, Android, TV, big screen) | the tab streams the answer, sources first; a citation opens the player at its time; follow-up works; English and Arabic; UI tests |
| 49.13 | **Remove the stopgap** | the 5,000 cap and the per-line ranking path are gone once every video has a document; help pages and docs updated |

## 7. Acceptance

- On the home after the upgrade and backfill: الله, الصبر عند المصيبة and a rare word each answer
  in under 50 ms from the app; no search 500 in a week of logs.
- اسلام and إسلام return the same videos.
- Typing three letters shows suggestions; a misspelling shows did-you-mean.
- The update to 18 went through update-rehearsal on the exact commit before the live home.

## 8. Decisions

- One delivery, no phases: word forms, AI mode and every client are in this epic (owner, 2026-11-01).
- No language model in ordinary search; AI mode is a separate tab, run only when chosen, like
  Google's AI mode (owner, 2026-11-01).
- The relevance list is drafted by us and reviewed once by the owner (49.9).
