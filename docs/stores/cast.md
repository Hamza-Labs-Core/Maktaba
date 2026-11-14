# Google Cast

The Maktaba Cast receiver (story 58.07) and the iPhone, Android and Chrome senders (story
58.06). Story 58.04 adds the console steps and the submission notes here; story 58.07 adds the
test device registration.

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

## Results

No result yet (the first release candidate records the first line).
