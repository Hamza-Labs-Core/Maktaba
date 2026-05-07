# Implementation Plan — Story 15.1 Local network discovery (mDNS / Bonjour)

> Companion to [story-15-01-mdns.md](story-15-01-mdns.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Server-side advertiser | `api/internal/discovery/mdns.go` — wraps `github.com/grandcat/zeroconf` (or `github.com/hashicorp/mdns` if reflectors prove problematic). Bound to a `MDNSAdvertiser` lifecycle on the API service. |
| Client-side resolvers | Mobile (`apps/mobile/plugins/native-discovery/`), desktop (`apps/desktop/src-tauri/src/discovery.rs`), tvOS (`apps/tvos/Sources/Features/ServerPicker/MDNSBrowser.swift`), AndroidTV (`apps/androidtv/.../discovery/MDNSBrowser.kt`). The web client deliberately does not browse mDNS. |
| Service type | `_maktaba._tcp.local.` (port from config; default 8080 / 443). |
| TXT records | `version`, `name`, `tls`, `auth_required`, `mdns_id`. |
| Config | `[discovery] mdns.enabled = true` (default), `mdns.name = $hostname`, `mdns.bind_iface = ""` (auto, RFC 6762). `mdns_id` is generated once and persisted in the DB (`server_identity` table — see §2). |
| Out of scope | Cloud relay (Story 15.2), QR pairing (Story 15.5), DLNA (Story 15.4). |

## 1. Architecture diagram

```
   ┌─────────────────────────────┐
   │ api service                 │
   │  ┌──────────────────────┐   │   _maktaba._tcp.local.
   │  │ MDNSAdvertiser       │   │
   │  │ - on start: register │ ─►│   (sends multicast to 224.0.0.251 / ff02::fb)
   │  │ - on shutdown: bye   │   │
   │  └──────────────────────┘   │
   └─────────────────────────────┘
                  ▲
                  │ TXT { version, name, tls, auth_required, mdns_id }
                  │
   ┌──────────────┴────────────┐
   │ Client (mobile/desktop/TV)│
   │  ┌────────────────────┐   │
   │  │ MDNSBrowser        │   │ ──► Settings → Server picker
   │  │  - on launch       │   │
   │  │  - on net change   │   │
   │  └────────────────────┘   │
   └───────────────────────────┘
```

## 2. Database addition

We need a stable `mdns_id` that survives restarts and database recreates (within the same server identity). A new table:

`shared/db/migrations/0050_server_identity.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE server_identity (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- singleton
    mdns_id         UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO server_identity (id, mdns_id) VALUES (1, gen_random_uuid())
ON CONFLICT DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS server_identity;
-- +goose StatementEnd
```

The advertiser reads `mdns_id` once at start. If the table is empty (impossible after the migration runs), the advertiser refuses to start — single-source.

## 3. Server implementation

`api/internal/discovery/mdns.go`:

```go
package discovery

import (
    "context"
    "fmt"
    "net"
    "github.com/grandcat/zeroconf"
)

type Advertiser struct {
    server *zeroconf.Server
    cfg    Config
    id     string
}

func New(ctx context.Context, cfg Config, mdnsID string, version string) (*Advertiser, error) {
    txt := []string{
        "version=" + version,
        "name=" + cfg.Name,
        "tls=" + boolStr(cfg.TLS),
        "auth_required=" + boolStr(cfg.AuthRequired),
        "mdns_id=" + mdnsID,
    }
    ifaces, err := bindInterfaces(cfg.BindIface)
    if err != nil { return nil, err }
    s, err := zeroconf.RegisterProxy(
        cfg.Name,                   // instance name
        "_maktaba._tcp",            // service
        "local.",                   // domain
        cfg.Port,                   // port
        cfg.Hostname,               // host (defaults to OS hostname)
        cfg.IPs,                    // explicit IPs to advertise
        txt,
        ifaces,
    )
    if err != nil { return nil, err }
    return &Advertiser{server: s, cfg: cfg, id: mdnsID}, nil
}

func (a *Advertiser) Shutdown(ctx context.Context) error {
    if a.server != nil { a.server.Shutdown() }
    return nil
}
```

Wired in `api/cmd/api/main.go` at server boot:

```go
mdnsID, _ := store.MDNSID(ctx)
adv, _ := discovery.New(ctx, cfg.Discovery, mdnsID, version.Version)
defer adv.Shutdown(context.Background())
```

`bindInterfaces` resolves `cfg.BindIface`:
- empty → all multicast-capable, non-loopback interfaces.
- comma-separated names → only those.
- For multi-NIC hosts on segmented VLANs, operators set this explicitly. Documented in `docs/operations/multi-vlan.md`.

## 4. Client browsers

### 4.1 Mobile (Capacitor plugin)

`apps/mobile/plugins/native-discovery/ios/MDNSBrowser.swift` uses `NWBrowser` (Network.framework):

```swift
let browser = NWBrowser(
    for: .bonjour(type: "_maktaba._tcp", domain: "local."),
    using: .tcp
)
browser.browseResultsChangedHandler = { results, _ in
    let servers = results.compactMap(parseTXT)
    bridge.notify("serversChanged", servers)
}
browser.start(queue: .main)
```

Android side uses `NsdManager`:

```kotlin
nsdManager.discoverServices("_maktaba._tcp.", PROTOCOL_DNS_SD, listener)
```

The plugin exposes a JS API consumed by the React shell to populate the server picker.

### 4.2 Desktop (Tauri / Rust)

`apps/desktop/src-tauri/src/discovery.rs` uses `mdns-sd`:

```rust
let daemon = ServiceDaemon::new()?;
let receiver = daemon.browse("_maktaba._tcp.local.")?;
while let Ok(event) = receiver.recv() {
    if let ServiceEvent::ServiceResolved(info) = event {
        emit("server-found", parse_txt(info));
    }
}
```

### 4.3 Native (tvOS / AndroidTV)

`MDNSBrowser.swift` uses `NWBrowser` (same as mobile iOS).
`MDNSBrowser.kt` uses `NsdManager` (same as mobile Android).

## 5. Re-resolution on network change

Each client registers for the platform's network change notification:
- iOS/tvOS: `NWPathMonitor`.
- Android: `ConnectivityManager.NetworkCallback`.
- Desktop/Linux: poll `/sys/class/net/*/operstate`; macOS: `SCNetworkReachability`; Windows: `NotifyAddrChange`.

On change, the browser is restarted. The story TC requires "Server restart: TXT records re-published; clients re-resolve within 10 s." The mDNS protocol's TTL handles this naturally; clients also force a re-query on the change event for fast convergence.

## 6. mdns_id is canonical identity

Hostnames change. The story EC: "Server changes hostname: clients see two entries until the old one TTLs out; we treat `mdns_id` as the canonical identity." Client logic:

```ts
// shared/web/src/discovery/mergeServers.ts
function dedupe(servers: Discovered[]): Discovered[] {
    const byID = new Map<string, Discovered>();
    for (const s of servers) {
        const prev = byID.get(s.mdnsID);
        if (!prev || prev.lastSeen < s.lastSeen) byID.set(s.mdnsID, s);
    }
    return [...byID.values()];
}
```

## 7. IPv6-only and multicast caveats

- IPv4: 224.0.0.251 / UDP 5353.
- IPv6: ff02::fb (link-local multicast).
- The advertiser binds both A and AAAA records on every selected interface.
- LAN with mDNS reflectors: documented in `docs/operations/multi-vlan.md`. We do **not** suppress the advertisement on reflector LANs; instead, operators bind to the desired interface explicitly.

## 8. Test plan

### 8.1 Server unit tests

| Test | What it pins |
|---|---|
| `TestAdvertiserPublishesAllTXTKeys` | Spin up advertiser; pcap the multicast (or use `zeroconf.Browse` in-test); assert all 5 TXT keys present. |
| `TestMDNSIDStableAcrossRestart` | Restart the advertiser; `mdns_id` unchanged. |
| `TestBindIfaceFiltersToConfigured` | Two NICs; configure `bind_iface = lo0`; assert advertisement absent on `en0`. |
| `TestShutdownSendsByeBye` | On shutdown, the goodbye packets (TTL=0) are observed. |

### 8.2 Client integration

| Test | What it pins |
|---|---|
| `TestMobileResolveWithin2s` | Mock advertiser via `dnssd` test harness; client `MDNSBrowser.start()` resolves within 2 s wall. |
| `TestNetworkChangeRestartsBrowser` | Simulate NWPath change; assert one fresh query (Wireshark stub or NWBrowser mock). |
| `TestDedupeByMDNSID` | Two records with same `mdns_id` → single server in the picker. |

### 8.3 End-to-end

| Test | What it pins |
|---|---|
| `e2e_DiscoverThenSignIn` | Spin up real server in CI's docker-compose; mobile sim resolves and signs in. |
| `e2e_TwoServersOnSameLAN` | Two servers; picker shows both with distinct `name`. |

## 9. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Multi-VLAN with reflector | Operator binds explicit iface; advertisement scoped. Documented. | `TestBindIfaceFiltersToConfigured` |
| Hostname change | New advert appears; old TTLs out (≤ 120 s by default); dedupe by `mdns_id`. | `TestDedupeByMDNSID` |
| IPv6-only LAN | AAAA record published; clients resolve via NWBrowser/`NsdManager` IPv6. | `e2e_IPv6OnlyResolves` |
| Clients on cellular (no LAN) | mDNS does not reach; clients fall back to relay (Story 15.2) or manual. | n/a |
| Web client | No mDNS; user enters URL or scans QR (Story 15.5). | n/a |
| Clock-skew or hostname collision | mDNS protocol resolves via instance-name renaming (zeroconf handles). | `TestInstanceNameCollision` |
| Server behind NAT inside a container | `mdns.bind_iface` set to the host interface via Docker host-network mode; documented. | `docs/operations/docker.md` |
| `mdns_id` table missing rows | Advertiser refuses to start; clear log error. | `TestAdvertiserRefusesWithoutID` |

## 10. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/grandcat/zeroconf` | latest | Server-side mDNS advertiser (pure Go). |
| `Network.framework` | system | iOS/macOS/tvOS NWBrowser. |
| `androidx.core` (`NsdManager`) | system | Android service discovery. |
| `mdns-sd` | latest crate | Tauri/Rust client. |

## 11. Acceptance checklist

**Server**
- [ ] Advertises `_maktaba._tcp.local.` with all 5 TXT keys.
- [ ] `mdns_id` persisted; survives restart.
- [ ] `bind_iface` configurable.

**Clients**
- [ ] Mobile, desktop, tvOS, AndroidTV browse on launch and on net-change.
- [ ] Web does not browse (no native API).

**Tests**
- [ ] All §8 tests pass.

**Docs**
- [ ] Multi-VLAN operations doc.
- [ ] `specs/epics/15-discovery/README.md` ticks story 15.1.
