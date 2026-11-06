---
schema: 1
kind: pr
pr: 139
title: Plex can discover the emulated HDHomeRun tuner
date: 2026-10-20
session: 89e92223
source: claude-trail.md
---

## What happened
Plex could not discover Maktaba's emulated HDHomeRun. #139 fixed three causes: the SSDP
location was relative, UDP 65001 was not answered, and the device ID failed its check digit.
