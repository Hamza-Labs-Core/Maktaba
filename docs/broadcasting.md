# Broadcasting: the guest's side

A publisher's channels can be watched by guests who have no Maktaba or home account and never
pay (Epic 50, stories 50.26 and 50.27). This page is what a guest meets. The edge's routes,
settings and credentials are in [broadcast-edge.md](broadcast-edge.md).

## Getting in

- **An invite link** (`watch.maktaba.tv/i/<invite>`): the page has one button. Pressing it
  counts one device and stores a guest pass (30 days, renewed while used) in a cookie, then opens
  the channel (an invite for one channel) or the publisher's grid. Opening the link, or a chat
  app's preview of it, uses nothing.
- **An open channel** (only for a verified publisher): anyone with its address watches after one
  Turnstile check, which gives a 12 h session.
- **A TV:** the TV shows a QR code and a 4-character code. The QR code opens
  `watch.maktaba.tv/pair?tv=…` on a phone, which shows the same 4 characters to check. The guest
  pastes the invite link there (or sends the phone's open session), and the TV starts. A TV
  counts as its own device on the invite; an open session is shared with the phone.

## The pages

| Page | What it shows |
|---|---|
| `/<lang>/<publisher>` | the channels the guest may watch, with what is on now and next |
| `/<lang>/<publisher>/<channel>` | the player, now and next, today's programmes, the credit of a Creative Commons programme, Report |
| `/<lang>/<publisher>/guide` | today's programmes on each channel |
| `/<lang>/legal/…` | copyright, terms and privacy, with the full text on maktaba.tv |

Arabic (right to left) is the default; `/en/` shows English, and without a prefix the browser's
language decides. The header shows "{publisher} on Maktaba" with the publisher's logo; colours
are Maktaba's. The footer says "Free to watch". A page never links to another publisher, and
Maktaba lists, recommends or features no channel.

The video is shown as it is: the watermark and strip are burned in by the home. Channels switch
with up and down, Page Up and Page Down, a vertical swipe on the video, or by typing the
channel's number.

## What a guest can see

| State | When | What the page does |
|---|---|---|
| live | the channel plays | plays |
| tuning | the channel is waking (on demand): the first guest woke the home, which sends its first segments within about 15 seconds | plays as soon as it starts; "Starting the channel…" |
| offline | the home is offline | the slate plays, with "This channel's home is offline…" and no error |
| off air | the home is not broadcasting it | a note and Try again |
| full | the channel or publisher is at its viewer limit, or the network has too many devices | a note and Try again |
| geo blocked | the publisher's geo rules exclude the guest's country | a note |
| ended | the channel was unpublished or stopped | "This broadcast has ended." |
| invite used, invite expired | the invite has no device left, or ran out | a note to ask for a new one |
| revoked | the publisher withdrew the invite or this device's pass | a note to ask for a new invite |

Every message is in English and Arabic.

## Privacy

No viewer account and no email. One functional cookie holds the guest pass or the open session.
An IP address is used only as a salted hash, for at most 24 hours, to apply the limits.
Cloudflare processes the requests (Workers, Turnstile, Analytics Engine).

## In the apps

**Watch a broadcast** in Maktaba TV on the Apple TV (the home picker, and Settings) and on
Android TV, Google TV and Fire TV (the home picker, and Account), and in Maktaba Player on iPhone,
iPad, the Mac and Android phones, foldables and tablets (the home picker, Profile on Android, or
an invite's link) is a space of its own: no home is chosen and no one signs in.

- **Ways in:** the publisher's handle (the part of the watch address after `watch.maktaba.tv`)
  and the invite's `MK-` short code; an invite link pasted, opened as `maktaba://watch/i/<invite>`
  or scanned on a phone; on the TVs, the QR code and the 4-character code to compare, the
  TV waiting on `GET /api/v1/pair/<nonce>` with its own poll key until a phone sends a pass.
- **Saved broadcasts:** each publisher's pass (or a TV's open session) is kept in the Keychain
  (`…ThisDeviceOnly`) on Apple devices, and on Android sealed with an AES key in the Android
  Keystore that never leaves the device, in a file the apps' backup rules keep out of backups and
  device transfers: a pass counts as one of its invite's devices. One entry holds the list,
  newest first, dropped once it expires; **Remove** takes it off the
  device (the invite's device use stays counted on the edge).
- **Watching:** the publisher's channels with now and next; the player shows the picture as the
  edge sends it, the channel, the programme and a CC BY programme's credit, and the state's words
  from the same table as the pages (`shared/broadcast/viewer-states.json`). The player reads
  `live.maktaba.tv` through MaktabaKit's `StreamProxy` on the device's loopback (on Android, a
  Media3 data source that rewrites each request's address), which puts the newest viewer token
  in each path, so the 10-minute token is refreshed by the heartbeat without restarting the
  picture; only the channel's own paths on its edge are fetched. On Android a segment whose body
  ends before its declared length (or a transport stream segment that ends partway through a
  packet) is a load error the player tries again, never a whole segment; the same check guards
  the Android players' home streams. Up and down on the remote, or a vertical swipe, change the channel.
- **Not offered** under a parental limit (the account's, or the device's Screen Time limit; on
  Android, any sign-in on the device the home last said was limited): a broadcast is outside any
  home's ratings. The edge never learns who watches, so this is the app's convenience, not a
  server rule; the device's own restrictions are the boundary.

## Not yet

- The desktop Player for Windows and Linux.
- The Report button in the apps' player menu: the cloud's report route checks a Turnstile token
  the native apps cannot show yet.
- Universal links and Android app links (`https://watch.maktaba.tv/i/…` opening the app): the
  edge has to serve the apps' association files; until then the link opens the watch page, which plays in the browser.
- The remote `guest.enabled` switch that would hide the entry in the apps.
