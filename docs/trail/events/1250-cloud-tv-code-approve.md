---
schema: 1
kind: pr
pr: 1250
title: "A TV's Maktaba Cloud code is approved in the home apps and on maktaba.tv (58.03)"
date: 2026-11-14
session: ff9eaade-bd8f-4224-9087-4fbcea60ab40
agent: claude-code
prs: [1325]
---

## What happened
- Written after both pull requests merged, from their descriptions, commits and diffs; the
  sessions that did the work left no event.
- #1250 merged as 51c469119, stacked on #1240 (the cloud and home API) and retargeted to main
  once it merged. A cloud code is approved where a home code already is: **Approve a TV or
  device** in the Android and Apple Players and the Remote, and **Sign in a device** (`/pair`)
  in the web. The home code is tried first; on a 404, a code of the cloud alphabet goes to
  `GET /api/pairing/cloud-code/{code}`, the app asks "Is this your TV? Samsung, in Jordan." and
  only a yes posts `/api/pairing/cloud-approve`. `409 cloud_not_linked` tells the person to use
  the home's own code. **Sessions** lists the apps signed in with a code
  (`/api/me/app-sign-ins`), each signed out there; an older home's 404 hides the section.
- The decision sits in a testable model on each platform (`ApproveCode` in Android core and in
  MaktabaKit); the platform is named by its maker (tizen to Samsung, webos to LG) the same way
  on all three.
- #1325 merged as 06524f134, two minutes later. The TV's QR opens `maktaba.tv/tv?c=<code>`,
  whose own head script (the site's CSP allows no inline code) forwards only a valid code to
  `app.maktaba.tv/tv`; without JavaScript the page links there. On the cloud page a signed-in
  person picks one of the homes linked to their account (an offline one disabled) and
  approves with `POST /v1/tv/codes/{code}/approve`.
- The cloud approves nothing itself: it signs a one-use assertion (purpose `tvcode`) for the
  account, the home and the code and relays it to the home's new
  `POST /api/pairing/cloud-assertion`. The home checks it against the cloud key pinned at claim,
  uses it once, and approves as the user linked to that account. Owning a home's registration
  is not enough (403 `not_linked`).
- The six `maktaba.tv/tv` entries left `links-baseline.json`, which is empty again; the new page
  has eight reference images rendered in the CI Docker image.

## What we learned
- A TV approved through the cloud must end when the cloud account is unlinked. The second
  commit of #1325 (e147cfb20) records the TV's refresh family as a cloud sign-in of the link
  (scope `play`) before any token is handed out, and hands out none if that record fails. A
  refreshed cloud-code family stays restricted even when it carries a cloud scope, so an
  admin's link never yields an admin TV.
- The Android Sessions list is lazy and loads the app sign-ins after the sessions. Its
  test needed two follow-up commits: it waits for the section (the fake home answers it 1.5 s
  late, as a loaded runner would) and scrolls to each row before asserting it, because after
  scrolling to the second row the header and first row can leave the composition (2026dd30b,
  f618e1082).
- #1250 took seven merges of the #1240 branch before it retargeted; a stacked child follows
  every move of its parent.

## Keep doing
- Prove each refusal of a cross-service approval with a test on both sides: a replayed
  assertion, an unlinked account, another code, a sign-in assertion, another cloud's key on the
  home; no account, owner-only, offline and unreachable on the cloud.
