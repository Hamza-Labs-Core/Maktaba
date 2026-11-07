---
schema: 1
kind: pr
pr: 814
title: A plugin's state shows while the pipeline is down (Epic 54, 54.07 §8)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
---

## What happened
- The supervisor's plugins runner writes the home's decision on each plugin into its own status part (waiting with the gate's reason, stopped while held, off, running); the parts roll up worst first, so a refusal or hold outranks the pipeline's running and the pipeline's own phase wins when the gate passes. First half of #708.

## What we learned
- Update-rehearsal scenario 15 could not see a plugin's state: the rehearsal's pipeline is a stand-in, and the runner wrote `off` for every installed plugin, leaving the phase (and the gate's reason) to the pipeline. The home decides the gate; writing it where the home writes its own status makes the state true without a pipeline.
- `store.Rollup` (worst phase first) already gives the precedence wanted: no new merge code, only what each part writes; a test of the merged view pins it.
