---
schema: 1
kind: pr
pr: 1621
title: "The real Google Cast SDK runs in CI; how Cast goes on for App Store review"
date: 2026-11-24
session: 366f9ce3-e03b-40f4-944e-650e7dafc5b7
agent: claude-code
---

## What happened
- Decision `apple-cast` (2026-11-24): turn Cast on and test it for review.
- The Player's Cast code and its UI tests already existed, but only against a fake context.
- #1621 adds a Debug launch argument, `-maktaba-cast real:<app id>`. It starts the real Google
  Cast SDK with Google's public Default Media Receiver (`CC1AD845`).
- A new UI test checks the SDK end to end on CI's simulators:
  - it sets up;
  - it discovers after the local network screen;
  - the picker reports that no TV was found and offers AirPlay.
- `docs/stores/cast.md` gives the order for turning Cast on: register, deploy, checklist,
  publish, then the App Privacy answers and review notes.

## What we learned
- **Cast cannot go on from code alone.** `shared/cast/app.json` has no `receiver_app_id`, and
  `cast.maktaba.tv` did not resolve on 2026-11-24. Registering the receiver is an owner step
  in the Cast console, and deploying it is gated on `CAST_RECEIVER_LIVE`.
- **An unpublished receiver loads only on devices registered in the Cast console**
  (https://developers.google.com/cast/docs/registration). App Review's devices need it
  published.
- **The iOS sender SDK 4.8.x needs no multicast entitlement.**
  - Google's sender page asks only for `_googlecast._tcp`, `_<app id>._googlecast._tcp` and
    the local network prompt.
  - The permissions page says Apple will not grant the multicast entitlement just for Cast.
  - The #1574 audit had said it was needed; that is corrected there.

## Open
- **Owner: register the receiver and give its ID.** It costs $5 once, at
  `https://cast.maktaba.tv/r/`. Then set `CAST_RECEIVER_LIVE`, register the test devices, run
  the checklist and publish.
- **The change that turns Cast on follows once the ID exists:** the ID in `app.json` and the
  regenerated `Config/Cast.xcconfig`.
- **Google Cast SDK on the simulator.** Whether discovery runs there is not documented by
  Google. This PR's first CI run is the evidence.
