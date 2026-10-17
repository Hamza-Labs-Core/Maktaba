# Handover: library intelligence, Plex/dizqueTV extensions and the TV channel grid

Status as of 2026-10-17 (updated after #48 merged). Read this first, then `specs/epics/06-job-queue/plan-06-11-queue-graph.md`.

## 1. What this work is

The goal is to turn a NAS of YouTube archives (64 Plex libraries, about 148k files) into properly organised shows, with Plex collections and linear TV channels.

Two codebases share one set of rules:

- **mediaintel**, under `pipeline/scripts/mediaintel/`. A standalone tool on a SQLite cache, used to run the real library tonight. Its CLI is `python -m scripts.mediaintel --db <cache.db> <cmd>`, and it has a control centre (`serve`, port 8770).
- **Maktaba**: the same logic as queue stages in the job engine (`engine/`, `extensions/plex`, `extensions/dizquetv`), with the Go API and the web control centre.

The shared rules live in `maktaba_pipeline`:

| Area | Module |
|---|---|
| Series, season and talk-show detection | `classify/series_detector.py` |
| Topic tags | `classify/topic_tags.py` |
| Clip groups and promo rules | `classify/clip_groups.py` |
| Channel planner | `schedule/` (one entry point: `schedule/entry.py::plan_catalog`) |
| The rules as settings | `settings.py` (configs ↔ JSON, checked), `rules.py` (sections, schema, reader), `classify/settings_help.py`, `schedule/settings_help.py` |

## 2. Branches and PRs, and the order to merge them

| PR | Branch → base | What it carries |
|---|---|---|
| #46 | `feat/series-intel` → `main` | mediaintel: scan, YouTube, link, detect, fingerprints, tags, the Plex/dizqueTV writes, the control centre, the plex-index step, the thumbnail cache, review pages run in parallel |
| #48 (**merged into #46**, 427c132) | `feat/clip-groups` → `feat/series-intel` | talk shows by title brand, numbered shows without playlists, ordinal and yearly seasons, topic collections (ملف), clip and stand-alone groups (filler), promo links, the `maktaba-auto` label, and 100% collection coverage |
| #50 (merged) | `feat/tv-grid` → `feat/clip-groups` | channel planner redesign (planner v2): show slots, cadence, continuity across years, fillers, Quran non-stop channels. Merged into `feat/clip-groups` (ac252c7). |
| #47 (draft) | `feat/queue-engine` → `feat/series-intel` | the Maktaba queue engine and every stage as a queue (plex_index, thumb_small, review_pages, topic_collections, promo_links, clip_groups, plex_groups, plan_channels running the TV grid); Go API; web control centre |

#48 is merged into #46, and #46's CI is green (3998c13). Next: #46 → main, then #47 onto main. `feat/queue-engine` contains #46 with #48 and #50 (merge cc15b57, 2026-10-17).

Worktrees on the dev PC:

| Path | Branch |
|---|---|
| `E:\tv\Maktaba` | series-intel (the live mediaintel install) |
| `E:\tv\Maktaba-clips` | clip-groups |
| `E:\tv\Maktaba-grid` | tv-grid (merged as #50; safe to remove once nothing is open there) |
| `E:\tv\Maktaba-queue` | queue-engine |
| `E:\tv\Maktaba-apply` | detached checkout of clip-groups, used for the Media Masters write; safe to remove |
| `E:\tv\Maktaba-qa`, `-qb`, `-qc` | old workstream branches, already merged; safe to remove |

## 3. Decisions the user made (do not re-litigate)

1. **Tools only.** Every library action goes through the tools (mediaintel or Maktaba stages), never through manual one-offs.
2. **Writes are automatic.** Plex, dizqueTV and channel writes happen without a review step, and every write logs its diff. A review/approval system is deferred; it will be a Maktaba feature covering Maktaba's own changes too.
3. **Plex and dizqueTV are Maktaba extensions**, not core.
4. **Transcription and the audio chain run on a Mac worker.** Workers are multi-host, and each declares its host, the stages it runs and its path map.
5. **Collection classes:**
   - Seasons are exclusive: one episode, one season.
   - Topic collections (ملف) may overlap seasons.
   - Clip and stand-alone collections exist for filler.
   - Promos are linked to their show.
   - Every long video ends up in some collection.
6. **The TV channel model:**
   - Limited series and ongoing programmes are different show types. An ongoing programme (weekly talk show, daily lessons) is ONE show; years exist only for browsing.
   - A show owns a slot, and its slot continues across years. When a series ends, the next show in the slot's queue takes over.
   - Each channel's week mixes shows like a real TV channel.
   - Fillers pad every slot.
   - Quran channels play Quran non-stop, rotating reciters per day or by time of day.
7. **Production style (talk/produced/cinematic) is optional.** The user did not ask for it as a grouping, so it is never a primary rule.
8. **Job-made collections carry the Plex label `maktaba-auto`.** Plex displays it as "Maktaba-auto".
9. **Existing collections are protected.** An episode leaves one only if it is placed in another season, is a short, matches the promo/teaser title rule, or the collection is branded and the episode's title lacks the show name. A copy (mkv beside an mp4) is swapped for its kept twin.
10. **No hard-coded rules.** Every threshold, word list, schedule rule and policy is a setting with a UI, its default the current behaviour (detect / groups / planner sections). When a reviewer disputes an intended behaviour, make it a setting rather than change it (e.g. `quran_fallback_any_recitation`).

## 4. Running mediaintel (the live install)

The cache is `E:/tv/mediaintel/mediaintel.db`. Next to it are `mediaintel.json` (path map), `thumbs/` (thumbnail cache), `reports/` (review pages, channel plan) and `backups/`.

A full run against Plex, with the Plex and dizqueTV writes:

```
cd E:/tv/Maktaba/pipeline
PYTHONIOENCODING=utf-8 uv run --extra mediaintel python -m scripts.mediaintel --db E:/tv/mediaintel/mediaintel.db \
  run --from-plex --exclude-library Movies --dizquetv-db E:/tv/dizquetv/.dizquetv \
  --out E:/tv/mediaintel/reports --plex --tv --backup-dir E:/tv/mediaintel/backups [--from-step STEP] [--no-youtube]
```

Steps run in this order: scan, plex, youtube, link, detect, signatures, exact, tag, group (PR #48), export-plex, dizquetv-shows, report (in parallel), scan-visual, plan-channels, export-dizquetv.

Control centre:

```
… serve --log E:/tv/mediaintel/run-plex.log --port 8770 --dizquetv-db E:/tv/dizquetv/.dizquetv --reports E:/tv/mediaintel/reports
```

Restart it after changing serve.py or the html files. Its **Settings** page (`/settings`) edits the rules; they are saved in `E:/tv/mediaintel/settings.json` (only what was changed) and every step reads its section. **Preview channels** there plans into `reports/preview/` without writing anything.

`run --skip STEP` leaves a step out (e.g. `--skip scan-visual` while the NAS is slow).

Never print or commit the Plex token. It lives in `E:\tv\dizquetv\.dizquetv\plex-servers.json`.

## 5. State of the real library

**2026-10-17, 01:00:** the live install (`E:\tv\Maktaba`, detached at 2fffb4d) is re-running from export-plex (`run … --from-step export-plex --skip scan-visual --no-youtube --plex --tv`): the PR #48 collections (talk shows, topics, clip and stand-alone groups, promos) are being written to Plex; then dizquetv-shows, review pages, plan-channels (TV grid) and export-dizquetv (the channels). Log: `E:/tv/mediaintel/run-plex.log`. Plex refused nothing so far; 49 old junk collections (e.g. أحمد منصور's) were retired after their items were confirmed in the new ones.

Earlier (2026-10-16):

- **Plex:** the export-plex run of 2026-10-16 03:24 wrote the season collections for 57 libraries: 2,214 new, 128 updated and 426 unchanged, with 982 mkv copies swapped for their mp4.
  - **Media Masters** was written with the PR #48 code and verified: 6 collections, all labelled.
  - **Not yet in Plex:** the other PR #48 results (talk shows, topics, clips, promos), because #48 hasn't been merged and run with `--plex`.
- **dizqueTV:** the custom shows per series and season were updated on 2026-10-16. The channels have not been created with any planner. The only planner object is the Oct-14 trial, `maktaba-plan · Mix · unknown 6` on channel 100, which the next export replaces.
- **The 2026-10-16 run** was stopped during scan-visual, on purpose, before export-dizquetv wrote channels with the old planner.
  - Review pages stopped at تامر اللبان after an NAS timeout (now handled).
  - Done 2026-10-17: the install was moved to the #48 code and the run restarted from `detect` (not `report`, so the new detection and the group step run), then from export-plex after the Plex fixes.
  - scan-visual can be skipped; it is optional.
- **Libraries that still need their YouTube channel read:** القناة الرسمية للشيخ فيصل بن قزار الجاسم, مركز رواسخ, Alshakhanah Alshangy. In the cache, Alshangy is wrongly mapped to the "Alsh5anah weekly" channel; take the correct channel from its videos' saved details.
- **Upload dates:** about 2,700 dates for أحمد منصور (and many on other channels) are approximate, "N years ago" turned into YYYY-10-15. `--exact` fixes series members.

## 6. Known issues

- **The NAS stalls.** SMB timeouts appear as Microsoft-Windows-SMBClient/Connectivity events 30809 and 30807, with 25 s directory listings. They happen even with nothing of ours running. Keep NAS reads at 2 at a time.
- **Plex runs its own maintenance.** Plex's Butler tasks ("Generating loudness data") slow Plex badly. Plex calls retry 5 times, waiting 0/10/30/60/120 s.
- **One library, two Plex sections.** إياد قنيبي is two Plex libraries of one name (sections 14, 15). mediaintel and the Maktaba `plex_groups` stage now write each collection in the section holding its items (fixed 2026-10-17).
- **Plex apps cache library views.** After a big collection write, restart the Plex app to see the changes.
- **Windows-only Go test failures,** all pre-existing: serverkeys, the graphql schema test, libraries `canonicalPath`, and `system` `TestDiskFreeBytes_RealPath`.
- **Repeated real-Postgres runs.** The real-Postgres Go test fails when run twice on the same database; use a fresh one.
- **Flaky prune test.** `tests/engine/test_ops.py` has a clock-based prune test that occasionally fails.
- **Un-runnable commit.** In #47, commit 2a825eb doesn't run on its own because `plan_catalog` arrives in 086add4. The branch head is fine.
- **Review pages.** In Maktaba they are files in the app cache; no API route serves them yet.
- **No LLM.** Maktaba has none (only Whisper STT and e5 embeddings). An optional LLM helper for naming, classification and promo linking was proposed and is awaiting the user's answer.

## 7. What is left

1. ~~Merge tv-grid into clip-groups, and clip-groups into queue-engine; plug the new planner into `plan_catalog`.~~ **Done:** #50 merged into clip-groups (ac252c7) and into queue-engine (52ba0c7). `plan_catalog` runs the TV grid. Planner version 2 is seeded by slot 0095 (b9b72b7, 0e127e7). The dizqueTV export writes each slot queue, and each Quran reciter lane, as one `maktaba-plan · <slot>` show in queue order.
2. ~~Run the full tests on the merged queue-engine.~~ **Done** (2026-10-16), all green:
   - Python: 1,735 passed, 6 skipped, on PG 16.4 and SQLite.
   - Go: queue handlers and the API root pass, and the real-Postgres queue test passes.
   - Web: 221 passed.
   - migration-lint (170 files), ruff and mypy: clean.
3. Real-library rollout with mediaintel:
   1. Read the 3 missing YouTube channels.
   2. Run with `--from-step detect` (or the full run).
   3. Write Plex one library first, check it, then all.
   4. dizqueTV channels with the new planner: preview first.
4. Make PRs #47 and #48 ready for review once CI is green, and review them. #50 is merged into #48.
5. ~~Rules as settings in Maktaba.~~ **Done** (2026-10-17): stages read `app_settings` `rules.<section>` (`StageContext.rules()`, `rules.read()`), a changed rule changes the stage fingerprints; Go `GET /api/rules`, `PATCH /api/rules/{section}` (admin) validating against `api/internal/handlers/rules/schema.json` (regenerate with `python -m maktaba_pipeline.rules --schema api/internal/handlers/rules/schema.json`; a test checks it is current); web Settings → Rules. Plex: refused items stay out, per-section collections, only out-of-place items are moved.
6. Maktaba one-library trial on this PC (e.g. Nabil): `import_mediaintel`, queues with the Plex extension, compare against mediaintel's collections, then switch over library by library.
   The import (2026-10-17) also brings `settings.json` into `app_settings` `rules.<section>` (each value checked with `rules.config`; a value already set in Maktaba is kept) and copies `thumbs/` into the thumbnail cache with a done `thumb_small` job per video (`--settings` / `--thumbs`, default: beside the cache). `plex_groups` adopts a collection with the planned title **and** the `maktaba-auto` label that has no link yet (the topic / clip / stand-alone collections mediaintel wrote); one without the label is still left alone.
7. Later: the review/approval system in Maktaba, serving review pages through the API, the optional LLM helper, and running the Mac worker for transcription.

## 8. The TV grid (planner v2, PR #50)

**Shows.** The planner (`schedule/channel_planner.py`, reached only through `schedule/entry.py::plan_catalog`) folds seasons into shows, one per folder + series key. Each show has a type, a cadence, a slot length and a strength.

- **Type:**
  - `limited`: a series with an end.
  - `ongoing`: kind `ongoing`, a title-brand talk show, or two or more year seasons. Its years or guests are one run.
  - `collection`: stand-alone videos grouped by topic.
- **Cadence:** `weekly`, or `strip` (every weekday at the same time). It comes from the show's exact air dates when there are at least 5 gaps (median gap ≤ 2.5 days → strip). Otherwise format defaults decide: lessons, Q&A and drama → strip; programmes, interviews and talk shows → weekly; limited series of up to 60 episodes → strip.
- **Slot length:** 30, 60, 90 or 120 min, from the median episode length.
- **Strength:** format, prime topics, size and an ongoing run. Production style is only a 0.1 tie-breaker.

**Slots own queues.** Every slot is owned by a show and keeps a queue: a `Lane` keyed by its slot (`mix.wed.2000`, `mix.strip.1700`), stable across re-plans.

- An ongoing show plays all its years back to back in its slot. When a series ends, the next show in the queue takes the slot. Shows never interleave.
- **Patterns:**
  - `strip`: morning, afternoon and evening. One strip show per block on Sun–Thu; Fri and Sat get weekly shows.
  - `weekly`: prime and late night, a show per day.
  - `strip7`: the Ramadan grid after iftar.
  - `quran`: dawn.
- **Fill order and variety:**
  - Strips fill first, then weekly slots with prime time first, then weekends.
  - Cadence is a ceiling: a weekly show never takes a strip.
  - Adjacent slots vary the genre.
- **Fixed slots:** Friday slots go around the khutbah (12:00–13:30).
- **Small channels:** a channel with too few shows gives queues extra airings, with a warning; set `extra_airings=False` to leave the time as filler instead.

**Fillers.** A slot pads from a chain:

1. its own shows' promos;
2. clips on its topics (from the clip groups);
3. the channel's on-topic filler;
4. general filler.

Long blocks also get filler between episodes, and no filler from the last 8 picks repeats. Time nothing fits is dead air, and the plan reports it. dizqueTV only fills per channel, so its filler lists are the topic pool (weight 300), promos (150) and general (100).

**The Quran channel** (`kind="quran"`) plays recitation non-stop:

- One lane per reciter and style (مرتل حفص, مجود, ورش), taken from the reciters' own libraries and named after the reciter, keeping surah order.
- A reciter per day (`by_day`, the default) or every 4 hours (`by_time`). Time rules give مجود 04:00–07:00 and ورش 21:00–22:00.
- A reciter resumes where he stopped, so the khatma continues across days.
- Filler is only short recitation and dua clips.

**In Maktaba:**

- `plan_channels` passes brands, season names, exact dates, clip groups and promo links to the planner.
- The plan JSON carries each lane's `pattern`, `cadence`, `slot_minutes` and `shows`.
- `dizquetv_channels` writes each queue a slot plays as one `maktaba-plan · <slot name> [key]` show in queue order, so a dizqueTV `order: next` slot runs on through the years. Only `maktaba-plan · ` objects are touched; a same-named show the user made is a reported conflict.
- Details are in plan 06-11 §17.7.

**Dry run** (copy of the live cache, 2026-10-16; nothing written):

- **Scale:** 48 channels: 13 themes, منوعات, 33 presenter channels and the Quran channel. Planning takes about 2½ min for 2,301 seasons.
- **Fill:** programmes take 77–98% of the week, and no slot has more than 3 min of dead air.
- **Files:** the review is `E:/tv/mediaintel/reports/tv-grid-dryrun.txt`, the plan page `E:/tv/mediaintel/reports/tv-grid-plan.html`.

**Open questions (from PR #50):**

1. **Small channels repeat.** 37 of 48 channels need extra airings, and 18 moved weekly shows to strips; most are presenter channels with 10–40 shows. Options: a shorter broadcast day (more filler, or the Quran channel at night), a higher `presenter_min_hours`, or folding small presenters into the themed channels.
2. **Prime-time strength.** Tafsir and lesson series often win 20:00. Should prime prefer `program` / `interview` formats more strongly?
3. **Cadence comes mostly from format defaults**, because almost no exact air dates exist yet. Running `scan-youtube --exact` for series members would give the real rhythm.
4. **Reciter libraries** use a 60% recitation-hours threshold. ياسر سلامة (≈ 51–65%) and د. أيمن سويد (79%, a tajweed teacher) are borderline: confirm them, or list reciters in config.
5. **Quran continuity** is "resume where he stopped". Restarting a khatma each day is not implemented.
6. **dizqueTV fills per channel, not per slot.** The promo-first chain is exact only in Maktaba's schedule mode and the simulation.
7. **Presenter channels** don't exclude current-affairs shows (this predates PR #50).
8. **Ramadan queues are separate:** during Ramadan a show restarts at its own position in the Ramadan grid rather than continuing its regular slot.
9. **Plan size:** the plan JSON lists every episode per queue, so the HTML is about 15 MB for the whole cache.

