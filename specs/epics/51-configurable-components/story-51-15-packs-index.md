# Story 51.15 — Packs and the signed component index

> Epic 51 · Phase 3 · Depends on: 51.01 · Needed by: 51.16, 51.18

## Why

Some components need files the payload does not carry: the ML wheels for `runtime.ml`, the
ollama binary for `runtime.ollama` on a Core Mac, the models. Today these come from three
places with three levels of trust:

- the venv's wheels come from PyPI, each checked against its `--hash` in the release's
  `constraints.txt` (`pipeline/src/maktaba_pipeline/venv_bootstrap.py`);
- the Whisper, Cohere and embedder models come from Hugging Face at whatever revision is
  newest (`pipeline/src/maktaba_pipeline/models/fetch_cli.py`), checked by nothing but TLS;
- ollama models come from Ollama's registry (`cmd/maktaba-server/internal/llmserver`).

A component that can be added later needs one answer to "what exactly do I download for this
release, and how do I know it is ours?". This story builds it: **packs**, built by the release
job from the same commit, and one **index** per release that lists every pack and every model
file with its sha256 and size, signed the way releases are signed today. It ships no installer
(51.16) and changes nothing on a running home.

## Description

### 1. What a pack is

A pack is a `.tar.zst` of the files one `asset` component needs on one platform, built by the
release from the commit being released. File name: `<id>-<version>-<platform>.tar.zst`, for
example `runtime.ml-0.4.2-darwin-arm64.tar.zst`. Platforms use the catalog's names
(`darwin-arm64`, `windows-amd64`, `linux-amd64`, `linux-arm64`).

Inside, always:

```
pack.json        {"schema":1,"id":"runtime.ml","version":"0.4.2","platform":"darwin-arm64",
                  "files_sha256":"<sha256 of files.sha256>","unpacked_bytes":2140000000}
files.sha256     one line per entry, the Linux payload's format (payload_tool.py):
                 f <mode> <size> <sha256> <path>   |   l <target> <path>
<the files>
```

What each first-party pack holds:

| Pack | Contents | Built from |
|---|---|---|
| `runtime.ml` | `wheels/` (every wheel of the `stt` and `search` extras for the platform, plus `index.json`, the wheelhouse format `appvenv` already reads), `constraints.txt` (and `constraints-cuda.txt` on Windows) | `uv.lock` through `deploy/packaging/node/build-release.sh`'s constraints, `pip download --only-binary=:all: --platform …` |
| `tool.yt-dlp` | `wheels/` for the `mediaintel` extra, `constraints.txt` | same |
| `runtime.ollama` | `ollama/ollama`, `ollama/llama-server` (darwin-arm64 only today) | `OLLAMA_URL` / `OLLAMA_SHA256` in `tools/release/mac-app/pins.env`, re-signed like `payload.sh` does when a signing identity is set |
| `lang.*` | Epic 52 decides; the format is the same | — |

No pack holds a script. Nothing in a pack is run at install time (§6.4).

### 2. Models are pinned, not packed

Models stay on Hugging Face (H6). Their catalog entries (51.01) already hold
`source = "hf:<repo>@<revision>"` (a commit sha, never a branch) and `files = [{path, sha256,
size}]`. A small tool, `tools/release/cmd/pin-models` (new), fills those from Hugging Face's
API (`lfs.oid` is the sha256) in a normal PR; the release only copies them into the index.
Ollama models (`model.llm.<name>`) are pinned the same way by their registry manifest digest
and each layer's sha256 (`source = "ollama:qwen2.5:7b@sha256:<manifest>"`).

### 3. The index: `components-<version>.json`

One per release, written by `tools/release/cmd/gen-components-index` (new):

```json
{ "schema": 1, "version": "0.4.2", "commit": "<sha>",
  "published": "2026-11-20T10:00:00Z", "expires": "2027-11-20T10:00:00Z",
  "server": ">=0.4.2 <0.4.3",
  "packs": [ { "id": "runtime.ml", "platform": "darwin-arm64",
               "name": "runtime.ml-0.4.2-darwin-arm64.tar.zst",
               "sha256": "…", "size": 812000000, "unpacked_bytes": 2140000000,
               "files_sha256": "…", "requires": [] } ],
  "models": [ { "id": "model.whisper-turbo", "source": "hf:mlx-community/whisper-large-v3-turbo@<rev>",
                "platforms": ["darwin-arm64"], "gated": false,
                "files": [ { "path": "weights.safetensors", "sha256": "…", "size": 1617000000 } ] } ] }
```

- `published` is the commit time (a re-run writes the same file, as `release.json` does today).
- `expires` is `published` + **365 days**.
- `server` is always the exact version: first-party packs move with the server (H5).
- `release.json` gets `"components": {"index": "components-0.4.2.json", "sha256": "…"}` so the
  updater finds it without guessing a file name (`shared/go/releaseverify/manifest.go`).

### 4. Signed by the existing release identity

The index and every pack are attached to the same GitHub release and listed in the release's
`checksums.txt`. `checksums.txt` is already signed by keyless cosign in the `sign-and-release`
job (`checksums.txt.sigstore.json`), with the identity `releaseverify` pins:
`.github/workflows/release.yml`, a tag push, the exact tag, the repository's numeric ids
(`shared/go/releaseverify/identity.go`). So there is **one signature per release**, no new key,
no new secret.

Verifying an index (`releaseverify.VerifyComponentsIndex`, new, in
`shared/go/releaseverify/components.go`):

1. Verify `checksums.txt` against its Sigstore bundle with the trusted root the updater uses
   (`root.go`, refreshed through TUF; `root_e2e.go` under `-tags e2etrust` for tests).
2. Check the index's sha256 is the one `checksums.txt` lists, and `release.json`'s
   `components.sha256` agrees.
3. Parse; refuse unknown `schema`.
4. Refuse when `now > expires` (`index_expired`).
5. Refuse when `version` is not this server's version (`index_wrong_version`).
6. Refuse when this machine has seen a newer index **for the same version**: a higher
   `published`, or the same `published` with a different sha256 (`index_replayed`). The newest
   seen per version is kept in `<data>/state/components-index.json` and only goes up.

A pack is then checked against the index entry: size first (refuse before reading more), then
sha256 of the whole file, then, after unpacking (51.16), every file against `files.sha256`.

### 5. Built in the release job

A new job `package-packs` in `release.yml`, a matrix over the four platforms (Linux runners
for the wheelhouses, which `pip download --platform` fetches for any target; a `macos-15` leg
for `runtime.ollama`). It runs `tools/release/packs/build.sh` (new), uploads the packs as an
artifact, and `sign-and-release` adds them and the index to `dist/` **before** "Generate
checksums", so they are covered by the one signature. `release-assets.sh` learns the new names.
`gen-release-json --check` fails a release whose index lists a pack that is not in `dist/`.

Packs are reproducible from the commit: `SOURCE_DATE_EPOCH`, sorted tar entries, uid/gid 0,
fixed zstd level 19. A re-run of the job produces the same bytes, so a re-run's `checksums.txt`
does not change (the rule `release-assets.sh` already enforces).

### 6. Why this design (H6)

| Option | Taken? | Why |
|---|---|---|
| List packs and index in the signed `checksums.txt` | **yes** | one signature, the identity and trust root already pinned and tested |
| A second Sigstore bundle on the index alone | no | two signatures on one release to keep in step, for nothing more |
| A minisign key for packs | no | a second key to guard and rotate |
| Full TUF | no | four roles for one publisher; we take its expiry and anti-replay rules |
| Re-host models in our release | no | GBs per release; Hugging Face pins + sha256 give the same guarantee |
| Global "refuse an index older than the newest seen" | no | it would block a home rolled back to 0.4.2 after seeing 0.4.3 from installing 0.4.2's packs; the rule is per version, and the `server` range blocks old packs on a new server |

## Acceptance criteria

- **Given** a tagged release,
  **when** `release.yml` finishes,
  **then** the release has one `components-<version>.json` and one pack per (asset component,
  platform) the catalog marks `in_payload = false`, all listed in the signed `checksums.txt`.
- **Given** the index of a release,
  **when** `VerifyComponentsIndex` runs with the production trust root,
  **then** it passes, and every pack's sha256 and size match the index.
- **Given** an index whose bytes differ from `checksums.txt`'s line,
  **when** it is verified,
  **then** it is refused with `verify_failed` and nothing is written.
- **Given** an index past its `expires`,
  **when** it is verified,
  **then** it is refused with `index_expired`.
- **Given** this machine saw the 0.4.2 index published at T,
  **when** a 0.4.2 index published before T is offered,
  **then** it is refused with `index_replayed`.
- **Given** a server at 0.4.3,
  **when** it is offered the 0.4.2 index,
  **then** it is refused with `index_wrong_version`.
- **Given** the same commit built twice,
  **when** both runs finish,
  **then** every pack is byte-identical.
- **Given** a catalog model with a branch name instead of a commit sha as revision,
  **when** the `catalog` lint runs,
  **then** it fails.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fake Sigstore (`releaseverifytest`), a signed release with an index | verify | passes |
| T02 | unit | same, one byte of the index flipped | verify | `verify_failed` |
| T03 | unit | index `expires` yesterday | verify | `index_expired` |
| T04 | unit | watermark for 0.4.2 at T | verify 0.4.2 index at T−1, then same T with other bytes | `index_replayed` both |
| T05 | unit | server 0.4.3 | verify 0.4.2 index | `index_wrong_version` |
| T06 | unit | watermark for 0.4.3; running 0.4.2 (rolled back) | verify 0.4.2 index | passes |
| T07 | unit | signature from another workflow path in the fake Sigstore | verify | `verify_failed` (identity) |
| T08 | unit | `gen-components-index` on a test catalog | generate twice | identical bytes; `server` exact |
| T09 | unit | `packs/build.sh` on a tiny test catalog | build twice | identical `.tar.zst`; `files.sha256` matches |
| T10 | unit | `gen-release-json --check` with an index naming a missing pack | check | fails |
| T11 | lint | catalog model `@main` | `catalog` job | fails with the entry's line |
| T12 | CI | `components-packs` job | build the packs of a PR's commit for linux-amd64 and verify them with the e2e trust root | passes; N10 measured in 51.16 |

## Edge cases

- **A pack too big for GitHub** (2 GB asset limit). `runtime.ml` with CUDA torch on Windows is
  about 2.6 GB. The build splits any pack over 1.9 GB into `…tar.zst.part1`, `.part2`; the
  index lists the parts in order with their own sha256 and the whole file's sha256.
- **A Hugging Face revision deleted upstream.** The download fails, the component stays
  `Failed` with `download_failed`; the next release's `pin-models` run picks a revision that
  exists. The home's published copy on the NAS (`stt/model_share.py`) still works.
- **A gated model** (Cohere). `gated: true` in the index; the sha256s still bind it. The
  download needs the owner's Hugging Face token, as today.
- **Clock wrong on the home.** A clock years ahead expires the index early; one far behind
  never expires it. The supervisor already warns on clock skew; expiry is a freeze guard, not
  the only one (the version pin is).
- **A fork** (`MAKTABA_UPDATE_REPO`) signs with its own workflow identity, as for releases.
- **Release re-run** after a partial upload: same bytes, same `checksums.txt`, accepted.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/go/releaseverify/components.go` (new) | `ComponentsIndex` type, `VerifyComponentsIndex`, the per-version watermark file, error kinds `index_expired`, `index_wrong_version`, `index_replayed`. |
| `shared/go/releaseverify/components_test.go` (new) | T01–T07 with `releaseverifytest`. |
| `shared/go/releaseverify/manifest.go` | `Components *ComponentsRef` in `Manifest`. |
| `shared/go/releaseverify/releaseverifytest/` | `ReleaseOptions` can add an index and packs to a fake release. |
| `tools/release/cmd/gen-components-index/` (new) | writes the index from the catalog, the built packs and the commit time. |
| `tools/release/cmd/pin-models/` (new) | fills `source@revision` and `files` for HF and ollama models in the catalog. |
| `tools/release/cmd/gen-release-json/` and `tools/release/genrelease/genrelease.go` | `components` field; `--check` that every indexed pack is in `dist/`. |
| `tools/release/packs/build.sh` (new) | builds packs per platform, reproducible; splits >1.9 GB. |
| `tools/release/packs/test_build.sh` (new) | T09. |
| `.github/workflows/release.yml` | job `package-packs`; `sign-and-release` adds packs + index to `dist/` before "Generate checksums". |
| `tools/release/release-assets.sh` | knows the pack and index names. |
| `shared/components/catalog.toml` (from 51.01) | model entries pinned by revision + files. |
| `.github/workflows/_components-packs.yml` (new), `.github/workflows/ci.yml` | the `components-packs` job and its `changes` filter (packs, index, `releaseverify/components*`, the installer). |

### Tests

- T01–T10 in the Go unit job (`releaseverify`, `tools/release`).
- T11 in the `catalog` lint job (51.01).
- **CI only:** `components-packs` builds and verifies real packs for one platform on every PR
  that touches packs, the index or the installer.

### Rollout

Ships dark: the release gains files nobody reads until 51.16. The first release with it is
checked by hand once: `cosign verify-blob` of `checksums.txt`, then the index's line in it.

### Rollback

Drop `package-packs` from `release.yml`. Releases without an index are what every release is
today; 51.16 treats a missing index as "nothing can be added later" (`index_missing`).

### Dependencies

51.01 (the catalog and its asset entries). No change to the updater's behaviour.
