#!/usr/bin/env python3
"""Daily watch of a single Maktaba Cloud node between deploys (#1665, docs/cloud-single-node.md).

Checks:
  1. TLS certificates for the relay apex, a wildcard subdomain (tls-probe.<relay>), the api,
     and the web app — fails if verification fails or expiry is under --min-cert-days (14).
  2. /readyz on the relay and the api — fails if either does not answer HTTP 200.
  3. Over SSH with a pinned host key (when --ssh-cmd is given):
     - newest /var/backups/maktaba-cloud/db-*.dump age — fails if none or older than
       --max-backup-hours (26);
     - /opt/maktaba-relay/ops/backup.sh --check — full scratch restore verification.
"""
from __future__ import annotations

import argparse
import shlex
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from typing import Any


def tls_context(ca_file: str = "") -> ssl.SSLContext:
    ctx = ssl.create_default_context(cafile=ca_file or None)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    return ctx


def check_cert(host: str, min_days: int, ca_file: str = "", timeout: float = 15.0) -> tuple[bool, str]:
    try:
        with socket.create_connection((host, 443), timeout=timeout) as sock:
            with tls_context(ca_file).wrap_socket(sock, server_hostname=host) as tls:
                cert: dict[str, Any] = tls.getpeercert() or {}
    except Exception as exc:  # noqa: BLE001
        return False, f"{host}: TLS failed ({exc})"
    not_after = cert.get("notAfter")
    if not not_after:
        return False, f"{host}: certificate has no notAfter"
    seconds_left = ssl.cert_time_to_seconds(not_after) - time.time()
    days_left = seconds_left / 86400.0
    issuer_parts = dict(kv for rdn in cert.get("issuer", ()) for kv in rdn)
    issuer = issuer_parts.get("organizationName") or issuer_parts.get("commonName") or "unknown"
    detail = f"{host}: {days_left:.1f} days left (issuer: {issuer})"
    if days_left < min_days:
        return False, f"{detail} — below {min_days} days"
    return True, detail


def check_readyz(host: str, ca_file: str = "", timeout: float = 15.0) -> tuple[bool, str]:
    url = f"https://{host}/readyz"
    req = urllib.request.Request(url, headers={"User-Agent": "maktaba-node-watch"})
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=tls_context(ca_file)) as resp:
            status = resp.status
    except urllib.error.HTTPError as exc:
        return False, f"{url}: HTTP {exc.code}"
    except Exception as exc:  # noqa: BLE001
        return False, f"{url}: request failed ({exc})"
    if status != 200:
        return False, f"{url}: HTTP {status}"
    return True, f"{url}: 200 OK"


def parse_newest_backup_age(output: str, now: float | None = None) -> tuple[str, float]:
    """Parse `<epoch> <path>` from the node's newest dump probe; returns (name, age_hours)."""
    line = output.strip().splitlines()[-1].strip() if output.strip() else ""
    if not line:
        raise ValueError("no db-*.dump backup found in /var/backups/maktaba-cloud")
    parts = line.split(None, 1)
    if len(parts) != 2:
        raise ValueError(f"unexpected backup stat line: {line!r}")
    mtime = float(parts[0])
    name = parts[1].rsplit("/", 1)[-1]
    ref = time.time() if now is None else now
    age_hours = (ref - mtime) / 3600.0
    return name, age_hours


def check_backup_age(ssh_cmd: list[str], max_hours: float, backup_dir: str = "/var/backups/maktaba-cloud") -> tuple[bool, str]:
    remote = (
        f"newest=$(find {shlex.quote(backup_dir)} -maxdepth 1 -name 'db-*.dump' | sort | tail -n 1); "
        f"[ -n \"$newest\" ] && stat -c '%Y %n' \"$newest\""
    )
    try:
        proc = subprocess.run([*ssh_cmd, remote], capture_output=True, text=True, timeout=60, check=False)
    except (subprocess.TimeoutExpired, OSError) as exc:
        return False, f"backup age: ssh failed ({exc})"
    if proc.returncode != 0:
        return False, f"backup age: no dump or ssh failed (exit {proc.returncode}): {(proc.stderr or proc.stdout).strip()[:300]}"
    try:
        name, age_hours = parse_newest_backup_age(proc.stdout)
    except ValueError as exc:
        return False, f"backup age: {exc}"
    detail = f"newest backup {name}: {age_hours:.1f} h old"
    if age_hours > max_hours:
        return False, f"{detail} — exceeds {max_hours:g} h"
    if age_hours < -1.0:
        return False, f"{detail} — timestamp is in the future"
    return True, detail


def check_backup_restore(ssh_cmd: list[str], deploy_dir: str = "/opt/maktaba-relay") -> tuple[bool, str]:
    remote = f"{shlex.quote(deploy_dir)}/ops/backup.sh --check"
    try:
        proc = subprocess.run([*ssh_cmd, remote], capture_output=True, text=True, timeout=600, check=False)
    except (subprocess.TimeoutExpired, OSError) as exc:
        return False, f"backup --check failed ({exc})"
    out = (proc.stdout + proc.stderr).strip()
    if proc.returncode != 0:
        return False, f"backup --check failed (exit {proc.returncode}): {out[-500:]}"
    summary = out.splitlines()[-1] if out else "ok"
    return True, summary


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--relay-domain", required=True)
    ap.add_argument("--api-domain", required=True)
    ap.add_argument("--app-domain", required=True)
    ap.add_argument("--wildcard-label", default="tls-probe")
    ap.add_argument("--min-cert-days", type=int, default=14)
    ap.add_argument("--max-backup-hours", type=float, default=26.0)
    ap.add_argument("--ssh-cmd", default="", help="SSH command prefix (e.g. 'ssh -F ~/.ssh/config node')")
    ap.add_argument("--ca-file", default="")
    args = ap.parse_args(argv)

    tls_hosts = [
        args.relay_domain,
        f"{args.wildcard_label}.{args.relay_domain}",
        args.api_domain,
        args.app_domain,
    ]
    results: list[tuple[str, bool, str]] = []

    for h in tls_hosts:
        ok, detail = check_cert(h, args.min_cert_days, args.ca_file)
        results.append((f"tls:{h}", ok, detail))

    for h in (args.relay_domain, args.api_domain):
        ok, detail = check_readyz(h, args.ca_file)
        results.append((f"readyz:{h}", ok, detail))

    if args.ssh_cmd.strip():
        ssh = shlex.split(args.ssh_cmd)
        ok, detail = check_backup_age(ssh, args.max_backup_hours)
        results.append(("backup:age", ok, detail))
        ok, detail = check_backup_restore(ssh)
        results.append(("backup:check", ok, detail))

    failed = 0
    for name, ok, detail in results:
        tag = "OK  " if ok else "FAIL"
        print(f"[{tag}] {name}: {detail}", flush=True)
        if not ok:
            failed += 1
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
