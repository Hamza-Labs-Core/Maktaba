"""web/Dockerfile and cloud/Dockerfile copy every file outside web/ that the web typecheck reads.

`pnpm build` in both images runs `tsc -p tsconfig.json`, and web/tsconfig.json includes
`../apps/tv/web/src`; a path alias (`@maktaba/flowchart`) also points outside web/. Every
file reached from there (a relative import, a JSON import, an alias) has to be COPYed into the
build stage at its repo-relative place under /src, or the image's typecheck fails. That used to
show only in the e2e Docker build, late: story 58.09 (#1277) added four COPY lines to each
image that way, and story 58.11 a fifth. This walks the imports the typecheck follows and fails
here, naming the file, the file importing it and the Dockerfile that does not copy it.
"""

from __future__ import annotations

import fnmatch
import json
import re
from collections.abc import Iterable
from pathlib import Path, PurePosixPath

import pytest

ROOT = Path(__file__).resolve().parents[3]
WEB = ROOT / "web"
TSCONFIG = WEB / "tsconfig.json"
DOCKERFILES = [ROOT / "web/Dockerfile", ROOT / "cloud/Dockerfile"]
# where the build stage keeps the repo: WORKDIR /src/web, and /src/<path> for each file outside web/
IMAGE_ROOT = "/src"

SOURCE_SUFFIXES = (".ts", ".tsx", ".mts", ".cts")
# moduleResolution "Bundler": the specifier as written, then these suffixes, then a directory's index
TRY_SUFFIXES = ("", ".ts", ".tsx", ".d.ts", ".mts", ".cts", "/index.ts", "/index.tsx", "/index.d.ts")
# `.js` written for a `.ts` file, as TypeScript allows
JS_TO_TS = {".js": (".ts", ".tsx", ".d.ts"), ".jsx": (".tsx",), ".mjs": (".mts",), ".cjs": (".cts",)}

SPECIFIER = re.compile(
    r"""\bfrom\s*["']([^"'\n]+)["']"""  # import … from "x", export … from "x"
    r"""|\bimport\s*\(\s*["']([^"'\n]+)["']\s*[,)]"""  # import("x"), import("x", {with: …})
    r"""|\bimport\s*["']([^"'\n]+)["']"""  # import "x"
    r"""|\brequire\s*\(\s*["']([^"'\n]+)["']\s*\)"""  # import x = require("x")
)


def strip_jsonc(text: str) -> str:
    """JSON from JSONC: comments and trailing commas out, string contents untouched ("src/*" holds a /*)."""
    out: list[str] = []
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c == '"':
            j = i + 1
            while j < n and text[j] != '"':
                j += 2 if text[j] == "\\" else 1
            out.append(text[i : j + 1])
            i = j + 1
        elif text.startswith("//", i):
            while i < n and text[i] != "\n":
                i += 1
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            i = n if end == -1 else end + 2
        else:
            out.append(c)
            i += 1
    return re.sub(r",(\s*[}\]])", r"\1", "".join(out))


def load_tsconfig(path: Path = TSCONFIG) -> dict:
    config = json.loads(strip_jsonc(path.read_text(encoding="utf-8")))
    assert "extends" not in config, f"{path}: extends is not followed here; teach this test to read it"
    return config


def rel(path: Path) -> str:
    return path.resolve().relative_to(ROOT).as_posix()


def outside_web(path: Path) -> bool:
    return not path.resolve().is_relative_to(WEB.resolve())


def excluded(path: Path, patterns: list[str]) -> bool:
    """tsconfig "exclude": each pattern, relative to web/, names a directory prefix or a glob."""
    rel_web = Path(path).resolve().relative_to(WEB.resolve(), walk_up=True).as_posix()
    for pattern in patterns:
        if any(ch in pattern for ch in "*?"):
            if fnmatch.fnmatch(rel_web, pattern) or fnmatch.fnmatch(rel_web, pattern.replace("**/", "")):
                return True
        elif rel_web == pattern or rel_web.startswith(pattern.rstrip("/") + "/"):
            return True
    return False


def included_sources(config: dict) -> list[Path]:
    """Every .ts/.tsx/.mts file the typecheck starts from: its include roots, less its exclude."""
    files: list[Path] = []
    for root in config.get("include", []):
        assert not any(ch in root for ch in "*?"), f"tsconfig include {root!r}: a glob; teach this test to read it"
        base = (WEB / root).resolve()
        candidates = [base] if base.is_file() else sorted(base.rglob("*"))
        for f in candidates:
            if f.is_file() and f.name.endswith(SOURCE_SUFFIXES) and not excluded(f, config.get("exclude", [])):
                files.append(f)
    return files


def specifiers(text: str) -> Iterable[str]:
    for m in SPECIFIER.finditer(text):
        yield next(g for g in m.groups() if g is not None)


def _resolve_file(base: Path) -> Path | None:
    for suffix in TRY_SUFFIXES:
        candidate = Path(str(base) + suffix)
        if candidate.is_file():
            return candidate.resolve()
    for js, tss in JS_TO_TS.items():
        if base.name.endswith(js):
            for ts in tss:
                candidate = base.with_name(base.name[: -len(js)] + ts)
                if candidate.is_file():
                    return candidate.resolve()
    return None


def resolve(spec: str, importer: Path, config: dict) -> Path | None:
    """The repo file a specifier names, or None for a package (node_modules) or a name that resolves nowhere."""
    if spec.startswith(("./", "../")) or spec in (".", ".."):
        return _resolve_file(importer.parent / spec)
    options = config.get("compilerOptions", {})
    base_url = WEB / options.get("baseUrl", ".")
    for pattern, targets in options.get("paths", {}).items():
        if "*" in pattern:
            prefix, _, suffix = pattern.partition("*")
            if not (spec.startswith(prefix) and spec.endswith(suffix) and len(spec) >= len(prefix) + len(suffix)):
                continue
            star = spec[len(prefix) : len(spec) - len(suffix)]
        elif spec != pattern:
            continue
        else:
            star = ""
        for target in targets:
            found = _resolve_file(base_url / target.replace("*", star))
            if found is not None:
                return found
    return None


def typecheck_inputs_outside_web(config: dict) -> dict[str, str]:
    """{file outside web/ the typecheck reads: the first file found importing it (or the include naming it)}.

    Starts from every included file; follows imports transitively from each file outside web/ (an
    included file in web/ is walked anyway, so only its imports that leave web/ are kept).
    """
    needed: dict[str, str] = {}
    queue: list[Path] = []
    for f in included_sources(config):
        if outside_web(f):
            if rel(f) not in needed:
                needed[rel(f)] = "web/tsconfig.json include"
                queue.append(f)
        else:
            queue.append(f)
    seen: set[Path] = set()
    while queue:
        f = queue.pop()
        if f in seen:
            continue
        seen.add(f)
        for spec in specifiers(f.read_text(encoding="utf-8")):
            target = resolve(spec, f, config)
            if target is None or not outside_web(target):
                continue
            if rel(target) not in needed:
                needed[rel(target)] = rel(f)
            if target.name.endswith(SOURCE_SUFFIXES):
                queue.append(target)
    return needed


def build_stage_copies(dockerfile_text: str) -> list[tuple[str, str]]:
    """(source, destination) of each COPY from the build context in the stages that run pnpm."""
    text = re.sub(r"\\\n", " ", dockerfile_text)
    stages = re.split(r"(?im)^\s*FROM\s", text)
    copies: list[tuple[str, str]] = []
    for stage in stages:
        if not re.search(r"(?im)^\s*RUN\b.*\bpnpm\b", stage):
            continue
        for m in re.finditer(r"(?im)^\s*COPY\s+(.+)$", stage):
            args = m.group(1).split()
            if any(a.startswith("--from") for a in args):
                continue
            args = [a for a in args if not a.startswith("--")]
            dest = args[-1]
            copies.extend((src.rstrip("/"), dest) for src in args[:-1])
    return copies


def copied_at_its_place(path: str, copies: list[tuple[str, str]]) -> bool:
    """A COPY of the file, or of a directory holding it, to /src/<the same repo path>."""
    for src, dest in copies:
        if path == src or path.startswith(src + "/"):
            if PurePosixPath(dest) == PurePosixPath(IMAGE_ROOT) / src:
                return True
    return False


def missing_copies(dockerfile_text: str, needed: dict[str, str]) -> list[str]:
    copies = build_stage_copies(dockerfile_text)
    return sorted(p for p in needed if not copied_at_its_place(p, copies))


# --- the guard ------------------------------------------------------------------------------------


@pytest.fixture(scope="module")
def needed() -> dict[str, str]:
    return typecheck_inputs_outside_web(load_tsconfig())


@pytest.mark.parametrize("dockerfile", DOCKERFILES, ids=lambda p: rel(p))
def test_each_image_copies_what_the_typecheck_reads(dockerfile: Path, needed: dict[str, str]) -> None:
    missing = missing_copies(dockerfile.read_text(encoding="utf-8"), needed)
    lines = [f"  {p} (imported by {needed[p]})" for p in missing]
    assert not missing, (
        f"{rel(dockerfile)} does not COPY these files the web typecheck reads, each to /src/<its repo path>:\n"
        + "\n".join(lines)
        + f"\nadd e.g. `COPY {missing[0]} {IMAGE_ROOT}/{missing[0]}` before `COPY web ./`"
    )


def test_the_walk_finds_the_known_inputs(needed: dict[str, str]) -> None:
    # the positive control: the include root, a JSON import, a directory and the path alias
    assert any(p.startswith("apps/tv/web/src/") for p in needed)
    assert "apps/tv/web/bridge.schema.json" in needed
    assert any(p.startswith("shared/compat/") for p in needed)
    assert "tools/plugin-sdk/js/src/flowchart.ts" in needed
    assert all(not p.startswith("web/") for p in needed)


@pytest.mark.parametrize("dockerfile", DOCKERFILES, ids=lambda p: rel(p))
def test_a_dropped_copy_fails(dockerfile: Path, needed: dict[str, str]) -> None:
    # the negative control: without its shared/compat line, the image misses those files
    text = dockerfile.read_text(encoding="utf-8")
    assert re.search(r"(?m)^COPY shared/compat .*\n", text)
    dropped = re.sub(r"(?m)^COPY shared/compat .*\n", "", text)
    missing = missing_copies(dropped, needed)
    assert missing and all(p.startswith("shared/compat/") for p in missing)


def test_every_path_alias_outside_web_is_followed() -> None:
    # a "paths" alias is resolved like a relative import; each one leaving web/ names a real file
    paths = load_tsconfig()["compilerOptions"].get("paths", {})
    for alias, targets in paths.items():
        for target in targets:
            if not (WEB / target).resolve().is_relative_to(WEB.resolve()):
                assert "*" not in alias, f"{alias}: a wildcard alias outside web/; check resolve() reads it"
                assert resolve(alias, WEB / "src/x.ts", load_tsconfig()) is not None, f"{alias} -> {target}"


# --- the helpers ----------------------------------------------------------------------------------


def test_jsonc_keeps_slashes_in_strings() -> None:
    text = '{\n  // a comment\n  "paths": {"@/*": ["src/*"]}, /* block */\n  "include": ["a",],\n}\n'
    assert json.loads(strip_jsonc(text)) == {"paths": {"@/*": ["src/*"]}, "include": ["a"]}


def test_specifiers_reads_every_form() -> None:
    text = """
import a from "./a";
import type { B } from '../b';
export * from "./c";
export { d } from "./d";
import "./e.css";
const f = await import("./f");
import {
  g,
} from "../../g.json";
import h = require("./h");
"""
    assert list(specifiers(text)) == ["./a", "../b", "./c", "./d", "./e.css", "./f", "../../g.json", "./h"]


def test_copies_skip_other_stages_and_from() -> None:
    dockerfile = """
FROM node AS build
WORKDIR /src/web
COPY web/package.json ./
COPY shared/a \\
     /src/shared/a
COPY --chown=1 shared/b /src/shared/b
COPY shared/wrong /elsewhere
RUN pnpm run build
FROM golang AS go
COPY shared/c /src/shared/c
RUN go build
FROM caddy
COPY --from=build /src/web/dist /srv
"""
    copies = build_stage_copies(dockerfile)
    assert ("shared/a", "/src/shared/a") in copies and ("shared/b", "/src/shared/b") in copies
    assert not any(src == "shared/c" or src.startswith("/src") for src, _ in copies)
    needed = {"shared/a/x.ts": "i", "shared/b": "i", "shared/c/y.ts": "i", "shared/wrong/z.ts": "i"}
    assert missing_copies(dockerfile, needed) == ["shared/c/y.ts", "shared/wrong/z.ts"]
