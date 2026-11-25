#!/usr/bin/env python3
"""Every code the home sends for its work has its words on the web and in both Remotes.

The home sends codes: a stage's id, a running job's step, a failure's cause. The web
(web/src/i18n), the Android Remote (res/values*/strings.xml through ServerWords.kt and
AttentionWords.kt) and the Apple Remote (StageWords.swift, AttentionWords.swift and
MaktabaKit's catalog) each say them in their own words, in English and Arabic. The apps'
own tests hold that too (ServerWordsTest.kt, StageWordsTests.swift, stageWords.test.ts), but
they run only when an app changes, and the codes are added in the pipeline, its plugins and
the API. This reads only text, so it runs on every pull request (the lint job's `repo`).

    app_words.py            0 when every code has its words everywhere, 1 naming each gap

Read: the stages (pipeline engine/graph.py, the first-party plugins' manifests), the steps
(`.progress("<step>", ...)` and the steps they list, in the pipeline and the plugins) and the
causes (api queues/causes.go, pipeline engine/causes.py). A step that is a stage's id takes
the stage's title; one named with spaces is keyed with underscores. The same step and the
same stage read the same on the web and in both apps.
"""

from __future__ import annotations

import json
import re
import sys
import tomllib
from pathlib import Path
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
ARABIC = re.compile(r"[؀-ۿ]")

Words = dict[str, tuple[str | None, str | None]]
"""A code's English and Arabic (None: none)."""


# ---- the home's codes ------------------------------------------------------------------


def stages(root: Path = ROOT) -> dict[str, str]:
    """id -> label of every stage: the graph's, and those a first-party plugin declares."""
    src = (root / "pipeline/src/maktaba_pipeline/engine/graph.py").read_text(encoding="utf-8")
    out = dict(re.findall(r'\bname="([a-z0-9_]+)",\s*label="([^"]+)"', src))
    out |= dict(re.findall(r'_legacy\(\s*"([a-z0-9_]+)",\s*"([^"]+)"', src))
    declared: dict[str, str] = {}
    for manifest in sorted((root / "plugins/first-party").glob("*/manifest.toml")):
        for stage in tomllib.loads(manifest.read_text(encoding="utf-8")).get("stage", []):
            declared[stage["name"]] = stage.get("label", stage["name"])
    for name in re.findall(r'\b_fp\(\s*"([a-z0-9_]+)"\s*\)', src):
        if name not in declared:
            raise SystemExit(f"app_words: graph.py takes {name} from a manifest, none declares it")
    return declared | out


def steps(root: Path = ROOT) -> set[str]:
    """Every step a stage reports, keyed with underscores."""
    out: set[str] = set()
    for base in ("pipeline/src", "plugins/first-party"):
        for path in sorted((root / base).rglob("*.py")):
            if "tests" in path.parts or path.name.startswith("test_"):
                continue
            src = path.read_text(encoding="utf-8")
            if "progress(" not in src:
                continue
            out.update(re.findall(r'\.progress\(\s*"([^"]+)"', src))
            for listed in re.findall(r"\bsteps\s*=\s*\[([^\]]*)\]", src):
                out.update(re.findall(r'"([^"]+)"', listed))
            for listed in re.findall(r"\b[A-Z_]*STEPS\b[^=\n]*=\s*[\[(]([^\])]*)[\])]", src):
                out.update(re.findall(r'"([^"]+)"', listed))
    return {s.replace(" ", "_") for s in out}


def causes(root: Path = ROOT) -> set[str]:
    """Every cause the home groups failures by: the API's codes and kinds, the engine's codes."""
    go = (root / "api/internal/handlers/queues/causes.go").read_text(encoding="utf-8")
    consts = dict(re.findall(r'(Cause[A-Za-z]+)\s*=\s*"([a-z_]+)"', go))
    out: set[str] = set()
    for name in ("causeLabels", "kindLabels"):
        body = go.split(f"var {name}", 1)[1].split("\n}", 1)[0]
        out.update(re.findall(r'(?m)^\s*"([a-z0-9_]+)":', body))
        out.update(consts[c] for c in re.findall(r"(?m)^\s*(Cause[A-Za-z]+):", body))
    py = (root / "pipeline/src/maktaba_pipeline/engine/causes.py").read_text(encoding="utf-8")
    out.update(re.findall(r"""\(\s*"([a-z_]+)",\s*r["']""", py))
    return out


# ---- each client's words ---------------------------------------------------------------


def web(root: Path = ROOT) -> dict[str, Words]:
    en, ar = (json.loads((root / f"web/src/i18n/{x}.json").read_text(encoding="utf-8")) for x in ("en", "ar"))

    def of(pattern: str) -> Words:
        rx = re.compile(pattern)
        return {m[1]: (en[k], ar.get(k)) for k in en if (m := rx.fullmatch(k))}

    return {
        "stage": of(r"queue\.stage\.([a-z0-9_]+)\.title"),
        "step": of(r"queue\.running\.step\.([a-z0-9_]+)"),
    }


def android(root: Path = ROOT) -> dict[str, Words]:
    res = root / "apps/android/remote/src/main/res"

    def strings(folder: str) -> dict[str, str]:
        tree = ElementTree.parse(res / folder / "strings.xml")
        return {
            e.attrib["name"]: "".join(e.itertext()).replace("\\'", "'").replace('\\"', '"')
            for e in tree.getroot().iter("string")
        }

    en, ar = strings("values"), strings("values-ar")
    code = root / "apps/android/remote/src/main/kotlin/com/hamzalabs/maktaba/remote"

    def of(file: str, name: str) -> Words:
        src = (code / file).read_text(encoding="utf-8")
        body = src.split(f"val {name}: Map<String, Int> = mapOf(", 1)[1].split("\n)", 1)[0]
        return {
            c: (en.get(r), ar.get(r))
            for c, r in re.findall(r'"([a-z0-9_]+)" to R\.string\.([a-z0-9_]+)', body)
        }

    return {
        "stage": of("ServerWords.kt", "stageWords"),
        "step": of("ServerWords.kt", "jobStepWords"),
        "cause": of("AttentionWords.kt", "causeWords"),
    }


def apple(root: Path = ROOT) -> dict[str, Words]:
    kit = root / "apps/apple/MaktabaKit/Sources/MaktabaKit"
    catalog = json.loads((kit / "Localizable.xcstrings").read_text(encoding="utf-8"))["strings"]

    def arabic(key: str) -> str | None:
        unit = catalog.get(key, {}).get("localizations", {}).get("ar", {}).get("stringUnit", {})
        return unit.get("value")

    def of(file: str, name: str, key: str) -> Words:
        src = (kit / file).read_text(encoding="utf-8")
        body = src.split(f"static let {name}: [String: String] = [", 1)[1].split("\n    ]", 1)[0]
        pairs = re.findall(r'"([a-z0-9_]+)": "((?:[^"\\]|\\.)*)"', body)
        return {c: (e, arabic(key.replace("%", c))) for c, e in pairs}

    cause = of("AttentionWords.swift", "causes", "queue.issues.cause.%")
    src = (kit / "AttentionWords.swift").read_text(encoding="utf-8")
    same = src.split("static let sameWords", 1)[1].split("]", 1)[0]
    for code, other in re.findall(r'"([a-z0-9_]+)": "([a-z0-9_]+)"', same):
        cause[code] = cause[other]
    return {
        "stage": of("StageWords.swift", "stages", "queue.stage.%.title"),
        "step": of("StageWords.swift", "steps", "queue.running.step.%"),
        "cause": cause,
    }


# ---- the check -------------------------------------------------------------------------


def gaps(root: Path = ROOT) -> list[str]:
    home_stages, home_steps, home_causes = stages(root), steps(root), causes(root)
    if len(home_stages) < 50 or len(home_steps) < 15 or len(home_causes) < 30:
        return [
            f"the home's lists read short: {len(home_stages)} stages, {len(home_steps)} steps,"
            f" {len(home_causes)} causes (a file moved, or is written another way: fix the reading here)"
        ]
    clients = {"the web": web(root), "the Android Remote": android(root), "the Apple Remote": apple(root)}
    out: list[str] = []
    for client, words in clients.items():

        def need(kind: str, code: str, table: Words, client: str = client) -> None:
            en, ar = table.get(code, (None, None))
            if not en:
                out.append(f"{client}: no words for the {kind} {code}")
            elif not ar or not ARABIC.search(ar):
                out.append(f"{client}: no Arabic for the {kind} {code} ({en})")

        for stage in sorted(home_stages):
            need("stage", stage, words["stage"])
        for step in sorted(home_steps):
            need("step", step, words["step"] if step in words["step"] else words["stage"])
        # the web shows the home's own label for a cause it has no words for
        for cause in sorted(home_causes) if "cause" in words else ():
            need("cause", cause, words["cause"])
    for kind in ("stage", "step"):
        for code, said in sorted(clients["the web"][kind].items()):
            for client in ("the Android Remote", "the Apple Remote"):
                here = clients[client][kind].get(code)
                if here and here != said:
                    out.append(f"{client}: the {kind} {code} reads {here}, the web's {said}")
    return out


def main() -> int:
    found = gaps()
    for g in found:
        print(f"::error::app_words: {g}")
    if found:
        print(
            "Add the words: web/src/i18n/{en,ar}.json (queue.stage.<id>.title, queue.running.step.<step>);"
            " apps/android/remote res/values{,-ar}/strings.xml with ServerWords.kt or AttentionWords.kt;"
            " apps/apple/MaktabaKit Localizable.xcstrings with StageWords.swift or AttentionWords.swift."
        )
        return 1
    print(f"app_words: {len(stages())} stages, {len(steps())} steps and {len(causes())} causes have their words")
    return 0


if __name__ == "__main__":
    sys.exit(main())
