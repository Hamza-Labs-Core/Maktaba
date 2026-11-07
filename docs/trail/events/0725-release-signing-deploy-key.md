---
schema: 1
kind: pr
pr: 725
title: Tags are pushed with a deploy key, so releases are signed as a push
date: 2026-11-06
session: 818590a4-153a-40b5-942f-3549e01e064a
prs: [699]
---

## What happened
- After #699, auto-tag pushed tags with `GITHUB_TOKEN` and dispatched the tag workflows. v0.3.85 (ad47c900f, release run 42321936100) was signed by a `workflow_dispatch` run, and its own `releaseverify` step refused it (`expected BuildTrigger to be "push", got "workflow_dispatch"`). The release stayed a draft, and that draft is now deleted. v0.3.83 (871c3e3a8) and v0.3.84 (bc51b0848) were tagged with no workflow started at all.
- auto-tag now pushes the tag over SSH with a write deploy key (secret `RELEASE_TAG_DEPLOY_KEY`), fails before tagging without it, dispatches nothing, and checks that each tag workflow got a push run. `release.yml` takes no dispatch.
- Desktop Release run 42321939437: every Compose UI test on the linux-aarch64 player leg failed with a Skiko `UnsatisfiedLinkError`. The ubuntu-22.04-arm image has no libGL, libEGL, libX11 or fontconfig. The Linux player legs now install them.

## What we learned
- The v0.3.85 certificate (Rekor index 3727499394) differs from a tag push's in one claim only, the trigger (OID 1.3.6.1.4.1.57264.1.20). The SAN, `BuildConfigURI` and the ref are the same. v0.3.82's `certificateIdentity` refuses it, and accepts the same certificate with only the trigger changed to `push`. A release has to come from a tag push, or every home's updater refuses it.
- A re-run of a dispatched run is still `workflow_dispatch`. A tag with no push run of `release.yml` can't get an acceptable release unless the tag is deleted and pushed again. v0.3.83 to v0.3.85 stay as tags with no release. v0.3.82 is the latest until the next tag.
- CI builds the Desktop Player on x64 only, so arm-only native-library gaps show up first at release. Reproduce them locally with `docker --platform linux/arm64 ubuntu:22.04` and `ldd -r` on the extracted `.so`.

## Open
- The deploy key and the `RELEASE_TAG_DEPLOY_KEY` secret need setting up (docs/updates.md, "The release deploy key"). Until then auto-tag fails at its first step and makes no tag.
