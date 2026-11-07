"""payment_provider_allowlist.py (story 54.19 T07): providers stay in their own modules."""

from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import payment_provider_allowlist as ppa  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]


def _store(tmp: Path, files: dict[str, str]) -> Path:
    store = tmp / "store"
    for name, text in {
        "payments/providers/acme/provider.json": json.dumps({"id": "acme", "markers": ["acme", "api.acme-pay.test"]}),
        "payments/providers/acme/index.ts": 'fetch("https://api.acme-pay.test/v1/charges") // acme',
        "payments/providers/index.ts": 'import { acmeModule } from "./acme/index.js";',
        **files,
    }.items():
        p = store / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
    return store


def test_a_provider_inside_its_module_passes(tmp_path: Path) -> None:
    assert ppa.check(_store(tmp_path, {"commerce/ledger.ts": "export const x = 1;"})) == []


def test_a_provider_named_outside_its_module_fails(tmp_path: Path) -> None:
    out = ppa.check(_store(tmp_path, {"commerce/ledger.ts": 'if (provider === "acme") {}'}))
    assert out == ["store/commerce/ledger.ts: 'acme' belongs in provider 'acme' under store/payments/providers/"]


def test_a_known_sdk_nobody_declares_fails_anywhere(tmp_path: Path) -> None:
    out = ppa.check(_store(tmp_path, {"web/x.ts": 'import Paddle from "@paddle/paddle-node-sdk";'}))
    assert any("'paddle'" in p and "a payment provider module" in p for p in out)
    out = ppa.check(_store(tmp_path, {"payments/providers/acme/x.ts": "paypal.com"}))
    assert any("'paypal'" in p for p in out)


def test_stripe_is_refused_everywhere_even_in_tests_and_lockfiles(tmp_path: Path) -> None:
    for name in ("payments/providers/acme/index.ts", "commerce/x.test.ts", "package-lock.json"):
        out = ppa.check(_store(tmp_path / name.replace("/", "_"), {name: '"stripe": "1.0.0"'}))
        assert any("Stripe" in p for p in out), name


def test_tests_may_configure_providers(tmp_path: Path) -> None:
    assert ppa.check(_store(tmp_path, {"payments/registry.test.ts": 'id: "acme"', "testing/h.ts": "PAYMENTS_ACME_KEY"})) == []


def test_the_repository_passes() -> None:
    assert ppa.main(["--root", str(ROOT)]) == 0
