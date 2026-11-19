# Epic 49: Search that answers like Google: ranked, instant, and Arabic-aware

## 1. Why

Searching what was said is what Maktaba is for: type a word, get the best videos, jump to the
moment it is spoken. On the home (45,339 videos, 23.1 M transcript lines) it does not work:

- **It is slow, and sometimes fails.** A common word (الله) matches millions of lines. The keyword
  query ranks every matching line (`ts_rank` per segment, `max` per video) before it answers:
  23-60 s, and a 500 when the 60 s statement timeout cancels it (server log, 2026-11-01). The app
  waits it out, so search seems not to work in the app. PR #513 caps the ranking at 5,000 matching
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
  home's test queries, story 49.9).
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
| 49.9 | **Relevance check built from the transcripts** | test searches generated from the home's own transcripts, no hand-written list: (a) *known-item*: 200 distinctive sentences and phrases taken from random videos, each must bring its own video in the top 3, also with the spelling and word-form variants of 49.4; (b) *topic*: for videos with chapters or topic tags, a chapter title or topic word must bring those videos in the top 10; (c) *common words*: الله, الصبر and the 20 most frequent content words must answer under 50 ms and rank videos that use the word most (per minute) above passing mentions; field weights, the exact/stem balance and the phrase boost tuned until they pass; the generator and its fixed seed kept as a regression test, re-run on a sample in CI |
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

- One delivery, no phases: word forms, AI mode and every client are in this epic (2026-11-01).
- No language model in ordinary search; AI mode is a separate tab, run only when chosen, like
  Google's AI mode (2026-11-01).
- The relevance tests are generated from the home's own transcripts, not a hand-written list (2026-11-01).

## 9. As built

### 49.1 PostgreSQL 18 + pg_textsearch in every bundle

- Every app payload (Mac, Linux x86_64 and aarch64, Windows) carries PostgreSQL 18.7 in
  `postgres-18/`, beside the PostgreSQL 16 in `postgres/` that the server still runs. The pins
  are `POSTGRES18_*` and `PGTEXTSEARCH_*` in `tools/release/{mac,linux,win}-app/pins.env`, the
  same theseus-rs builds as 16, checked by SHA-256. `payload.sh` leaves out what the 18 builds add
  and Maktaba never loads: LLVM and `llvmjit`, PL/Python and its Python, the bitcode, ecpg and
  sqlite. About 27 MB compressed on the Mac.
- pgvector 0.8.7 and pg_textsearch 1.5.1 are built from their pinned source tags against 18:
  gcc or clang with `with_llvm=no` and no `-march=native`, Visual C++ on Windows. On Windows,
  pg_textsearch 1.5.1's `Makefile.win` lacks six of its Makefile's objects (`boolean`,
  `build_merge`, `rls`, `debug/injection`, `index/compaction_job`, `segment/graph_snapshot`), so the
  build passes the Makefile's object list to nmake.
- The bundled server (`pgbundle`) adds `shared_preload_libraries=pg_textsearch` whenever its
  install's `lib/` holds the module, and merges it into a household's own
  `shared_preload_libraries`. On 16, or on a machine's fetched Postgres, it adds nothing.
- `payload.json` gains `postgres_18` (version, source, SHA-256, `dir`, and its libraries on Linux
  and Windows) and `pg_textsearch`. Its `postgres` stays 16, so `app_postgres_major` and the
  updater's major-version gates are unchanged.
- Licences: `licenses/postgresql-18.txt` and `licenses/pg_textsearch.txt` (LICENSE and NOTICE).
- Proof: each payload's `check.sh` runs `tools/release/pg18-check.sh` on the built payload. It
  checks that `postgres --version` is 18.x; that the server starts with the bundle's preload;
  that `CREATE EXTENSION` works for pg_textsearch, vector, pg_trgm, pgcrypto and unaccent; that a
  BM25 index on Maktaba's kind of configuration ranks the right row first through the index;
  that the schema migrates (`maktaba-api migrate up`); and that the payload's own 16 cluster
  upgrades to 18 with `pg_upgrade`. The Linux job also runs `TestRealPgTextsearchPreloaded`
  (pgbundle's `Prepare` and start flags on the payload's 18).
- **How it differs from the spec.** 18 ships beside 16 rather than replacing it. A bundle that
  only carried 18 would leave every home on 16 unable to update (the updater refuses another
  major, `pg_major_change`) and a container started from the new image unable to start
  (`pg_major`) until 49.2's upgrade exists. The switch of the running server to 18 comes with
  49.2's upgrade. So do Docker Compose's and CI's `pgvector/pgvector:*-pg18` images: those run
  the server's database, and they move when the server does. A node's fetched Postgres
  (`pgbin`, which the deb and rpm also use) moves in 49.3.

### 49.2 The major upgrade in the updater (16 → 18)

- **Where it runs.** The server's start runs it (`serve_postgres.go`, `majorUpgradeStep`, before
  the cluster's `Prepare`), on any install that runs the bundled Postgres: the Mac, Windows and
  Linux apps, the Docker image, an update by hand or by the updater. The payload has run
  `postgres-18/` since 49.1 (`pgbundle.payloadHome`); its `postgres/` (16) is the old major's
  programs that the step reads.
- **A cluster of its own.** The 18 cluster lives in `<state>/postgres-18` (`pgbundle.DataDirFor`),
  made by `pg_upgrade` from `<state>/postgres`. The files are cloned where the filesystem can
  (APFS, btrfs, XFS: no extra room), else copied when there is room (the backups' reserve kept
  free). They are never linked: a linked old cluster cannot start again. The 16 cluster is not
  changed, so whatever switches back to the release before (the updater's rollback, the
  launcher's guard, the desktop app) starts it on its own cluster with its own code.
- **Its own record.** The upgraded cluster's database record is `state/database-pg18.json`
  (`apppayload.ClusterMajorIn`: the install's directories say which record is in use, in every process), seeded from `state/database.json` at the upgrade. The release
  before reads its own record exactly as it left it. Its own refusals (a record from a newer
  release, `db_refused_older`) never meet the newer cluster's.
- **Stale copies.** The new cluster records what it was made from: the old cluster's system
  identifier and its last checkpoint (`maktaba-upgraded-from.json`). When the old cluster has
  moved on (the release before ran on it after a rollback), the next start sets the copy aside
  (`postgres-18.stale`) and upgrades again, so nothing written meanwhile is lost. A run that
  stopped part way leaves a marker with no checkpoint and is redone. A different cluster in the
  old directory is never taken over the upgraded one.
- **What pg_upgrade needed.**
  - The old server gets `wal_level=logical` (a machine's replication slots refuse a server
    without it).
  - Matching checksums and the same superuser and password.
  - A short socket directory.
  - The app role's grants on pg_catalog's replication-slot functions come off the 16 cluster
    for the upgrade and back on after it. PostgreSQL 17 changed
    `pg_create_logical_replication_slot`'s signature, and pg_upgrade's restore of that ACL
    fails. The marker goes first, so the release before re-grants at its next start should the
    step stop in between. 18 runs the cluster SQL on its own signatures.
  - An old cluster not shut down cleanly is started and stopped once.
  - Afterwards every extension is updated (`ALTER EXTENSION … UPDATE`) and
    `vacuumdb --analyze-in-stages --missing-stats-only` runs.
- **When it cannot run.** On no room or a failed `pg_upgrade`, the home keeps running
  PostgreSQL 16 on its cluster (`pgbundle.HoldMajor`; every command's `Resolve` honours it), logs
  why, and tries again at its next start. A home is never left unable to start.
- **Every command agrees on the cluster in use.** The record and the cluster are read from the
  install's directories (`apppayload.ClusterMajorIn`: `postgres-18` with no hold), in every
  process: the server, the container's entrypoint (its image fallback marks the right record), an
  offline `restore`, and the updater.
  - A restore onto a release that ran on 16 is refused on an 18 home (`restore_other_major`).
    That release would start on the untouched 16 cluster and never put the dump back.
  - A storage move carries `postgres-18` (`stateFolders`), checks the cluster in use on its copy,
    and refuses while either cluster runs.
  - A home whose 16 cluster waits for its upgrade counts as set up (`Bundle.HasDatabase`), and
    `Prepare` never makes an empty 18 cluster beside it.
- **pg_upgrade's own servers** take free ports each try. A stop interrupts pg_upgrade, which
  then stops the servers it started, rather than killing it (Windows: killed, as before). A
  server an earlier, hard-stopped try left behind is stopped by the next start, on the half-made
  directory and on the old cluster alike. The set-aside `.stale` copy goes before a redo's room
  is counted, so a disk that took one copy takes the next.
- **The pre-update backup** is the existing database step's dump (`pgbundle`, run by the 18
  release before its migrations). A failed migration puts it back into the 18 cluster. The 16
  cluster remains the way back to the release before.
- **Proof.**
  - Update-rehearsal scenario 17 (`TestUpdateRehearsal17PostgresMajorUpgrade`): a seeded 16 home,
    an update to an 18 release that fails its watch and rolls back onto 16 (counts, record and
    cluster unchanged), then an update to an 18 release that passes (served on 18, every row,
    pg_textsearch preloaded and created, the 16 cluster still there).
  - `TestRealMajorUpgrade` on real 16 and 18 programs, in the rehearsal's kit job (Linux, where
    ext4 takes the copy path) and in the Mac and Windows app jobs on their own payloads (the Mac's
    APFS takes `--clone`): the
    upgrade, grants on both majors, the rollback's start on 16, the redo after 16 moved on,
    recovery of a 16 cluster stopped hard.
- **How it differs from the spec.**
  - No `--link`: a linked old cluster could not be rolled back to.
  - The old binaries are the payload's own `postgres/`, not the old bundle's.
  - The release keeps advertising PostgreSQL 16 in `payload.json`'s `postgres` and
    `release.json`'s `app_postgres_major`. The releases on homes refuse an update whose major
    differs (`pg_major_change`), so a payload that said 18 there could never be installed by
    them.
  - Retiring the 16 cluster (its disk space, once a release after the upgrade has committed)
    is left for later; it is kept meanwhile.
  - If `<state>/postgres` is ever made anew by hand after an upgrade (another cluster's system
    identifier), the upgraded cluster is kept, and that new 16 cluster is not taken over it.
