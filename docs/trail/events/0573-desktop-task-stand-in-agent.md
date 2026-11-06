---
schema: 1
kind: pr
pr: 573
title: Windows task tests run a stand-in agent that starts at once
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 619509bd5. desktop-shell-windows flaked on main (run 41509445044). The real Task Scheduler tests ran a PowerShell stand-in agent, and PowerShell's cold start on a fresh windows-latest runner (20-50 s) exceeded the tests' 30 s first-run wait.
- The tasks now run the test binary itself (`stand_in_agent`, told what to do by a file in the task's working directory), and a wait that runs out prints the task's `schtasks /V` state.
- The only failure in the last 300 CI runs; not a product bug.

## What we learned
- PowerShell's cold start on a fresh windows-latest runner takes 20-50 s; do not use it as a test stand-in.
