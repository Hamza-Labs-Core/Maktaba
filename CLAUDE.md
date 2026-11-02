# Maktaba: notes for Claude sessions

1. **Read [`docs/claude-trail.md`](docs/claude-trail.md) first.** It holds the owner's standing
   rules, past decisions and why, failures and fixes, the live state, what is in flight and what
   waits on the owner.
2. **Before your session ends, add to it, in a trail-only PR:** your decisions (and the options
   you weighed), what failed and how it was fixed, and any change to the live state or the
   waiting list. Add only; date every entry; cite real PR numbers or shas (`gh` / `git`), never
   guessed ones.
3. **A code PR never touches `docs/claude-trail.md`.** Trail entries go only in a PR of their
   own that changes that one file and nothing else: CI runs no job and no test on it
   (`ci-success` passes in seconds), so merge it as soon as it is open. Any other file in the
   same PR runs its jobs as usual.
4. Never write secrets there or anywhere in the repo.
