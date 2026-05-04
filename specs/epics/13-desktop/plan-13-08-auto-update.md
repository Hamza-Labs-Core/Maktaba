# Implementation Plan — Story 13.8 Auto-Update

> Companion to [story-13-08-auto-update.md](story-13-08-auto-update.md).
> Tauri's built-in updater fetches signed delta updates from a server-side manifest.
> Channels: `stable` (default), `beta` (opt-in).

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
      "https://updates.maktaba.app/v1/{{target}}-{{arch}}/{{channel}}.json"
    ],
    "dialog": false,
    "pubkey": "PUBLIC_ED25519_KEY_BASE64",
    "windows": { "installMode": "passive" }
  }
}
```

`{{channel}}` substitution: read from local config; defaults to `stable`.

## 2. Update manifest format

```json
{
  "version": "1.4.2",
  "notes": "Bug fixes and a faster Library page.",
  "pub_date": "2026-05-04T12:00:00Z",
  "platforms": {
    "darwin-aarch64": {
      "signature": "<ed25519 base64>",
      "url": "https://updates.maktaba.app/v1/darwin-aarch64/Maktaba_1.4.2_aarch64.app.tar.gz"
    },
    "darwin-x86_64":  { "signature": "...", "url": "..." },
    "windows-x86_64": { "signature": "...", "url": "..." },
    "linux-x86_64":   { "signature": "...", "url": "..." }
  }
}
```

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
```

On boot, the endpoint URL is rewritten with the chosen channel via `tauri::Manager::config_mut` substitution.

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
