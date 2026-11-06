---
schema: 1
kind: pr
pr: 349
title: Client certificates on the home's database broke the node's pipeline
date: 2026-10-26
session: f43fad60
source: claude-trail.md
---

## What happened
Client certificates on the home's database broke the PC node's pipeline. #349 issued a
certificate per machine on the home, made every node role present it, and stopped password
rotation on TLS.
