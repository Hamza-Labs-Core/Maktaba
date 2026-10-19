# Plan 27.11 — Channel strip and display titles — implementation

> Implementation plan for [story-27-11-channel-strip-display-titles.md](story-27-11-channel-strip-display-titles.md).
> Cross-links: the scheduler's `title_snapshot` ([Plan 27.2](plan-27-02-program-scheduler.md)
> D8), the live engine's block ffmpeg ([Plan 27.3](plan-27-03-live-stream-engine.md)), the
> guide ([Plan 27.4](plan-27-04-epg-generation.md)), the channel editor
> ([Plan 27.8](plan-27-08-channel-admin-ui.md)). Migration slot **0122**.

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **The strip is burned in by the streaming service, per block.** | Every client sees it (HDHomeRun, Plex, VLC, apps) with no client work; one encode per channel already exists. |
| D2 | **One block = one ffmpeg = one set of strip texts.** | The text changes exactly at block boundaries, the 3 s pre-start included, with no runtime signalling. |
| D3 | **Text through files, never the filter string** (`textfile=`, `expansion=none`; the clock by `sendcmd` `reinit`). | Titles carry `: ' , % [ ]`; escaping them for two parser levels is error-prone and an injection risk. |
| D4 | **Arabic via drawtext shaping** (`text_shaping=1`; harfbuzz + fribidi in the gyan full build and Homebrew). Detected once per binary with a test render; without it the strip is skipped and logged once. | Unshaped Arabic is unreadable; skipping is better than broken letters. A PNG fallback (Pillow + raqm in the pipeline) is a follow-up. |
| D5 | **Right-to-left lines are wrapped in RLM marks.** | drawtext lays out a left-to-right paragraph; the marks keep «!», «؟» and numbers at the right ends. |
| D6 | **The clock is computed in Go**, a `sendcmd` file switching two drawtext instances at each rotation step and minute. | `%{localtime}` knows only the host zone; Go's IANA zones (embedded `time/tzdata`) give exact DST, any digits and 12/24 h; rotation follows wall time so it continues across blocks. |
| D7 | **Display titles are stored on the video** (`display_title`, `display_title_manual`, `display_title_source`) and the series (`display_name`), made by a `display_title` stage after `detect`. | One computation, reused by the scheduler, guide, XMLTV, dizqueTV export and strip. |
| D8 | **The scheduler lays the display parts over `title_snapshot`**; the stage refreshes future blocks' snapshots in place. | The guide and the streaming service read only the snapshot (no library join per read); no re-plan when a title changes. |
| D9 | **Rules first, model optional** (`use_llm` off), cached by input hash in `display_title_llm`. | Rules are free and deterministic; the model costs seconds per batch and is only worth it for what rules leave messy. |
| D11 | **Long lines page instead of being cut** (`overflow` cycle, default): pages switched by the sendcmd timeline (`reinit text=` on `drawtext@now` / `@next` / `@nowi` / `@nexti`). | The strip can cycle, so nothing needs to be dropped; the same timeline as the clock keeps pages in step with the picture. Titles then live only in files (text files, the sendcmd script), never in the filter string. |
| D12 | **Quran verses are located in a bundled Tanzil text** (`data/quran`, CC BY 3.0, verbatim + notice), indexed by its first two folded words. | A verse is never cut; its reference is a page of the strip and, when a stored title must be short, its stand-in. Local, fast (one 0.3 s index build), no service. |
| D10 | **Per-channel strip settings live in `mode_config.look.strip`**; `write_channels` carries them across plan rewrites; the API writes them without marking the channel edited. | The look is already the channel's on-air presentation; the plan never sets a strip. |

## 1. Files

```
pipeline/src/maktaba_pipeline/classify/display_title.py   # rules, model prompt/parse (pure)
pipeline/src/maktaba_pipeline/channels/display.py         # snapshot merge + lookup
pipeline/src/maktaba_pipeline/engine/stages/display_title.py
pipeline/src/maktaba_pipeline/schedule/tv_settings.py     # TvStripConfig, StripClock
pipeline/src/maktaba_pipeline/rules.py                    # sections tv_strip, display_titles
streaming/internal/channel/strip.go                       # settings, texts, layout, clock, filters
streaming/internal/channel/{blockargs,station,plan,look,repo_pg,settings,config}.go
api/internal/handlers/videos/videos.go                    # display_title in detail / PATCH
api/internal/handlers/channels/strip.go                   # PATCH/DELETE /api/channels/{id}/strip
web/src/pages/Admin/channels/ (Strip section + preview), web/src/pages/VideoDetail.tsx
shared/db/migrations/0122_display_titles{,.sqlite}.sql
```

## 2. Display titles (pipeline)

`clean_title(raw, cfg, brands, boilerplate)`:

1. Put back yt-dlp's fullwidth look-alikes; drop a leading number («#20 », «175- ») as the
   episode; drop a file extension.
2. Remove URLs, hashtags, @mentions, emojis (`So` symbols except ﷺ ﷽), `strip_patterns`,
   dates, `strip_words`.
3. Split on `|`, `•`, `·`, spaced dashes / tatweel / `#`, `::`. Per part: a presenter
   credit goes (a later part from an honorific on; the first part only a short trailing
   one); an episode marker gives the number; a part that is a brand, the series name, a
   shared phrase or only honorifics goes; brand words inside a part go with their
   honorifics.
4. Fold repeated punctuation, drop a dangling «…» (a cut YouTube title) and unbalanced
   brackets (balanced ones, a quoted verse's, are kept); join what is left with « - ».
5. `fit_title`: with `max_length` > 0 a longer title is cut at a word; a quoted verse is
   never cut inside: per `verse_display` its reference replaces it, or its opening words
   are kept with «…» after the closing bracket.

`classify/quran.py`: `find_quotes` (spans `﴿…﴾`, `{…}`, brackets / quotes / bare words
after «قوله تعالى», «الآية (…)»), `locate` (folded word stream of the Tanzil text with the
basmala of each surah's first verse removed; candidates by the quote's first two words,
every word must match, a fallback anchored on a later pair tolerates ⌊n/4⌋ different
words; unique hits only), `VerseRef.label` («البقرة ١٨٣», "Al-Baqara 2:183", ranges).
`verse_reference(title)` gives the snapshot's `strip_ref` unless the title names its
surah.

`title_boilerplate(titles)`: folded parts in at least `boilerplate_share` (0.3) and
`boilerplate_min_count` (5) of the titles — computed per library and per series.

Stage `display_title` (library, pool cpu, lane seasons, placement home, model_rules
tagcheck, debounce 30/300 s; edge `detect → display_title (same)`): computes every video
and series, asks the model for `needs_llm` titles when `use_llm` (cache first), writes
changed rows, then merges the display parts into every future `channel_programs` row of
the library whose snapshot changes. Output hash: the titles.

Scheduler: `SqlRepo.replace_future_blocks` merges `display_info` (manual > rules/model,
series display name, episode) into each block's snapshot (`merge_snapshot`).

dizqueTV: `with_display_titles` replaces the Plex record titles (by `plex_items.video_id`)
before programmes are built.

## 3. The strip (streaming)

- `ProgramsFrom` reads `title_snapshot` → `ProgramBlock.Info` (`on_air`, title).
- `PlanNext` sets `Segment.Next`: the next programme or live block in the look-ahead.
- `PGSettings` reads `rules.tv_strip` (`StripSource`); a station lays the channel's
  `look.strip` over it (`ApplyStripRules(…, channel=true)`: no font file per channel).
- `Station.attachStrip`: font (`FindStripFont`: setting, `MAKTABA_CHANNEL_STRIP_FONT`,
  Noto Naskh/Sans Arabic, Tahoma, Arial…), shaping check (cached), `BuildStrip` writes
  `now.txt`, `next.txt`, `name.txt`, `clock.cmd` under `<station dir>/strip/` (removed when
  the block ends).
- `BlockArgs` appends `StripInput.Filters()` after the watermark: `drawbox` (bar),
  `drawtext` lines, channel-name box, clock box + `sendcmd` + `drawtext@clkl` /
  `drawtext@clkt`; `enable=lt(mod(t+offset,every),show)` in interval mode; a scrolling
  line's `x` is `if(lte(tw,area), aligned, marquee)`. A watermark on the strip's edge is
  lifted by the strip's height.
- Paging (`strip_pages.go`, overflow cycle): `PageText` fills pages greedily by an
  estimated width (per-character em classes, 90 % of the area), then balances them (the
  narrowest width that keeps the page count); brackets open at a page's end are closed
  there and reopened on the next; `withRef` adds the verse reference; `PageSchedule`
  cycles the pages over the block (`page_seconds` × 1–2 by fill; the next line × 1.5)
  with «k/n»; `CmdScript` merges the pages' and the clock's changes into one sendcmd file
  (one line per instant).
- Layout: bar `height_pct` of the frame; clock box `1.9 × height` at the end side; title
  `font_size_pct` of the height, next line ⅔ of it; truncation by an estimated
  characters-per-width before writing the file.

## 4. API and web

- `PATCH /api/videos/{id}` `display_title` (set / clear the manual title; enqueues the
  library's `display_title` job so future blocks update); detail returns
  `display_title`, `display_title_auto`, `display_title_manual`, `display_title_source`.
- `PATCH /api/channels/{id}/strip` (validated partial object merged into
  `mode_config.look.strip`), `DELETE` (back to the defaults); the channel DTO carries the
  stored strip; `/api/channels/now` carries `on_air`.
- Web: Strip section with a live preview (real now/next, `Intl.DateTimeFormat` per zone,
  digits, RTL); settings sections localised; the display title in the item view.

## 5. Tests

- pipeline: title rules with the real examples and Latin titles; boilerplate; labels;
  snapshot merge; model prompt/parse/hash; the stage after `detect`, fingerprint
  (titles, manual, rules), future snapshots and a manual title, the model with a stand-in
  asked once (cache).
- streaming: settings overlay and validation, texts per block kind, next-programme pick,
  layout (clock side, watermark), paging at word boundaries (Arabic, Latin), verse pages
  in order with their brackets and the reference last, the page timeline over a block and
  the slower next line, the sendcmd script, zone offsets and DST (New York 2026-03-08 / 11-01,
  Cairo 2026-04-24 / 10-29), clock rotation and minute steps, sendcmd escaping, the filter
  chain never carrying titles, BlockArgs wiring, and a real ffmpeg render of an Arabic
  strip compared against the bare frame, and of a paged title and a paged verse whose
  pages differ (skipped without shaping).
- pipeline (verses): `locate` on real quotes (a range, spelling variants, a surah's first
  verse, ambiguous quotes refused), `find_quotes` (no braces taken for verses), stored
  titles keep the whole verse, a cut title never cuts inside it (each `verse_display`),
  the snapshot's `strip_ref`.
- api / web: strip PATCH validation and merge, display title PATCH; preview, strip form,
  item view.
