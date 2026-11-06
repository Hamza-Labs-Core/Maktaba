---
schema: 1
kind: decision
pr: 605
title: Epic 50 section 15: Cloudflare in front of the portal and console
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 1bd26d476. Stories 50.30-50.34 put the user portal and the operator console (`admin.maktaba.tv`) behind the Cloudflare proxy, with WAF, rate limits and Turnstile. The origin is reachable only through a Cloudflare Tunnel from the database leader, and `CF-Connecting-IP` is trusted only from that local connector. Relay video stays DNS only because of Cloudflare's terms.
- The console uses Maktaba's own staff accounts, separate from customer accounts, with required MFA, short sessions, re-authentication, lockout and full audit. Cloudflare Access was dropped, and admin routes are 404 on every user name.
- The independent plugin store (`plugins.maktaba.tv`, #606) sits behind the same proxy with its own auth, admin pages, alerts and deploy token. Cloudflare deploys run from CI only; the owner's setup steps and estimated costs are in section 15.7.
