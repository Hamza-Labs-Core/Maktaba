# Story 52.03 — The language pack contract, registry and fallback pack

> Epic 52 · Phase 1 · Depends on: — · Needed by: 52.4, 52.5, 52.6, 52.16

## Why

Requirement: support every language, with languages added as add-ons. Today the only language knowledge is Arabic-specific and scattered:
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
    def scripture(self, tokens: list[Token]) -> list[ScriptureRun]: ...  # optional, default [];
                                    # runs checked against a canonical text (ar: Quran, 52.15)
```

Rules every pack keeps:

- **Deterministic** (decision 3, 2026-11-03: no LLM judge): the same input, data and
  `version` always give the same answer. No sampling, no model calls, no LLM.
- Pure and thread-safe after `load()`. No network ever: data comes only from the pack's Epic 51
  asset folder (52.16), never downloaded by the pack.
- `key()` is idempotent (`key(key(x)) == key(x)`) and stable within a `version`.
- `tokens()` covers the text: concatenating token spans plus the gaps gives back the text.
- `known()` never raises. Missing data means `UNSURE`.
- Vocabulary (52.10) is consulted by the caller, not the pack, so packs stay data-only.

### 2. The registry

- The registry is a fixed table in `language_packs/registry.py`: each pack class with the Epic 51
  component id of its data (`ar` → `lang.ar`, `en` → `lang.en`, `fr` → `lang.hunspell.fr`, …;
  `fallback` needs only `runtime.lang`). There are no Python entry points: Epic 51 keeps entry-point
  extensions a developer feature (51 §3 "Not in this epic"), and packs from other people come
  through Epic 54 and must pass this story's conformance suite.
- A pack is **usable** when its component is on for this machine (the pipeline's desired state,
  `maktaba_pipeline.components.store.desired`, want `installed`; an asset is never `enabled`) and its data folder is
  found (`components.packs.pack_dir(id)`, 52.16). Otherwise its script falls to the fallback.
  The switch is the component's (`POST /api/components/lang.ar/install`, `DELETE /api/components/lang.ar`);
  this epic adds no second switch.
- `language_packs` table (next free slot, 0182; 0181 is the highest once Epic 51 merges):
  `code PK, component, version, data_sha256, tokenizer, state ('loaded'|'off'|'missing'|'error'),
  error, loaded_at`. It is a **report**, written by the registry on each load, read by
  `GET /api/language-packs` (52.14). It has no `enabled` column.
- `registry.for_script(script) -> LanguagePack`: the usable pack for a script, preferring the
  transcript's language when two packs share a script (e.g. `fr` and `en` both `Latn`).
- `registry.versions() -> dict[str, str]`: each usable pack's code `version` plus `data_sha256`,
  the sha256 the registry computes over its data files at load (cached per `current` folder);
  part of `word_check`'s fingerprint (52.6). A pack upgrade re-checks only transcripts that pack
  touched. Epic 51 packs move with the server version and land in a new `<version>_<sha12>`
  folder on every update (Epic 51 §4.9; the unpacker keeps the files, not `pack.json`,
  `cmd/maktaba-server/internal/packs/update.go`), so the fingerprint uses the files' digest,
  never the folder name: an update whose dictionary bytes did not change re-checks nothing.
- The registry reloads when the pipeline's component runner sees a desired-state change
  (`components_changed`, `pipeline/src/maktaba_pipeline/components/runner.py`), so installing
  `lang.hunspell.fr` needs no worker restart.

### 3. Script runs (code-switching)

`language_packs/scripts.py` splits a sentence into runs by Unicode `Script` property
(`unicodedata2` or ICU's `uscript`). Common and Inherited characters (digits, punctuation,
harakat) join the run around them. Each run goes to `for_script`. A run in a script with no
enabled pack uses the fallback, which never says `UNKNOWN` (so it never adds
`not_in_dictionary`).

### 4. The generic Hunspell pack (about 100 languages)

LibreOffice's Hunspell dictionaries cover about 100 languages. One generic pack class serves all
of them, and each language's data installs as its **own Epic 51 asset** (`lang.hunspell.fr`,
`lang.hunspell.de`, `lang.hunspell.tr`, …, 52.16) carrying only that language's `.aff`/`.dic`
files, its `NOTICE` and a `samples.txt`:

- `normalise`: NFC, strip bidi controls; the dictionary's `ICONV` table if it has one.
- `tokens`: ICU word break (UAX #29), with the dictionary's `WORDCHARS` joined into words.
- `known`: Hunspell on the token as written, then lower-cased: `KNOWN`, else `UNKNOWN`; a
  capitalised mid-sentence unknown word is `UNSURE` (a likely name).
- `suggest`: Hunspell's suggestions.
- `protected`, `scripture`: nothing.

A dictionary's licence (GPL, LGPL, MPL, BSD and others vary by language) is recorded in the
pack's `NOTICE`; the data ships as its own pack, never in the app payload. A language gets a dedicated
pack (like `ar` and `en`) only when Hunspell is not enough, e.g. Arabic's morphology.

### 4a. The fallback pack

For any language Whisper hears that has no full pack installed:

- `normalise`: NFC, strip bidi controls.
- `tokens`: ICU word break (UAX #29, `PyICU` `BreakIterator.createWordInstance`), so languages
  without spaces (Thai, Japanese, Chinese) get real words. Without PyICU, a `\w+` regex.
- `known`: `wordfreq`'s Zipf frequency: at or above 2.5 `KNOWN`, below `UNSURE`. Never `UNKNOWN`
  from frequency alone, so the fallback never adds `not_in_dictionary`.
- `suggest`: none.
- `protected`, `scripture`: nothing.

The settings page (52.14) shows, for each language heard in the library, whether a full pack
exists and offers to install its `lang.hunspell.<code>` component.

### 5. Epic 51 (configurable components), as built

Packs are not a component kind of their own: Epic 51's kinds are `process`, `stages`, `loop`,
`extension` and `asset` (`shared/components/catalog.toml` header). So:

- the pack **code** (`ar`, `en`, `hunspell`, `fallback`) is part of the pipeline package and runs
  inside `word_check`, which is the `review.words` component (`stages`);
- the pack **data** is an `asset` component per language (`lang.ar`, `lang.en`,
  `lang.hunspell.<code>`), and the fallback's wheels are `runtime.lang`;
- install, update, remove, holds and the UI are Epic 51's (Components page, group `languages`);
  this story designs no installation UI.

The catalog entries, the pack recipes and the pipeline's way to find a pack's folder are
story 52.16.

### 6. Conformance suite

`language_packs/conformance.py`: a pytest-parametrised suite any pack runs against itself. It
checks the rules in §1 with property tests (Hypothesis) over the pack's own sample sentences
(`samples.txt` shipped with the pack), and checks determinism by running every method twice.
Built-in packs (and the generic Hunspell pack over 3 fixture dictionaries) run it in CI. Epic 54
will run it on third-party packs before its store lists them.

## Acceptance criteria

1. **Given** the pipeline installed with `runtime.lang`, `lang.ar` and `lang.en` on, **when** the
   registry loads, **then** `ar`, `en` and `fallback` are `loaded` in `language_packs`, and a
   Hunspell language whose component is absent is not listed.
2. **Given** `lang.hunspell.fr` installed and enabled (`POST /api/components/lang.hunspell.fr/install`),
   **when** the pipeline sees `components_changed` (no restart), **then** the registry lists `fr` (generic Hunspell pack), and `for_script("Latn")`
   returns `fr` for a French transcript, `en` for an English one.
3. **Given** the sentence «قال النبي ﷺ in the hadith…», **when** split, **then** the Arabic run goes
   to `ar`, the English run to `en`, and token offsets are code points into the original text.
4. **Given** a Japanese sentence and no `ja` pack, **when** tokenised, **then** the fallback uses
   ICU word breaks (more than one token for a sentence without spaces).
5. **Given** a word in a language with no full pack, **when** the fallback's `known()` runs,
   **then** it returns `KNOWN` or `UNSURE`, never `UNKNOWN`.
5a. **Given** a French misspelling «bonjoru», **when** the `fr` Hunspell pack checks it, **then**
   `UNKNOWN`, with «bonjour» among the suggestions; the same call twice gives the same answer.
6. **Given** `lang.ar` removed (`DELETE /api/components/lang.ar`), **when** an Arabic run comes up,
   **then** the fallback handles it and the `ar` row says `missing`.
7. **Given** any built-in pack, **when** the conformance suite runs, **then** tokens cover the text,
   `key` is idempotent, `known` never raises on 1,000 random strings.
8. **Given** a pack's version changes, **when** `registry.versions()` is read, **then** the change
   shows, and 52.6's fingerprint changes only for transcripts in that pack's language.
9. **Given** a grep of `word_review/` and `engine/stages/word_*.py` in CI, **when** it finds a
   language literal (`"ar"`, `"en"`), **then** the lint fails.

## Build plan

### Files

- `pipeline/src/maktaba_pipeline/language_packs/__init__.py`, `base.py`, `registry.py`,
  `scripts.py`, `hunspell.py` (the generic pack), `fallback.py`, `conformance.py` (new).
- `pipeline/pyproject.toml`: a `lang` extra (the `runtime.lang` pack's wheels, 52.16) with `wordfreq`, `PyICU` (optional on
  platforms without ICU wheels), `spylls` (pure-Python Hunspell reader, MPL-2.0) or `cyhunspell`.
- `shared/db/migrations/` next free slot: `language_packs` (the report), Postgres and SQLite twins.
- `tools/lint/`: the language-literal check for word review code.

### Tests

- `pipeline/tests/language_packs/test_registry.py`: the pack table, a component off or its
  folder missing (fallback), a `components_changed` reload, `data_sha256` unchanged across a new
  `<version>_<sha12>` folder with the same bytes, script preference; report rows on SQLite and
  scratch Postgres.
- `test_scripts.py`: runs on mixed Arabic/English/digits/harakat; offsets.
- `test_fallback.py`: ICU and regex paths, wordfreq thresholds.
- `test_hunspell_pack.py`: the generic pack over small fixture dictionaries (French, German,
  Turkish), `WORDCHARS`, `ICONV`, capitalised unknowns as `UNSURE`.
- `test_conformance_builtin.py`: the suite over `ar`, `en`, `hunspell` (fixtures), `fallback`.
- No real model loads. Packs' data is small test data in fixtures.

### Rollout

- No user-visible change. Packs load only when `word_check` (52.6) runs.
- Back out: revert; the report table is unused.

## Edge cases

- A script shared by languages that spell alike (Persian and Urdu in `Arab`): a transcript in
  `fa` uses a `fa` pack if installed, else the fallback, never `ar` (Arabic's dictionary would
  flag every Persian word).
- Arabic-Indic digits in an English run: `Common` script, joined to the run, kind `number`.
- PyICU missing on a worker: the fallback uses the regex tokenizer and records
  `tokenizer=regex` in the pack row, so results stay reproducible per machine.
- A broken pack (raises on load): the registry logs it, marks the row `state = 'error'` with the
  error, and uses the fallback. The asset itself stays *Installed* in Epic 51's terms; the
  pipeline runner's status row for `review.words` (part `pipeline`) carries
  `detail.last_error`, shown in the Components page health block (51.14).
