---
schema: 1
kind: decision
pr: 600
title: Epic 50 spec: home broadcasting and the Broadcaster tier
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 51f0c1568. Specced publishing a home's Maktaba TV channels to outside viewers through Maktaba Cloud: README section 14, research-broadcasting.md and stories 50.20-50.29.
- Decisions: viewers never pay (invite or open only); Maktaba charges for infrastructure, not content (Maktaba Cloud is a hosting provider with no cloud-side curation); only publishable items air (own recordings, CC BY detected at YouTube intake, or sources with recorded permission; the scheduler skips the rest); the rights section is practical and marked not legal advice.
- Design: the home pushes each segment once to a multi-tenant Cloudflare edge (Worker, Durable Objects, R2); token-free cache keys keep the home's upload independent of audience; quality levels made at home; Cloudflare Stream as fallback.
- Tiers: Pro guest sharing plus a Broadcaster add-on, about $46/month at its allowance on the primary path.

## Open
- Prices, a Cloudflare terms confirmation and a lawyer's review gate the launch.
