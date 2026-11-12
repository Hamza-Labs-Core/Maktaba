# Story 57.10 — Live TV under parental controls

> Epic 57 · Phase 1 · Depends on: 57.01, 57.03 · Needed by: 57.05, 57.11, 57.13
> **Status:** built (checked against the code 2026-11-12). What differs from the text is under As built.

## Why

Live TV is where a filter is easiest to miss. Channel streams (`/stream/channel/{id}/…`) and the
HDHomeRun routes are not signed today, a channel with no library is visible to every signed-in
user, and the guide keeps programme ratings (`title_snapshot.rating`) that nothing enforces. Plex
live TV (`plex_livetv`, the part of Maktaba that still works with Plex) and the remote live
sessions (`/api/tv/remote/sessions`) need the same check.

## Description

### 1. Channel and programme tiers

- A channel's tier (57.01, `channels.age_tier`, set by an admin) is the most a channel can show.
  A channel with no tier is unrated, and hidden from restricted users unless their policy treats
  unrated as a tier or allows the channel.
- A programme's tier (`channel_programs.age_tier`) is resolved when the guide is written: the
  guide's rating string (XMLTV `<rating system="…">` or Plex's `contentRating` for Plex live TV
  items, parsed by 57.01's tables), else the tier of the scheduled video (Maktaba's own channels
  play library videos), else the channel's tier.
- **What a restricted user gets:** channels whose tier is within the limit (or allowed); the guide
  shows every programme on those channels, and a programme above the limit shows as "Not
  available" with its time only, no title or description.

### 2. Playing

- `POST /api/channels/{id}/play` answers the channel's signed stream URL (`{channel_id, url,
  expires_at, until}`), and `GET /api/channels/{id}/access` answers whether the caller may watch
  the channel now and when that next changes (`{channel_id, restricted, allowed, until}`). Both,
  and the remote viewing routes (`/api/tv/remote/…`), check the channel and the programme on
  air. A channel the account may not see is the 404 of an unknown channel. A permitted channel
  whose programme on air is above the limit is 403 `restricted` with `reason: programme` and
  `retry_at`, when the next permitted programme starts.
- **While a channel plays** the streaming service asks the API
  (`GET /api/tv/parental/verdict`, with this machine's local token, cached 15 seconds and cut at
  the programme boundary) whether the viewer it serves may watch what plays now. When the
  verdict turns, the stream is cut and the next request is refused. A verdict that cannot be read
  refuses.
- **No slate video is made per viewer.** The refusal is the answer; each client draws its own
  slate ("Not available right now") from `…/access`, and asks again at `until`.
- **Signing**: a channel's stream URL is signed (audience `streaming-channel`, subject the
  channel) for an account, or for the admin token. Every `/stream/channel/…` request needs that
  token, this machine's gate naming the viewer, or a tuner address.
- **A second machine.** A channel's signed URL plays at the home only. A node's gate does not
  take it (401), so live TV through the home from a second machine's address is refused.
- **HDHomeRun** routes and the guide exports on the streaming service (for Plex DVR, which
  cannot sign) answer only this machine, the Plex server set up in Maktaba and the addresses an
  admin lists (`GET|PUT /api/tv/hdhr/allowed`, at most 64 addresses or CIDRs). A tuner address
  names no viewer, so nothing limits it per viewer: list only machines every viewer of which may
  watch everything.

### 3. Exports

`/api/channels/xmltv` and `/playlist.m3u` answer 403 `restricted` to a restricted user (they are
for feeding other software).

## Acceptance criteria

- **Given** a 9+ child, a channel at 9 and one at 16, **then** the guide lists only the 9+ channel.
- **Given** the 9+ channel scheduling a 16+ programme at 20:00, **then** the guide shows "Not
  available" at 20:00, a child watching at 19:59 has the stream cut at 20:00 and sees the
  client's slate, and the stream plays again when the next allowed programme starts.
- **Given** a channel with no library and no tier, **then** a restricted child does not see it.
- **Given** a request to `/stream/channel/{id}/…` with no signature, no viewer named by the
  gate and no tuner address, **then** it is refused.
- **Given** a request to the HDHomeRun routes from an address that is not its Plex server, **then**
  403.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit | ratings in XMLTV and Plex forms | resolve programme tier | 57.01 mapping |
| T02 | Go + Postgres | channel tiers 9, 16, none (no library) | child lists channels, guide | only 9; programmes masked |
| T03 | streaming Go test | unsigned and signed channel URLs | GET | refused; 200 |
| T04 | streaming Go test | HDHomeRun from Plex address, loopback, other | GET | 200, 200, 403 |
| T05 | streaming Go test (fake clock) | boundary into a 16+ programme | session | cut and refused, then served again |
| T06 | Go role test | child | xmltv, m3u | 403 `restricted` |
| T07 | Player UI test (iPhone sim) | `MockHome` guide with a masked programme | open Live TV | "Not available" row; slate on play |

## Strings

| Key | English | Arabic |
|---|---|---|
| `live.parental.notAvailable` | Not available | غير متاح |
| `live.parental.slate` | Not available right now. This channel comes back when the next programme starts. | غير متاح الآن. تعود هذه القناة عند بدء البرنامج التالي. |
| `live.admin.hdhrAllowed` | Addresses allowed to use the tuner | العناوين المسموح لها باستخدام الموالف |

## Edge cases

- **A guide with no ratings at all**: programmes take the scheduled video's tier or the channel's.
- **Plex DVR on the same Mac**: loopback is allowed; nothing changes for it.
- **A child on Apple TV** (57.11): the same server behaviour.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/channels/`, `guide/` | channel and programme filter; masked programmes |
| `pipeline` guide writer (`channel_programs`) | programme tier |
| `streaming/internal/channel/handler.go`, `streaming/internal/hdhr/handler.go`, `streaming/internal/server/server.go` | signing, address allowlist, boundary check |
| `api/internal/handlers/tvremote/` | session check |
| web live TV settings (admin) | HDHomeRun addresses (a small form) |
| `api/internal/handlers/parental/livetv.go`, `api/internal/parental/livetv.go`, `streaming/internal/liveguard/` | as built: the routes, the verdicts, the guard |
