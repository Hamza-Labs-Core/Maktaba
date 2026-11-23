---
schema: 1
kind: pr
pr: 1549
title: "The root README is short again: 516 lines to 127, its detail moved to the docs that own it"
date: 2026-11-23
session: 6c776e9f-79b4-4a72-a98b-2044a761ee11
agent: claude-code
prs: [1549]
---

## What happened
- The root README had grown to 516 lines. It held the pitch, two app tables, the architecture,
  developer prerequisites, every install path in full, signing, the cloud relay, the project
  tree, a roadmap and the domains.
- #1549 cuts it to 127 lines: what Maktaba is, one install section per way to run the home (Mac,
  Windows, Linux, NAS, Docker), a compact apps table, developer links, licence and security.
- Each removed section was checked for a home. Most already had one (`deploy/packaging`,
  `docs/hardware.md`, `docs/wiki/deployment.md` §12, `apps/desktop`, `deploy/cloud-relay`,
  `specs/epics/README.md`, `repo-layout.md`, `docs/cloud-dns.md`). The rest moved:
  - the apps' files, testers, signing, sign-in by code and building from source moved to
    `apps/README.md`;
  - the prerequisites with install commands moved to `CONTRIBUTING.md`;
  - the tech stack moved to the architecture guide, in English and Arabic;
  - the Docker one-container install moved to `docs/wiki/deployment.md`.
- maktaba.tv is not live, so the NAS and Docker guides link to their source in
  `web/packages/install/site/` and `deploy/nas/`, with a note that maktaba.tv/install will host
  them.

## What we learned
- The README drifted further than the docs it summarised. It still said Tailwind and Vidstack
  (neither is in `web/`), streaming on Go 1.25 (1.26), 54 epics (60), and Linux aarch64 without
  transcription. `CONTRIBUTING.md` still required Go 1.23. Releases had added files it never
  named: `Maktaba-Core_*.dmg` and the Samsung and LG launcher packages.
- A summary that repeats details goes stale. A summary that only links does not.
- `links.mjs` also checks scheme-less `maktaba.tv/...` text against `dist-manifest.json`, so
  naming a page that is not live yet is safe only when the page is in the manifest
  (`/install/` is).

## Keep doing
- Keep the README to links and one or two lines per topic. Put each detail in the one doc that
  owns it.
- Check every version and file name against `gh release view` and the code when editing an
  overview.

## Stop doing
- Adding feature detail, file lists or version numbers to the root README.

## Open
- The README's NAS and Docker links go to HTML source in the repository. Once maktaba.tv is
  live (`vars.WEBSITE_LIVE`), they should point at maktaba.tv/install.
- `docs/hardware.md`'s Windows/Linux worker table still says aarch64 has no Whisper engine,
  while its NAS section says the `stt` extra now installs one.
