# Maktaba on a NAS: the network

[العربية](networking.ar.md)

Epic 59 story 59.08. This is the source the maktaba.tv install guides (story 59.14) take their
network sections from: host or bridge, how apps find the home, HTTPS, a reverse proxy in front,
and the firewall.

## Host network (the default)

Every compose file and template runs the container on the host's network
(`network_mode: host`). Apps find the home by mDNS (`_maktaba._tcp`), TVs and Plex find the
tuner by SSDP, and the home is at the NAS's own address.

| Port | What | Clashes with |
|---|---|---|
| 8088 | the web app (the address a household types) | nothing on DSM (80, 443, 5000, 5001) or TrueNAS (80, 443) |
| 8080 | the API the apps and the cluster's machines use | QNAP QTS's own UI |
| 8081 | live TV | |
| 54329 | the bundled Postgres, for machines that join the home | nothing (DSM's own Postgres is on 5432) |

A port another program holds is moved at start into Maktaba's fallback range (8780 to 8790).
Setup and Settings → This machine say which port moved, where to, and which program holds it
when the NAS lets Maktaba see it; when the web port moves, the container log prints the new
address in English and Arabic.

The bundled Postgres listens on loopback only until a machine joins the home; the join opens it
to the LAN, with TLS, and it goes back to loopback when the last machine leaves.

When the NAS has several addresses (bonded links, VLANs), set `MAKTABA_HOST_IP` to the one your
devices use: mDNS is announced on that interface only, and Maktaba's links name it.

## How apps find the home

**By default, Maktaba's own responder.** In the container Maktaba answers mDNS itself, under its
own name, `<nas>-maktaba.local`, beside the NAS's own responder (avahi-daemon on DSM and
Unraid). Both share UDP 5353 (`SO_REUSEADDR`), each answers only for its own names, and the NAS
keeps its name. Docker's bridges (`docker0`, `br-*`) are never announced on.

**Optional: the NAS's Avahi.** Mounting the host's system D-Bus socket makes Maktaba publish
through the NAS's avahi-daemon, under the NAS's own name:

```yaml
    volumes:
      - /var/run/dbus/system_bus_socket:/var/run/dbus/system_bus_socket
```

Its cost: the socket reaches every system service D-Bus policy lets the container's user talk
to, not only Avahi, and `:ro` does not change that (a read-only bind still lets the container
connect). Use it only if you want the home under the NAS's name; nothing else needs it.

## Bridge network

Docker Desktop (Mac, Windows) has no host network, and some households keep every container on a
bridge. Use `docker-compose.bridge.yml`: it publishes 8088, 8080 and 8081 (never 54329: a bridge
home has no other machines) and needs `MAKTABA_HOST_IP`, this machine's LAN address.

On a bridge, multicast stays inside Docker, so Maktaba turns mDNS and SSDP off (it reads the
bridge from the default route's interface; `MAKTABA_NETWORK=host` or `bridge` overrides that).
Setup says: *This container is on a bridge network, so apps can't find Maktaba by themselves.
They can still connect by its address (http://192.168.1.20:8088), by its HTTPS name, or with a
code from Maktaba Cloud.* Settings → This machine says the same.

macvlan (Unraid's `br0` custom network) gives the container its own address on the LAN, where
multicast works: it counts as the host's network.

## HTTPS

The home's own HTTPS name (a name of the home's own, with the home's own certificate, story
50.15) works on a NAS as anywhere, and needs nothing from the NAS. A reverse proxy is not required.

## A reverse proxy in front (optional)

When the NAS's own proxy (DSM's Login Portal, a proxy app on TrueNAS, Nginx Proxy Manager on
Unraid) serves Maktaba under a name of yours, it needs:

- **the whole site proxied to port 8088** (the web app serves `/api/` and `/stream/` itself);
- **WebSocket**: `Upgrade` and `Connection` passed on for `/api/` (the app's live updates use
  `/api/cluster/ws`);
- **no buffering** for `/stream/` (video) and for `/api/` (its event streams), and long read
  timeouts (an hour) on both;
- **no upload limit**, or at least 10 GB (`client_max_body_size 0` in nginx);
- **`X-Forwarded-For` and `X-Forwarded-Proto`** set by the proxy.

Maktaba believes `X-Forwarded-For` only from a proxy it trusts: one on the NAS itself (it
reaches Maktaba over loopback) is always trusted. A proxy at another address (a container on a
bridge, another machine) goes in **Settings → Storage & discovery → Trusted proxies**,
`[server] trusted_proxies` in server.toml. From any other address the header is ignored, and
the logs show the proxy's address instead of the client's.

For a proxy in a container, enter that container's own address (Maktaba's log shows it as the
peer of a refused header), never the whole Docker network such as `172.17.0.0/16`. A network
includes its gateway (`172.17.0.1`), and Docker's port publishing (docker-proxy) delivers
connections from other machines from that gateway address, with whatever header the client
wrote. `0.0.0.0/0` and `::/0` are refused: they would believe everyone.

The home's approved machines are a separate matter, with no setting: a machine forwarding the
requests of the clients signed in through it signs the client's address with its own key, and
the home believes that signature alone. It never trusts a machine's IP address for this, it
cannot be turned off, and it does not depend on Trusted proxies: an empty list still shows each
client behind a machine. A machine that is revoked or removed loses it on its next request. A
machine on an older version shows as its own address until it is updated.

An nginx server block (Nginx Proxy Manager's Advanced tab takes the `location` blocks):

```nginx
client_max_body_size 0;

location / {
    proxy_pass http://127.0.0.1:8088;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}

location /api/ {
    proxy_pass http://127.0.0.1:8088;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $http_connection;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 3600s;
}

location /stream/ {
    proxy_pass http://127.0.0.1:8088;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_read_timeout 3600s;
}
```

Caddy does all of it with one line (`reverse_proxy 127.0.0.1:8088`, `flush_interval -1` for the
streams).

**DSM (Login Portal).** Control Panel → Login Portal → Advanced → Reverse Proxy → Create: the
source is HTTPS with your host name on 443, the destination HTTP `localhost` on 8088. Under
Custom Header → Create → WebSocket, which adds `Upgrade` and `Connection`. DSM's proxy sets
`X-Forwarded-For` and `X-Forwarded-Proto` itself, and reaches Maktaba over loopback, so no
trusted proxy needs adding. DSM's screens do not show the upload limit or buffering; if a large
upload stops or a video stalls only behind the proxy, use the home's HTTPS name instead.

**TrueNAS.** TrueNAS has no proxy of its own for apps: install one from the catalog (Nginx Proxy
Manager, Caddy or Traefik) with the settings above, and add its address to Trusted proxies
when it runs on a bridge (its container address, as Maktaba's log shows it; not the Docker
network's range, which includes the gateway).

## The firewall

DSM's firewall, when on (Control Panel → Security → Firewall → Edit Rules), needs an allow rule
from your LAN's subnet (for example 192.168.1.0/24) for:

- TCP 8088 (the web app), and 8080 and 8081 for the apps, TVs and the cluster's machines;
- UDP 5353 (mDNS), so apps find the home, and UDP 1900 and 65001 (SSDP, HDHomeRun) for TVs and
  Plex;
- TCP 54329 only when another machine joins the home.

Allow them from the LAN only, never from everywhere. TrueNAS has no firewall in its UI; a
router's port forward to the NAS is never needed (the home's HTTPS name and Maktaba Cloud reach
it from outside).
