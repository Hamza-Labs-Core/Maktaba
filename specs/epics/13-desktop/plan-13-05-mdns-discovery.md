# Implementation Plan — Story 13.5 mDNS Discovery

> Companion to [story-13-05-mdns-discovery.md](story-13-05-mdns-discovery.md).
> Server-side advertisement owned by Epic 15 Story 15.1; this plan owns the client-side resolver and picker UI.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Service type | `_maktaba._tcp.local.` (matches Epic 15 Story 15.1). |
| Library | `mdns-sd` Rust crate (lightweight, async). |
| Placement | `src-tauri/src/mdns.rs` + Tauri command `discover_servers`. |
| Cache | Last-seen list persisted in `app data dir / servers.json` for fast first-launch render. |
| UI | `web/src/features/desktop/ServerPicker.tsx` — first-launch wizard + Settings → Servers. |
| Pairing | QR-code pairing (Story 15.5) when manual auth is needed. |
| Out of scope | Server-side advertisement (Epic 15). |

## 1. Rust resolver

```rust
// mdns.rs
use mdns_sd::{ServiceDaemon, ServiceEvent};

pub struct Discoverer {
    daemon: ServiceDaemon,
    servers: Mutex<Vec<DiscoveredServer>>,
}

#[derive(Clone, Serialize)]
pub struct DiscoveredServer {
    pub name: String,
    pub host: String,
    pub port: u16,
    pub addresses: Vec<String>,
    pub last_seen: i64,            // unix ms
    pub txt: HashMap<String, String>,  // version, instance id
}

impl Discoverer {
    pub fn start(app: AppHandle) -> Result<Self> {
        let daemon = ServiceDaemon::new()?;
        let receiver = daemon.browse("_maktaba._tcp.local.")?;
        let servers = Mutex::new(load_cache(&app));

        let app2 = app.clone();
        std::thread::spawn(move || {
            while let Ok(event) = receiver.recv() {
                match event {
                    ServiceEvent::ServiceResolved(info) => upsert(&app2, info),
                    ServiceEvent::ServiceRemoved(_, name) => remove(&app2, &name),
                    _ => {}
                }
            }
        });
        Ok(Self { daemon, servers })
    }
}

#[tauri::command]
pub fn discover_servers(state: State<Discoverer>) -> Vec<DiscoveredServer> {
    state.servers.lock().unwrap().clone()
}
```

Cache persists to `app.path().app_data_dir()?.join("servers.json")`.

## 2. Multi-NIC dedup

`DiscoveredServer` keyed by `instance_name` (the mDNS service instance), not by IP. Multiple NICs surfacing the same server collapse into one row.

## 3. Client UI

```tsx
// web/src/features/desktop/ServerPicker.tsx
import { invoke } from '@tauri-apps/api/core';

export function ServerPicker({ onPick }: { onPick(s: ServerConfig): void }) {
  const { data: servers, refetch } = useQuery(['mdns','servers'],
    () => invoke<DiscoveredServer[]>('discover_servers'),
    { refetchInterval: 2_000, staleTime: 0 });

  return <Wizard>
    <h2>{t('desktop.serverPicker.title')}</h2>
    {servers?.length ? <Listbox items={servers} onPick={(s) => onPick({ host: s.host, port: s.port, name: s.name })}/>
                     : <ManualEntry onSubmit={onPick}/>}
    <ManualEntry footer onSubmit={onPick}/>
  </Wizard>;
}
```

Settings → Servers includes a "Switch server" command that re-opens this picker.

## 4. Manual entry fallback

```tsx
function ManualEntry({ onSubmit }: { onSubmit(s: ServerConfig): void }) {
  return <Form onSubmit={(values) => onSubmit({ host: values.host, port: Number(values.port) || 4400, name: values.name || values.host })}>
    <Input name="host" required label={t('desktop.serverPicker.host')}/>
    <Input name="port" type="number" defaultValue={4400}/>
    <Input name="name" placeholder={t('desktop.serverPicker.name.placeholder')}/>
    <Button type="submit">{t('desktop.serverPicker.connect')}</Button>
  </Form>;
}
```

## 5. mDNS-blocked path

If `discover_servers` returns 0 entries after 5 s, the picker surfaces "mDNS unavailable — enter your server manually" (without retrying aggressively).

## 6. QR pairing

When the chosen server requires pairing (Story 15.5), a `<QRPairingDialog>` shows a TOTP-style code; the user opens the server admin UI on another device, enters the code, and the desktop app receives a one-time refresh token to complete login.

## 7. Edge cases

| Case | Handling |
|---|---|
| mDNS blocked by VPN/firewall | Manual entry path remains. |
| Server changes IP on LAN | Discovery re-resolves on next launch; `last_seen` updates without user action. |
| Multi-NIC machine | Dedupe by instance name. |

## 8. Test cases

### 8.1 Rust unit

| Test | Asserts |
|---|---|
| `service event resolves into list` | `ServiceResolved` event upserts row. |
| `removed event removes` | `ServiceRemoved` clears row. |
| `cache persists across restart` | Write file; re-init Discoverer; rows present. |

### 8.2 Manual

- LAN with one server: picker auto-fills within 2 s.
- Three servers: all listed.
- Zero servers: picker shows manual entry only.
- Switch server from menu: picker re-opens.

## 9. Performance

- Daemon idle CPU < 1%.
- `discover_servers` returns ≤ 1 ms (cached state).

## 10. Dependencies

- Epic 15 Story 15.1 (server-side advertisement).
- Story 15.5 (QR pairing).
- Story 13.1 (Tauri shell).
