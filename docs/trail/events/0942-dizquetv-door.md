---
schema: 1
kind: pr
pr: 942
title: dizqueTV runs as a first-party plugin, with programmes built on the home (Epic 54, issue #827)
date: 2026-11-08
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [844]
---

## What happened
- `dizquetv_show`, `dizquetv_channels` and `dizquetv_audit` moved to `tv.maktaba.dizquetv`, in the `service` runner (no network), following wave 8's Plex pattern (#844). `dizquetv_show` and `ext.dizquetv` are now placed on the home, since the door serves dizqueTV there only. Roles: migration 0214.
- The token was the problem. Each programme's `icon` held `X-Plex-Token`, and dizqueTV hands stored programmes back on show, filler and channel reads. So the door has dizqueTV operations of its own:
  - show and filler writes take records with no token, and the door builds the token-bearing parts;
  - channel writes refer to programmes dizqueTV already stores, and the door fills them in;
  - reads are whitelist projections with no picture fields;
  - a final check withholds any answer that still contains the token, in full, URL-encoded or in part.
- **Security review round 1** (the Epic 50 stream):
  - **The egress hole:** channel `icon`, `offlinePicture`, `offlineSoundtrack` and `watermark.url` were written as sent, and pass-through programmes kept every field. dizqueTV's ffmpeg and the guide's clients fetch those, so a plugin with no network could still make the home fetch any URL. Now they accept only: empty; the stored value; a file under `dz_art_url`; or dizqueTV's own uploads (at its address or `dz_public_url`), with an optional `?v=` version. Pass-through programmes keep only `duration`, `isOffline` and a redirect's `type` and `channel`.
  - **Low items, both fixed:** the cluster door now carries Plex only, and a failed Plex upstream read refuses the call.
- **CodeQL** flagged `go/request-forgery` on the logo check's HEAD, which used the plugin's string once it passed `artLink`. The request is now built from the parsed `dz_art_url` and the checked file name.
- **affected-tests:** `relay.DBArtBase`, newly wired in `api/main.go`, had no test. It got one (mutation-checked), plus a `No-Test-Change` trailer for the wiring and the SchemaRev bumps. Review round 3 approved; merged at 926c79f73.

## What we learned
- A door that builds request bodies on the home and answers with projections avoids the harder job of scrubbing every answer. Keep a final, fail-closed leak check on top anyway.
- **A runner with no network can still reach out** through any field the upstream product fetches (ffmpeg inputs, guide pictures). Every URL-valued field written through a door needs an allow-list of the home's own hosts, matched to the links the pipeline really builds. A parity case with real uploads proved the allowed hosts.
- Leak tests need random tokens (one containing "dizquetv" matched ordinary text). A leak test must check for leaks before coverage, or a naive port fails on the wrong assertion.
- A refusal test should check the reason as well as the error: a helper sending `programs: null` made every case look refused.
- CodeQL `go/request-forgery`: build the outgoing URL from the admin's parsed base plus the checked name, never from the caller's string, even after a prefix check.
- A door's extension list defaults to all of them. Give each door only what its callers need: the cluster door had picked up dizqueTV from the defaults.
- A child agent without a PR-watch tool should hand the merge back explicitly. The parent took over the affected-tests and CodeQL fixes and the fail-closed merge.

## Open
- The door trusts the plugin's pairing of a rating key with its file and duration. It could read them from Plex itself; the review rated this low.
- An automated review flagged the Android direct-remote pinning (`PinnedNames.kt`, story 50.15), outside #942. It was passed to the Epic 50 stream.
