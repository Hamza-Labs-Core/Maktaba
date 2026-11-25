---
schema: 1
kind: incident
title: "A rollback died with its SSH session, a kept migration hid a contract one, and a green rehearsal noticed neither"
date: 2026-11-24
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
prs: [1628]
---

## What happened
- The second review of #1628 broke its rollback on a real stack, after 23 of 23 rehearsal cases had
  passed and a failed deploy had gone back on QA:
  - **`migrate undo` gave the first refusal it met.** A newer migration that was only better kept (its
    table had rows) hid an older contract one: the rollback said success, and the previous release
    answered 500 on a schema without its column. Every migration is now judged first, and the
    gravest refusal wins.
  - **apply and rollback were children of the SSH session.** Cut 4 s into a rollback, compose died on
    the closed output, which the script read as "the previous release does not come up": a healthy
    release was held down until a person came. They are now systemd units of the node's own, followed
    by the workflow and picked up again; a lease and a timer bring back a deploy nobody works on.
  - **Six guarantees taken out of the scripts at once, and the rehearsal still passed:** no backup, no
    lock, the saved set removed in place, a web app switched in two steps, no settled mark, a broken
    retention. Each now has a case that fails
    without it, and a nightly job takes 14 guarantees out one at a time and requires that.
- The new cases found more before the third review: `docker stop` can miss a container that is
  restarting at that moment; a client killed by a signal ends at 137, not ssh's 255, so a follower that
  retries on 255 alone gives up; `pkill -f` in a shell kills its own shell.

## What we learned
- "All cases pass" says what the cases test, not what they are cited for. Take a guarantee out and see
  a case fail before citing the rehearsal for it.
- A remote command that must end is not the SSH session's child, and the state it would leave when its
  caller is gone for ever needs an owner on the machine itself.
- When several checks can refuse, collect them all and rank them: first-refusal-wins hides the worst.

## Keep doing
- Give a reviewer the head and let it break the thing on a throwaway stack: reading found none of these.

## Open
- The third review of #1628.
