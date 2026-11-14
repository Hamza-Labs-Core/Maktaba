# Story 54.50 — Two variants of every server release: Full and Core

> Epic 54 · Phase 5 · Depends on: 54.45, 54.46, 54.48, Epic 51 (51.15, 51.17) · Needed by: 54.51 · Not built

## Why

A person who wants only a media home should not download speech models and ML wheels, and a person who wants everything should not visit a store before the home works. Each server release therefore comes in two variants on every platform. The small one is not a new idea: it is the `core` bundle the catalog and setup already have, so there is one notion of "small", with one list.

## Description

### 1. One definition

- **Full** is the `full` bundle: Maktaba's plugins come with it, preinstalled, and a fresh home works as today with no store visit and no internet.
- **Core** is the `core` bundle and nothing else: no other Maktaba plugin inside, and none of the ML wheels or models the left-out plugins need. The admin installs the plugins they want from the store or from files.
- The release variant and setup's bundle choice are the same concept and the same list, kept in `shared/components/catalog.toml` (`bundles`). No second list is added anywhere.
- **The name.** This story proposes the one name **Core** (Arabic: «الأساسي») for the small variant and **Full** («الكامل») for the other, in the app, in setup and on the download page, because setup and the Mac download already say Core. "Lite" is not used.

### 2. What `core` contains today, and where it disagrees with "core is not a plugin"

Today the `core` bundle is: `core.postgres`, `core.api`, `core.streaming`, `core.pipeline`, `core.plugin_runners`, `core.node`, `core.updates`, `core.switchover`, `media.thumbnails`, `library.convert`, `library.dedup`, `plugins.host`, `discovery.mdns`, `queue_health`, `analytics`, `backups` and `updates.auto`.

Story 54.44 says what must always run is core and is not a plugin, and that every switchable component a Maktaba plugin implements is a plugin. Three members of the `core` bundle are such plugins. This story does not redefine the bundle; each is an open question:

| Item | In the `core` bundle today | Under "core is not a plugin" | Question |
|---|---|---|---|
| Thumbnails (`media.thumbnails`, `tv.maktaba.thumbnails`) | yes | a Maktaba store plugin | Does the Core variant preinstall it, or leave it to the admin? |
| Convert (`library.convert`, `tv.maktaba.convert`) | yes | a Maktaba store plugin | the same |
| Dedup (`library.dedup`, `tv.maktaba.dedup`) | yes | a Maktaba store plugin | the same |

If they stay in the bundle, the Core variant carries three Maktaba plugins preinstalled and "Core" means "the `core` bundle", not "no plugins". If they leave it, the `core` bundle changes for setup too, and existing Core homes keep the three installed (54.48 §4). Either way the bundle stays the one definition.

### 3. Per platform

The server packages a release builds today, and the variant each gets:

| Package (today's artifact) | Full | Core |
|---|---|---|
| Mac app (`Maktaba_<v>_aarch64.dmg`, `Maktaba-Core_<v>_aarch64.dmg`) | exists | exists; today it differs only by leaving out Ollama |
| Windows app (NSIS `-setup.exe`, portable `.zip`) | one installer today | to add |
| Linux app (`.AppImage`, `.deb`, `.rpm`, x86_64 and aarch64) | the `maktaba-full` package adds the ML and `yt-dlp` packs offline | plain `maktaba` acts as Core |
| Server archives (`maktaba-server-<v>-<os>-<arch>.tar.gz`, `.zip`) and `maktaba-server` `.deb`/`.rpm` | to add | today's archive, which carries no packs |
| Docker and NAS image (`maktaba-server`), the TrueNAS app | to add as a tag | today's image |
| Node packages for joined machines (`maktaba-node-<v>`) | not a home: follows 54.48 §5 | the same |

Artifact names add the variant as a word after the product name, as the Mac app already does (`Maktaba-Core_…`); images add a tag suffix (`:<v>-core`). The unsuffixed name stays what it is today on each platform so existing update feeds keep working.

### 4. The download page

`web/packages/website/src/downloads.json` gains the Core file beside each platform's primary file, and `copy/download.en.md` and `copy/download.ar.md` say in the same words on every card: Full has everything and works with no internet; Core is smaller and you add plugins from the store. Sizes shown are read from the release's files, never written by hand.

### 5. Sizes

The sizes of each variant per platform are to be measured from a built release and recorded here and on the download page. None is stated in this story.

## Tests

- The release workflow builds both variants for every platform in §3 and lists each in the signed checksums.
- A fresh Core install has exactly the `core` bundle's components on, and its installer contains no file of a left-out plugin or of `runtime.ml` (checked from the archive listing).
- The download page lists both variants for every platform in English and Arabic.
