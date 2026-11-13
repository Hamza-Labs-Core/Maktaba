---
schema: 1
kind: pr
pr: 1228
title: "Big screen: a visible way out, in the top row and in the View menu"
date: 2026-11-13
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
agent: claude-code
---

## What happened
- #1228 merged as 66f82173b. Leaving big screen took Settings, or Back on Home and a
  confirmation; nothing on the screen or in the app's menu said how. Every big screen page now
  has **Leave big screen** after its tabs (the arrows reach it, it leaves at once), the desktop
  apps have View → Leave Big Screen, and View → Big Screen leaves when the page is already in
  big screen, so its shortcut goes in and out.
- Asked how to leave big screen, the session first answered that the web app had no such mode.
  It had searched the main checkout, which was 4,583 commits behind `origin/main`, and a shell
  glob error had hidden the search's own failure.
- `affected-tests` refused the change to `apps/desktop/src-tauri/src/lib.rs`: the file has no
  inline tests, so a change to the menu's wiring had no test of its own. The routing became one
  function in `bigscreen.rs` with a test, and a new integration test
  (`tests/menu_contract.rs`) reads the shell's sources and the web app's `KNOWN_MENU_IDS` and
  fails on an id the shell never declares.
- `tests/stt/test_benchmark.py` failed integration with `StopIteration`, as it had once on
  #1204: it replaced `time.monotonic` for the whole process with a short list of readings, and
  another thread used them up. The measure now reads the clock through its own function.

## What we learned
- The main checkout in this repository can be far behind; search `origin/main` (`git grep
  <text> origin/main`) or a fresh worktree before saying a thing does not exist.
- In zsh a glob that matches nothing aborts the command (`no matches found`); a search built
  with `--include=*.tsx` printed that and nothing else. Quote the pattern, and read an empty
  result as "the search failed" until it is shown to have run.
- A monkeypatch of `module.time.monotonic` patches the `time` module itself, for every thread.
  Give the code its own clock function and patch that.
- The desktop shell's menu ids are kept by hand in Rust and in TypeScript; a source-reading
  test is a cheap way to hold the two lists together.

## Keep doing
- Fix a test that fails a second time for the same reason, even when it is outside the change.

## Stop doing
- Answering that a feature does not exist from one failed search.

## Open
- The new menu item was not clicked in a running desktop app; its logic is unit tested and
  the menu compiles.
- Leave big screen is not shown over a playing video or a live channel, which have no top row.
