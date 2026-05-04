# Implementation Plan — Story 13.8 Auto-Update

> Companion to [story-13-08-auto-update.md](story-13-08-auto-update.md).
> Tauri's built-in updater fetches signed delta updates from a server-side manifest.
> Channels: `stable` (default), `beta` (opt-in).
>
> ACL: see `plan-13-01-macos.md` §Capabilities. This story requires
> `src-tauri/capabilities/updater.json` (granting `updater:default`).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Updater | `tauri-plugin-updater`. |
| Signing | Ed25519; private key in CI secret; public key bundled in `tauri.conf.json`. |
| Channel selection | Setting written to `~/Library/Application Support/com.maktaba.desktop/channel` (or platform equivalent); changes take effect on next check. |
| Manifest URL | `https://updates.maktaba.app/v1/{platform}-{arch}/{channel}.json`. (Static JSON; can be hosted on Cloudflare/S3.) |
| Version skew display | Settings → About reads server's `/api/system/version` and compares with bundled version. |
| Out of scope | Server hosting of updates (post-v1 ops); rollback distribution. |

## 1. tauri.conf.json

```json
"plugins": {
  "updater": {
    "active": true,
    "endpoints": [
      "https://updates.maktaba.app/v1/stable/{{target}}-{{arch}}/{{current_version}}.json"
    ],
    "dialog": false,
    "pubkey": "PUBLIC_ED25519_KEY_BASE64",
    "windows": { "installMode": "passive" }
  }
}
```

Tauri's built-in URL substitutions are `{{target}}`, `{{arch}}`, and
`{{current_version}}` only. `{{channel}}` is **not** a built-in; the
config above hard-codes the `stable` path as the default.

For multi-channel support (stable + beta), override the endpoint list at
boot time after reading the local channel file (see §4):

```rust
fn build_updater(app: &AppHandle) -> Result<Updater> {
    let channel = read_channel_file(app)?;  // "stable" or "beta"
    let endpoint = format!(
        "https://updates.maktaba.app/v1/{channel}/{{target}}-{{arch}}/{{current_version}}.json",
        channel = channel,
    );
    app.updater_builder().endpoints(vec![endpoint])?.build()
}
```

`app.updater_builder()` is the supported way to override updater config
at runtime. (`tauri::Manager::config_mut` does **not** exist — earlier
drafts of this plan referred to it; ignore.)

## 2. Update manifest format

```json
{
  "version": "1.4.2",
  "notes": "Bug fixes and a faster Library page.",
  "pub_date": "2026-05-04T12:00:00Z",
  "platforms": {
    "darwin-universal": {
      "signature": "<ed25519 base64>",
      "url": "https://updates.maktaba.app/v1/stable/Maktaba_1.4.2_universal.app.tar.gz"
    },
    "windows-x86_64": { "signature": "...", "url": "..." },
    "linux-x86_64":   { "signature": "...", "url": "..." }
  }
}
```

**Decision: macOS publishes a single `darwin-universal` entry**, not
separate `darwin-aarch64` and `darwin-x86_64` entries. Plan 13-01 §7
already builds `universal-apple-darwin` (one DMG covers both
architectures), and shipping per-arch updater entries on top of a
universal build wastes bandwidth and complicates the release pipeline.
Tauri's updater on macOS resolves `darwin-universal` for both
`darwin-aarch64` and `darwin-x86_64` at runtime when the bundle is fat.

Build script publishes one manifest per channel.

## 3. Updater wiring

```rust
// main.rs
use tauri_plugin_updater::UpdaterExt;

fn check_for_updates(app: &AppHandle) -> tauri::Result<()> {
    let app2 = app.clone();
    tauri::async_runtime::spawn(async move {
        match app2.updater().unwrap().check().await {
            Ok(Some(update)) => {
                let _ = app2.emit("updater:available",
                    serde_json::json!({"version": update.version, "notes": update.body }));
                // Download in the background; install on quit by default.
                let _ = update.download(|chunk_len, total| {
                    let _ = app2.emit("updater:progress", serde_json::json!({"chunk_len": chunk_len, "total": total}));
                }, || {
                    let _ = app2.emit("updater:downloaded", ());
                }).await;
            }
            Ok(None) => {}
            Err(e) => log::warn!("update check failed: {e}"),
        }
    });
    Ok(())
}
```

Schedule: on launch + every 24 h (`tokio::time::interval`).

## 4. Channel switching

```rust
#[tauri::command]
fn set_channel(app: AppHandle, channel: String) -> tauri::Result<()> {
    let dir = app.path().app_config_dir()?;
    fs::write(dir.join("channel"), &channel)?;
    Ok(())
}

fn read_channel_file(app: &AppHandle) -> tauri::Result<String> {
    let dir = app.path().app_config_dir()?;
    Ok(std::fs::read_to_string(dir.join("channel"))
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| "stable".to_string()))
}
```

On boot, `build_updater` (§1) reads this file and constructs the
`endpoints` list with the chosen channel baked in. Channel changes take
effect on the next launch (or after an explicit `app.updater_builder()`
rebuild). There is no `config_mut` API; do not attempt to mutate
`tauri.conf.json` at runtime.

`Allow downgrades` setting: when on, the updater compares versions and proceeds even if the manifest version is lower; default off.

## 5. UI

Web side:

```ts
// web/src/features/desktop/UpdateToast.tsx
listen<{version:string;notes:string}>('updater:available', ({ payload }) => {
  toast.show(t('desktop.update.available', payload), {
    actions: [
      { label: t('desktop.update.installOnQuit'), onClick: () => {} },
      { label: t('desktop.update.installNow'),    onClick: () => invoke('install_update_now') },
    ],
  });
});
```

```rust
#[tauri::command]
fn install_update_now(app: AppHandle) -> tauri::Result<()> {
    tauri::async_runtime::block_on(async move {
        if let Ok(Some(update)) = app.updater().unwrap().check().await {
            update.download_and_install(|_,_|{}, ||{}).await?;
            app.restart();
        }
        Ok(())
    })
}
```

## 6. Version-skew surfacing

Settings → About:

```tsx
const server = useQuery(['system','version'], () => api.get('/system/version'));
const client = getAppVersion();
const skew = compareSemver(client, server.data?.version);
return <p>{t('desktop.about.serverVersion', { server: server.data?.version, client })}{skew !== 0 && <Warning text={t('desktop.about.skew')}/>}</p>;
```

## 6.1 Signing & key management

The updater verifies every download with an Ed25519 signature; the app
ships the public key, the signing pipeline holds the private key.

**Key generation.** Run once per project; output is the keypair for the
release pipeline:

```bash
tauri signer generate -w ~/.tauri/maktaba_signing.key
```

This writes the password-encrypted private key to
`~/.tauri/maktaba_signing.key` and prints the corresponding public key
to stdout.

**Public-key storage.** The public key is committed to source under
`src-tauri/keys/maktaba_signing.pub` and referenced from
`tauri.conf.json` via `plugins.updater.pubkey`:

```json
"plugins": {
  "updater": {
    "pubkey": "@@SRC_TAURI_KEYS_MAKTABA_SIGNING_PUB@@"
  }
}
```

(In practice Tauri inlines the base64 string; the file under
`src-tauri/keys/` is the source of truth checked into git.)

**HSM / CI secrets.** The private key is uploaded as a sealed secret to
the release CI (GitHub Actions) and to the Apple Developer account
signing pipeline. It is **never** committed, never printed in logs,
never copied to a developer laptop. CI mounts the secret to a tmpfs at
build time and unmounts on exit.

**Rotation playbook.** Compromise or scheduled rotation:

1. Generate a new keypair as above (`tauri signer generate`).
2. Commit the new public key to `src-tauri/keys/` alongside the old one
   (`maktaba_signing.pub` and `maktaba_signing.next.pub`).
3. Ship a release whose updater config trusts **both** keys (Tauri
   supports a public-key list); this release is signed with the **old**
   private key so existing installs accept it.
4. Wait for ≥ 90% adoption (tracked via the existing
   `/api/system/version` telemetry; see §6 above).
5. Cut over: ship a release signed with the **new** private key,
   trusting only the new public key.
6. Revoke the old key (delete from CI secrets, rotate any HSM slot).
7. Stragglers stuck on pre-cutover versions get a one-time "manual
   reinstall required" prompt rendered via the existing version-skew UI.

A rotation event is documented in `docs/security/keys.md` (cross-ref:
plan-13-03 §9 already references this file for Linux GPG keys).

## 7. Edge cases

| Case | Handling |
|---|---|
| Update server unreachable | Silent retry next interval. |
| Disk full mid-download | Download fails; toast surfaces error; no crash. |
| Signature invalid | Updater refuses install; logs warning. |
| Beta → stable rollback | If next stable is lower than current beta, only proceeds if "Allow downgrades" is on. |
| Server schema change vs old client | Client surfaces "Server is older than client — update server first" inside `<UpdateGuard>` rendered on routes that depend on the missing field. |

## 8. Test cases

### 8.1 Smoke

- Build fixture with version 9.9.9 in manifest; running 1.0.0 picks it up within 24 h interval (test with shortened interval).
- Tampered `signature` field → install refused; warning logged.

### 8.2 Manual

- Switch to beta channel: next check pulls beta manifest.
- Reset to stable + Allow downgrades on: next stable installed.
- Network down: silent (no nag).

## 9. Performance

- Periodic check uses HTTP HEAD + small JSON; bandwidth ≤ 5 KB/check.
- Background download paused if user toggles a "pause downloads" setting.

## 10. Dependencies

- Story 13.1 (Tauri base).
- Build & signing infrastructure.
- `/api/system/version` endpoint (Epic 21 or shared system endpoint).
