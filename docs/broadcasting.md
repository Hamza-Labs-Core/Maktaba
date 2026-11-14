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
| tuning | the channel is waking (on demand) | plays as soon as it starts; "Starting the channel…" |
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

## Not yet

The **Watch a broadcast** entry in the Apple TV, iOS, Android and Android TV apps (the short code,
the QR pairing screen and Saved broadcasts) follows in its own change; the pairing API it uses is
already on the edge.
