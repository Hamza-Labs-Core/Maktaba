---
schema: 1
kind: pr
pr: 922
title: The origin-only-via-edge and origin-aop rehearsals (Epic 50, 50.31, T06 to T08)
date: 2026-11-07
session: 4a773653-5bd9-4035-a87c-484f3431746c
prs: [883, 884, 927, 930]
---

## What happened
- Two cloud-rehearsal scenarios for 50.31 (issue #884), run on `compose-origin.yml` over the HA pair:
  - The nodes run production's Caddyfile (with `issuer internal` instead of the Cloudflare DNS challenge) and a copy of the UFW firewall in iptables (`node/firewall.sh`).
  - The leader runs the api on the loopback, behind a socat stand-in for cloudflared.
  - A stand-in edge writes `CF-Connecting-IP` the way Cloudflare does, and an attacker container tries everything else.
- origin-only-via-edge (T06, T07):
  - The Floating IP answers 80 and 443 only. SNI `api.maktaba.tv` is refused, and plain HTTP for it gets 421.
  - A forged `CF-Connecting-IP` is replaced by the edge and dropped by the relay.
  - Killing the leader moves the connector: promoted in about 38 s, the api back through the edge about 1 s later. The old leader comes back as a replica.
- origin-aop (T08): from the ranges with the certificate, 200. Without the certificate, the TLS handshake is refused (stricter than the spec's 403, and the spec now says so). From outside the ranges with the certificate, 403.
- T08 first got 200 from outside the ranges. That was a production bug, split out and fixed in #927. An audit after it found the same bug in the witness's token check, fixed in #930.
- The shared node Dockerfile and entrypoint changed (iptables and socat, an `ORIGIN_MODE` path), so the whole rehearsal matrix ran.

## What we learned
- **The shared log redaction zeroes an IPv4 address's last octet** (`shared/log/go/rules.go`, `redact_rules.json` keeps only `127.*`). The api logged the attacker as 10.61.0.0, which looked like Docker masquerading. The edge's echo of its `{remote_host}` (`X-Edge-Saw`) showed 10.61.0.66, and the redaction rule explained the rest.
  - Any test that reads a client IP from a Go service's log must compare redacted values.
  - A `strings.Contains(line, "6.6.6.6")` check on a redacted log can never fail. The relay check had that bug until the Epic 50 stream's review caught it.
- With redaction, two addresses in one /24 look the same in a log. The attacker now sits in another /24 (10.61.7.66, on a /16 Floating IP network) from the edge and the gateway. So the api's 10.61.7.0 could not have come from a hop that rewrote the header with its own peer address.
- `pgrep -f "TCP-LISTEN:7844"` run through `bash -c` matched its own command line. The `[T]CP-LISTEN` pattern doesn't match itself.
- A Docker bridge with `enable_ip_masquerade: "false"` keeps container-to-container sources real. The masquerade fear was a red herring here, but compose-direct.yml needs the option for the same reason.

## Keep doing
- Echo what each hop saw (a response header from the stand-in edge) when a test asserts who a service saw. It separates the network from the logging in one run.
- Put the addresses a test must tell apart in different /24s whenever logs are redacted.

## Open
- The Epic 50 stream will make the relay check also require the attacker's (redacted) address once #928 makes the relay log its resolved client IP.
