# Google Cast

The Maktaba Cast receiver (story 58.07) and the iPhone, Android and Chrome senders (story
58.06). Story 58.04 adds the submission notes here; story 58.07 adds the receiver's console
registration, the test devices, its release and the LAN spike.

## The receiver

- **Where:** `https://cast.maktaba.tv/r/`, the one URL registered in the Cast console. The page
  there (the loader) picks the receiver build for the sender's `api_level` from
  `https://cast.maktaba.tv/r/builds.json`: the newest build at or below it, or an
  "Update the home" screen for a home older than every build. Builds live at `/r/<level>/` and
  stay while their level is in the 12-month window (until 12 months after a newer level's build
  replaced them). One registration serves every home version.
- **What:** `web/packages/castreceiver` (a CAF Web Receiver with Shaka for HLS, a Cloudflare
  Worker in front of static assets). No media goes through it: the TV fetches every byte from the
  home or the relay. The contract with the senders is
  [docs/developers/contributing/casting.md](../developers/contributing/casting.md).
- **Release:** a `v*` tag runs `cast-receiver-release.yml`: build, tests (T01 to T04), the kept
  builds copied from the live site, then `wrangler deploy` when the receiver changed. It deploys
  only once the repository variable `CAST_RECEIVER_LIVE` is `true`; until then it builds and
  tests and says so in the run's summary.

## Console registration (once)

1. In the [Google Cast SDK Developer Console](https://cast.google.com/publish), under the
   project's Google account, pay the one-time registration fee ($5).
2. **Add new application** → **Custom Receiver**. Name: Maktaba. Receiver application URL:
   `https://cast.maktaba.tv/r/`. Leave Guest Mode off.
3. The console shows the **application ID** (8 characters). It is public: it goes in
   `shared/cast/app.json` (story 58.06), which every sender reads.
4. Leave the application **unpublished** until the checklist below has passed on the test devices,
   then **Publish**. It never needs republishing: the registered URL never changes, and new
   receiver builds deploy behind it.

## Test devices (unpublished builds)

An unpublished receiver loads only on registered devices.

1. Find the device's serial number: Google TV Streamer and Chromecast with Google TV under
   **Settings → System → About → Status** (or the box's label); a TV with Cast built in in its
   own About screen, or the Google Home app's device settings.
2. In the console, **Add new Device**: the serial number and a description (for example
   "Streamer, living room"). It takes about 15 minutes to apply.
3. Restart the device. It then loads `https://cast.maktaba.tv/r/` for the application ID, and its
   receiver page can be inspected from Chrome at `chrome://inspect` on the same network (the
   remote debugger), or with Google's Cast Command and Control tool.
4. A debug receiver build (`npm run build:debug`) turns on CastDebugLogger and the console log;
   a release build logs nothing to the device.

## The LAN spike (Local Network Access)

The first device task of story 58.07, recorded here and in research.md. A receiver page is HTTPS,
so its media must be HTTPS: the home's per-home HTTPS name (story 50.15) is what makes LAN playback
possible. Chrome's Local Network Access treats a public name that resolves to `192.168.x.x` as
local, and whether Cast devices enforce it (they have no permission prompt) is not documented.

| Step | On | Record |
|---|---|---|
| 1 | Google TV Streamer, Chromecast with Google TV | cast a direct-play MP4 from a sender on the home's network, `home` set to the home's HTTPS LAN name |
| 2 | the same | the receiver's network log in the remote debugger: does `GET /api/discovery/home` on the LAN name succeed, or fail with a Local Network Access or CORS error? |
| 3 | the same | an HLS session on the LAN name: do the segments load? |
| 4 | the same, the LAN name blocked | the receiver falls back to the relay within 6 seconds and shows the Remote badge |

If LAN fetches are blocked, the receiver keeps using the relay for everything (the cost: relay
bandwidth), and the finding goes in research.md with the device's firmware version.

**Result:** not run yet.

## Submission notes

The store's reviewers sign in to the project's review home, review.maktaba.tv (story 58.04).
The notes, what reviewers get and how a submission round goes are in
[review-home.md](review-home.md): its text goes in nowhere a reviewer reads (Google's Cast console has no review): the notes are what a tester of the published receiver follows, with a sender signed in to the review home. The password is never written
here: the notes name the release secret `REVIEW_HOME_VIEWER_PASSWORD` (and, for the parental
checks, `REVIEW_HOME_CHILD_PASSWORD`), and the release session fills each in when it submits.
After the round, the release checklist rotates both ([review-home.md](review-home.md#a-submission-round)).

## Real-device checklist

Run before each receiver release candidate is published in Google's Cast console, and before a
sender release that changes casting (58.06 T06, 58.07 T05). The rule and the result line are in
[README.md](README.md).

**What CI covers instead:** the receiver in Playwright with a CAF stub, the senders' unit and UI
tests with SDK fakes. **Only a device shows:** anything involving Google's SDK on a device, the
LAN fetch rules, the formats, and discovery and the session between a real sender and receiver.

### Devices

- Google TV Streamer
- Chromecast with Google TV
- a TV with Cast built in (Vizio or Sony)
- senders: an iPhone, an Android phone, Chrome on a computer

The devices are registered as test devices in the Cast console (their serial numbers), so they
load the unpublished receiver. Capture the receiver's log with the Cast CaC Tool (or Chrome's
remote debugging of the receiver) for the whole run.

### Checklist

| # | Check | Pass when |
|---|---|---|
| 1 | Cast from each sender on the home's network | the device is found, the receiver loads, the item plays |
| 2 | The home's LAN HTTPS name | the receiver fetches from it directly, with no mixed-content or certificate error |
| 3 | The relay (the phone away from the home's network) | the item plays through the relay |
| 4 | Direct play, then an HLS fMP4 session | both play; the receiver reports progress to the home |
| 5 | Dolby Vision and HDR10 files on the Google TV Streamer | play in HDR |
| 6 | EAC3 audio on a receiver or soundbar | passthrough |
| 7 | Subtitles, then Arabic subtitles | drawn by Maktaba's layer, Arabic joined and right to left |
| 8 | A queue of three items | the receiver moves to the next item and the senders follow |
| 9 | A sender in Arabic | the receiver's screens show Arabic, mirrored |
| 10 | Leave the item paused past its URL's lifetime, then resume | the receiver renews the URL itself and plays |
| 11 | An HEVC file on the Chromecast with Google TV | the session is HLS fMP4 or direct, never HEVC in TS |
| 12 | A Chromecast 3rd gen (H.264 1080p only) with an HEVC file | a transcode to H.264 plays |
| 13 | Two senders cast to one TV | the second takes over; the first sees its mini controller end |
| 14 | A grant left to expire mid-film (more than 6 hours, or a shortened TTL on a test home) | the film plays to its end, then the "cast has ended" screen |
| 15 | The receiver's console in the remote debugger during 1 to 14 | no Content Security Policy violation, no uncaught error |
| 16 | Each sender in English, then Arabic: cast a video, lock the phone (or close the Chrome tab) for 20 minutes | the TV plays on; the home's Continue Watching shows the TV's position |
| 17 | Play all, then Shuffle, from each sender | the TV plays the server queue; each sender's mini controller follows the next item |
| 18 | The iPhone's first cast | the local network screen, then iOS's prompt, then the TV list with AirPlay beside the Cast TVs |
| 19 | Chrome on the home's `http://` address, then on its `https://` address | no Cast button, then the button and Chrome's TV picker |
| 20 | The Android Player on a test home from before the cast grant | the TV shows Google's player with the plain signed URL; the phone says the home is older |
| 21 | A guest network that hides devices from each other | each sender says the TV must be on the same network |

## Results

No result yet (the first release candidate records the first line).
