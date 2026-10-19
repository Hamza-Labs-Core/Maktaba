# Story 27.11 — Channel strip and display titles

## Description

News channels run a bar across the bottom of the picture: what is on, what comes next,
the time in a few capitals. Maktaba's channels get the same **channel strip**, burned
into the picture by the streaming service, so every player (the apps, VLC, Plex Live TV,
an HDHomeRun client) sees it without any client work. It shows the programme on air (the
series, «الحلقة 45» and the episode's own title), «التالي: …», and a clock that rotates
between cities (Makkah, Cairo, Greenwich, New York by default) with daylight saving
handled by the IANA time zones.

What the strip shows must read well on air. Library titles are what YouTube or the file
system gave: «في الخنادق… لا يوجد ملحدون! ｜ برنامج من زاوية أخرى ｜ الحلقة 45»,
«كيف نخاطب الجيل الجديد؟ - 1438⧸10⧸12». So every video gets a **display title**: a short,
clean on-air title made by rules (and, optionally, the local language model), with the
series and episode number kept apart. The guide, XMLTV, the dizqueTV export and the strip
all use it; a title set by hand in the library always wins.

Services: pipeline (rules, the `display_title` stage, the scheduler's snapshot), streaming
(the strip in each block's ffmpeg), api (manual titles, the channel's strip settings),
web (strip editor with a live preview, display title in the library item view). Migration
slot 0122.

## Acceptance criteria

### Display titles

- **AC1** Every video of a library gets `videos.display_title` from its library title by
  the rules (settings section `display_titles`): the channel's and presenter's names
  (the tagger's `branding()`), the series' name, phrases most of the library's or the
  series' titles share, presenter credits («| د حسام عقل»), hashtags, emojis, URLs,
  quality tags (`(1080p)`, `[HD]`, `Official`, `Full`), dates (`1438⧸10⧸12`,
  `2019-05-01`), the fullwidth substitutes yt-dlp writes into file names (`｜ ： ⧸`), file
  extensions and repeated punctuation are removed. The stored title is not cut by
  default (`max_length` 0: the guide and XMLTV take the whole clean title, the strip pages
  it); with a limit it is cut at a word boundary, never mid-word and never inside a quoted
  verse (AC2b).
- **AC2** The episode number comes from the season order (`series_episodes`), else from
  the title («الحلقة 45», «حلقة 14:», «Episode 3», «#20 …», «175- …», «الحلقة الأولى»),
  and is shown as `episode_label_ar` / `episode_label_en` («الحلقة {n}» / «Ep {n}»).
  Each series gets `series.display_name`, cleaned the same way.
- **AC2b** A title that quotes a Quran verse (`﴿…﴾`, `{…}`, brackets or quotes or bare
  words after «قوله تعالى» / «الآية») has the verse found in the bundled Tanzil "Simple
  Clean" text (CC BY 3.0, verbatim, with its notice and `LICENSE-TANZIL.txt`): words are
  matched after folding (no tashkeel; alef, ya, ta marbuta forms unified), across verse
  ends (a range), tolerating a word or two spelt otherwise; a quote found in more than one
  place gives no reference. The reference is «البقرة ١٨٣» / "Al-Baqara 2:183" (a range
  «الإنفطار ١٣–١٤»). When a title must be cut, `verse_display` decides: `reference`
  (default) puts the reference in the verse's place («تفسير البقرة ١٨٣»), `opening_words`
  keeps the verse's first words with «…» after the closing bracket, `full_if_fits` keeps
  the verse alone when it fits, else the reference. A title that names its surah
  («سورة البقرة …») gets no extra reference. Verse titles are never sent to the model.
- **AC3** The stage `display_title` (library subject) runs after `detect`; its input
  fingerprint covers the titles, series and episode order, channel names, manual titles,
  the rules and (when used) the model; an unchanged library is not re-run.
- **AC4** With `use_llm` on (off by default) the local model of the tag checks (rules
  `tagcheck`: server and model) rewrites titles the rules leave long, in several parts or
  empty, `llm_batch` titles per question; each answer is cached in `display_title_llm` by
  its input hash, so a title is asked once; a model that does not answer leaves the rules'
  titles in place. The stage runs on the home (placement `home`, `model_rules=tagcheck`).
- **AC5** A title set by hand (`PATCH /api/videos/{id}` `display_title`) is stored in
  `display_title_manual`, wins over rules and model, and is cleared with an empty value.
- **AC6** The scheduler writes the display parts into each block's `title_snapshot`
  (`on_air`, `strip_title`, `strip_series`, `strip_episode`; with `use_in_guide`, `title`,
  `series`, `episode`, `episode_title`, the library title kept as `source_title`;
  `strip_ref`: the reference of a quoted verse), and a
  `display_title` run updates the snapshots of the future blocks at once (no re-plan).
- **AC7** With `use_in_guide` the dizqueTV export's programme titles are the display
  titles too.

### The strip

- **AC8** Settings section `tv_strip` holds every channel's defaults (off by default);
  each channel's own strip settings (`mode_config.look.strip`, edited in its Look) are
  laid over them and survive plan rewrites. Settings: enabled, position (bottom default,
  top), height, colours and opacity, font file (host setting only), font size, direction
  (auto / rtl / ltr), show next and its labels, filler label, overflow (truncate / scroll
  marquee and speed), show mode (always / at programme start and every N minutes), clock
  on/off, clocks (rows: IANA zone, Arabic label, other label; default Makkah, Cairo,
  Greenwich, New York), seconds per clock (8), 12/24 h, digits (auto: Arabic-Indic in a
  right-to-left strip / arabic / western), channel name, long titles (`overflow`: cycle
  — default — / truncate / scroll), seconds per page and the page indicator.
- **AC8b** With `overflow` cycle nothing is cut: a line wider than the strip (measured
  with per-character width estimates and a 10 % margin) is split at word boundaries into
  balanced pages that take turns in place, each for `page_seconds` (a full page up to
  twice as long), with a small «١/٢»; the «التالي» line pages on its own, 1.5 times slower.
  A quoted verse is shown whole across pages in order, each page closing and reopening
  its brackets, and its reference follows on the last page (or a page of its own). The
  pages are switched by the same `sendcmd` timeline as the clock, from the block's start.
- **AC9** The strip shows: for a programme its `on_air` line; for filler the clip's
  display title or the filler label; for a live block its title; for a slate only what is
  next. The second line is «التالي: <next programme>» (filler is never "next").
- **AC10** The strip changes exactly at block boundaries, including the next block's
  pre-start 3 s early: each block's ffmpeg draws its own texts.
- **AC11** Titles are never put into the filter string: each block's lines go to UTF-8
  text files (`drawtext textfile=`, `expansion=none`), the clock's texts to a `sendcmd`
  file; Arabic is shaped (`text_shaping=1`) and right-to-left lines keep their
  punctuation and numbers in place.
- **AC12** Shaping is detected once per ffmpeg (help lists `text_shaping` and a test
  render of an Arabic word succeeds); without it the strip is skipped and logged once
  (`channel_strip_unavailable`), never drawn with broken letters.
- **AC13** The clock rotates through the cities every `clock_seconds` of wall time
  (continuing across blocks) and is refreshed every minute; times are computed with the
  IANA zones (Go `time/tzdata` embedded), so DST changes (Cairo, New York) are exact even
  inside a block. The clock sits at the strip's end: the right in a left-to-right strip,
  the left in a right-to-left one.
- **AC14** A watermark on the strip's edge is moved off it (above a bottom strip, below a
  top one).
- **AC15** The strip is CPU drawing (drawbox / drawtext) before the encoder; the hardware
  encoder path (NVENC, VideoToolbox) is unchanged.

### UI

- **AC16** The channel editor's Look has a Strip section (follow defaults / on / off and
  every setting) with a live preview over a sample frame using the channel's real now and
  next lines, RTL, the rotating clock in the configured zones, digits and format.
- **AC17** The settings page lists `tv_strip` and `display_titles` (Arabic and English).
- **AC18** The library item view shows the display title and where it came from (rules,
  model, by hand) and lets a person set or reset it.

## Edge cases

- **EC1** A title that is only the series and an episode marker has an empty display
  title; the strip says «series · الحلقة n».
- **EC2** A bad pattern in `strip_patterns` is skipped; the other rules apply.
- **EC3** An unknown time zone in the clocks is refused (the setting keeps its default).
- **EC4** No Arabic font on the host: the strip is skipped and logged once.
- **EC5** A long title: shown in pages (cycle), cut with … (truncate) or run across like a
  ticker (scroll; the clock box covers it as it passes). A single word wider than the
  strip is a page of its own, never split.
- **EC6** A database before slot 0122: the scheduler and the strip keep working with the
  library titles.

## Out of scope

- The strip in the local dizqueTV (only its programme titles use the display titles).
- A pre-rendered PNG fallback when ffmpeg cannot shape text (documented as a follow-up).
