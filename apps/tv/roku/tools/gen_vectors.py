"""Writes the expected outputs of shared/i18n/visual-vectors.json from the reference (story 58.15).

The vector file holds each case's input; this fills in what Roku must draw, from `visual.py`
(python-bidi and arabic-reshaper at the pinned versions in requirements.txt). A case is:

    {"id": "...", "input": "...", "visual": "..."}                       # visual_text(input)
    {"id": "...", "input": "...", "maxWidthChars": 12, "visual": "..."}  # cut to 12 characters
    {"id": "...", "input": "...", "base": "rtl", "visual": "..."}        # paragraph direction
    {"id": "...", "input": "...", "widthChars": 20, "maxLines": 2, "lines": [...]}  # visual_lines

`note` is free text for the reader. Run from the repository root:

    uv run --no-project --python 3.12 --with-requirements apps/tv/roku/tools/requirements.txt \
        python apps/tv/roku/tools/gen_vectors.py [--check]

`--check` fails when an expected output differs from the reference (CI runs it).
"""

from __future__ import annotations

import argparse
import importlib.metadata
import json
import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))

import visual  # noqa: E402

ROOT = Path(__file__).resolve().parents[4]
VECTORS = ROOT / "shared" / "i18n" / "visual-vectors.json"


def expected(case: dict[str, Any]) -> dict[str, Any]:
    out = dict(case)
    base = case.get("base", "auto")
    if "widthChars" in case:
        out.pop("visual", None)
        out["lines"] = visual.visual_lines(case["input"], case["widthChars"], case.get("maxLines", 0), base)
    else:
        out.pop("lines", None)
        out["visual"] = visual.visual_text(case["input"], case.get("maxWidthChars", 0), base)
    return out


def render(doc: dict[str, Any]) -> str:
    """One case per line, so a diff shows the case that changed."""
    head = {k: v for k, v in doc.items() if k != "vectors"}
    lines = ["{"]
    for k, v in head.items():
        lines.append(f"  {json.dumps(k)}: {json.dumps(v, ensure_ascii=False)},")
    lines.append('  "vectors": [')
    cases = [json.dumps(c, ensure_ascii=False) for c in doc["vectors"]]
    lines.append(",\n".join(f"    {c}" for c in cases))
    lines.append("  ]")
    lines.append("}")
    return "\n".join(lines) + "\n"


def build(doc: dict[str, Any]) -> dict[str, Any]:
    ids = [c["id"] for c in doc["vectors"]]
    dupes = sorted({i for i in ids if ids.count(i) > 1})
    if dupes:
        raise SystemExit(f"duplicate vector ids: {', '.join(dupes)}")
    doc = dict(doc)
    doc["reference"] = {
        "python-bidi": importlib.metadata.version("python-bidi"),
        "arabic-reshaper": importlib.metadata.version("arabic-reshaper"),
    }
    doc["vectors"] = [expected(c) for c in doc["vectors"]]
    return doc


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--check", action="store_true", help="fail when the file is not what the reference gives")
    ap.add_argument("--file", type=Path, default=VECTORS)
    args = ap.parse_args(argv)
    text = args.file.read_text(encoding="utf-8")
    want = render(build(json.loads(text)))
    if args.check:
        if text != want:
            old = json.loads(text)["vectors"]
            new = json.loads(want)["vectors"]
            for a, b in zip(old, new, strict=True):
                if a != b:
                    want_out = b.get("visual", b.get("lines"))
                    have_out = a.get("visual", a.get("lines"))
                    print(
                        f"::error::{args.file.name}: {a['id']} expects {want_out!r}, file has {have_out!r}",
                        file=sys.stderr,
                    )
            print(f"{args.file}: not what the reference gives; run gen_vectors.py", file=sys.stderr)
            return 1
        print(f"{args.file}: {len(json.loads(want)['vectors'])} vectors match the reference")
        return 0
    args.file.write_text(want, encoding="utf-8")
    print(f"wrote {args.file}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
