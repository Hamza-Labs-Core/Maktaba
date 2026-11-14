# Story 58.06 — Google Cast senders in the Android Player, the Apple Player and the web

> Epic 58 · Phase 2 · Depends on: 58.07 · Needed by: 58.14, 58.16

## Why

Cast reaches TVs Maktaba will never have an app on:
- Chromecast and the Google TV Streamer.
- Every Google TV set: Sony, TCL, Hisense's Google TV models.
- Vizio's TVs with Chromecast built-in. Vizio's own app platform needs a partner agreement
  (research.md §8).

Today only the Android Player casts (`apps/android/player/…/media/Cast.kt`), and only to Google's
Default Media Receiver, on the LAN:
- No Arabic.
- No queue beyond one item.
- Progress is only what the phone reports.
- The phone has to stay awake for the home to learn where the TV stopped.

The Apple Player and the web cannot cast at all.

## Description

All three senders use Maktaba's receiver (58.07, application ID kept in `shared/cast/app.json`)
instead of the Default Media Receiver.

**What a sender hands the receiver**, in `MediaInfo.customData`, never in headers:

```json
{"home": "https://192-168-1-20.<home-id>.<direct-domain>:8444",
 "grant": "<cast grant>",
 "video_id": "…", "queue_id": "…", "start_sec": 754,
 "lang": "ar", "api_level": 3}
```

- **`home`** is the address the sender itself uses when that address is reachable from the TV.
  That is the home's HTTPS name of story 50.15, or the relay. Never `localhost`, never a `.local`
  name, and never a bare IP over HTTPS, whose certificate check would fail on the receiver.
- **`grant`** is a cast grant: a token the home mints for the sender
  (`POST /api/cast/grants {video_id | queue_id}`). It is scoped `cast`, names one video or one
  play queue, lasts at most 6 hours, and allows only:
  - opening stream sessions for those items,
  - reporting their progress,
  - reading their titles, posters and subtitles.

  It is never the person's own token. A grant for a restricted child carries the child's
  parental limit (58.14).
- The receiver uses the grant to renew signed URLs and to report progress itself, so the phone
  can sleep or leave the house and the home still knows where the TV is.
- **The contract is shared.** `shared/cast/app.json` holds the receiver's application ID (empty
  until the Cast console registration; no build shows Cast until it is set), its URL and the
  custom namespace. `shared/cast/load-vectors.json` holds the load's custom data case by case,
  and the web, Android and Apple senders each run it.
- **Older homes.** A sender learns that a home predates the grant from the grant route's 404.
  The web never meets such a home: the home serves the web itself.

**Android Player.** The `CastOptionsProvider` application ID becomes Maktaba's, and `CastItems`
adds the custom data above. The existing Media3 `CastPlayer` handover (remote-to-local) is kept.
The Default Media Receiver stays as a fallback when the home is older than the cast grant (the
grant route answers 404): the sender switches with `CastContext.setReceiverApplicationId` and
sends a plain signed URL, as today. The Apple Player cannot switch application IDs after setup,
so for an older home it loads a plain signed URL on Maktaba's receiver, which plays a load
without Maktaba custom data as it came (58.07).

**Apple Player.** It gains the Google Cast iOS SDK beside AirPlay:
- **Info.plist.** `NSLocalNetworkUsageDescription` (which exists for home discovery) and
  `NSBonjourServices` with `_googlecast._tcp` and `_<APPID>._googlecast._tcp`.
- **Interstitial.** Google's required screen explaining local network access appears before the
  first cast.
- **The route picker.** It shows AirPlay and Cast devices side by side. AirPlay stays the default
  on Apple TVs and AirPlay TVs.
- **No tvOS.** The Cast SDK is iOS and iPadOS only.

**The web.** It gains the Cast Web Sender, **Cast to a TV** (`web/src/lib/cast/`,
`components/CastControl.tsx`):
- The button shows only in Chrome where `chrome.cast` loads, on an HTTPS page, outside Maktaba
  Cloud, with an application ID set and a home address a TV can reach.
- On a home reached over plain HTTP, the button is hidden, with a line in Help.
- It follows the receiver's media status (the queue's next video, the position) and renews the
  grant over the custom namespace 30 minutes before it ends.

**Queue and controls.** Play all and Shuffle (Epic 56) cast the server queue (`queue_id`). The
receiver walks it with the grant. Pause, seek, next, audio and subtitle choice are sent as
standard Cast media commands. The receiver's state is shown in each sender's mini controller.

**Payments:** none. Casting is free on every home and every plan. Relay bandwidth used by a TV
playing through the relay counts against the home's relay plan as any stream does.

## Acceptance criteria

- **Given** an iPhone and a Google TV Streamer on the same network, **when** the person casts a
  video, **then** it plays on the TV in direct play or HLS fMP4 (58.02), with Arabic subtitles if
  chosen, and the iPhone shows a mini controller.
- **Given** a cast in progress, **when** the phone is locked for 20 minutes, **then** the home's
  Continue Watching shows the TV's position (the receiver reported it).
- **Given** the web on a home reached at `http://`, **then** no Cast button is shown.
- **Given** a home older than this story, **then** the Android Player casts to the Default Media
  Receiver as today.
- **Given** a cast grant for video A, **when** it is used to open a session for video B, **then**
  403 (role test, 58.17).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go Postgres | `POST /api/cast/grants` as viewer, admin, restricted child, no one; use for other items | mint and use | scope `cast`, ≤ 6 h; other items 403; no one 401 |
| T02 | Kotlin unit (`CastTest.kt`) | a `MediaItem` | convert | custom data shape; no headers or user token |
| T03 | Swift unit (MaktabaKit) | a video and a queue | build cast request | same shape; home address never `.local` or bare IP |
| T04 | Vitest (web) | `chrome.cast` stub | load on https / http | button shown / hidden |
| T05 | Player UI tests (Android Robolectric; Apple throwaway iPhone simulator) | a fake `CastContext` | cast, lock, resume | mini controller; interstitial before the first cast on iOS; en and ar, dark |
| T06 | Real-device checklist | Google TV Streamer, a Chromecast with Google TV, a Vizio or Sony TV with Cast | iPhone, Android and Chrome senders | direct play, HLS fMP4, subtitles, queue, Arabic |

**Honest note:** there is no Cast emulator (research.md §5.1). Senders are tested against fakes of
the Cast SDK (T02–T05). The pairing of a sender with a receiver is checked only on real devices
(T06).

## Strings

| Key | English | Arabic |
|---|---|---|
| `cast.localNetwork.title` | Find TVs on your network | ابحث عن أجهزة التلفاز على شبكتك |
| `cast.localNetwork.body` | To cast, Maktaba needs to see devices on your Wi-Fi. You'll be asked next. | لكي تبثّ، يحتاج Maktaba إلى رؤية الأجهزة على شبكة Wi-Fi لديك. سيُطلب منك ذلك الآن. |
| `cast.castingTo` | Playing on %1$s | يُعرض على %1$s |
| `cast.oldHome` | This home is older, so the TV shows Google's player. | هذا المنزل أقدم، لذا يعرض التلفاز مشغّل Google. |
| `cast.web.needsHttps` | Casting needs the home's secure address (https). | يحتاج البث إلى العنوان الآمن للمنزل (https). |

## Edge cases

- **Guest or hotel networks blocking mDNS**: no device is found. The sender's empty state says
  the TV must be on the same network, and suggests AirPlay or the TV's own app where one exists.
- **The sender is on the relay but the TV is on the home's LAN**: the sender passes the home's
  HTTPS LAN name if the home's `connections` list one. The receiver tries it first and falls back
  to the relay (58.07).

## Build plan

| File | Change |
|---|---|
| `api/internal/castgrant/` (new: `castgrant.go`, `gate.go`) | `POST /api/cast/grants`; the `cast` audience, and the gate (before the parental gate) that limits a grant to `castgrant.Routes` for its items |
| `apps/android/player/…/media/Cast.kt`, `CastSender.kt`, `CastButton.kt` | Maktaba's app ID, custom data, fallback |
| `apps/apple/Player/…/Cast/` (new), `project.yml` | Cast iOS SDK, interstitial, picker |
| `web/src/lib/cast/` (new), `web/src/components/CastControl.tsx` | Web Sender |
| `shared/cast/app.json`, `shared/cast/load-vectors.json` (new) | the receiver's application ID, URL and namespace; the load vectors every sender runs |
