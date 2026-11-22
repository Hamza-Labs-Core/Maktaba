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
| 49.15 | **Arabic titles rank by similarity** | found in 49.8, the bundled server's database being `--locale=C`, where pg_trgm finds no trigram in an Arabic word: `similarity()` with `videos_title_trgm_idx` (migration 0098) gives every Arabic title 0, and title matches fall back to newest first. Done when an Arabic query ranks the Arabic title closest to it first on a `--locale=C` database, Latin titles keep their order, and a test on Postgres shows both |

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
- **The last step never stops a start.** After the upgrade, every extension is updated and
  the missing statistics are made (`FinishUpgrade`). If that fails, the cluster runs as
  pg_upgrade made it: its extensions at their 16 versions, which work, and some planner
  statistics missing. The failure is logged at error level and recorded in the cluster
  (`maktaba-after-upgrade.failed`), and every start retries until it succeeds. A failure that
  repeats leaves the home running on 18 and saying why, never restarting.
- **When it cannot run.** On no room or a failed `pg_upgrade`, the home keeps running
  PostgreSQL 16 on its cluster (`pgbundle.HoldMajor`; every command's `Resolve` honours it), logs
  why, and tries again at its next start. A home is never left unable to start.
- **Every command agrees on the cluster in use.** The record and the cluster are read from the
  install's directories, in every process: the server, the container's entrypoint (its image
  fallback marks the right record), an offline `restore`, and the updater.
  `apppayload.ClusterMajorIn` says 18 when `postgres-18` holds a cluster, no hold is recorded,
  and the 16 cluster hasn't moved on since its upgrade. That last test reads the 16 cluster's
  `global/pg_control` (system identifier and latest checkpoint) against the upgrade's marker,
  without running a program. On a home rolled back to a release before the upgrade, 16 is
  therefore in use, and a restore marks `database.json`, the record that release reads. An 18
  start's redo of the upgrade carries that mark into `database-pg18.json`.
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

### 49.3 Cluster: machines upgrade after the home; a 16 machine keeps serving

- **A machine on 16 follows a home on 18.** A machine's copy is a logical subscription: schema
  from its own migrations, rows from the home's publication. That works from an 18 publisher to
  a 16 subscriber, and the other way round: a home held on 16 with machines already on 18. The
  home's `pg_upgrade` from 16 carries no replication slots, so each machine's copy finds its slot
  gone. The copy manager makes such a copy anew (`nodecopy.Manager.resubscribeReason`), at the
  cost of one full first copy per machine. The home's grant drops the machine's old slot when
  the copy asks afresh.
- **The machine upgrades with its release** (`serve_copy.go`). A copy runs on a Postgres the
  machine has of either major its payloads carry, 16 or 18 (`copyMajors`). A payload with
  `postgres-18/` runs the copy on 18; before, anything but 16 made it fetch 16.
  - **Where the copy lives.** It is kept where a home of its major keeps its cluster
    (`pgbundle.DataDirFor`: `<state>/postgres` for 16, `<state>/postgres-18` for 18). A switch
    that makes the machine the home therefore finds its cluster where an 18 home looks for it.
  - **The other major's directory.** A cluster there is set aside (`<dir>.pg<major>-<time>`,
    never deleted) and the copy is made again from the home. That covers the copy made before
    the machine's payload moved to 18, and the machine's own database from before it joined. A
    promotion can never find a stale one.
  - **When the set-aside fails** (a server left holding the directory on Windows), that start
    keeps no copy and says so, rather than not starting.
  - **What a machine serves meanwhile.** Until its new first copy is complete, a machine whose
    copy is remade serves its home's reads through the home, as any machine whose copy is
    rebuilding does. With the home away, it serves only its own routes.
- **Proof (the replica job).** PostgreSQL 18 is built from the Linux payload's pins. Every run
  checks the major its copy started in the machine's log.
  - `TestTwoMachinesOnePC` with the home's database on 18 and the machine's copy on 16. Part way
    through, the machine moves to 18 (`MAKTABA_TEST_PG18_BIN`): it restarts on 18 programs, its
    16 copy is set aside, a fresh copy in `postgres-18` follows the home, and the home-gone
    checks then run on that copy.
  - The same with the copy on 18 from the start.
  - The home on 16 with the machine on 18.
  - `TestSwitchTheHome` with every machine on 18: a machine whose copy runs on 18 is promoted
    and starts as the home.
  - Unit tests: `TestCopyOfTheOtherMajorIsSetAside`, `TestTheOtherDirectorysClusterIsSetAside`.
- **How it differs from the spec.** `pgbin` (the copy's fetched Postgres) still pins 16. A
  machine whose payload has `postgres-18/` doesn't fetch, and a fetched 16 copy follows an 18
  home. A home promoted from a fetched 16 copy runs on 16, the `[postgres] home` the switch
  pins, until a person moves it.

### 49.4 Arabic normalisation (`maktaba_search` v2) and word forms (`maktaba_stem`)

- **Normalisation** is `maktaba_normalize(text)` (migration 0231), one `translate()`:
  - harakat (U+064B–U+0652, U+0670) and tatweel (U+0640) go;
  - أ إ آ ٱ → ا, ى → ي, ة → ه, ؤ → و, ئ → ي;
  - Arabic-Indic and Persian digits → ASCII;
  - Latin keeps `maktaba_search`'s `unaccent` step.
- **Word forms** are the `maktaba_stem` configuration: the same normalisation, then PostgreSQL's
  own Snowball stemmers (Arabic for words, English for ASCII words). `maktaba_stem_prepare()`
  first strips what the Arabic stemmer leaves:
  - the prefixes وال بال فال كال لل;
  - the imperfect tense's ي ت ن before a three-letter root (a four-letter word, or one with
    ون ين ان وا after the root). On 16 and 18 the stemmer alone leaves `يصبر` as it is.

  So الصبر, بالصبر, صبروا, يصبر, تصبر and نصبر all become صبر, and three-letter words such as
  يوم, تمر and نور keep their first letter. Remaining roughness (يزيد → زيد, والصابرات →
  صابرا) is for story 49.9's relevance check to measure. The exact form is weighted above
  its stem, so the word as typed ranks first.
- **The two sides of a match** are `maktaba_tsv` / `maktaba_tsv_stem` (a text) and
  `maktaba_query` / `maktaba_query_stem` (a query, `websearch_to_tsquery`), all `IMMUTABLE`,
  ready for story 49.5's indexes.
- **SQLite** normalises in the application (`api/internal/textnorm`, the same folding) before
  FTS5 indexes or matches a text. A test keeps it equal to Postgres's function. Word forms
  are Postgres only (§4).
- **Proof** (`api/internal/textnorm`), on Postgres 16 and 18 through the real migrations:
  - اسلام finds إسلام, هدي finds هدى, مدرسه finds مدرسة, الله finds اللَّهُ;
  - يصبر, تصبر and صبروا find الصبر, with the exact word ranked first;
  - the Go and SQL foldings agree on a corpus;
  - on SQLite, FTS5 over the normalised text finds every spelling, and an unnormalised query
    doesn't (the control).
- **How it differs from the spec.**
  - The normalisation is a SQL function, not a custom `unaccent` rules file. A file in the
    server's `tsearch_data` would have to reach every Postgres the migrations run on: the
    bundles, Docker Compose's image, CI's service containers, an operator's own server. A
    configuration that names a missing file fails its migration there.
  - The migration doesn't rebuild `transcript_segments.search_tsv` (23.1 M rows on the home).
    The repo's migration rules forbid long-running DDL and rewrites in a migration (rules 4
    and 5), and nothing needs it. Story 49.5's per-video documents carry the normalised text,
    and story 49.7's passages match the lines of the videos found on the fly, a bounded set.
    `search_tsv` serves the line path, unchanged, until story 49.13 removes it.

### 49.5 `video_search_docs` and its BM25 indexes, exact and stemmed

- **The table** is `video_search_docs` (migration 0232), as §5.4 has it: one row per video
  (`video_id` → `videos` with `ON DELETE CASCADE`, `library_id`, `language`, `title`,
  `speakers`, `body`, `transcript_id`, `built_at`), with a btree on `library_id`. Story 49.6's
  `keyword_index` stage fills it. A machine's copy leaves it out (`cluster_copy_excluded()`).
- **Two BM25 indexes**, each over title, speakers and body (pg_textsearch multi-field):
  - `video_search_docs_bm25`: `maktaba_normalize()` of each field, `maktaba_search`;
  - `video_search_docs_bm25_stem`: `maktaba_stem_prepare()` of each field, `maktaba_stem`.

  Story 49.7 queries each with the same function applied to the query (`OPERATOR(textsearch.<@>)
  textsearch.to_bm25query(maktaba_normalize($q), 'video_search_docs_bm25')`), and weights the
  exact score above the stemmed one.
- **Where the indexes are made.** pg_textsearch exists on PostgreSQL 18 only, where the
  server preloads it, and it isn't a trusted extension:
  - the bundled server's bootstrap and cluster SQL create it, as the superuser, when the
    server has it and preloads it (`pgbundle.textsearchSQL`, `clusterRev` 5), as pgvector is;
  - it lives in its own schema, `textsearch`, never `public`, with USAGE for the app role.
    A few of its functions (test and debug ones) are the superuser's alone. The app role's
    migrations grant on every function of `public` (slot 0103's `maktaba_worker` grants), and
    a grant on a function the granting role may not run fails. In `public`, a fresh home's
    first migration failed. A restore leaves the schema's entry and its grant out, as it does
    the extensions';
  - the migration doesn't build the indexes. It ships `maktaba_search_index_statements()`,
    which returns a `CREATE INDEX CONCURRENTLY IF NOT EXISTS` for each index missing (after a
    `DROP INDEX CONCURRENTLY` of one a failed build left invalid), and nothing without the
    extension;
  - `maktaba-api migrate up` runs those statements after the migrations, outside a
    transaction (`api/internal/searchindex`). The server runs it at every start, so a fresh
    home on 18 builds them at its first start, and a home whose schema migrated on 16 builds
    them at its first start on 18. A build that fails is logged, the start goes on, and the
    next start tries again.
- **Without pg_textsearch** (PostgreSQL 16, an operator's own server, a machine's fetched
  Postgres) the table is there with no BM25 index. Keyword search keeps its line path.
- **SQLite** gets the same table, and an FTS5 table `video_search_docs_fts` (title, speakers,
  body, keyed by an unindexed `video_id`: a rowid of a table without an integer key can change
  at a `VACUUM`). The application writes it with `api/internal/textnorm`'s normalised text. A
  trigger deletes a document's FTS5 row with the document.
- **Proof:**
  - `api/internal/searchindex`, on Postgres 16 and 18 through the real migrations: the
    schema goes up, down (slot 0230's exclusion back) and up again.
  - On 18 with pg_textsearch, `Ensure` builds one index on `maktaba_search` and one on
    `maktaba_stem`, both CONCURRENTLY, and builds nothing on a second call. It rebuilds an
    index marked invalid.
  - Ranked queries are answered by each index's Index Scan: احمد المصيبه finds أحمد … المصيبة
    through the exact index, يصبر finds الصبر through the stemmed one, and talks finds
    talking.
  - On SQLite, FTS5 finds a document by its normalised text, and a deleted video takes both
    rows.
  - CI runs the 18 tests in the replica job, on its PostgreSQL 18 server.
  - Each payload's `pg18-check.sh` runs `maktaba-api migrate up` as a home does: an app role
    that is no superuser, on a database with the extensions made by the superuser and
    pg_textsearch in its schema. It checks that both indexes are there and valid.
  - `TestRealPgTextsearchPreloaded` checks that the bundle's own bootstrap puts the extension
    in `textsearch`, and that the app role can grant on all of `public`'s functions.
- **How it differs from the spec.**
  - The indexes are over the normalised fields (`maktaba_normalize`, `maktaba_stem_prepare`),
    not the raw ones. Story 49.4 made normalisation a SQL function instead of a rules file
    inside the configuration, so the index expression applies it.
  - There is a second, stemmed index. §5.3 asks for the document indexed twice; §5.4's
    listing shows only the exact one.
  - The migration doesn't create the indexes; `migrate up` does, after it. A migration can't
    tell whether pg_textsearch is there without a `DO` block, and a `DO` block can't build an
    index CONCURRENTLY (the repo's migration rule 4).
  - Title and speakers aren't yet weighted above body. pg_textsearch 1.5.1's multi-field
    index scores the fields as one document. Story 49.9 tunes the weights against the
    relevance check.

### 49.6 The `keyword_index` stage and its backfill

- **The stage** (`pipeline/src/maktaba_pipeline/engine/stages/keyword_index.py`) is a core
  stage of the always-on `core.search` component, on any machine's CPU, after
  `second_listen` (its edge). On a video it builds the document from:
  - the lines of the video's first active transcript (by id, as `content_screen` reads it), in
    order, blank lines left out;
  - the names of the speakers of those lines (`segment_speakers`; unnamed clusters left out);
  - the title the video shows (its manual display title, else the one found for it, else
    its metadata title) with its file name without the extension.

  It upserts `video_search_docs` with the transcript, its revision and the stage's version
  (migration 0233: `transcript_revision`, `doc_version`). On SQLite it also writes the
  document's FTS5 row, normalised in Python (`search/textnorm.py`, held equal to
  `maktaba_normalize()` by a test). A video with no active transcript, or deleted, loses its
  document. The row is its own state: the stage keeps no input fingerprint, and a run with
  nothing to change is `unchanged`.
- **What queues it.**
  - A new transcript: the `second_listen` edge, which every transcript passes.
  - A title or display title set in the API: `videos.KeywordIndexRefresh`.
  - A speaker renamed or merged in the API: `speakers.KeywordIndexRefresh`, for the videos
    whose active transcript has the speaker's lines.
  - The plan. On a library it queues the videos whose document is missing, older than the
    stage's version, or built from another transcript, revision or title, and the documents
    of videos with no transcript left. A worker that serves the stage plans every library
    every half hour, so a title another writer changes is caught, and a version bump
    rebuilds every document.
- **The backfill** is that plan: one job a video, at priority 900 (behind all other work), on
  the Queues page as **Keyword index**. Until a video has its document, keyword search finds
  it by its lines.
- **The BM25 indexes build after the start, not in `migrate up`** (issue #1479):
  - on a large library a build outlasts any bound a start's migrations have, and a build cut
    short leaves an invalid index that each start would drop and build again, never
    finishing;
  - the home API's `api:search_index` loop builds what is missing after the start, with no
    statement or lock timeout, one API at a time (an advisory lock), and looks again every
    hour;
  - `searchindex.Ready` says when both indexes are valid; story 49.7 ranks by BM25 only then;
  - `maktaba-api search-indexes` builds them in the foreground, for an operator and for
    `pg18-check.sh`, which also checks that `migrate up` builds none.

  This replaces 49.5's build in `migrate up`.
- **Proof:**
  - `pipeline/tests/stages/test_keyword_index.py`, on SQLite and Postgres:
    - the edge and the Queues page label;
    - the document's lines, speakers and title, and SQLite's FTS5 row;
    - a title change, a transcript edit and a transcript that arrives some other way each
      queue the video;
    - a version bump queues every video at priority 900, one job each, and a video keeps
      its document until its job runs;
    - a deleted video or a lost transcript removes the document;
    - the worker's plan of each library;
    - Python's folding equals Postgres's.
  - The API's hooks: `TestTitleChangeQueuesKeywordIndex` and
    `TestRenameAndMergeQueueKeywordIndex`.
  - The large-library case, `TestLargeLibraryBuildsInTheBackground` (the replica job, on
    PostgreSQL 18), with 45,000 documents:
    - a build cut short after 2 s leaves an invalid index, and the loop drops it and builds
      both (43 s locally);
    - writes and reads go through during the build, and search is not Ready meanwhile;
    - a document written during the build is found by the index afterwards.
  - `TestBuildOnceOneAtATime`: a second builder builds nothing while the lock is held, and
    the session's timeouts are back afterwards.
- **How it differs from the spec.**
  - Priority 900, not 10: here a lower number runs first, and 900 is the backfill priority
    of the other planners (`word_check`, `content_screen`).
  - Pool `cpu`: there is no `index` pool. The meaning stage `index` is a plugin stage on the
    GPU's pools, and building a document is a few queries.
  - The plan, not a version-bump hook, re-queues done videos. The engine re-queues no done
    item on its own, and the document's `doc_version` lets one query find them.
  - One transcript a video: the first active one, as the other transcript stages read it.

### 49.7 The query: BM25 videos, phrase boost, passages, hybrid

- **Keyword search ranks videos by their documents** (`api/internal/handlers/search/bm25.go`)
  once both BM25 indexes are valid (`searchindex.Ready`, checked once a minute).
  - Two queries run in parallel, each `ORDER BY score LIMIT k`: the exact index (`search_text`,
    `maktaba_search`, the query through `maktaba_normalize`; its top 100) and the stemmed one
    (`stem_text`, `maktaba_stem`, through `maktaba_stem_prepare`; its top 50, Snowball costing
    more per row).
  - A video's score is twice its exact score plus its stemmed score, so the word as typed
    ranks first and its other forms follow.
  - Both queries join `videos`, so the library filter (a limited user's libraries), the
    language filter and the account's limits are part of each.
- **Phrase boost.** For a query of more than one word, the first 100 videos whose active
  transcript has a line saying the words together (`phraseto_tsquery` on the lines' GIN)
  each gain the list's best score. They rank above every video whose words are scattered,
  in BM25 order among themselves.
- **Passages and match counts** are read as the videos are ranked: normalised, then by the
  word's forms. A video found through a spelling variant (اسلام finding إسلام) shows its
  matching lines and a passage marking the word as written. `normalisedLines`:
  - reads the shown page's videos' lines once, and folds them in Go (`textnorm`, held equal
    to `maktaba_normalize`);
  - matches a line whose folded words hold every one of the query's exact lexemes;
  - asks Postgres's stemmer only about the lines that fail that and yet contain every
    stemmed lexeme;
  - marks the line's own words that matched.

  A page of 20 videos, some 6,000 lines, takes 12 to 28 ms locally. Working `maktaba_tsv`
  out line by line in SQL costs about 40 µs a line, so it is not used. A line with
  punctuation inside a word (Qur'an, well-known) or another form of the word goes to
  Postgres, which decides: at most 300 lines, within the budget's rest. A common word inside
  others (الله in والله) sends many, and past either bound the lines matched as typed stand. A query with websearch's operators (a quoted phrase, `-word`,
  `OR`) keeps the line path's passages. The query has a 60 ms budget. Past it, the page
  shows the line path's passages, and a video found only through its document shows 0
  matches there. A BM25 video's lines are counted for the page shown, not for the whole
  result (`savedVideo.L`).
- **Hybrid** fuses the BM25 videos with the meaning search's videos by weighted RRF (k=60,
  today's weights), as the line path's videos were.
- **Fallbacks.** Before the indexes are valid, on PostgreSQL 16 or on SQLite, keyword search
  ranks the matching lines as before. While story 49.6's backfill runs, the videos with an
  active transcript and no document yet are found by their lines and fused in. They sit
  half a place behind the BM25 video of the same rank.
- **Migration 0234** stores the normalised text as two generated columns, `search_text` and
  `stem_text`, and builds the indexes on them (`video_search_docs_bm25_text`,
  `video_search_docs_bm25_stem_text`). `maktaba_search_index_statements()` drops 0232's
  expression indexes once the new ones are there.
  - The reason: a BM25 `ORDER BY ... LIMIT k` works the score out again for each row it
    returns. Over the expression indexes, that normalised each document again, about 0.6 ms
    a row (285 ms for the top 500). Over a stored column it is 0.15 to 0.25 ms a row.
- **Proof:**
  - `TestBM25_Postgres` (PostgreSQL 18):
    - الله ranks the video that says it most first, and keeps the other library out for a
      limited user;
    - a sentence ranks the video saying it as a phrase above one whose words are scattered;
    - a rare word finds its videos;
    - word forms follow the word as typed;
    - matches are counted and passages are marked;
    - `TestBM25_VariantsHaveMatchesAndPassages`: اسلام, هدي, مدرسه, صبروا, cafe, Qur'an and
      comes each find the video saying إسلام, هدى, مدرسة, الصبر, café, Qur'an or coming, with at least one match and a
      passage marking the word as written, on the results and on the video's passages
      page;
    - hybrid keeps BM25's order where the meaning search agrees;
    - a video with no document yet is found by its lines.
  - `TestBM25_NotReadyUsesTheLines` covers the fallback.
  - `TestBM25_Perf45k` (the replica job) uses a 45,000-document library: about 400 words a
    document from a Zipf vocabulary of 17,576 words, with الله in nine in ten. Locally p95 is
    15 ms for الله, 15 ms for a sentence, 13 ms for a rare word, and 16 ms for الله
    filtered to one library. CI's runner takes about twice as long: with the stemmed list at
    100 too, it measured 53 ms.
- **How it differs from the spec.**
  - The indexes are on stored columns, not the documents' fields as an array: see migration
    0234 above.
  - The exact index gives its top 100 and the stemmed one its top 50, so keyword search shows
    up to 150 videos (the two lists' union), not the 500 of `rules.search.max_videos`. The meaning search's videos still fuse
    in for hybrid. (No longer: both give their top 500 since the scores come from the index
    scan, "The BM25 score from the index scan" below.)
  - There is no separate pre-filter by the `library_id` index for a user with few libraries.
    The filter is in the BM25 query, and pg_textsearch's filtered top-k (`filtered_seed`)
    serves it: the one-library case measured the same as the whole library.
  - SQLite keeps the line path. Its FTS5 table is written (story 49.6), and ranking by it is
    not built.


### 49.8 Suggestions, did-you-mean, facets, sort (API)

- **The terms dictionary** (migration 0235). `search_terms` has one row per library and word:
  - `term`: the word's key as search matches it (folded as `maktaba_normalize` folds, lower
    case, Latin accents removed);
  - `shown`: the spelling the transcripts use most (harakat and tatweel removed), so a
    suggestion shows مدرسة, not the key مدرسه;
  - `df`: how many of the library's documents hold the word.

  The `keyword_index` stage keeps it, in the transaction that writes or drops a document: the
  old document's words leave the counts and the new one's join them
  (`pipeline/src/maktaba_pipeline/search/terms.py`). `video_search_docs.terms_counted` says
  whether a document's words are counted, so a document written before the dictionary
  existed is only added. The stage's version is 2, so its plan rebuilds every document once
  and fills the dictionary. Go's `api/internal/searchterms` keys words the same way; both
  read `shared/testdata/searchterms.json` in a test.
- **The speakers dictionary**, `search_speakers` (the same migration), is kept the same way
  from each document's named speakers. It has one row per library, name and each word start
  of the name (الشيخ أحمد is found by الشي and by أحم), with how many documents name the
  speaker.
- **A library merged into another** loses its documents and its dictionary rows in the merge's
  transaction (`api/internal/handlers/libraries/merge.go`). The plan of the target rebuilds
  each moved video's document there and counts it in the target's dictionaries. The plan also
  rebuilds any document whose library is not its video's. A job that finds the old library
  gone takes nothing off it: its dictionaries went with it.
- **Suggestions as you type** (`GET /api/search/suggest`), at most 10:
  - the caller's own earlier searches that start with the query (at most 3);
  - the query with its last word completed from the dictionary, most documents first (at
    most 5, from 2 letters): الصبر عند ال → الصبر عند الله;
  - titles of the caller's videos that start with the query (at most 3, in order);
  - named speakers with a word of their name that starts with the query, named in the most
    documents first (at most 2).

  Each source is an index lookup that stops early:
  - the words are read most frequent first (`search_terms_df`), and the lookup stops after a
    few times the answers it needs (ال starts most Arabic words). For one library the order
    is exact; across several, the window grows with them, and a word's place can still be
    a little low;
  - titles are an index scan of `videos_shown_title_prefix` (the title a video shows, lower
    case) in its own order, stopped at the third;
  - speakers come from `search_speakers`, not from every video's speakers.

  The answer keeps `suggestions` (the texts, as before) and adds `items`, each with its
  `kind` (`history`, `term`, `title`, `speaker`) and a title's `video_id`.
- **Did-you-mean** (`did_you_mean` on `POST /api/search`) comes when a search finds fewer
  than 5 videos. Each word of 3 letters or more that the caller's libraries hold in no
  document is replaced by the closest word of the same script, by trigram similarity (at
  least 0.3), then by documents. A word some documents hold is replaced only by one that 10
  times as many hold: a transcript's own misspelling is a word too. The answer is the
  query with the words as the transcripts write them.
- **Facets** (`facets` on `POST /api/search`, with the first page) count the videos found
  (the titles and transcripts lists, at most 1,000) by:
  - library (with its name);
  - language;
  - named speaker (the top 20);
  - year (the date a video shows: published, else added);
  - duration band (`under_5m`, `5m_20m`, `20m_60m`, `over_60m`).

  Each year and duration bucket carries the filter that narrows the search to it. The
  request's filters now apply `speaker`, `duration_sec` and `date` to every keyword leg
  (BM25, the line path, titles). `lt`, an upper bound left out, is new beside `gte` and
  `lte`.
- **Sort** (`sort` on `POST /api/search`): `relevance` (the default), `newest` (the date a
  video shows) or `matches` (most matching lines first). The kept search keeps the order,
  so every page follows it. For `matches`, a BM25 video's lines are counted by the lines'
  full-text index (the words as written), as the line path counts them: at most 5,000
  matching lines in all (`ftsCandidates`), as the line path reads them. The page still
  shows its own count, by the word's spellings and forms.
- **Roles.** Each part reads only the caller's libraries: a limited user's own (or the
  request's narrower filter), every library for an account that sees all. A parental limit
  hides videos within a library, and the dictionary cannot tell which video a word comes
  from. So a restricted account gets no dictionary word: no completed word and no
  did-you-mean. It still gets its earlier searches, and the titles and speakers of the
  videos it may see. A speaker is one of the dictionary's first 20 names for the prefix
  that a video it may see names (`speakers_name_idx`, then the limit on that video). A failed facet or did-you-mean lookup answers the search without
  them.
- **Proof:**
  - `TestBM25_SearchHelp` (PostgreSQL 18, the replica job):
    - the facets' counts equal the videos found, and each year, duration and speaker
      bucket's filter narrows to its videos;
    - newest and most-matches order, kept across pages;
    - المدرصة gets المدرسة;
    - the last word completed, titles and speakers suggested;
    - another library's word is never suggested nor offered as did-you-mean to a limited
      user, and is to the admin.
  - `TestSearchHelp_SQLite` is the same on SQLite.
  - `TestSearchUnderParentalFilter_*`: a restricted account's suggestions hold no hidden
    title and no dictionary word.
  - The stage's tests (`test_keyword_index.py`, SQLite and Postgres) cover the dictionary's
    counts across an edit, a lost transcript, two libraries and a version-1 document.
  - `TestBM25_Perf45k` times suggestions on the 45,000-document library: 32,578 dictionary
    rows, a title on every video (all start with درس) and 1 to 3 named speakers each, from
    3,000 names that all start with الشيخ. Measured locally, against a 50 ms limit:
    - suggest takes p95 1.7 ms for ال (the commonest prefix), 0.6 ms for الص, 0.6 ms for
      درس (every title), 1.6 ms for الشي (every speaker), 1.3 ms for الشي as a restricted
      account, 0.5 ms for a sentence's last word and 1.4 ms for one library;
    - did-you-mean takes p95 2.2 ms.

    The first version scanned every title and speaker and summed every word with the
    prefix: 120 ms for ال and 165 ms for الشي. The review found it, and the fixture had no
    titles or speakers to show it.
  - `TestMergePostgres`: a merge drops the merged library's documents and dictionary rows,
    and the target's document stays. The stage's tests rebuild a video whose library was
    merged away into its new library.
- **How it differs from the spec.**
  - Did-you-mean's similarity is at least 0.3, not 0.4: one wrong letter in a five-letter
    Arabic word (مدرصه for مدرسه) scores 0.33.
  - pg_trgm does not read the key itself. The bundled server's database is
    `--locale=C`, where pg_trgm finds no trigram in an Arabic word. A generated column,
    `search_terms.trgm`, spells the key's 29 Arabic letters in ASCII, and its GIN index
    serves the lookup; a key is compared only with keys of the same script. Found on the
    way: `videos_title_trgm_idx` (migration 0098) has the same limit, so title similarity
    ranks no Arabic title (unchanged here).
  - SQLite has no pg_trgm. Did-you-mean compares the 2,000 most frequent words that share
    the word's first letter or its last two, in Go, by the same similarity.
  - Suggestions are completed words, titles and speakers, by prefix (`text_pattern_ops`
    btrees), not pg_trgm. A title is suggested by its start only, not by a word inside it.
  - A video deleted outright (its document cascading with it) leaves its words counted until
    the dictionary is rebuilt. A suggestion may then name a word that finds nothing. The
    stage's own deletions (a soft-deleted video, a lost transcript) take the words off.

### 49.15 Arabic titles rank by similarity

- **The cause.** The bundled server's database is `--locale=C`, where pg_trgm finds no trigram
  in an Arabic word. Search's title matches (`runTitles`) ordered by `similarity(v.title, q)`,
  0 for every Arabic title, so Arabic title matches came newest first.
- **The fix.** Similarity is now taken over the title and the query folded
  (`maktaba_normalize`, lower case) with their Arabic letters spelled in ASCII, as
  `search_terms.trgm` spells them (story 49.8). pg_trgm reads those in any locale. A Latin
  title's letters pass through the spelling unchanged, so Latin titles keep their order.
- **Proof:** `TestBM25_ArabicTitlesRankBySimilarity` (the replica job, whose servers are
  `--locale=C` like the bundled one). الصبر ranks درس الصبر above a long title that also says
  it, though the long one is newer, and patience does the same for Latin titles. Without the
  change the Arabic order was newest first.
- **Not changed.** The match itself is still `ILIKE '%q%'` on the title as written: a query
  with harakat, or a hamza the title spells otherwise, finds no title. For an Arabic query
  the trigram index (migration 0098) cannot narrow the match, so it reads the library's
  titles (keyword search's documents find these videos by their titles as well).

### The BM25 score from the index scan (found while building 49.9)

- **What was wrong.** Each row of a BM25 query was scored again from its whole text: about
  1.8 ms for a 5,000-word document, the home's average (23.1 M lines over 45,000 videos).
  - Measured on 5,000 such documents, a search took 180 ms, not 49.7's 15 ms. 49.7's 45,000
    test documents had 400 words, so its test could not see the cost.
  - The index scan itself returns the top 100 in 0.6 ms; Postgres then worked out each
    row's `ORDER BY` value again.
- **Why.** pg_textsearch's planner hook replaces that value with the score its scan already
  computed (`bm25_get_current_score()`), with two conditions:
  - the `ORDER BY` must be the operator alone, not a select-list column;
  - the hook finds its operators and that function by unqualified name, on the session's
    `search_path`, once per connection. The bundled server makes the extension in its own
    schema, `textsearch`, which no path held.
- **The fix.**
  - The bundle sets the app role's `search_path` to `"$user", public, textsearch`
    (`pgbundle.textsearchSQL`; `clusterRev` 6, so a home made before runs it at its next
    start). `textsearch` comes last, so a name of the app's still resolves in `public`.
  - Each leg selects `textsearch.bm25_get_current_score()` and orders by the operator
    (`bm25LegSQL`).
  - A plan without the BM25 index scan (a table, or a library's rows, few enough to read
    whole and sort) has no scan to take the score from. `bm25_get_current_score()` then
    gives the last score of the connection's last index scan: the extension never clears
    it. So each leg works out its first and last rows' scores from their text and compares
    them, within 1e-3: another search's score is another document's, far off. That is two
    documents, a few ms at most. When they differ, or the check fails, the leg asks again
    with the operator in the select list, and a log line says so (`search_bm25_rescored`). `TestBM25_ScoresAreEachRowsOwn` makes that plan on one connection after
    another search's index scan; without the check, a row carried that search's score.
- **Measured.**
  - 5,000 documents of 5,000 words (`TestBM25_LongDocuments`, the replica job): p95 8.9 ms
    for الله and 8.6 ms for a sentence, at 500 results (the check of the first and last rows
    included).
  - 45,000 documents (`TestBM25_Perf45k`): p95 under 2.5 ms, and 13.9 ms for one library.
  - The real bundled PostgreSQL 18 gives the app role that path
    (`TestRealPgTextsearchPreloaded`).
- **So the 500 results of the spec are back.** Both indexes give their top 500 (49.7 kept
  100 and 50 for the cost).


### 49.10 Web search page

The Search page (`web/src/pages/Search.tsx`) uses story 49.8's API; the parts are in
`web/src/pages/searchHelp.tsx` and the URL and request helpers in `searchShared.tsx`.

- **Suggestions as you type** replace the `<datalist>` of plain texts. The box is an ARIA 1.2
  combobox (`aria-expanded`, `aria-controls`, `aria-activedescendant`) over a listbox:
  - each suggestion shows its kind with an icon and a word: an earlier search, a word, a
    video's title, a speaker; a home before 49.8 (texts only) lists them without a kind;
  - the 200 ms debounce and the sequence guard stay: a slower, older answer is dropped;
  - up and down move through the list (wrapping), Enter chooses, Escape closes it and keeps
    the text; Enter with nothing highlighted submits what was typed; leaving the box closes
    the list until the next keystroke; the highlighted option is scrolled into sight;
  - a title opens its video (`/videos/<video_id>`); an earlier search, a word or a speaker is
    searched as its text;
  - a press on the list does not take focus from the box, so a click and a tap work; the list
    is placed with logical properties and mirrors in Arabic.
- **Did you mean** is a row above the results ("Did you mean المدرسة?", «هل تقصد المدرسة؟»)
  whose corrected words are a button: it runs that search with the same sort and filters. The
  row is left out when the answer has none or repeats the query.
- **Facets** are a panel of chip groups (Library, Language, Speaker, Year, Length), each chip
  a toggle button with its count (read as "N videos"):
  - a library shows its `label`, a language its name in the page's language
    (`Intl.DisplayNames`), a band its words ("Under 5 min", "5 to 20 min", "20 to 60 min",
    "Over an hour");
  - a press runs the search again with `filters`: `library_id`, `language` and `speaker` are
    lists (any of them); a year and a duration band send their bucket's own `filter` (`date`,
    `duration_sec`), and a second year or band replaces the first, as the request has one range
    of each;
  - filters of different facets combine; "Filters in use" lists each as "Facet: value" with a
    remove button, and "Clear all filters" takes them all off;
  - a bucket still chosen that the narrowed answer no longer lists stays, pressed and without
    a count, so it can be taken off where it was put on.
- **Sort** is a select (Relevance, Newest, Most matches) beside the List / Grid toggle. A
  change runs the search again; relevance is not sent. The select shows the answer's `sort`.
- **State.** The URL carries the order and filters beside `q`, `mode` and the pages:
  `sort`, `lib`, `lang`, `spk` (repeated for each), `year` and `dur`. A search is shareable,
  and each refinement is a history entry, so Back returns to the search before it, by its sid,
  with no new request. A year and a band in a link become the ranges the server gives those
  buckets (UTC years, the bands' seconds). New words keep the order and drop the filters
  (they were the old results' counts); the same words, did-you-mean and a recent search keep
  both.
- **Facets while paging.** The facets and did-you-mean come with the first page only; the
  page keeps them with the saved search on screen while either list pages, and keeps them per
  sid in the tab's session storage (`mkt:search:help`), so a reload or Back by sid shows them
  again. The matches page's back link and its re-run of an expired search carry the order and
  filters too.
- **Proof:** `Search.help.test.tsx` (SRC-44 to SRC-53 in `web/test-cases/search.md`):
  - suggestion kinds, the combobox's keyboard and ARIA, a title opening its video, a pointer
    choice;
  - did-you-mean shown and run with the filters kept;
  - each facet's request body, combined facets, removal, Clear all, Back by sid;
  - a deep link's filters, the sort request and the answer's sort shown;
  - facets kept across pages and a reload;
  - Arabic, and axe in English and Arabic with the list open, did-you-mean and facets shown.

  `SearchVideo.test.tsx` covers the matches page's back link and re-run.
- **Not done here.**
  - A recent search shows its own order and filters only when this browser ran it: the recent
    searches list carries no sort or filters. A saved search keeps neither: it runs with the
    order on screen and no filters.
  - Facet buckets are chips, not a side panel of checkboxes as in the 11.4 mockup: they wrap
    on a phone and need no second column.

### 49.11 Apple and Android apps, TV, big screen

Each client has its own part; the parts not listed here are not built yet.

#### Apple: the Player (iPhone, iPad, Mac) and the Apple TV

Both apps use one model in MaktabaKit: `SearchModel` (`SearchPage.swift`) and the search help's
types and words (`SearchHelp.swift`). The behaviour and the words follow the web's page (49.10).

- **What the apps read** (`SearchHelp.swift`, `Browse.swift`):
  - a suggestion's `kind` and a title's `video_id` from `items`, else the plain `suggestions`
    of a home before 49.8. A kind the app does not know reads as none; a title without its
    video is left out; the same text and kind come once;
  - `sort`, `did_you_mean` and `facets` on `POST /api/search`, each read tolerantly: a field
    of the wrong shape reads as absent, a bucket without a value is left out, a year sent as
    a number is read as its text. A home before 49.8 sends none of them, and the apps search
    as they did. A did-you-mean that only repeats the query is not shown.
- **What the apps send.** `sort` when it is not relevance, and `filters` when any is chosen:
  - `library_id`, `language` and `speaker` as lists (any of them);
  - a year and a duration band as their bucket's own `filter` (`date`, `duration_sec`). When
    the answer does not list the bucket, the range the home gives it is sent (UTC years, the
    bands' seconds). A second year or band replaces the first.
- **What a search keeps** (`SearchModel.Ask`):
  - new words keep the order and drop the filters;
  - the same words keep both;
  - the did-you-mean keeps both;
  - a refinement (an order, a bucket, a filter removed, Clear all) is the search on screen
    with that one change;
  - Try again, and a kept search that expired while paging, ask again with the order and
    filters.

  A chosen bucket the narrowed answer no longer lists stays, chosen and without a count, so it
  can be taken off where it was put on; a library keeps the name seen before.
- **The Player** (`Player/Search/SearchScreen.swift`, iPhone, iPad and Mac):
  - each suggestion shows an icon and its kind (Recent, Word, Video, Speaker); a title opens
    its video, any other is searched as its words. A suggestion's tap also submits the field
    (what was typed), so the submit that follows a title is not run as a search;
  - "Did you mean …?" is a row over the results, its corrected words marked where each
    language puts them; a tap runs them and the field shows them;
  - **Filters** (with how many are in use) opens a sheet, Narrow the results, with each
    facet's buckets and their counts. A press searches again at once, and the sheet stays
    open on the new counts. A library shows its name, a language its name in the app's
    language, a year in the app's digits, a band its words ("Under 5 min", "5 to 20 min",
    "20 to 60 min", "Over an hour");
  - the filters in use are chips under the bar ("Language: Arabic"), each of which takes
    itself off, then Clear all filters;
  - the sort is a menu (Relevance, Newest, Most matches);
  - every refinement waits for the field to let go of the keyboard first, as a search and a
    result's page do (`LetGoFirst`);
  - a search that found nothing still shows its did-you-mean and the filters in use, so a
    filter can be taken off there.
- **The Apple TV** (`apps/tv/tvos/MaktabaTV/Views/SearchView.swift`) searched titles with
  `GET /api/videos?q=` as you typed. It now uses the home's search, with the same help:
  - the suggestions show under the keyboard with their kind, and a title opens its page;
  - what is typed runs from **Search for “…”**, the first button under the keyboard (or the
    keyboard's own search key, where tvOS gives one). The home keeps every search as a
    recent one, so the TV no longer searches each word as it is typed;
  - over the results, did-you-mean, the sort menu and **Filters**, in rows the focus moves
    along with the remote (`focusSection`). The filters' sheet has a row of buckets per facet,
    and Menu or Done closes it;
  - Titles and In the transcript are cards that open the video's page, with Show more.
- **Words.** The help's words are MaktabaKit's (`SearchWords`, its catalog), shared by both
  apps, in English and Arabic as the web words them. The suggestion kinds are keyed
  (`search.kind.<kind>`): "Speaker" is «متحدث» as a kind and «المتحدث» as a facet.
  Interpolated words carry their bidi isolates, so an English word in an Arabic sentence
  keeps its place.
- **Proof:**
  - `SearchHelpTests.swift` (MaktabaKit):
    - the suggestion kinds, and an older home's plain texts;
    - the answer's new fields, and their absence from an older home;
    - odd shapes read tolerantly;
    - the request bodies of facet choices, with and without the bucket's own filter;
    - the client's `sort` and `filters`;
    - the view model's asks: combined facets, removal, Clear all, new words, did-you-mean,
      Try again;
    - a chosen bucket the answer no longer lists;
    - the words in English and Arabic.
  - `PlayerSearchUITests` (iPhone and iPad, against `MockHome`):
    - the suggestion kinds in English and Arabic, and a title opening its video;
    - the sheet's counts, three filters combined, the chips, one taken off, then Clear all;
    - the sort, and new words dropping the filters but keeping the order;
    - the did-you-mean in Arabic.

    The accessibility audit and Dynamic Type check run on the filters in use and on the
    did-you-mean.
  - `TVSearchUITests` (Apple TV, with the Siri remote, in the TV UI tests job):
    - the suggestion kinds, and a title opening its page;
    - Search for, two filters combined from the sheet, a chip taken off, the sort;
    - the did-you-mean in Arabic.
- **Not done here.**
  - The TV's transcript results open the video's page, not the player at a passage: the
    TV's video page has no start time to open at.
  - A recent search runs as new words: the recent list carries no order or filters, so
    the filters on screen are not applied to another query.
  - The Android Player, Android TV, the desktop player, the web big screen (`apps/tv/web`)
    and Roku are the other parts of 49.11.
