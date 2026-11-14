# Playback: profiles, device reports and signed URLs

A player asks the home for a stream with `POST /api/stream/sessions`. The answer is a signed
URL to play: the file itself (`direct`), or an HLS or DASH playlist (`remux`, `transcode`). This
guide is for people who write a player, a TV app in particular. It says how the home picks the
mode, what a device should report, and which URLs a player may fetch without a header.

The request and the answer are in the [API reference](https://maktaba.dev/api/), under
**Sessions** (`SessionRequest`, `DeviceCaps`, `Session`).

## The client profile

`client_profile` names what kind of player asks. The streaming service keeps one profile per
kind (`streaming/internal/capability`): the containers, codecs, HDR formats, height, bitrate
and audio it plays. An unknown name is `generic`: H.264 and AAC in HLS, at most 720p.

| Profile | For |
|---|---|
| `generic` | anything that names no profile, or one the home does not know |
| `desktop-player` | the Windows and Linux desktop Player |
| `browser-chrome`, `browser-safari` | the web app |
| `ios-native`, `tvos` | the Apple apps |
| `android-native`, `androidtv` | the Android apps |
| `firetv`, `tizen`, `webos`, `roku`, `cast`, `vega` | the TV platforms (Fire TV, Samsung, LG, Roku, a Cast receiver, Vega OS) |

Each TV profile alone is a **baseline** that plays on every model of its platform: H.264 at
1080p, in MP4 or as HLS with TS segments. A newer TV plays far more than that, and what it plays
depends on its model and year, not its platform. So a TV app also reports what its device plays.

## Reporting what the device plays

`device_caps` is optional. Fill it from the platform's own query:

| Platform | Query |
|---|---|
| Fire TV, Android TV | `MediaCodecList`, `Display.getHdrCapabilities` |
| Samsung (Tizen) | `webapis.avplay`, `webapis.productinfo` |
| LG (webOS) | `MediaSource.isTypeSupported` with `webOS`'s `deviceInfo` |
| Roku | `roDeviceInfo` (`CanDecodeVideo`, `CanDecodeAudio`, `GetDisplayProperties`, `GetAudioOutputChannel`) |
| Cast receiver | `cast.framework.CastReceiverContext.canDisplayType` |
| Vega OS | its W3C media capability calls |

```json
{
  "containers": ["mp4", "mkv", "ts"],
  "video": [{"codec": "hevc", "max_height": 2160, "max_fps": 60, "bit_depth": 10},
            {"codec": "av1", "max_height": 2160}],
  "hdr": ["hdr10", "hlg", "dv-p5", "dv-p8"],
  "audio": ["aac", "ac3", "eac3"],
  "passthrough": ["eac3", "truehd"],
  "max_audio_channels": 8,
  "hls_segments": ["ts", "fmp4"],
  "dash": true,
  "max_bitrate_kbps": 80000
}
```

The rules:

- The report **narrows** the profile freely: a Fire TV Stick without HEVC lists only `h264`, and
  an HEVC file is transcoded for it.
- The report **widens** the profile only up to its platform's **ceiling**. A buggy or modified
  app can ask for a 4K HEVC direct play, never for something its platform cannot play: Samsung
  never gets Dolby Vision, Roku gets it only as profile 5 and gets AV1 only by DASH, and a Cast
  receiver gets no MKV. What a report asked beyond the ceiling is named in the session's log
  line (`clamped`).
- A field left out is the profile's. An empty list is the device's answer (none), not a field
  left out. With `video` given and no `max_bitrate_kbps`, the
  platform's ceiling holds. A relay plan's bitrate cap still applies over the device's.
- Keys the home does not know are ignored, so an app may send a newer report to an older home.
  A value of the wrong type, or a list of more than 32 entries, is a 400.
- Report again whenever the output changes (the TV's HDMI switched to a receiver without HDR, a
  Fire TV moved to another screen): open a new session at the same position. A session never
  changes its mode, and a session opened for one report is never handed to another. A user keeps
  at most two sessions of a video open for different reports (two TVs); opening a third closes
  the oldest.
- If the device cannot play what it got, retry once with `force_transcode`.

## How the mode is picked for a TV

For a TV profile, the streaming service weighs, in order:

1. `force_transcode`: always a transcode.
2. A picture taller, a bitrate higher, or a codec, bit depth or frame rate the device does not
   decode: a transcode to H.264, tone-mapped to SDR for an HDR source.
3. HDR. A Dolby Vision profile 8 or 7 source goes to a device without its `dv-*` as its HDR10
   (8.1, 7) or HLG (8.4) base layer. A profile 5 source has no base layer another format reads:
   a transcode. HDR10 or HLG the device does not show: a transcode.
4. Audio the device neither decodes nor passes through: TrueHD and DTS become EAC3 where the
   device decodes EAC3 or passes it through, anything else AAC, beside the video as it is. On
   Roku a stereo AAC track goes beside Dolby audio, for a TV without a receiver.
5. A video codec the platform plays only by DASH (Roku's AV1) goes by DASH.
6. A container the device does not play, or any of 3 and 4: a remux into HLS (fMP4 segments for
   HEVC and AV1, TS for H.264). Nothing else to change: direct play.

The older profiles decide on codecs, container, height and bitrate, and transcode (tone-mapped
to SDR) HDR they do not show: a Dolby Vision source they lack plays as its base layer where they
show it, and profile 5 is a transcode. Codec names are read as one name each, so a profile's
`h265` is the probe's `hevc` and `ec-3` is `eac3`. HEVC is on `android-native` and `androidtv`
only: Chrome decodes it only on some hardware, and Safari and AVPlayer refuse HEVC tagged `hev1`.

## URLs a player fetches without a header

Samsung's AVPlay, LG's `<video>`, Roku and a Cast receiver cannot be relied on to send a cookie
or an `Authorization` header. Every media URL the home hands out is signed (`?sig=`):

- `manifest_url` and `direct_url` carry their signature. A playlist's child URLs (variant
  playlists, segments, init sections, subtitle renditions) come back signed with the same token.
- `stream_sig` is the token for the session's own paths, in every mode, direct play too:
  `/stream/{session_id}/subs/sidecar/{lang}.vtt`, `/stream/{session_id}/subs/auto.vtt` and
  `/stream/{session_id}/chapters.json`. Append `?sig=` and the token. It expires with the
  session's URLs.

A signed URL is the credential: keep it out of logs and crash reports.

## Where it lives

| Part | Path |
|---|---|
| Profiles, ceilings, the merge and the verdict | `streaming/internal/capability` |
| The session's open | `streaming/internal/grpcsrv/server.go` |
| The request, `device_caps` and `stream_sig` | `api/internal/handlers/streaming` |
| The report vectors each app's reader is tested against | `shared/playback/caps-vectors.json` |
