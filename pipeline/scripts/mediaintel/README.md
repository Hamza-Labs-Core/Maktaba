# mediaintel — organise a YouTube-archive library into series and seasons

Tools for libraries built with yt-dlp (lecture and program channels): gather
everything the files, their sidecars, Plex and YouTube know about each video,
detect **series → seasons → ordered episodes**, review the result visually,
and write one Plex collection per season.

> **Superseded by the queue graph (plan 06-11).** Inside Maktaba every step below is a stage of the
> job queue (`maktaba_pipeline.engine`): `thumb_hash`, `link`, `fingerprint`, `style_sample`,
> `youtube_sync`, `detect`, `fp_gate`, `tag`, `rate_style`, `plan_channels`, `write_channels`, with
> Plex and dizqueTV as extensions (`maktaba_pipeline.extensions`). Re-queueing a library re-adds it
> to the right queue and only changed work runs. `run` here (`pipeline.py`) stays for stand-alone
> use until the queue has replaced it; `explain` stays. To bring an existing cache into Maktaba
> without recomputing anything: `python -m maktaba_pipeline.tools.import_mediaintel --help`
> (a dry run unless `--apply`).

The detection core is the Story 26.3 implementation
(`maktaba_pipeline.classify.series_detector`), written as a pure module
so the pipeline's `classify` stage can call it once Maktaba ingests these
libraries itself.

```
uv sync --extra mediaintel          # yt-dlp is an optional extra
uv run --extra mediaintel python -m scripts.mediaintel --db <cache.db> <command>
```

## One command

```
# point at folders …
mediaintel --db cache.db run "Y:/الشيخ نبيل العوضي" "Y:/another channel"
# … or at Plex: every library whose folder the path map reaches
mediaintel --db cache.db run --from-plex --exclude-library Movies --dizquetv-db …/.dizquetv
```

`run` does steps 1–6 below for those folders (plus tagging / visual style once
those modules are present) and writes the review report. `--exact` also fetches
exact upload dates for series members (slow, polite); `--no-youtube` uses local
and Plex evidence only. Nothing is written to Plex: `export-plex` stays a
separate, reviewed step (`--diff` lists every episode a change adds or removes).

## Control centre

`serve` is the control centre: progress, the log, settings, and **Start a run** (all or some
libraries, what to write, the step to start at and steps to leave out). To keep it running on its
own (it starts again after a crash), on Windows:

```
set MEDIAINTEL_DB=E:	v\mediaintel\mediaintel.db
set MEDIAINTEL_DIZQUETV=E:	v\dizquetv\.dizquetv
scripts\mediaintel\control-centre.cmd
```

It logs runs to `run-plex.log` beside the database and reports to `reports/` there.

`explain TEXT` answers "why is this video where it is": title, air date and its
quality, length, shape, playlists and positions, YouTube link method, Plex item,
and the verdict (episode N of a season, promo/recap extra, second upload, short,
or unassigned).

Plex reports server-side paths; the path map rewrites them to the local view of
the same share. Put it in `mediaintel.json` next to the cache
(`{"path_map": [["/volume1/share/Youtube/", "Y:/"], ...]}`; list every view Plex uses,
e.g. a Docker container's mount as well as the NAS path) or pass
`--path-map PLEX_PREFIX=LOCAL_PREFIX`.

## Workflow

| Step | Command | Uses | Writes |
|---|---|---|---|
| 1 | `scan-local Y:/channel …` | ffprobe (duration, size), thumbnails → dHash (ffmpeg), `.info.json` (video and playlist), `ytdl-archive.txt` | `files`, `folders`, `yt_videos`, `yt_playlists` |
| 2 | `scan-plex --dizquetv-db …\.dizquetv` | Plex library items, mapped to local paths (`--path-map PLEX=LOCAL`) | `files.plex_*` |
| 3 | `scan-youtube [--exact]` | yt-dlp: channel tabs and **every playlist with positions** (titles requested in `--lang ar`, since YouTube auto-translates them); approximate dates from the English listing; `--exact` fetches exact upload dates per video | `yt_*`, `yt_channels` |
| 4 | `link` | file ↔ YouTube video: exact yt-dlp filename → folded title → title+duration → fuzzy (renamed `@handle`) | `files.youtube_id` |
| 5 | `detect [--min-episode-minutes 3]` | the detector (below) | `series`, `series_episodes`, `shorts` |
| 6 | `report --out reports/` | HTML per channel: a thumbnail grid per season, extras, unassigned | `reports/*.html` |
| 7 | `export-plex [--apply]` | one manual collection per season, in episode order | Plex |

`status` prints per-folder coverage (durations, thumbnails, links, exact and approximate dates, Plex).

Every step is cached and re-runnable. Channels that no longer exist on YouTube
still work from local evidence (steps 1, 2, 5–7); they just have fewer dates.

### YouTube politeness

`scan-youtube` listing calls are cheap (one per tab/playlist). `--exact` costs
one request per video, so it runs sequentially with a randomised `--delay`
(default 3 s) and stops at the first "confirm you're not a bot" response
(re-run later to resume). `--exact-scope series` (default) only fetches videos
that are in a playlist or a detected series. `--cookies-from-browser firefox`
uses a signed-in session.

## Detection

Seasons are seeded from real playlists (`PL…`/`OL…`; channel tabs are not
series) and grown with signals, so episodes missing from playlists still land in
the right season:

- **Shorts are never episodes.** That covers the Shorts tab, `#shorts`, vertical
  video, or anything under `--min-episode-minutes` (promos, next-episode
  trailers, highlight clips). They are counted, not listed.
- **Key and season from playlist titles.** Presenter and generic words are dropped;
  `الموسم الثاني`, `قصة وآية 3` and `مشاهد4` give the season, and `رمضان 2024`
  gives the year.
- **Same name ≠ same show.** Seeds join one series only when their core duration
  bands agree. A short-form playlist airing *during* a long-form season of the
  same name is that season's promos.
- **Attaching unlisted videos** needs title evidence (the series key, the season's
  distinctive title parts, a near-twin of a member title, or the same branded
  thumbnail), a matching duration band and a matching air-date window. A
  wrong year or wrong length is a veto. A video plausible in two different
  series is left unassigned for review.
- **Order:** playlist position (flipped when it runs against exact dates), attached
  episodes slotted beside their numbered siblings (`الجزء الخامس` after
  `الرابع`, Arabic ordinals included), then air date.
- **One copy per episode.** A TV-run upload and a later re-upload of the same
  episode (same story title once the series name and markers are removed, same
  length, same or no episode number) keep one copy; the other is listed as a
  duplicate.
- **Late episodes.** A numbered episode uploaded shortly after its season
  (within the season's own span, at least 90 days) still joins it; one aired after
  the series' last season ends opens the **next season**. A recap
  (`ومضات`, `ملخص`) without an episode number is an extra.
- **Clip playlists** (`مقاطع مميزة من …`) become `clips` series, which are not
  exported unless `--include-clips`.

Pure modules (unit-tested in `tests/classify/`):

| Module | Role |
|---|---|
| `text/arabic.py` | folding (harakat, tatweel, hamza forms, ة/ه, ى/ي, digits, `#hashtags`) and yt-dlp filename sanitising (verified against yt-dlp) |
| `discovery/ytdlp_sidecars.py` | `.info.json` / `ytdl-archive.txt` parsers |
| `classify/title_template.py` | season/episode/year markers (Arabic ordinals up to 49), teaser detection, title segments |
| `classify/series_detector.py` | the detection described above |
| `thumbnail/dhash.py` | 64-bit dHash from ffmpeg's 9×8 grayscale output (no imaging dependency) |

## Signatures: intro, end song, frames

For every episode-length video `scan-local` stores a **chromaprint** of the opening
and closing 90 s (theme song, intro, end nasheed) and dHashes of frames at 15 s,
45 s, 25 %, 50 % and 75 % (title card, set, presenter). Episodes of one season
measured 0.81–0.91 audio similarity, different series 0.53–0.54. The detector uses
a match as *evidence only*: uploads often cut the intro or end song, so a missing
match never counts against a video. Shorts (< 3 min) get no signatures.

## Indexing jobs (resume, never repeat)

Every per-file task is a job in `index_jobs` (`probe`, `thumbnail`, `signature`,
`visual_style`), shaped like Maktaba's `processing_jobs` (stage, state, attempts,
max_attempts, error; path ≈ video_id). A job is done for one file size/mtime and
stage version and is committed as soon as it finishes; failures keep their error
and stop after 3 attempts. A folder whose mtime has not changed since its last
complete scan is skipped outright. Lengths and sizes Plex already knows count as
done probes. `status --jobs [--folder X]` shows done / failed / gave up per stage.

## Production style

`scan-visual` samples a few 30 s windows of 3 episodes per season (ffmpeg, centre crop,
scene cuts, motion, light, colour, "inserts") and rates the season **static** (audio
over a still, tier 0), **talk** (studio/lecture, 1), **produced** (multi-camera,
location, b-roll, staged sets, 2) or **cinematic** (AI imagery / dramatisation, 3).
Borderline seasons (low confidence, or "talk" with a dark low-key look such as an AI
virtual set) are re-sampled with 8 episodes. `set-style LIBRARY SERIES STYLE` pins a
style by hand (`--clear` removes it); the channel planner groups and schedules by tier.

## dizqueTV custom shows

`dizquetv-shows [--library X] [--apply]` keeps dizqueTV's custom shows in step with
the seasons: an existing show named `<library> - <series>` gets every season back to
back (S1 then S2 …), and each season of a multi-season series gets its own show
`<library> - <series> - الموسم N` (created when missing; `--create-series` also creates
whole-series shows). Dry run first; it lists the episodes leaving each show and the
channels that play them (dizqueTV channels keep their own copy, so regenerate their
schedule afterwards). `--apply` backs each show up to `--backup-dir` before changing
it and reads it back to verify.

## Plex export safety

- Dry run unless `--apply`.
- Collections the tool created are tracked and updated in place; a same-named
  collection it didn't create is only replaced with `--replace-existing`.
- Plex keeps manual-collection membership as a tag named after the collection,
  so deleting a same-named collection *after* creating its replacement empties
  the new one. The tool always deletes first, then reads every written
  collection back and fails loudly if the episodes or their order differ.
- Older collections that a new season fully covers are listed as *superseded*
  and removed only with `--remove-superseded`.
