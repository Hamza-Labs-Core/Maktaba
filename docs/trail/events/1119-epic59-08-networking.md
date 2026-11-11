---
schema: 1
kind: pr
pr: 1119
title: "Epic 59 story 59.08: networking on a NAS (mDNS beside the NAS's own, bridge, trusted proxies)"
date: 2026-11-11
session: dea9a844-6ac4-4597-9dbd-99393fec0390
---

## What happened
- #1119 (merged at 18128e8ad) ships story 59.08:
  - **mDNS in a container:** Maktaba answers under its own name, `<host>-maktaba.local`, beside the NAS's
    avahi-daemon (UDP 5353 shared with SO_REUSEADDR), never claiming the NAS's name and never
    announcing on Docker's bridges. A mounted host system bus publishes through the NAS's Avahi
    instead (avahi-utils is in the image).
  - **Bridge networks:** the default route's interface driver (`veth`) marks a bridge; mDNS and
    SSDP go off there. Setup and Settings → This machine say that apps connect by address, by
    the HTTPS name or with a Maktaba Cloud code (en+ar). `MAKTABA_HOST_IP` names the address.
  - **Ports:** a port another program holds moves, and setup, the log and Settings name the
    program that holds it.
  - **Postgres:** a test proves it stays on loopback until a node may join.
  - **`[server] trusted_proxies`:** shared/go/clientip walks X-Forwarded-For right to left over
    trusted hops. Loopback is always trusted, plus the list. It replaced chi's RealIP, which
    believed the header from any peer. The cloud's CF-Connecting-IP resolver moved into the
    same module, unchanged.
  - **Docs:** docs/nas/networking(.ar).md covers host or bridge, the ports, the mDNS paths and the
    D-Bus socket's cost, reverse proxies (DSM Login Portal, TrueNAS, nginx, Caddy) and the DSM
    firewall.
  - **NAS CI:** new cells `mdns-own`, `mdns-dbus` and `bridge`; "59.08" added to `landed`.
- The home first trusted its approved nodes by `cluster_nodes.remote_addr`, so that clients
  proxied through a node kept their own address. An independent security review (at cc9df187e)
  found nothing blocking: before this, chi's RealIP was worse. It sent a follow-up instead,
  #1121 (`fix/epic59-08-clientip-hardening`):
  - node trust only through a delegation signature that covers the forwarded client, or the
    node's authenticated cluster link, with the join-time address no longer used;
  - `/0` refused in trusted_proxies, and docs that point to the proxy's single address, never
    a Docker network that holds its gateway;
  - streaming's loopback-only checks on `clientip.Peer`;
  - zoned IPv6 hops kept with the zone stripped;
  - plain en+ar docs that node trust needs no setting.
- Problems hit on the way and fixed in #1119:
  - **affected-tests blocked twice:** `supervisor/reconcile.go` changed with no test change, and
    the cloud's resolver moved into the shared module. Tests that pin each one were added.
  - **Test budget:** `TestMachineRoutesAdminOnly` broke the soft cap from runner load (0.02 s
    locally).
  - **Both mDNS cells failed four ways in turn:**
    - `avahi-browse` without `-k` names `_smb._tcp` "Microsoft Windows Network", so the wait
      never matched;
    - `docker top -eo comm` is refused without a pid column;
    - the dbus cell called Avahi ready when its daemon forked, before it owned its bus name;
    - the NAS's dbus-daemon reset the connection, since it looks up a caller's groups and
      drops a uid it does not know.

    The advertising log line now names the responder and, in a container with a bus that
    leads nowhere, why. That line named the last cause.
  - **truenas-app deploy failed twice:** setup's finish step writes server.toml before it
    creates the database, and the container started the server as soon as server.toml
    appeared. Two initdb runs then shared one folder. The container now also waits for setup's
    pending file (state/setup.json) to be gone. Main had the same race and passed by timing.
  - **Merges of main:** they conflicted with 59.11 and 59.10 in tests/nas/cells.json,
    tests/nas/cell.py, the compose file, the TrueNAS checklist and the NAS README. Each
    `landed` list was kept whole.
  - **docker-compose.bridge.yml dropped:** 59.11's compose file has `[bridge]` blocks that
    maktaba.tv's install page turns on, so the separate bridge file and its release asset went.
  - **The plugind flake** `a_wait_for_a_slot_is_not_the_jobs_time` passed on its one re-run.

## What we learned
- A joining machine's recorded address is not an identity. Trust what a node forwards only
  through something the node proves on each request, its delegation key or its link.
- A "ready" a test harness sees (a daemon forked, a file written) is not a service that
  answers. Wait for the thing the code under test asks for.
- A system D-Bus refuses a uid it cannot look up. A container that talks to the host's bus
  must run as an account the host knows.
- A config file that appears is not a finished setup when setup writes it before its last
  step.
- One log line that says which path was taken and why turned a third blind CI round into a
  fix.

## Keep doing
- Every merge of main through tools/dev/merge-main.sh, keeping every `landed` entry.
- One watcher per pull request that merges fail-closed on the exact head.
- A finding that belongs to another story's change gets its own pull request (see #1124).

## Stop doing
- Polling for a file as the sign that another process finished.
- Calling a sibling daemon ready on its fork.

## Open
- #1121 (the clientip hardening) merges after this, once main is merged into it again.
- The device checklists' "the apps find the home" steps (synology 13, truenas 15, unraid 8) are
  still to run on real NAS hardware.
