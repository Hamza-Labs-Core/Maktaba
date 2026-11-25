---
schema: 1
kind: pr
pr: 1629
title: "Specs 13.19-13.22: the desktop Player's libmpv follow-ups (device_caps, subtitles, engine setting, Live TV and JavaFX's removal)"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
Four Epic 13 stories, specs only, each written from what 13.18 (#1591) built and from its PR's "Differences from the story":
- **13.19 (M):** `device_caps` from a start-up probe on its own libmpv handle: hardware decoders and software speed per codec, cached per GPU, driver, CPU and libmpv. It also measures the audio device's channels. `VideoCap` gains an optional `hardware` flag. A transcode keeps 5.1 for a 5.1 device.
- **13.20 (M):** a direct file's own subtitle tracks (text and PGS) from mpv's `track-list`, beside the home's tracks, all drawn by mpv. Profile → Playback gets the size and a new shared colour setting. Arabic is shaped by libass.
- **13.21 (S):** Profile → Playback gets **Video engine** (Automatic or the older engine). `MAKTABA_PLAYER_ENGINE` stays first. The fallback notice links to a `#video-engine` help section.
- **13.22 (M):** the channel screen moves to `MpvVideo`, then JavaFX leaves in a second PR with a defined release.
- Corrections to the first commit (48e41c11f): 13.19's probe timing, its incomplete-probe rule and a transcode's audio as it is built (1f1ad3634); 13.20's home transcriptions (ae9ee1420).
- Bringing main in conflicted only in the generated wiki databases (`web/wiki-app/public/db/`). Taking main's side and running `generate_wiki.py` again resolved them.

## What we learned
- **The desktop watch screen never applied the subtitle size.** `Watch.kt` gives `VideoSurface` no `captionScale`, the desktop Profile → Playback has no size row, and no Player has a colour setting: 13.18's "size and colour settings" did not exist to apply.
- **A direct file on the desktop offers no subtitles at all.** `Playback.followTracks` fills `subtitleOptions` only from an HLS master.
- **`desktop-mpv` has no `Ceiling`.** A report only narrows it, and `decide` already reads the report's per-codec heights and bit depths for every profile. Narrowing direct play needs no home change, only tests.
- **The home encodes H.264 only.** A device report cannot pick a transcode's video codec, only its height and its audio channels.
- **"The release after" means nothing when a tag is cut every 30 minutes.** A removal is tied to a pull request whose merge conditions are stated: days since a tag first carried both stories, no open issue that needs the older engine, and the real-library tests green for 14 days.
- The wiki's Arabic `content` is an excerpt of about 600 characters. A translation ends where the English excerpt is cut, even inside a code span.

## Keep doing
- Writing follow-up specs from the built branch's code, not from the parent spec: three of the four found that the parent spec's assumptions did not hold.
- Resolving the generated wiki databases by regenerating them, never by hand.

## Stop doing
- Specs that promise to apply a setting without checking that the setting exists.

## Open
- None of 13.19–13.22 is built. 13.20's colour setting reaches the Android Players only in a later story.
- 13.22's removal pull request waits for its stated conditions.
