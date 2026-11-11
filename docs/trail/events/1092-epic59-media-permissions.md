---
schema: 1
kind: pr
pr: 1092
title: "Story 59.03: media permissions, read-only libraries, subtitles stored in the database"
date: 2026-11-10
session: 8a91d226-107e-4a4c-aa7c-59081800990e
agent: claude-code
---

## What happened
- Merged in b15657cdd (head 1395cc63d) with every check green.
- **The check** (`shared/go/mediaaccess`) gives each library root one of three results:
  - **rw:** a temporary folder can be made, and removed, under `root/.maktaba`;
  - **ro:** the root can be listed but not written; the result also says whether the mount is `:ro`;
  - **none:** the root cannot be listed.
  - A sample read adds "partial" and up to 5 unreadable examples.
- **When it runs:** at setup, when a library is added or its roots change, from **Check access**,
  and after a write fails with a permission error. Never at every start, because the probe write
  wakes sleeping disks.
- **The fix text** is in the platform's own terms: DSM, TrueNAS, Unraid, or a general one.
- **Read-only libraries are supported:**
  - writing actions answer 409 `library_read_only`;
  - a scan of a `none` library answers 409 `library_no_access`;
  - subtitles go to `subtitle_files.content` (migration 0219, replicated, not in
    `cluster_copy_excluded()`).
- **Handover:** the first child's provider stream closed mid-work. The lead committed its
  uncommitted edits, unverified, on `wip/epic59-03-handover`. A second session took over:
  - It reviewed and kept that commit: maktaba-server passes the platform `hostinfo.Detect`
    found (#1089) to the roles as `MAKTABA_HOST_PLATFORM`, and setup asks `hostinfo` directly.
  - The web sub-branch `feat/epic59-03-web` was already in the PR in a later form.
  - It brought main in again with `merge-main.sh`, cleanly.
- **NAS CI:** `landed` gained `59.03`. The `access_ro`, `convert_409` and `access_none`
  assertions were named in `cells.json` but never written in `cell.py`; they are now. All of
  them ran and passed:
  - `media-ro`: read only, with the `:ro` mount named, and convert answers 409;
  - `media-foreign`: read only;
  - `media-none`: adding the folder is refused with 422; after it loses access, it shows none
    and its scan answers 409.

## What we learned
- **CodeQL alerts 188–195** (`go/path-injection` in `mediaaccess.go`) were re-checked after the
  handover. They stay dismissed as false positives:
  - every caller is admin-only (`Create`/`Patch` check `IsAdmin`, the access routes use
    `requireAdmin`) or the local `maktaba-server setup` command;
  - the stored roots are absolute and cleaned, and a relative root is `none`, never probed;
  - the only write is the fixed-name `.maktaba` probe, and the code refuses one that is not a
    real directory (`Lstat`).
- **A mode-000 root never becomes a `none` library when it is added:** `OSPathChecker` refuses
  it with 422 `not-readable`. A `none` library only comes from a folder that loses access later,
  so `access_none` opens the folder, adds it, closes it and checks again.
- **The merge with main changed the branch's Arabic product name** to «مكتبة» (main's
  convention), but its Arabic tests still expected "Maktaba". Run the web and pipeline tests
  after every merge with main, not only the Go tests.
- **A test fake from main** (`_LibrariesDB` in `test_native_choose.py`) answered only `roots`,
  while this branch's write-grant query reads `roots, access`. A fake that ignores the SQL breaks
  silently when the query grows.
- **Two "Something went wrong." texts:** the Folder access panel shows its own error when a test
  leaves `/access` unanswered, which collided with main's Run now error test. Scope error
  queries by role.

## Open
- #1097 (`logs_clean`) and #1098 (`thumbnail`) still change the `landed` line. Whichever merges
  second keeps every entry, `59.03` included.
