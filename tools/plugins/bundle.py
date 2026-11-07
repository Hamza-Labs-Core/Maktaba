"""The first-party plugin bundle (Epic 54, stories 54.22 §2, 54.27 §2).

Every plugin under ``plugins/first-party/`` (a folder with a ``manifest.toml``) goes into
one canonical JSON file: its manifest text, its strings in English and Arabic, and its
compiled statements (``queries/*.sql``, story 54.24). The server binary embeds
``shared/plugins/firstparty.json`` (Go ``embed``) and the pipeline wheel carries the same
bytes (``maktaba_pipeline/plugins/data/firstparty.json``), so both come from the release
that was signed: the API registers only these statements, the runner checks its copy's
digest against the API's before it imports any plugin, and nothing on disk can add to them.

``--write`` rewrites both copies; the default checks them (``make plugins-check``).
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import tomllib
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
OUT = ROOT / "shared" / "plugins" / "firstparty.json"
COPY = ROOT / "pipeline" / "src" / "maktaba_pipeline" / "plugins" / "data" / "firstparty.json"
sys.path.insert(0, str(ROOT / "plugins" / "sdk" / "python"))

from maktaba_plugin_sdk.statements import Registry, parse_file  # noqa: E402


def plugin(folder: Path) -> dict[str, Any]:
    text = (folder / "manifest.toml").read_text(encoding="utf-8")
    m = tomllib.loads(text)
    strings = {
        lang: json.loads((folder / "i18n" / f"{lang}.json").read_text(encoding="utf-8"))
        for lang in ("en", "ar")
    }
    sts = [
        parse_file(f.read_text(encoding="utf-8"), path=str(f))
        for f in sorted((folder / "queries").glob("*.sql"))
    ]
    Registry.of(m["id"], sts)  # two statements with one name or one text fail here
    out: dict[str, Any] = {
        "id": m["id"],
        "folder": folder.name,
        "manifest": text,
        "strings": strings,
        "statements": [s.to_json() for s in sorted(sts, key=lambda s: s.name)],
    }
    out["digest"] = hashlib.sha256(canonical(out).encode()).hexdigest()
    return out


def canonical(v: Any) -> str:
    return json.dumps(v, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def build() -> str:
    folders = (
        sorted(p for p in FIRST_PARTY.iterdir() if (p / "manifest.toml").is_file())
        if FIRST_PARTY.is_dir()
        else []
    )
    plugins = [plugin(f) for f in folders]
    doc = {"schema": 1, "plugins": plugins}
    doc["digest"] = hashlib.sha256(canonical(plugins).encode()).hexdigest()
    return json.dumps(doc, sort_keys=True, indent=1, ensure_ascii=False) + "\n"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--write", action="store_true")
    args = ap.parse_args(argv)
    text = build()
    if args.write:
        for p in (OUT, COPY):
            p.write_text(text, encoding="utf-8")
        return 0
    stale = [str(p.relative_to(ROOT)) for p in (OUT, COPY) if not p.is_file() or p.read_text(encoding="utf-8") != text]
    for p in stale:
        print(f"{p} is stale: run python3 tools/plugins/bundle.py --write", file=sys.stderr)  # noqa: T201
    return 1 if stale else 0


if __name__ == "__main__":
    sys.exit(main())
