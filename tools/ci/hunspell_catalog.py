#!/usr/bin/env python3
"""The Hunspell languages' catalog block and words (Epic 52, story 52.16).

``make catalog-sync`` runs it: from ``tools/release/packs/hunspell-languages.toml`` it
writes the ``lang.hunspell.<code>`` entries between the BEGIN / END lines of
``shared/components/catalog.toml``, each one's English and Arabic title and description in
``web/src/i18n/en.json`` / ``ar.json``, and the list's copy in the pipeline
(``language_packs/hunspell-languages.toml``). ``make catalog-check`` fails on a diff.
"""

from __future__ import annotations

import json
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tools" / "release" / "packs"))
import minitoml  # noqa: E402 - Python 3.10 on the runners has no tomllib
LIST = ROOT / "tools" / "release" / "packs" / "hunspell-languages.toml"
CATALOG = ROOT / "shared" / "components" / "catalog.toml"
COPY = ROOT / "pipeline" / "src" / "maktaba_pipeline" / "language_packs" / "hunspell-languages.toml"
EN = ROOT / "web" / "src" / "i18n" / "en.json"
AR = ROOT / "web" / "src" / "i18n" / "ar.json"
BEGIN = "# ---- BEGIN lang.hunspell"
END = "# ---- END lang.hunspell"
SINCE = "0.3.82"


def entries(langs: list[dict[str, str]]) -> str:
    out = []
    for lang in langs:
        cid = f"lang.hunspell.{lang['code']}"
        out.append(
            "\n".join(
                [
                    "[[component]]",
                    f'id         = "{cid}"',
                    'kind       = "asset"',
                    'group      = "languages"',
                    f'title_key  = "components.{cid}.title"',
                    f'desc_key   = "components.{cid}.desc"',
                    'requires   = ["runtime.lang"]',
                    f"# LibreOffice's {lang['folder']} dictionary ({lang['licence']})",
                    f'source     = "pack:{cid}"',
                    "in_payload = false",
                    'platforms  = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"]',
                    "bundles    = []",
                    f'since      = "{SINCE}"',
                ]
            )
        )
    return "\n\n".join(out)


def words(path: Path, langs: list[dict[str, str]], arabic: bool) -> None:
    raw = path.read_text(encoding="utf-8")
    data = json.loads(raw)
    want: dict[str, str] = {}
    for lang in langs:
        cid = f"components.lang.hunspell.{lang['code']}"
        if arabic:
            want[f"{cid}.title"] = f"قاموس {lang['ar']}"
            want[f"{cid}.desc"] = (
                f"قاموس Hunspell لـ{lang['ar']}: يتحقق من كلمات التفريغ بهذه اللغة في مراجعة الكلمات."
            )
        else:
            want[f"{cid}.title"] = f"{lang['en']} dictionary"
            want[f"{cid}.desc"] = (
                f"The Hunspell dictionary for {lang['en']}: word review checks transcripts "
                "in this language with it."
            )
    out: dict[str, str] = {}
    placed = False
    for k, v in data.items():
        if k.startswith("components.lang.hunspell."):
            continue
        out[k] = v
        if k == "components.lang.en.desc" and not placed:
            out.update(want)
            placed = True
    if not placed:
        out.update(want)
    text = json.dumps(out, ensure_ascii=False, indent=2) + "\n"
    if text != raw:
        path.write_text(text, encoding="utf-8")


def main() -> int:
    langs = minitoml.loads(LIST.read_text(encoding="utf-8"))["language"]
    cat = CATALOG.read_text(encoding="utf-8")
    m = re.search(re.escape(BEGIN) + r"[^\n]*\n(.*?)" + re.escape(END), cat, re.S)
    if m is None:
        print("hunspell_catalog: no BEGIN/END lines in the catalog", file=sys.stderr)
        return 1
    body = entries(langs)
    cat = cat[: m.start(1)] + "\n" + body + "\n\n" + cat[m.end(1) :]
    CATALOG.write_text(cat, encoding="utf-8")
    words(EN, langs, arabic=False)
    words(AR, langs, arabic=True)
    shutil.copyfile(LIST, COPY)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
