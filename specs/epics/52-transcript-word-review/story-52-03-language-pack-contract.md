# Story 52.03 — The language pack contract, registry and fallback pack

> Epic 52 · Phase 1 · Depends on: — · Needed by: 52.4, 52.5, 52.6

## Why

The owner: *"this needs to be done so that it can support all languages, added as addons or
whatever."* Today the only language knowledge is Arabic-specific and scattered:
`text/arabic.py` (folding), `stt/cohere.py` (the CAMeL dictionary), `classify/quran.py`. A word
checker built on those would be Arabic forever. This story defines one contract every language
plugs into, a registry that finds installed packs, and a fallback that gives *any* language
something useful on day one.

## Description

### 1. The contract

`pipeline/src/maktaba_pipeline/language_packs/base.py`:

```python
@dataclass(frozen=True, slots=True)
class Token:
    text: str
    start: int            # code points into the sentence text
    end: int
    kind: Literal["word", "number", "punct", "symbol"]
    script: str           # ISO 15924, e.g. "Arab", "Latn"

class Known(Enum):
    KNOWN = "known"; UNKNOWN = "unknown"; UNSURE = "unsure"

class LanguagePack(Protocol):
    code: str                       # BCP 47
    version: str
    scripts: frozenset[str]
    direction: Literal["rtl", "ltr"]
    def normalise(self, text: str) -> str: ...
    def key(self, token: str) -> str: ...
    def tokens(self, text: str) -> list[Token]: ...
    def known(self, token: str) -> tuple[Known, str | None]: ...   # with an analysis, for the UI
    def suggest(self, token: str, n: int) -> list[str]: ...
    def protected(self, tokens: list[Token]) -> list[bool]: ...
```

Rules every pack keeps:

- Pure and thread-safe after `load()`. No network at check time. Data fetched once at install.
- `key()` is idempotent (`key(key(x)) == key(x)`) and stable within a `version`.
- `tokens()` covers the text: concatenating token spans plus the gaps gives back the text.
- `known()` never raises. Missing data means `UNSURE`.
- Vocabulary (52.10) is consulted by the caller, not the pack, so packs stay data-only.

### 2. The registry

- Packs are found through the Python entry-point group **`maktaba.language_packs`** (`name =
  "module:factory"`). Core ships `ar`, `en` and `fallback` as entry points of the pipeline
  package itself. A third-party pack is a wheel with its own entry point.
- `language_packs` table (next free slot, 0173+): `code PK, version, enabled, source
  ('builtin'|'wheel'|'component'), data_sha256, installed_at, settings JSONB`. The registry writes
  a row for each pack it finds. An admin can disable one (the API is in 52.14).
- `registry.for_script(script) -> LanguagePack`: the enabled pack for a script, preferring the
  transcript's language when two packs share a script (e.g. `fr` and `en` both `Latn`).
- `registry.versions() -> dict[str, str]`: part of `word_check`'s fingerprint (52.6). A pack
  upgrade re-checks only transcripts that pack touched.

### 3. Script runs (code-switching)

`language_packs/scripts.py` splits a sentence into runs by Unicode `Script` property
(`unicodedata2` or ICU's `uscript`). Common and Inherited characters (digits, punctuation,
harakat) join the run around them. Each run goes to `for_script`. A run in a script with no
enabled pack uses the fallback, which never says `UNKNOWN` (so it never adds
`not_in_dictionary`).

### 4. The fallback pack

For any language Whisper hears:

- `normalise`: NFC, strip bidi controls.
- `tokens`: ICU word break (UAX #29, `PyICU` `BreakIterator.createWordInstance`), so languages
  without spaces (Thai, Japanese, Chinese) get real words. Without PyICU, a `\w+` regex.
- `known`: a LibreOffice Hunspell dictionary if one is installed for the language (searched in
  the pack's data folder), giving `KNOWN`/`UNKNOWN`. Otherwise `wordfreq`'s Zipf frequency: at or
  above 2.5 `KNOWN`, below `UNSURE`. Never `UNKNOWN` from frequency alone.
- `suggest`: Hunspell's suggestions if present, else none.
- `protected`: nothing.

### 5. Epic 51 (configurable components)

Until Epic 51 lands, a pack is an entry point plus a data folder under
`model_dirs.cache_dir()/language_packs/<code>/<version>/`, fetched like CAMeL's data
(`ensure_camel_data`). When Epic 51 lands, a pack becomes a **component** of kind
`language_pack`: Epic 51 owns install, update, enable and the UI. The `language_packs` table
becomes a view of Epic 51's component rows, and `source` becomes `component`. The protocol does
not change. This story does not design installation UI.

### 6. Conformance suite

`language_packs/conformance.py`: a pytest-parametrised suite any pack runs against itself. It
checks the rules in §1 with property tests (Hypothesis) over the pack's own sample sentences
(`samples.txt` shipped with the pack). Built-in packs run it in CI. A third-party pack's README
says how to run it.

## Acceptance criteria

1. **Given** the pipeline installed, **when** the registry loads, **then** it finds `ar`, `en` and
   `fallback` through `maktaba.language_packs` and writes their rows in `language_packs`.
2. **Given** a wheel exposing a `fr` pack, **when** installed and the worker restarts, **then** the
   registry lists `fr` and `for_script("Latn")` returns `fr` for a French transcript, `en` for an
   English one.
3. **Given** the sentence «قال النبي ﷺ in the hadith…», **when** split, **then** the Arabic run goes
   to `ar`, the English run to `en`, and token offsets are code points into the original text.
4. **Given** a Japanese sentence and no `ja` pack, **when** tokenised, **then** the fallback uses
   ICU word breaks (more than one token for a sentence without spaces).
5. **Given** a fallback word with no Hunspell dictionary, **when** `known()` runs, **then** it
   returns `KNOWN` or `UNSURE`, never `UNKNOWN`.
6. **Given** a disabled pack, **when** its script comes up, **then** the fallback handles it.
7. **Given** any built-in pack, **when** the conformance suite runs, **then** tokens cover the text,
   `key` is idempotent, `known` never raises on 1,000 random strings.
8. **Given** a pack's version changes, **when** `registry.versions()` is read, **then** the change
   shows, and 52.6's fingerprint changes only for transcripts in that pack's language.
9. **Given** a grep of `word_review/` and `engine/stages/word_*.py` in CI, **when** it finds a
   language literal (`"ar"`, `"en"`), **then** the lint fails.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/language_packs/__init__.py`, `base.py`, `registry.py`,
  `scripts.py`, `fallback.py`, `conformance.py` (new).
- `pipeline/pyproject.toml`: entry points; a `lang` extra with `wordfreq`, `PyICU` (optional on
  platforms without ICU wheels), `spylls` (pure-Python Hunspell reader, MPL-2.0) or `cyhunspell`.
- `shared/db/migrations/` next free slot, 0173+: `language_packs`, Postgres and SQLite twins.
- `tools/lint/`: the language-literal check for word review code.

### Tests

- `pipeline/tests/language_packs/test_registry.py`: entry points (a fake pack in a temp dist),
  disable, script preference; table rows on SQLite and scratch Postgres.
- `test_scripts.py`: runs on mixed Arabic/English/digits/harakat; offsets.
- `test_fallback.py`: ICU and regex paths, wordfreq thresholds, Hunspell present/absent.
- `test_conformance_builtin.py`: the suite over `ar`, `en`, `fallback`.
- No real model loads. Packs' data is small test data in fixtures.

### Rollout

- No user-visible change. Packs load only when `word_check` (52.6) runs.
- Back out: revert; the table is unused.

## Edge cases

- A script shared by languages that spell alike (Persian and Urdu in `Arab`): a transcript in
  `fa` uses a `fa` pack if installed, else the fallback, never `ar` (Arabic's dictionary would
  flag every Persian word).
- Arabic-Indic digits in an English run: `Common` script, joined to the run, kind `number`.
- PyICU missing on a worker: the fallback uses the regex tokenizer and records
  `tokenizer=regex` in the pack row, so results stay reproducible per machine.
- A broken third-party pack (raises on load): the registry logs it, marks the row
  `enabled=false` with the error, and uses the fallback.
