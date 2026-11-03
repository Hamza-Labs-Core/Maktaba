# Story 54.38 — Plugin files on disk, on Epic 51's packs code

> Epic 54 · Phase 1 · Depends on: 54.01, 54.37, Epic 51 (51.15, 51.16) · Needed by: 54.07,
> 54.10, 54.17

## Why

Epic 51 already puts components on disk safely: `cmd/maktaba-server/internal/packs` keeps one
folder per component under `<state>/components/<id>/`, each version in
`<version>_<sha12>/`, an atomically written `current` pointer file (no symlinks, for
Windows), a `.lock` across processes, `.partial/` for resumable downloads, `.staging-*`
folders and `.removing` markers, and `Layout.Recover` at boot. `shared/go/componentpack`
unpacks a `.tar.zst` with every rule of Epic 51 §6.4 (no absolute paths, no `..`, no hard
links or devices, every file's size and sha256 checked as it is written).

None of it takes a plugin as it is:

- `packs.Manager.Install`, `Remove` and `packs.Runner` act only on `kind = asset`;
- every pack is checked against the release's signed `components-<version>.json`
  (`shared/go/releaseverify/components.go`, Sigstore release identity, `server` = the exact
  server version); a plugin is signed by its developer and the store (54.14), not by the
  release, and has its own version;
- `componentpack.Unpack` expects `pack.json` and `files.sha256` first; a `.mkplugin` carries
  `manifest.toml` and `files.json` (54.01);
- `Layout.Recover(version)` re-points `current` at the folder named `<server version>_*`. A
  plugin whose own version happens to equal the server's would be re-pointed wrongly.

This story reuses the code and leaves the release's packs untouched.

## Description

### 1. Its own root

Plugins live in `<state>/plugins/<plugin id>/` (beside `<state>/components/`), as a
`packs.Layout{Root: <state>/plugins}`: the same `current` pointer, `.lock`, `.staging-*`,
`.removing` and folder names (`<plugin version>_<sha12 of files.json>/`). Packs never see this
root and the release's index never lists a plugin.

### 2. Recover per plugin version

`Layout.RecoverWith(func(id string) string)` takes the expected version per id. For plugins it
is the `plugin_installs.version` of that id (54.07); for packs it stays the server version, so
`Recover(version)` keeps its behaviour. Staging folders are deleted as today.

### 3. Unpacking a `.mkplugin`

`componentpack.UnpackPlugin(r, dest, Expect{FilesSHA256})` shares `Unpack`'s entry rules and
differs only in its head: `manifest.toml` and `files.json` first, `developer.sig` and
`store.sig` allowed beside them, and the digest checked is the one 54.14's verifier passed.
Signatures are checked before the first byte is unpacked (54.14), never by this reader.

### 4. Who does what

- The **API** verifies (54.14), validates the manifest (54.01) and writes `plugin_installs` and
  the desired row (54.07). It never writes the plugin folder.
- The **supervisor** does the files: a `plugins.Runner`, built like `packs.Runner`, reads the
  desired rows of `plugin.*` entries (54.37), unpacks a new version to staging, renames it into
  place, writes `current`, removes on `want = absent` after the drain, and reports its part
  (`plugins`) in `component_status`: `installing`, `removing`, `failed` with
  `verify_failed`, `unpack_refused`, `disk_space` or `busy`, the reason codes 51.16 already has.
- The loopback routes stay the supervisor's (per-boot token): `POST /plugins/{id}/install`
  (the verified package path), `POST /plugins/{id}/remove`, `GET /plugins/{id}/progress`,
  answering like `/components/{id}/…` (202, progress, 409 `busy`).
- `[components] packs = "off"` does not stop plugin installs; `[plugins] install = "off"`
  (54.10) does.

### 5. Offline files

51.16's offline file (`POST /api/components/offline`) carries packs and models only. A
`.mkplugin` is its own offline file (54.10). Importing an offline packs file never installs a
plugin.

## Acceptance criteria

- **Given** a store-signed `.mkplugin`,
  **when** it is installed,
  **then** `<state>/plugins/<id>/<version>_<sha12>/` holds its files, `current` names that
  folder, and `<state>/components/` is unchanged.
- **Given** plugin version `0.9.0` installed on server `0.9.0` and an older `0.8.0` folder kept
  for rollback with `plugin_installs.version = 0.8.0`,
  **when** the server boots,
  **then** `current` names `0.8.0_*`.
- **Given** a `.mkplugin` with a `..` path or a hard link,
  **when** it is unpacked,
  **then** nothing stays and the status reason is `unpack_refused`.
- **Given** a crash between rename and pointer,
  **when** the server boots,
  **then** `current` names the new folder and the staging folder is gone.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go) | fixture `.mkplugin` | `UnpackPlugin` | files and digest checked |
| T02 | unit (go) | hostile tars (`..`, absolute, hard link, device) | `UnpackPlugin` | refused, dest removed |
| T03 | unit (go) | versions equal to the server's | `RecoverWith` | pointer by `plugin_installs` version |
| T04 | integration (go) | supervisor `plugins.Runner` | install, remove, crash mid-rename | phases and reasons as §4 |
| T05 | unit (go) | packs `Recover(version)` | unchanged fixtures | same answers as before |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/packs/layout.go` | `RecoverWith`. |
| `shared/go/componentpack/unpack.go` | `UnpackPlugin`. |
| `cmd/maktaba-server/internal/plugins/` (new) | `Runner`, loopback routes. |
| `cmd/maktaba-server/serve_reconcile.go` | Start the plugins runner beside the packs runner. |

### Tests

- Unit T01–T03, T05; integration T04.

### Rollout

Dark until the first plugin install.

### Rollback

Release rollback. The `<state>/plugins/` folder is ignored by older releases.

### Dependencies

54.01 (the package), 54.37 (plugin entries); Epic 51 51.15 (`componentpack`), 51.16
(`packs.Layout`, `Recover`, the supervisor's install routes).
