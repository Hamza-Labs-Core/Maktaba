#!/usr/bin/env python3
"""Payment providers stay in their own modules (Epic 54, story 54.19 T07).

The store's payment providers are pluggable: each lives in `store/payments/providers/<id>/`,
with a `provider.json` that lists its markers (its SDK packages, API hosts, key prefixes
and words only it uses). This check reads every code file and lockfile under `store/` and
fails when:

- a provider's marker, or the name of a known payment SDK or API host, appears outside the
  folder of a provider that declares it (`payments/providers/index.ts`, which lists the
  modules, may import them);
- anything names Stripe or a Stripe-owned service (Lemon Squeezy) anywhere under `store/`:
  the store never uses them, for any payment (README §6.5).

Tests (`*.test.ts`, `store/testing/`) may name providers: they configure them. They are
still checked for Stripe.

    python3 tools/ci/payment_provider_allowlist.py [--root DIR]
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

#: never, anywhere under store/
FORBIDDEN = ("stripe", "lemonsqueezy", "lemon squeezy", "lemon_squeezy", "sk_live_", "pk_live_", "rk_live_")

#: payment SDKs, API hosts and words only a payment provider's module may name
KNOWN = (
    "adyen", "paddle", "fastspring", "2checkout", "verifone", "paypal", "braintree",
    "checkout.com", "tap.company", "gotap", "paytabs", "paymob", "accept.paymob", "square",
    "mollie", "worldpay", "razorpay", "payfort", "amazon pay", "hyperpay", "moyasar", "tabby",
    "tamara", "fawry",
)

CODE = {".ts", ".js", ".mjs", ".cjs", ".json", ".toml", ".sql", ".yaml", ".yml"}
SKIP_DIRS = {"node_modules", ".wrangler", "dist"}
REGISTRY = Path("payments/providers/index.ts")


def files(store: Path) -> list[Path]:
    out = []
    for p in sorted(store.rglob("*")):
        if not p.is_file() or any(part in SKIP_DIRS for part in p.relative_to(store).parts):
            continue
        if p.suffix in CODE or p.name.endswith("lock.json") or p.name.endswith(".lock"):
            out.append(p)
    return out


def providers(store: Path) -> dict[str, list[str]]:
    """Each provider folder's id and markers (its provider.json), plus the known names it is."""
    out: dict[str, list[str]] = {}
    for pj in sorted((store / "payments" / "providers").glob("*/provider.json")):
        doc = json.loads(pj.read_text())
        if doc.get("id") != pj.parent.name:
            raise SystemExit(f"{pj}: id {doc.get('id')!r} is not its folder's name")
        out[pj.parent.name] = [str(m).lower() for m in doc.get("markers", [])]
    return out


def check(store: Path) -> list[str]:
    problems: list[str] = []
    mods = providers(store)
    for f in files(store):
        rel = f.relative_to(store)
        text = f.read_text(errors="replace").lower()
        for word in FORBIDDEN:
            if word in text:
                problems.append(f"store/{rel}: names {word!r}: the store never uses Stripe or its services")
        parts = rel.parts
        if f.name.endswith(".test.ts") or parts[0] == "testing":
            continue
        own = parts[2] if len(parts) > 3 and parts[:2] == ("payments", "providers") else None
        allowed = set(mods.get(own, [])) if own else set()
        if own:
            allowed |= {k for k in KNOWN if k in own or any(k in m for m in mods.get(own, []))}
        names = {m: o for o, ms in mods.items() for m in ms}
        for k in KNOWN:
            names.setdefault(k, "")
        for marker, owner in sorted(names.items()):
            if marker in allowed or marker not in text:
                continue
            if rel == REGISTRY and re.search(rf"\./{re.escape(owner or marker)}/|{re.escape(owner or marker)}module", text):
                continue
            where = f"provider {owner!r}" if owner else "a payment provider module"
            problems.append(f"store/{rel}: {marker!r} belongs in {where} under store/payments/providers/")
    return problems


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--root", default=str(Path(__file__).resolve().parents[2]))
    args = ap.parse_args(argv)
    store = Path(args.root) / "store"
    if not store.is_dir():
        print("payment-provider allowlist: no store/ folder")
        return 0
    problems = check(store)
    for p in problems:
        print(p, file=sys.stderr)
    if problems:
        print(f"payment-provider allowlist: {len(problems)} problem(s)", file=sys.stderr)
        return 1
    print(f"payment-provider allowlist: {len(files(store))} files, every provider in its own module")
    return 0


if __name__ == "__main__":
    sys.exit(main())
