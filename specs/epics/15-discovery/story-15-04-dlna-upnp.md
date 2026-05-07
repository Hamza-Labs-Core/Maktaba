# Story 15.4 — UPnP / DLNA compatibility

For legacy devices (older smart TVs, PS3-era consoles), Maktaba speaks
basic DLNA so the library is browsable as a media server.

**Anchors:** [`architecture.md` §6](../../architecture.md).

## AC

- Opt-in toggle in Settings → Compatibility.
- Advertises as a `MediaServer` per UPnP AV; transcoded HLS sources are
  not exposed (DLNA can't consume them); only direct-play files are.
- DLNA clients see a flat list (no tagging / search / progress sync).
- Read-only: no DLNA-side delete or upload.
- Browsing tree: Library / Genre / Speaker / Recently Added.
- Subtitles: sidecar SRT exposed where the DLNA client supports them.

## TC

- Enable DLNA, browse from a Sony Bravia (2018): library appears and
  plays.
- Browse from VLC (DLNA client): same.
- Disable DLNA: server stops advertising within 30 s.

## EC

- DLNA-incompatible codec (HEVC on a 2014 TV): we don't advertise that
  file (would fail on the TV anyway).
- Cellular network mistakenly being treated as LAN by UPnP IGD: we
  refuse to advertise on non-private addresses.
- DLNA UUID conflicts with another product on the LAN: pick a
  deterministic UUID derived from `mdns_id`.
