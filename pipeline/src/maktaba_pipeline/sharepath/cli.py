"""``maktaba-pipeline storage ...``: shares, servers and the path migration (story 31.3).

::

    storage mounts                      this machine's network mounts and the shares
                                        they resolve to
    storage servers                     the storage_servers records
    storage server-add NAME --address A [--address B] [--char-map fullwidth]
                                        add a server or more addresses to one
    storage server-add --from-mounts [--name nas]
                                        a record for every server this machine mounts
    storage canonicalize [--apply] [--include-local] [--json]
                                        dry run (default) / convert the stored paths
    storage batches                     the migrations done so far
    storage revert [--batch ID]         undo a migration (the latest by default)
    storage auto                        what the home does by itself, once, now:
                                        register servers, check, back up, convert
    storage status                      the automatic conversion's state
    storage backups                     the path backups kept in the database
    storage restore-backup [--backup ID]
                                        write a backup's values back (the newest)
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import platform
import socket
import sys
from typing import Any

from ..engine.paths import PathMap
from . import migrate as mig
from .mounts import Mount, discover
from .resolver import StorageServer, match_server
from .servers import build_for_machine, load_servers, save_server

__all__ = ["add_parser", "main"]


def add_parser(subparsers: Any) -> None:
    p = subparsers.add_parser(
        "storage", help="Network shares, storage servers and the canonical path migration."
    )
    p.add_argument(
        "what",
        choices=(
            "mounts",
            "servers",
            "server-add",
            "canonicalize",
            "batches",
            "revert",
            "auto",
            "status",
            "backups",
            "restore-backup",
        ),
    )
    p.add_argument("name", nargs="?", default=None, help="server-add: the server's name.")
    p.add_argument("--address", action="append", default=[], help="server-add: an address.")
    p.add_argument(
        "--char-map",
        default="none",
        help="server-add: none | fullwidth | sfm | 0x22:0xff02,... (on-disk:client).",
    )
    p.add_argument(
        "--export",
        action="append",
        default=[],
        metavar="PATH=SHARE",
        help="server-add: an NFS export and the share it is (/volume1/Originals=Originals).",
    )
    p.add_argument(
        "--from-mounts",
        action="store_true",
        help="server-add: one record per server this machine has mounted.",
    )
    p.add_argument("--apply", action="store_true", help="canonicalize: write the changes.")
    p.add_argument(
        "--include-local",
        action="store_true",
        help="canonicalize: write file:///... for paths on no share (default: leave them).",
    )
    p.add_argument("--batch", default=None, help="revert: the batch id (default: the latest).")
    p.add_argument(
        "--backup", default=None, help="restore-backup: the backup id (default: the newest)."
    )
    p.add_argument("--json", action="store_true", help="Print JSON.")
    p.add_argument("--database-url", default=os.getenv("MAKTABA_DATABASE_URL") or None)
    p.add_argument("--path-map", default=os.getenv("MAKTABA_WORKER_PATH_MAP") or None)


def _print(args: argparse.Namespace, data: Any, text: str) -> None:
    sys.stdout.write((json.dumps(data, indent=2, ensure_ascii=False) if args.json else text) + "\n")


def _short_host(addr: str) -> str:
    """A server name proposal for an address: its reverse DNS name, else ``nas``."""
    try:
        name = socket.gethostbyaddr(addr)[0]
    except (OSError, UnicodeError):
        return addr.lower() if not addr.replace(".", "").isdigit() else "nas"
    return name.split(".")[0].lower() or "nas"


async def _run(args: argparse.Namespace) -> int:
    if args.what == "mounts":
        mounts = discover()
        if not args.json:
            lines = [
                f"{m.protocol}  //{m.host}/{m.share}  ->  {m.local}  ({m.source})" for m in mounts
            ]
            sys.stdout.write("\n".join(lines or ["no network mounts"]) + "\n")
        if not args.database_url:
            if args.json:
                _print(args, [m.as_json() for m in mounts], "")
            return 0
    if not args.database_url:
        sys.stderr.write("--database-url (or MAKTABA_DATABASE_URL) is needed\n")
        return 2
    from ..runtime import Database

    db = await Database.connect(args.database_url)
    try:
        return await _with_db(args, db)
    finally:
        await db.close()


async def _with_db(args: argparse.Namespace, db: Any) -> int:
    servers = await load_servers(db)
    if args.what == "servers":
        _print(
            args,
            [s.to_json() for s in servers],
            "\n".join(
                f"{s.name}: {', '.join(s.addresses) or '-'}  char_map={s.char_map}" for s in servers
            )
            or "no storage servers yet (storage server-add --from-mounts)",
        )
        return 0
    if args.what == "server-add":
        return await _server_add(args, db, servers)
    if args.what == "batches":
        rows = await mig.batches(db)
        _print(
            args,
            rows,
            "\n".join(
                f"{b['batch']}  {b['at']}  {b['machine']}  {b['changes']} changes"
                f"  {b['reverted']} reverted"
                for b in rows
            )
            or "no migrations",
        )
        return 0
    if args.what == "revert":
        batch, n = await mig.revert(db, args.batch)
        _print(args, {"batch": batch, "reverted": n}, f"batch {batch}: {n} values put back")
        return 0
    if args.what in ("auto", "status", "backups", "restore-backup"):
        return await _auto(args, db)
    resolver = build_for_machine(servers, explicit=PathMap.parse(args.path_map))
    if args.what == "mounts":
        if not args.json:
            sys.stdout.write("shares:\n")
        _print(
            args,
            resolver.share_list(),
            "\n".join(
                f"{s['share']}  ->  {s['local']}  ({s['source']})" for s in resolver.share_list()
            )
            or "no share resolves here (add the server: storage server-add --from-mounts)",
        )
        return 0
    # canonicalize
    if not servers:
        sys.stderr.write(
            "no storage servers: add the NAS first (storage server-add --from-mounts)\n"
        )
        return 2
    p = await mig.plan(db, resolver, include_local=args.include_local)
    if args.json:
        out = p.as_json()
    else:
        out = None
        lines = [f"machine {p.machine}; shares:"]
        lines += [f"  {s['share']}  ->  {s['local']}  ({s['source']})" for s in p.shares]
        for c in p.columns:
            lines.append(
                f"{c.table}.{c.column}: {c.rows} rows, {c.converted} to convert,"
                f" {c.already} already canonical, {c.unmatched} on no share"
                + (f", {c.collisions} collisions skipped" if c.collisions else "")
            )
            for share, n in c.by_share.most_common():
                lines.append(f"    {share}: {n}")
            for a, b in c.examples:
                lines.append(f"    e.g. {a}\n      -> {b}")
            for u in c.unmatched_samples:
                lines.append(f"    unmatched: {u}")
            bad = [v for v in c.verified if not v[2]]
            if c.verified:
                lines.append(
                    f"    check: {len(c.verified) - len(bad)}/{len(c.verified)} sampled"
                    " paths resolve to an existing file here"
                )
            for v in bad[:3]:
                lines.append(f"    MISSING: {v[0]} -> {v[1]}")
    if args.apply:
        batch = await mig.apply(db, p)
        msg = f"applied: {p.total} values in batch {batch}" if batch else "nothing to change"
        if out is not None:
            out["batch"] = batch
    else:
        msg = f"dry run: {p.total} values would change; run again with --apply"
    if out is not None:
        out["result"] = msg
        _print(args, out, "")
    else:
        sys.stdout.write("\n".join([*lines, msg]) + "\n")
    return 0


async def _server_add(args: argparse.Namespace, db: Any, servers: list[StorageServer]) -> int:
    exports = []
    for e in args.export:
        path, sep, share = e.partition("=")
        if not sep:
            sys.stderr.write(f"--export {e!r}: PATH=SHARE\n")
            return 2
        exports.append((path.strip(), share.strip()))
    added: list[StorageServer] = []
    if args.from_mounts:
        hosts: dict[str, list[Mount]] = {}
        for m in discover(platform.system()):
            if match_server(servers, m.host) is None:
                hosts.setdefault(m.host, []).append(m)
        if not hosts:
            _print(args, [], "every mounted server is known already")
            return 0
        if args.name and len(hosts) > 1:
            sys.stderr.write("--name with several unknown servers: add them one by one\n")
            return 2
        for host in hosts:
            name = (args.name or _short_host(host)).lower()
            addrs = [host, *args.address]
            s = await save_server(
                db,
                StorageServer(
                    name=name,
                    addresses=tuple(addrs),
                    char_map=args.char_map,
                    exports=tuple(exports),
                ),
            )
            added.append(s)
    else:
        if not args.name:
            sys.stderr.write("server-add needs a NAME (or --from-mounts)\n")
            return 2
        added.append(
            await save_server(
                db,
                StorageServer(
                    name=args.name,
                    addresses=tuple(args.address),
                    char_map=args.char_map,
                    exports=tuple(exports),
                ),
            )
        )
    _print(
        args,
        [s.to_json() for s in added],
        "\n".join(f"{s.name}: {', '.join(s.addresses)}" for s in added),
    )
    return 0


def main(args: argparse.Namespace) -> int:
    return asyncio.run(_run(args))


async def _auto(args: argparse.Namespace, db: Any) -> int:
    from . import auto
    from . import backup as bk

    if args.what == "backups":
        rows = await bk.list_backups(db)
        _print(
            args,
            rows,
            "\n".join(
                f"{b['backup']}  {b['at']}  {b['machine']}  {b['values']} values"
                f"  batch {b['batch'] or '-'}"
                for b in rows
            )
            or "no backups",
        )
        return 0
    if args.what == "restore-backup":
        bid, n = await bk.restore_backup(db, args.backup)
        _print(args, {"backup": bid, "restored": n}, f"backup {bid}: {n} values written back")
        return 0
    if args.what == "auto":
        state = await auto.run_once(db, auto.AutoEnv())
    else:
        state, req = await auto.load_state(db)
        state = {**state, "request": req}
    problems = state.get("problems") or []
    run = state.get("run") or {}
    last = state.get("last") or {}
    lines = [f"phase: {state.get('phase') or 'never ran'}"]
    lines += [f"  {p['code']}: {p.get('detail', '')}" for p in problems]
    if run:
        lines.append(f"run: batch {run.get('batch')}, {run.get('done', 0)}/{run.get('total', 0)}")
    if last:
        lines.append(
            f"last: batch {last.get('batch')}, {last.get('changes')} values,"
            f" {last.get('finished_at')}"
        )
    _print(args, state, "\n".join(lines))
    return 0
