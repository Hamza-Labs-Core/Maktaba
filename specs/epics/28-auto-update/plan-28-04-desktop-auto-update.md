# Implementation Plan — Story 28.4 Desktop auto-update (Tauri)

> Companion to [story-28-04-desktop-auto-update.md](story-28-04-desktop-auto-update.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Updater | Tauri 2 built-in updater plugin (no app code). |
| Feed | GitHub Release `latest.json` (dynamic endpoint). |
| Signing | `tauri signer generate` key pair; pubkey in config, privkey in CI secrets. |
| `latest.json` | Emitted by `tauri-action` when signing is configured. |
| Config files | `tauri.conf.json`, `desktop-release.yml`, `docs/auto-update.md`. |

## 1. `tauri.conf.json` updater block

```jsonc
"plugins": {
  "updater": {
    "active": true,
    "pubkey": "<minisign public key from `tauri signer generate`>",
    "endpoints": [
      "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest/download/latest.json"
    ],
    "windows": { "installMode": "passive" }
  }
}
```

`version` (top-level) is set from the tag in CI (28.1 §3) so the updater
compares the real version. `installMode: passive` gives Windows a
non-interactive update with a progress bar.

> The repo already has this block with a `REPLACE_WITH_PUBKEY`
> placeholder and the correct endpoint — this story replaces the
> placeholder with a real generated key and adds the `windows` install
> mode.

## 2. Key generation (documented in `docs/auto-update.md`)

```bash
# one-time, on a trusted machine:
pnpm --filter maktaba-desktop exec tauri signer generate -w ~/.tauri/maktaba.key
#  -> prints the PUBLIC key  -> paste into tauri.conf.json plugins.updater.pubkey
#  -> writes the PRIVATE key  -> store as the CI secret TAURI_SIGNING_PRIVATE_KEY
#     and its password as TAURI_SIGNING_PRIVATE_KEY_PASSWORD
```

The private key is never committed. Rotation: ship one release signed by
both keys, then swap the pubkey.

## 3. CI: sign + emit `latest.json`

`desktop-release.yml` — add the signing secrets to the `tauri-action`
step's env; `tauri-action` then signs each bundle and (because
`createUpdaterArtifacts` is implied by the updater config) generates the
`latest.json` and uploads it to the Release:

```yaml
- name: Build + upload Tauri bundles
  uses: tauri-apps/tauri-action@v0
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    TAURI_SIGNING_PRIVATE_KEY: ${{ secrets.TAURI_SIGNING_PRIVATE_KEY }}
    TAURI_SIGNING_PRIVATE_KEY_PASSWORD: ${{ secrets.TAURI_SIGNING_PRIVATE_KEY_PASSWORD }}
  with:
    projectPath: apps/desktop
    tagName: ${{ github.ref_name }}
    releaseName: ${{ github.ref_name }}
    args: ${{ matrix.args }}
```

Forks without the secrets keep producing **unsigned** installers (the
secrets resolve empty; `tauri-action` skips updater-artifact generation),
so the build stays green — matching the existing "signing deferred"
posture for Apple/Windows certs.

## 4. Test plan

| Test | Pins |
|---|---|
| config parse: `active`, endpoint, pubkey ≠ placeholder | T01 |
| config parse: `version` rewritten from tag in CI | T02 |
| QA: signed update applies on relaunch | T03 |
| QA: tampered bundle rejected | T04 |
| CI dry-run: `latest.json` present when secrets set | T05 |

## 5. Acceptance checklist

- [ ] Real pubkey in `tauri.conf.json`; endpoint = GitHub `latest.json`.
- [ ] `windows.installMode` set.
- [ ] Version set from tag in CI.
- [ ] `TAURI_SIGNING_*` secrets threaded into `tauri-action`.
- [ ] `latest.json` emitted + attached to the Release.
- [ ] Key-gen + rotation documented in `docs/auto-update.md`.
