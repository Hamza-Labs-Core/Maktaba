# Story 52.16 — Word review and language packs in the component catalog

> Epic 52 · Phase 1 · Depends on: 52.3, Epic 51 (51.01 catalog, 51.15 packs and the index,
> 51.16 install and remove, 51.18 updates carry packs) · Needed by: 52.4, 52.5, 52.6, 52.14

## Why

Epic 51 as built decides what runs from one embedded file, `shared/components/catalog.toml`.
A stage with no owner there fails the `catalog` CI job, and the runners treat it as part of
nothing. Word review needs a component for its stage and one per language's data. Epic 51 gives
the model but not these pieces:

- **The catalog is fixed per release.** It is embedded in every binary (Go `go:embed`, the
  pipeline's byte-for-byte copy from `make catalog-sync`) and both loaders refuse an unknown
  field. Nothing adds a component at run time, so every language pack must be a catalog entry
  shipped in a Maktaba release.
- **Packs are first-party assets only.** `release.yml`'s `package-packs` job builds one
  `<id>-<version>-<platform>.tar.zst` per asset from a recipe in `tools/release/packs/recipes/`
  (today `runtime.ml.sh` and `tool.yt-dlp.sh`). The signed index `components-<version>.json`
  (`shared/go/releaseverify/components.go`) lists them under the release's Sigstore identity, and
  a pack is valid only for that exact server version (`ServerRange` is `=<version>`).
  `cmd/maktaba-server/internal/packs` installs it into `<state>/components/<id>/<version>_<sha12>/`
  and writes the `current` pointer file. There is no recipe for dictionary data, and packs are
  always per platform, even for bytes that are the same on every platform.
- **The pipeline cannot find a data pack.** The supervisor feeds wheel packs (`runtime.ml`,
  `tool.yt-dlp`) to the venv builder through `Prepare` hooks (`cmd/maktaba-server/serve_packs.go`).
  No environment variable or helper gives the pipeline a data pack's folder.

## Description

### 1. The catalog entries

Added to `shared/components/catalog.toml` (and its pipeline copy), with the fields the loaders
accept. `since` is the first release that carries the entry.

```toml
[[component]]
id        = "review.words"
kind      = "stages"
group     = "processing"
title_key = "components.review.words.title"
desc_key  = "components.review.words.desc"
stages    = ["word_check"]
requires  = ["runtime.lang"]
wants     = ["lang.ar", "lang.en", "model.refine"]
settings  = ["rules.word_review"]
platforms = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
drain     = { grace_s = 30, checkpoint = false }
bundles   = ["full", "worker"]
since     = "<release>"

[[component]]
id         = "runtime.lang"
kind       = "asset"
group      = "runtimes"
title_key  = "components.runtime.lang.title"
desc_key   = "components.runtime.lang.desc"
# the wheels of the pipeline's lang extra (wordfreq, PyICU, spylls), fed to the venv builder
source     = "pack:runtime.lang"
in_payload = false
platforms  = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
bundles    = ["full", "worker"]
since      = "<release>"

[[component]]
id         = "lang.ar"
kind       = "asset"
group      = "languages"
title_key  = "components.lang.ar.title"
desc_key   = "components.lang.ar.desc"
requires   = ["runtime.lang"]
# Ayaspell Hunspell ar; CAMeL's morphology DB stays model.refine
source     = "pack:lang.ar"
in_payload = false
platforms  = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]
bundles    = ["full", "worker"]
since      = "<release>"
```

`lang.en` is the same as `lang.ar` (SCOWL `en_US` + `en_GB`). Each Hunspell language is
`lang.hunspell.<code>` (`lang.hunspell.fr`, `lang.hunspell.de`, …), the same shape with
`bundles = []`: installed when an admin asks, never by a bundle.

- **Placement** stays `any` (the default): `word_check` runs on any machine with the packs.
  A machine gets the assets its effective desired state wants (51.12), so a worker that runs
  `review.words` installs `runtime.lang`, `lang.ar` and `lang.en` itself.
- **Requirements**: `runtime.lang` is hard (no tokenizer without it: *Waiting*,
  `dependency_off`); the languages and `model.refine` are `wants` (missing: *Degraded*, the
  fallback pack covers the language). The Hunspell languages are in nobody's `wants`, so having
  few of them never makes `review.words` degraded.
- **No `features` entry.** Word review is admin-only; `GET /api/capabilities` keeps its nine
  features.
- **Words**: `components.<id>.title` and `.desc` for every new id in `web/src/i18n/en.json` and
  `ar.json`; the `catalog` job (`tools/ci/catalog_words.mjs`) fails without both. The group
  `languages` already has its words (`components.group.languages`) and its place on the
  Components page (`web/src/pages/Settings/componentWords.ts`).
- **Reasons**: none new. The pack failures use Epic 51's (`download_failed`, `verify_failed`,
  `disk_space`, `pack_missing`, …).

### 2. The Hunspell languages come from one list

There are about 100 LibreOffice dictionaries, and the catalog has no templated entries.
`tools/release/packs/hunspell-languages.toml` lists each one: code, LibreOffice folder, the
English and Arabic names, and the licence. `make catalog-sync` writes the `lang.hunspell.*`
block of `catalog.toml` and their en/ar words from it, and `make catalog-check` fails when they
differ. The first release lists 12 (fr, de, es, it, pt, nl, tr, ru, id, ms, sv, pl); the list
grows by PR, one release at a time.

### 3. The pack recipes

- `tools/release/packs/recipes/runtime.lang.sh`: `exec bash "$here/../wheelhouse.sh" "$1" "$2"
  "$3" --extra lang`, as `tool.yt-dlp.sh` does for `mediaintel`.
- `tools/release/packs/recipes/lang.ar.sh`, `lang.en.sh` and one shared `lang.hunspell.sh`
  (linked per id by the list): copy the `.aff`/`.dic`, `NOTICE`, the licence text and the
  pack's `samples.txt` from the LibreOffice dictionaries repository at the commit pinned in
  `tools/release/packs/lang-sources.toml`, checking each file's sha256 there. A recipe only
  gathers files: nothing in a pack runs at install.
- Data packs are built per platform like every pack (`build.sh` builds what the catalog asks of
  each platform), so the four are the same bytes. They are small (under 5 MB each); a
  platform-independent pack is not worth a change to Epic 51's index.
- Each pack moves with the server version: an update downloads the new `lang.*` packs before it
  switches and a rollback goes back to the kept ones (51.18). The registry's `data_sha256` (52.3)
  keeps a same-bytes update from re-checking the library.

### 4. The pipeline finds a pack's folder

- The supervisor adds `MAKTABA_COMPONENTS_DIR` (`packsDir(cfg)`, `<state>/components`) to the
  pipeline's environment (`cmd/maktaba-server/internal/supervisor/env.go`), next to
  `MAKTABA_MODEL_CACHE_DIR`.
- `pipeline/src/maktaba_pipeline/components/packs.py` (new): `pack_dir(id) -> Path | None` reads
  `<root>/<id>/current` with the rules of `packs.Layout.Current` (one line, no path separator,
  not starting with a dot, an existing folder) and answers `None` otherwise. A folder with
  `.removing` is `None`.
- **The pipeline's gate reads desired state only.** An asset requirement is met when its want is
  not `absent` (`pipeline/src/maktaba_pipeline/components/stages.py`), not when its files are on
  this machine; only the supervisor's reconciler waits for a pack's install, and only for
  processes (`cmd/maktaba-server/internal/supervisor/reconcile.go` `requirements`). So
  `word_check` checks `pack_dir("runtime.lang")` itself and returns `JobDeferred` (no attempt
  used) while it is missing, and the registry treats a missing `lang.*` folder as "fallback".
- `runtime.lang` is fed to the venv like `runtime.ml`: `serve_packs.go` gains
  `{"runtime.lang", []string{"lang"}}` in the extras list and `runtime.lang` in the `Prepare` and
  `Cleanup` maps.

### 5. Turning them on and off

All Epic 51 routes and CLI, admin-only (51.08): `PUT /api/components/{id}`,
`POST /api/components/{id}/install`, `/stop`, `/start`, `DELETE /api/components/{id}`,
`POST /api/components/preview` and `/apply`, and `maktaba-server components install
lang.hunspell.fr`. Removing `runtime.lang` while `review.words` is enabled is refused
(`409 component_required_by`). The offline file (`POST /api/components/offline`) carries the
`lang.*` packs a bundle needs, like any asset.

An asset's want is `installed` or `absent`: it never runs, so the seed never enables one
(`cmd/maktaba-server/internal/componentseed/seed.go`). Turning a language off is removing it.

## Acceptance criteria

1. **Given** the new catalog, **when** `make catalog-check` runs, **then** `word_check` has
   exactly one owner (`review.words`), every new `title_key` has English and Arabic words, and the
   Go and Python dumps agree.
2. **Given** a release build, **when** `package-packs` runs, **then** the signed index lists
   `runtime.lang`, `lang.ar`, `lang.en` and the listed `lang.hunspell.*` packs for every platform,
   and each pack's files match `lang-sources.toml`'s sha256.
3. **Given** a Full home, **when** an admin runs `maktaba-server components install
   lang.hunspell.fr`, **then** the pack is installed into `<state>/components/lang.hunspell.fr/`,
   `pack_dir("lang.hunspell.fr")` answers its folder, and `component_status` shows it installed
   (part `packs`).
4. **Given** `runtime.lang` `absent`, **when** `review.words` is enabled, **then** its phase is
   *Waiting* with `dependency_off`, and no `word_check` job is claimed. **Given** `runtime.lang`
   `installed` but still downloading, **then** `word_check` jobs defer without using an attempt
   until `pack_dir("runtime.lang")` answers.
5. **Given** `lang.ar` missing, **when** `review.words` runs, **then** `GET /api/components/review.words`
   says *Degraded* (`want_missing:lang.ar`) and Arabic runs use the fallback.
6. **Given** `DELETE /api/components/runtime.lang` while `review.words` is enabled, **then**
   `409 component_required_by` naming `review.words`.
7. **Given** a viewer, a play sign-in or no sign-in, **when** they call any of these routes for
   these ids, **then** 403 / 401 (Epic 51's role tests cover the routes; this story adds the ids
   to their table).
8. **Given** a `current` pointer naming `../x` or a missing folder, **then** `pack_dir` answers
   `None`.
9. **Given** an update to the next release with the same dictionary bytes, **then** the new
   `lang.*` packs install before the switch, and no `word_check` job is queued by it.

## Build plan

### Files

- `shared/components/catalog.toml`, `pipeline/src/maktaba_pipeline/components/catalog.toml`
  (`make catalog-sync`), `shared/components/testdata/catalog.dump.json`.
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`: the new titles and descriptions.
- `tools/release/packs/recipes/runtime.lang.sh`, `lang.ar.sh`, `lang.en.sh`, `lang.hunspell.sh`;
  `tools/release/packs/lang-sources.toml`, `tools/release/packs/hunspell-languages.toml` (new).
- The `catalog-sync` / `catalog-check` targets: the Hunspell block from the list.
- `pipeline/pyproject.toml`: the `lang` extra.
- `cmd/maktaba-server/serve_packs.go`: `runtime.lang` extras and hooks.
- `cmd/maktaba-server/internal/supervisor/env.go`: `MAKTABA_COMPONENTS_DIR`.
- `pipeline/src/maktaba_pipeline/components/packs.py` (new).

### Tests

- `shared/components/catalog_test.go` and `pipeline/tests/components/test_catalog.py`: the new
  ids, kinds and requirements; `review.words` owns `word_check`.
- `tools/release/packs/test_build.sh`: the `lang.*` recipes on a fixture source, sha256 checked,
  a wrong sha refused.
- `cmd/maktaba-server/serve_packs_test.go`: `runtime.lang` prepare and cleanup rebuild the venv
  with the `lang` extra.
- `cmd/maktaba-server/internal/supervisor/pyenv_test.go`: `MAKTABA_COMPONENTS_DIR` set, kept when
  already set.
- `pipeline/tests/components/test_packs.py`: `pack_dir` on good, missing, bad-name and
  `.removing` folders.
- API role tests (`api/internal/handlers/components`): the new ids in the admin-only table.

### Rollout

- Ships with the release that ships 52.6, together with 52.17, so an update leaves a Core home
  with `review.words` off. `update-rehearsal` (51.18) installs the new packs on a Full fixture
  home and checks their phases after the switch.
- Back out: revert. The ids leave the catalog; their desired rows stay and are ignored by older
  code, as for any id a release drops.

## Edge cases

- **A language whose dictionary licence does not allow redistribution:** it is left out of
  `hunspell-languages.toml`; the fallback covers it.
- **PyICU has no wheel for a platform:** `runtime.lang` installs without it and the fallback
  uses the regex tokenizer (52.3 records `tokenizer=regex`).
- **`[components] packs = "off"`:** nothing new installs (`packs_disabled`); a pack already on
  disk keeps working.
- **A Core home that wants word review:** the admin enables `review.words` and installs
  `runtime.lang` (and `lang.ar`, `lang.en`) in one `POST /api/components/apply`; the preview
  shows `review.words` *Waiting* until `runtime.lang` is in, and *Degraded* without
  `model.refine` (no Arabic morphology without `runtime.ml`).
