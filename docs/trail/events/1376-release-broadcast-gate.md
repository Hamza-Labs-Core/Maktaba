---
schema: 1
kind: pr
pr: 1376
title: "Release gate: tag only commits whose nightly broadcast rehearsal passed"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1376 merged as 997205cd5. `tools/release/next_tag.py` makes a commit tag-eligible only when
  the newest finished nightly broadcast rehearsal on main for the same broadcast files (the
  workflow's pull-request paths, compared by `git ls-tree`) passed. A red or missing nightly
  blocks; the newest eligible green commit is tagged and skipped ones are named in `reason=`.
- `--broadcast-runs` is optional: without it the tool fetches scheduled and dispatched runs with
  `gh` itself, so the release session's fixed command needed no edit. Any fetch failure means no
  tag. Only scheduled runs, and dispatched runs whose name names the commit they ran on, count;
  pull request and push runs never do (a fork could name its own run).
- The first nightly, dispatched by hand on 997205cd5 (run 43666460653), failed
  `broadcast-fanout` at 1,000 viewers: 233 home PUTs for 228 segment-rungs, and one object read
  from R2 6 times at one location (limit 2). Releases are blocked until a nightly passes.

## What we learned
- A new required input to a tool a scheduled session runs breaks that session; make it optional
  with a fail-closed default instead.
- A gate that reads workflow run names must ignore runs whose workflow file the PR controls.
- The PR-size scenarios (100 viewers) can pass while the nightly size finds real fan-out
  problems; start the first nightly by hand right after enabling such a gate, so the block is
  seen at once.

## Open
- Fix the duplicate uploads and the per-location R2 read stampede, then dispatch a nightly on
  main to unblock releases. The larger nightly runner is an owner action.
