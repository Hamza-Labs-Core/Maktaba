---
schema: 1
kind: pr
pr: 1672
title: "The website's help mockups without the broadcasting topic: main's unit job was red"
date: 2026-11-24
session: 0ff9751a-84bb-48fc-b049-202b5dd682e5
agent: claude-code
---

## What happened
#1653 took the broadcasting topic out of the help index, and the mockups drawn from it (`help.en.html`, `help.ar.html`) were not made again. main's `unit / unit` failed on `mockups.test.mjs` (run 45493789349), and with it every pull request that runs the design tests. #1672 runs `make.mjs` and commits the two files.

## What we learned
- **The website's mockups are generated from the help index too**: a change of a help topic needs `node web/packages/website/design/mockups/make.mjs`.

## Keep doing
- Checking whether main itself fails before looking for the fault in a branch: its own run showed it at once.

## Stop doing
- Reading a cancelled run on main as a passing one: three in a row were cancelled by the next merge, so the first that finished held several merges.

## Open
- The rendered PNGs of these two mockups still show 42 topics; CI does not compare them.
