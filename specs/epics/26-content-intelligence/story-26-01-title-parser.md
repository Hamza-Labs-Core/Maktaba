# Story 26.1 — Title parser (filename → structured metadata)

## Description

Parse a video's filename (and, when present, its containing directory
names) into structured fields the rest of Epic 26 can group and enrich
on. This is the single highest-signal, lowest-cost classifier in the
epic: a name like `Show.Name.S01E02.720p.x265-GROUP.mkv` already encodes
the show, season, episode, resolution, codec, and release group with no
network and no model.

The parser is a **pure function** living in the Pipeline Service:

```
parse(filename: str, *, dirnames: list[str] = []) -> ParsedTitle
```

It is called from the `classify` stage
([Story 26.7](story-26-07-background-enrichment-pipeline.md)) and reused
directly as a library by series detection
([Story 26.3](story-26-03-series-detection.md)) and enrichment matching
([Story 26.5](story-26-05-web-metadata-enrichment.md)). The result is
persisted 1:1 with the video in `media_parsed_titles`.

It must handle the four pattern families called out in the epic plus
Arabic naming, and it must **never raise** — an unparseable name yields
a low-confidence `ParsedTitle` carrying just the cleaned display title.

## Patterns to support

| Family | Example | Extracts |
|--------|---------|----------|
| Scene episode | `Show.Name.S01E02.720p.x265-GRP.mkv` | show, S=1, E=2, res=720p, codec=x265, group=GRP |
| Alt episode | `Show - 01x02 - Episode Title.mp4` | show, S=1, E=2, episode_title |
| Movie + year | `Movie Name (2024).mkv`, `Movie.Name.2024.1080p.mkv` | title, year=2024, res |
| Date-based | `Show.2024.03.14.Topic.mp4` | show, airdate=2024-03-14 |
| Multi-episode | `Show.S01E01E02.mkv`, `Show.S01E01-E03.mkv` | episode range |
| Arabic series | `المسلسل - الحلقة 5.mp4`, `اسم البرنامج الموسم 1 الحلقة 12.mkv` | show (Arabic), season, episode (from الموسم/الحلقة + Arabic-Indic digits) |
| Arabic film | `اسم الفيلم (2019).mkv` | title, year |

## Fields produced (`ParsedTitle`)

`title` (cleaned display), `show_name` (nullable; set for episodic),
`season` (int?), `episode` (int?), `episode_end` (int?, for ranges),
`episode_title` (str?), `year` (int?), `airdate` (date?),
`resolution` (`480p|720p|1080p|2160p|...`?), `source`
(`bluray|webdl|hdtv|dvd|...`?), `video_codec` (`x264|x265|h264|hevc|av1|...`?),
`audio_codec` (`aac|ac3|dts|...`?), `release_group` (str?),
`edition` (`extended|directors|remastered|...`?),
`language_hint` (str?, e.g. `ar`, from tokens like `Arabic`/`مدبلج`),
`kind` (`episode|movie|unknown`), `confidence` (0..1),
`parser_version` (int).

## Acceptance criteria

- `parse()` is pure (no I/O), total (never raises), and deterministic:
  same input → byte-identical output for a fixed `parser_version`.
- Each pattern family in the table above is recovered correctly on its
  canonical example; the season/episode integers are parsed without
  leading-zero issues (`S01E02` → `1`, `2`).
- **Latin-Indic and Arabic-Indic digits both parse.** `الحلقة ٥` and
  `الحلقة 5` both yield `episode = 5`.
- Tokens consumed as metadata (resolution, codec, group, year, episode
  markers) are stripped from `title`/`show_name`; separators (`.`, `_`,
  `-`) are normalised to spaces and the result is trimmed and
  case-cleaned (title-case for Latin, untouched for Arabic).
- `kind = movie` when a year is present and no S/E markers; `kind =
  episode` when S/E (or الموسم/الحلقة) markers are present; else
  `unknown`.
- `confidence` reflects how much structure was recovered: a full scene
  release ≈ 0.95; a bare `Movie (2024)` ≈ 0.8; a name that yielded only
  a cleaned title ≈ 0.2.
- Directory context is used as a fallback: if the filename lacks a show
  name but a parent dir matches a show/season pattern
  (`Show Name/Season 01/02.mkv`), the show + season come from the dirs.
- Result is persisted to `media_parsed_titles` (1:1 with `videos`,
  `ON DELETE CASCADE`); re-running the parser with a higher
  `parser_version` overwrites the row.
- Parsing **never** writes to `videos.title`/`description` directly —
  those are user/enrichment-owned (see
  [Story 26.6](story-26-06-enrichment-ui.md)).

## Test cases

- `test_scene_episode` — `Show.Name.S01E02.720p.x265-GRP.mkv` → all six
  fields, `kind=episode`, `confidence≥0.9`.
- `test_alt_episode_with_title` — `Show - 01x02 - The Title.mp4` →
  `episode_title="The Title"`.
- `test_movie_paren_year` / `test_movie_dotted_year` — both yield
  `year=2024`, `kind=movie`.
- `test_date_based` — `Show.2024.03.14.Topic.mp4` →
  `airdate=2024-03-14`, `kind=episode`.
- `test_multi_episode_range` — `Show.S01E01-E03.mkv` → `episode=1`,
  `episode_end=3`.
- `test_arabic_episode_arabic_digits` — `اسم - الحلقة ٥.mp4` →
  `episode=5`, `show_name="اسم"`, Arabic untouched.
- `test_arabic_season_episode` — `برنامج الموسم 1 الحلقة 12.mkv` →
  `season=1`, `episode=12`.
- `test_dir_fallback` — `parse("02.mkv", dirnames=["Show Name","Season 01"])`
  → `show_name="Show Name"`, `season=1`, `episode=2`.
- `test_unparseable_is_low_confidence` — `IMG_4471.mkv` → `kind=unknown`,
  `confidence≤0.25`, `title` present, no raise.
- `test_determinism` — parse the same name twice → identical dataclass.
- `test_no_metadata_bleed_into_title` — `Show.S01E02.1080p.BluRay.x264-GRP.mkv`
  → `show_name="Show"` (no `1080p`, `BluRay`, `x264`, `GRP` residue).
- `test_corpus_regression` — run the parser over a checked-in fixture
  corpus (`tests/fixtures/filenames.jsonl`, ≥300 real-world names with
  expected output) and assert ≥95 % exact-field accuracy; the corpus is
  the regression guard for parser changes.

## Edge cases

- **Year that is actually an episode count or resolution.** `2160`
  (resolution) vs `(2016)` (year): a 4-digit number in `1900–2099` only
  becomes `year` when delimited as a year (parenthesised, or a bare
  token not adjacent to `p`); `2160p` is resolution.
- **Show name *is* a number.** `1923.S01E01.mkv` → show `1923`, not
  year. S/E presence wins: an episodic marker forces `kind=episode` and
  a leading 4-digit becomes the show name, not a year.
- **Mixed-script names.** `Naruto الحلقة 12.mkv` → `show_name="Naruto"`,
  `episode=12`; Arabic episode markers parse even with a Latin show name.
- **Garbage release tags** (`[www.site.com]`, `{edition-x}`) are
  stripped as noise and never become the show name.
- **Double extensions / sample files.** `Show.S01E02.sample.mkv` →
  flagged `metadata.is_sample=true`; the parser still extracts S/E but
  the `classify` stage may skip enrichment for samples.
- **Absurdly long names** (>512 chars) are truncated for processing; the
  parser still returns within its time budget (≤2 ms/name).
