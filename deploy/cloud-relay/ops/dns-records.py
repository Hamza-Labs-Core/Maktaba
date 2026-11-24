#!/usr/bin/env python3
"""The node's DNS records on Cloudflare (docs/cloud-dns.md), from the CI deploy.

    CF_API_TOKEN=... dns-records.py ZONE IP NAME...     # each NAME an A record to IP

Each name gets exactly one A record to IP: DNS only (never proxied: relayed video never
goes through Cloudflare's proxy), TTL 300. A name with an A record elsewhere is updated. A name
with another record that answers for its address (AAAA, CNAME, HTTPS, SVCB, NS) is refused before
any name is changed: the node has no IPv6 name, and a stale one would send IPv6 clients to another
machine. Nothing is deleted; TXT, CAA and MX records are left alone. Idempotent: a record already
right is left alone. The token needs Zone -> DNS -> Edit on ZONE; it is never printed.
"""
from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE = os.environ.get("CF_API_BASE", "https://api.cloudflare.com/client/v4")
# record types that answer, beside an A record, where a client of the name goes
ELSEWHERE = {"AAAA", "CNAME", "HTTPS", "SVCB", "NS"}


def cf(method: str, path: str, body: dict | None = None) -> dict:
    req = urllib.request.Request(BASE + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Authorization", "Bearer " + os.environ["CF_API_TOKEN"])
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.loads(r.read())
    except urllib.error.HTTPError as e:
        try:
            errs = json.loads(e.read()).get("errors")
        except ValueError:
            errs = []
        sys.exit(f"dns-records: {method} {path.split('?')[0]}: {e.code} {errs}")


def main() -> None:
    if len(sys.argv) < 4 or not os.environ.get("CF_API_TOKEN"):
        sys.exit("usage: CF_API_TOKEN=... dns-records.py ZONE IP NAME...")
    zone, ip, names = sys.argv[1], sys.argv[2], sys.argv[3:]
    z = cf("GET", "/zones?" + urllib.parse.urlencode({"name": zone}))["result"]
    if not z:
        sys.exit(f"dns-records: the token sees no zone {zone}")
    zid = z[0]["id"]
    # every name is read and judged before the first is written
    records: dict[str, list[dict]] = {}
    for name in names:
        if name != zone and not name.endswith("." + zone):
            sys.exit(f"dns-records: {name} is not in {zone}")
        have = cf("GET", f"/zones/{zid}/dns_records?" + urllib.parse.urlencode({"name": name, "per_page": 100}))["result"]
        other = sorted({r["type"] for r in have} & ELSEWHERE)
        if other:
            sys.exit(f"dns-records: {name} has {', '.join(other)} records, which send clients elsewhere: "
                     "remove them first (nothing was changed)")
        records[name] = [r for r in have if r["type"] == "A"]
        if len(records[name]) > 1:
            sys.exit(f"dns-records: {name} has {len(records[name])} A records; one is expected (nothing was changed)")
    for name in names:
        want = {"type": "A", "name": name, "content": ip, "ttl": 300, "proxied": False,
                "comment": "Maktaba Cloud node (deploy/cloud-relay, cloud-deploy.yml)"}
        a = records[name]
        if not a:
            cf("POST", f"/zones/{zid}/dns_records", want)
            print(f"{name}: A {ip} created (DNS only, TTL 300)")
        elif a[0]["content"] != ip or a[0]["proxied"] or a[0]["ttl"] != 300:
            cf("PATCH", f"/zones/{zid}/dns_records/{a[0]['id']}", want)
            print(f"{name}: A {ip} updated (was {a[0]['content']}, proxied={a[0]['proxied']})")
        else:
            print(f"{name}: A {ip} (unchanged)")


if __name__ == "__main__":
    main()
