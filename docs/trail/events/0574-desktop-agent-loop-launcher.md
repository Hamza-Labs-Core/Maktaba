---
schema: 1
kind: pr
pr: 574
title: Agent-loop tests start the test binary as their launcher, not PowerShell
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 8e0553841. The Windows agent-loop tests in `agent_windows.rs` used PowerShell as a stand-in launcher, and its cold start on a fresh runner took one test to 51 s of its 60 s deadline.
- They now start the test binary itself through #573's `stand_in_agent` (new `launch` order), about 1 s each, and a timeout puts the agent's server.log in the CI report. No PowerShell is left in the desktop crate's tests.
