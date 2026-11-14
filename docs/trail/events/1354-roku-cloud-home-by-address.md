---
schema: 1
kind: pr
pr: 1354
title: "Roku knows the approving home by its address, not by the cloud's id"
date: 2026-11-14
session: 67aba93e-38ee-4ea4-a1ea-fdf374e67b7c
agent: claude-code
---

## What happened
- #1354 merged as 9a9bae7de. The Roku channel's cloud-code sign-in compared the poll answer's
  `home_id` (Maktaba Cloud's server id for the home) with `/api/discovery/home`'s `id` (the
  home's instance id). The two never match on a real home, so every Roku cloud sign-in ended
  unreachable. The mock home used one id for both, which hid it.
- `NetIsApprovedHome` (apps/tv/roku/source/net.brs) now checks by address, the same rule as
  the Samsung/LG launcher's `isApprovedHome` (#1348): an https connection by host name is
  proven by the certificate check; plain http or a raw IP counts only when the answering home's
  discovery `urls` list one of those proven names.
- The mock cloud gives a uuid `home_id` distinct from the discovery id and the home lists its
  relay, so screen test T03 fails with the old check (`local did not answer as the home`) and
  passes with the new one.

## What we learned
- Maktaba Cloud's `connectionsFor` also lists a home's plain public address as kind `direct`
  (for older apps), so a connection's kind says nothing about whether it proves the home; only
  https by host name does.
- A mock that reuses one value for two ids the real system keeps apart hides exactly this kind
  of bug: give distinct ids distinct values in mocks.

## Keep doing
- When a fix lands on one TV platform, check the others for the same fault: the launcher's fix
  (#1348) led straight to this one.

## Open
- The residual risk is shared with the launcher: a machine at a home's LAN address on another
  network that knows the home's relay name can still list it. Binding the LAN answer to the
  home's certificate would close it.
