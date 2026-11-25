---
schema: 1
kind: incident
title: "A failed first deploy started again at the next boot, and only a machine that really reboots shows what a boot does"
date: 2026-11-25
session: 72be8c47-3733-4379-9431-ce8fcdcdab2c
agent: claude-code
prs: [1628]
---

## What happened
- The third review of #1628 found one major problem, in the one path with nothing to go back to: **a
  first deploy whose tests failed was stopped, and the stack's unit started it again at the next
  boot**, and the deploy after it took no backup, because "first" was read as "nothing to save".
  Fixed: the unit's condition starts nothing while the node's only deploys failed, and `begin` backs up
  whatever database there is.
- Until then a reboot was simulated: Docker and the unit restarted, with another boot id written into
  the state folder. `reboot_rehearsal.py` now deploys to a virtual machine on the runner (QEMU with
  KVM, the Ubuntu cloud image the servers run) over SSH, reboots it with `systemctl reboot`, and cuts
  its power by killing its process. 14 cases, 25 minutes.
- With each of the two fixes taken out, the machine's cases fail with the words of the finding
  ("a refused release runs", "begin took no backup").
- Writing them found one more: a first commit followed at once by a power cut had its record half
  written, and boot would have refused the release. Boot now finishes a commit, and the saved set
  goes only after what replaced it is on the disk (`sync`).
- **The first version of the machine's mutation check passed for the wrong reason**: a broken test
  script failed every case, and "a case failed" counted as "noticed". A mutation now counts only when
  the case that names it fails, on a node that was set up, and on the machine with the finding's words.
- The delta's review then found that a rollback that can't reach Docker said "stopped" while the
  release served: an unanswered question was read as "no". #1708 fixes it.

## What we learned
- A state with nothing behind it (a first deploy, an empty node) is a path of its own: "nothing to
  restore" is neither "nothing to save" nor "nothing to stop from starting".
- A failed query is not an empty answer. `$(docker ps ...)` in a condition turns "Docker is down" into
  "nothing runs".
- GitHub's runners have KVM: a virtual machine with a cloud image answers SSH in under a minute, and a
  killed process is a power cut, unwritten data lost included.
- A mutation test needs the reason of the failure, not only the failure.
- A step's `env:` is printed before its script runs, so `::add-mask::` in that script comes too late for
  it. What must not be printed is a secret.

## Keep doing
- Simulate a reboot for the many cases and really do one for the few that decide whether a node serves.

## Open
- #1708 (the delta review's minors), before production; #1693 and #1707 have the rest.
