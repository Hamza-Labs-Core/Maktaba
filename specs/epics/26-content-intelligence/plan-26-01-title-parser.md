# Plan 26.1 — Title parser — implementation

> Implementation plan for [story-26-01-title-parser.md](story-26-01-title-parser.md).
> Self-contained. Cross-links: the parser is consumed by the `classify`
> stage ([Plan 26.7](plan-26-07-background-enrichment-pipeline.md)),
> series detection ([Plan 26.3](plan-26-03-series-detection.md)), and
> enrichment matching ([Plan 26.5](plan-26-05-web-metadata-enrichment.md)).
> It writes one table (`media_parsed_titles`, slot 0073). It is a pure
> library with **no** DB or network access of its own — persistence is
> done by its caller.

---

## 0. Decisions

| #  | Decision | Rationale |
|----|----------|-----------|
| D1 | **Pure function, own module, no I/O.** `parse(filename, *, dirnames=[]) -> ParsedTitle` lives in `pipeline/.../classify/title/parser.py`. Persistence is the caller's job. | Testability (huge fixture corpus, no DB), reuse (series + enrich call it directly), determinism. |
| D2 | **Ordered regex cascade, not a grammar / ML.** A prioritised list of compiled patterns; first match per field family wins; matched spans are removed before the title is cleaned. | Filename conventions are a finite, well-known set (Scene/P2P rules). A PEG/ML parser is overkill and non-deterministic; the cascade is O(patterns) and fully inspectable. We deliberately mirror the well-trodden `guessit`/`parse-torrent-name` approach rather than depend on those libs (Arabic support + zero-dep policy). |
| D3 | **Two-phase: extract-then-clean.** Phase 1 extracts structured tokens (S/E, year, res, codec, group, edition) and records their spans. Phase 2 removes those spans + separators and derives `title`/`show_name` from the residue. | Prevents metadata bleeding into the title (the story's `test_no_metadata_bleed_into_title`). |
| D4 | **Arabic handled by dedicated patterns + digit folding**, not transliteration. `الموسم`/`الحلقة` markers, Arabic-Indic digit folding (`٠-٩` → `0-9`), and Eastern punctuation normalisation run before the Latin cascade; Arabic text in the residue is left byte-intact (no case-folding, no title-casing). | Arabic is first-class in Maktaba; transliterating would corrupt the show name. Digit folding is the only safe normalisation. |
| D5 | **`confidence` is a transparent additive score**, not a model probability. Each recovered field contributes a fixed weight (S/E: +0.4, year: +0.3, res/codec/group: +0.1 each, clean title: +0.2), clamped to 1.0. | The story calibrates thresholds against it; a transparent score is debuggable and stable across `parser_version`. |
| D6 | **`parser_version` is an integer constant in the module.** Bumping it on any rule change lets the caller detect rows that need re-parsing. | Mirrors `MODEL_VERSION` in `content_type.py`; the `classify` stage backfills on bump. |
| D7 | **Directory context is a fallback only.** Filename wins; dirnames fill `show_name`/`season` only when the filename lacks them. | A `Season 01/02.mkv` layout is common; but a fully-named file must not be overridden by a misleading parent dir. |
| D8 | **Never raise.** Every code path returns a `ParsedTitle`; the worst case is `kind=unknown`, low confidence, cleaned title only. | The parser is on the `classify` critical path; a crash there must be impossible. |

If D2 is rejected for a dependency on `guessit`: we lose Arabic support
(guessit is Latin-centric), gain a heavy transitive dep tree, and lose
determinism guarantees across guessit versions — rejected.

---

## 1. Package layout (Pipeline Service, Python)

```
pipeline/src/maktaba_pipeline/classify/
├── __init__.py                 # re-exports parse, ParsedTitle, PARSER_VERSION
├── title/
│   ├── __init__.py
│   ├── parser.py               # parse() — the cascade orchestrator (D2, D3)
│   ├── patterns.py             # compiled regex tables (Latin + Arabic)
│   ├── normalize.py            # digit folding, separator + punctuation cleanup (D4)
│   ├── confidence.py           # additive scorer (D5)
│   ├── model.py                # ParsedTitle dataclass + PARSER_VERSION (D6)
│   └── tests/
│       ├── conftest.py
│       ├── test_parser_scene.py
│       ├── test_parser_movies.py
│       ├── test_parser_arabic.py
│       ├── test_parser_dirfallback.py
│       ├── test_parser_edge.py
│       └── test_corpus_regression.py
└── repo.py                     # write_parsed_title() — caller-side persistence (used by classify stage)

tests/fixtures/
└── filenames.jsonl             # ≥300 {name, dirnames, expected} regression rows
```

## 2. `model.py` — the result type (D6)

```python
from __future__ import annotations
from dataclasses import dataclass, field
from datetime import date

PARSER_VERSION = 1

@dataclass(frozen=True)
class ParsedTitle:
    title: str
    kind: str                       # "episode" | "movie" | "unknown"
    show_name: str | None = None
    season: int | None = None
    episode: int | None = None
    episode_end: int | None = None
    episode_title: str | None = None
    year: int | None = None
    airdate: date | None = None
    resolution: str | None = None
    source: str | None = None       # bluray|webdl|hdtv|dvd|...
    video_codec: str | None = None
    audio_codec: str | None = None
    release_group: str | None = None
    edition: str | None = None
    language_hint: str | None = None
    confidence: float = 0.0
    parser_version: int = PARSER_VERSION
    extras: dict = field(default_factory=dict)  # is_sample, numbering, etc.
```

## 3. The cascade (`parser.py`, D2/D3/D7/D8)

```python
def parse(filename: str, *, dirnames: list[str] | None = None) -> ParsedTitle:
    dirnames = dirnames or []
    try:
        return _parse_inner(filename, dirnames)
    except Exception:                      # D8: never raise
        return ParsedTitle(title=_fallback_title(filename), kind="unknown",
                           confidence=0.0)

def _parse_inner(filename: str, dirnames: list[str]) -> ParsedTitle:
    stem = strip_extension(filename)                 # also flags .sample
    text = normalize.fold_digits(stem)               # Arabic-Indic → ASCII (D4)
    spans: list[Span] = []
    fields = {}
    # Phase 1 — extract (order matters; first match wins per family)
    extract_episode_markers(text, fields, spans)     # SxxExx | xxXyy | الموسم/الحلقة | ranges
    extract_airdate(text, fields, spans)
    extract_year(text, fields, spans)                # only if not consumed as show-name (edge)
    extract_quality(text, fields, spans)             # res, source, codecs
    extract_release_group(text, fields, spans)
    extract_edition(text, fields, spans)
    extract_language_hint(text, fields, spans)
    # Phase 2 — clean title/show from the residue
    residue = remove_spans(text, spans)
    title, show, ep_title = derive_titles(residue, fields)
    fields.update(title=title, show_name=show, episode_title=ep_title)
    apply_dir_fallback(fields, dirnames)             # D7
    kind = classify_kind(fields)
    conf = confidence.score(fields)                  # D5
    return ParsedTitle(kind=kind, confidence=conf, **fields)
```

`patterns.py` holds the compiled tables. Representative entries:

```python
EPISODE_PATTERNS = [
    re.compile(r"[._ -]S(?P<s>\d{1,2})E(?P<e>\d{1,3})(?:[-]?E(?P<e2>\d{1,3}))?", re.I),
    re.compile(r"[._ -](?P<s>\d{1,2})x(?P<e>\d{1,3})", re.I),
    re.compile(r"الموسم[\s]*(?P<s>\d{1,2})"),         # Arabic season (digits pre-folded)
    re.compile(r"الحلقة[\s]*(?P<e>\d{1,3})"),         # Arabic episode
]
YEAR_PATTERN = re.compile(r"[(\[]?(?P<y>19\d{2}|20\d{2})[)\]]?")
RES_PATTERN  = re.compile(r"(?P<r>480|576|720|1080|2160|4320)p", re.I)
CODEC_PATTERN= re.compile(r"\b(?P<c>x264|x265|h\.?264|h\.?265|hevc|av1|xvid|divx)\b", re.I)
GROUP_PATTERN= re.compile(r"-(?P<g>[A-Za-z0-9]+)$")  # trailing -GROUP
```

`normalize.fold_digits` maps `٠-٩` (Arabic-Indic) and
`۰-۹` (Extended) to ASCII; `derive_titles` title-cases Latin
residue (`str.title()` with acronym guards) and leaves Arabic untouched
(detected via Unicode script of the first strong char).

The year-vs-show disambiguation (story edge case) lives in
`classify_kind`: if any episode marker matched, a leading 4-digit token
stays in the show name and is *not* promoted to `year`.

## 4. Data model — migration slot 0073

`shared/db/migrations/0073_media_parsed_titles.sql` (+ `.sqlite.sql`):

```sql
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- Slot 0073 (Epic 26 / Story 26.1) — deterministic filename parse, 1:1 with videos.
CREATE TABLE IF NOT EXISTS media_parsed_titles (
    video_id       UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    kind           TEXT        NOT NULL DEFAULT 'unknown'
                               CHECK (kind IN ('episode','movie','unknown')),
    title          TEXT        NOT NULL,
    show_name      TEXT,
    season         INTEGER,
    episode        INTEGER,
    episode_end    INTEGER,
    episode_title  TEXT,
    year           INTEGER,
    airdate        DATE,
    resolution     TEXT,
    source         TEXT,
    video_codec    TEXT,
    audio_codec    TEXT,
    release_group  TEXT,
    edition        TEXT,
    language_hint  TEXT,
    confidence     REAL        NOT NULL DEFAULT 0,
    parser_version INTEGER     NOT NULL DEFAULT 1,
    extras         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    parsed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (confidence >= 0 AND confidence <= 1)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Series detection groups by normalised show name; this index serves it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_parsed_titles_show_idx
    ON media_parsed_titles (show_name) WHERE show_name IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS media_parsed_titles_show_idx;
DROP TABLE IF EXISTS media_parsed_titles;
-- +goose StatementEnd
```

The `.sqlite.sql` variant drops `CONCURRENTLY`/`NO TRANSACTION`, uses
`TEXT` for `JSONB`/`DATE`/`UUID` per the existing SQLite convention (see
any prior `*.sqlite.sql`). Add the slot to
[`shared/db/migrations/MANIFEST.md`](../../../shared/db/migrations/MANIFEST.md)
under Epic 26 ownership.

## 5. `repo.py` — caller-side persistence

```python
async def write_parsed_title(conn, video_id: str, pt: ParsedTitle) -> None:
    await conn.execute(
        """INSERT INTO media_parsed_titles
             (video_id, kind, title, show_name, season, episode, episode_end,
              episode_title, year, airdate, resolution, source, video_codec,
              audio_codec, release_group, edition, language_hint, confidence,
              parser_version, extras)
           VALUES ($1,...,$20)
           ON CONFLICT (video_id) DO UPDATE SET ...""",
        video_id, pt.kind, pt.title, ... )
```

Called only from the `classify` stage
([Plan 26.7](plan-26-07-background-enrichment-pipeline.md)); the parser
itself stays pure.

## 6. Files to create / modify

**Create:** everything under `pipeline/.../classify/title/`, `classify/repo.py`,
the two migration files, `tests/fixtures/filenames.jsonl`.

**Modify:**
- `shared/db/migrations/MANIFEST.md` — register slot 0073.
- `pipeline/.../classify/__init__.py` — export `parse`, `ParsedTitle`,
  `PARSER_VERSION` (the `classify` package is new in this epic).

## 7. Dependencies

- None new at runtime (stdlib `re`, `unicodedata`). No `guessit` (D2).
- Depends on slot 0001 (`videos`). No dependency on any other Epic 26
  story — this is the leaf the others build on.

## 8. Test strategy

Unit tests per pattern family + the corpus regression
(`test_corpus_regression`) that loads `filenames.jsonl` and asserts
≥95 % exact-field accuracy. The corpus is the contract: any parser change
that regresses it fails CI. Determinism test pins output bytes for a
fixed `PARSER_VERSION`.

## 9. Performance

≤2 ms/filename (compiled patterns, no I/O). A 10 k-file library parses in
<20 s single-threaded; in practice it runs per-video inside `classify`,
so cost is amortised across the import.
